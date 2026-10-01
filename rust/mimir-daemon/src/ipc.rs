//! ipc.rs — Unix Domain Socket client to the Go solver
//!
//! Connects to /tmp/mimir.sock (Go mimir --daemon-mode) and sends
//! newline-delimited JSON SolveRequests, receiving SolveResponses.

use log::{debug, info, warn};
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use uuid::Uuid;

use crate::ax_extractor::{ExtractionResult, FieldType};

const SOCKET_PATH: &str = "/tmp/mimir.sock";
const SOLVE_TIMEOUT_SECS: u64 = 30;

/// JSON request sent to the Go solver.
#[derive(Debug, Serialize)]
struct SolveRequest {
    id: String,
    text: String,
    choices: Vec<String>,
    field_type: String,
    context: String,
}

/// JSON response from the Go solver.
#[derive(Debug, Deserialize, Clone)]
pub struct SolveResponse {
    pub id: String,
    pub answer: String,
    pub confidence: i32,
    pub reason: String,
    pub fill_strategy: String,
    pub backend: String,
    #[serde(default)]
    pub error: String,
}

/// Send an extraction result to the Go solver and return the solve response.
pub async fn solve(extraction: &ExtractionResult) -> Result<SolveResponse, String> {
    let req = SolveRequest {
        id: Uuid::new_v4().to_string(),
        text: extraction.text.clone(),
        choices: extraction.choices.clone(),
        field_type: match extraction.field_type {
            FieldType::Mcq => "mcq".to_string(),
            FieldType::Text => "text".to_string(),
        },
        context: String::new(),
    };

    let req_json = serde_json::to_string(&req)
        .map_err(|e| format!("serialize request: {}", e))?;

    debug!("ipc: sending to {}: {}", SOCKET_PATH, &req_json[..req_json.len().min(120)]);

    // Connect with a timeout
    let connect_result = tokio::time::timeout(
        std::time::Duration::from_secs(5),
        UnixStream::connect(SOCKET_PATH),
    )
    .await;

    let stream = match connect_result {
        Ok(Ok(s)) => s,
        Ok(Err(e)) => {
            warn!("ipc: could not connect to Go socket {}: {}", SOCKET_PATH, e);
            // Attempt to spawn Go solver and retry once
            try_spawn_go_and_wait().await;
            tokio::time::timeout(
                std::time::Duration::from_secs(5),
                UnixStream::connect(SOCKET_PATH),
            )
            .await
            .map_err(|_| "timeout waiting for Go socket after spawn".to_string())?
            .map_err(|e| format!("connect after spawn: {}", e))?
        }
        Err(_) => return Err("timeout connecting to Go socket".to_string()),
    };

    let (reader, mut writer) = stream.into_split();
    let mut reader = BufReader::new(reader);

    // Write request line
    writer
        .write_all((req_json + "\n").as_bytes())
        .await
        .map_err(|e| format!("write request: {}", e))?;

    // Read response line with overall timeout
    let mut response_line = String::new();
    tokio::time::timeout(
        std::time::Duration::from_secs(SOLVE_TIMEOUT_SECS),
        reader.read_line(&mut response_line),
    )
    .await
    .map_err(|_| "timeout waiting for solve response".to_string())?
    .map_err(|e| format!("read response: {}", e))?;

    debug!("ipc: response: {}", &response_line[..response_line.len().min(200)]);

    let resp: SolveResponse = serde_json::from_str(response_line.trim())
        .map_err(|e| format!("parse response: {}", e))?;

    if !resp.error.is_empty() {
        return Err(format!("solver error: {}", resp.error));
    }

    Ok(resp)
}

/// Try to spawn the Go mimir binary and wait for its socket to appear.
async fn try_spawn_go_and_wait() {
    use std::path::PathBuf;

    let exe = std::env::current_exe().unwrap_or_else(|_| PathBuf::from("bin/mimir-daemon"));
    let go_bin = exe
        .parent()
        .unwrap_or_else(|| std::path::Path::new("bin"))
        .join("mimir");

    info!("ipc: attempting to spawn Go solver at {}", go_bin.display());
    let _ = std::process::Command::new(&go_bin)
        .arg("--daemon-mode")
        .spawn();

    // Poll for socket to appear, up to 5 seconds
    for _ in 0..10 {
        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
        if std::path::Path::new(SOCKET_PATH).exists() {
            info!("ipc: Go socket appeared");
            return;
        }
    }
    warn!("ipc: Go socket did not appear after spawn");
}
