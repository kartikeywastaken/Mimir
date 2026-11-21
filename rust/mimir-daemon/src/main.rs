//! mimir-daemon — system-wide macOS solve daemon
//!
//! Entry point: checks permissions, spawns the Go solver subprocess,
//! starts the global hotkey listener (on a dedicated OS thread), and
//! routes hotkey events into the async solve pipeline via a channel.

mod ax_extractor;
mod filler;
mod hotkey;
mod ipc;
mod overlay;

use std::path::PathBuf;
use std::process::{Child, Command};

use log::{error, info, warn};
use tokio::sync::mpsc;

/// Absolute path to the Go mimir binary (sibling of this binary).
fn go_binary_path() -> PathBuf {
    let exe = std::env::current_exe().unwrap_or_else(|_| PathBuf::from("bin/mimir-daemon"));
    exe.parent()
        .unwrap_or_else(|| std::path::Path::new("bin"))
        .join("mimir")
}

/// Spawn the Go mimir binary in --daemon-mode (Unix socket server).
fn spawn_go_solver() -> std::io::Result<Child> {
    let bin = go_binary_path();
    info!("spawning Go solver: {}", bin.display());
    Command::new(&bin).arg("--daemon-mode").spawn().map_err(|e| {
        error!("failed to spawn Go solver at {}: {}", bin.display(), e);
        e
    })
}

#[tokio::main]
async fn main() {
    env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info")).init();
    info!("mimir-daemon starting (⌘+Shift+M to solve)");

    // ── Step 1: Check permissions ────────────────────────────────────────────
    check_permissions();

    // ── Step 2: Spawn Go solver ──────────────────────────────────────────────
    let _go_child = match spawn_go_solver() {
        Ok(child) => {
            info!("Go solver subprocess started (pid {})", child.id());
            Some(child)
        }
        Err(e) => {
            warn!(
                "could not spawn Go solver: {}. IPC will retry on first hotkey.",
                e
            );
            None
        }
    };

    // Give the Go socket server a moment to bind.
    tokio::time::sleep(std::time::Duration::from_millis(600)).await;

    // ── Step 3: Hotkey channel ───────────────────────────────────────────────
    // The hotkey listener runs on a blocking OS thread (CFRunLoop requirement).
    // It sends a unit signal over a channel to wake the async solve pipeline.
    let (tx, mut rx) = mpsc::channel::<()>(4);

    std::thread::spawn(move || {
        info!("hotkey listener thread started");
        hotkey::listen(move || {
            // Non-blocking send; drop the signal if the channel is full
            // (i.e. a solve is already in flight)
            let _ = tx.try_send(());
        });
    });

    info!("mimir-daemon ready — press ⌘+Shift+M in any app");

    // ── Step 4: Event loop ───────────────────────────────────────────────────
    loop {
        tokio::select! {
            _ = rx.recv() => {
                tokio::spawn(async {
                    if let Err(e) = run_solve_pipeline().await {
                        error!("solve pipeline: {}", e);
                    }
                });
            }
            _ = tokio::signal::ctrl_c() => {
                info!("mimir-daemon shutting down");
                break;
            }
        }
    }
}

/// `MIMIR_AUTOFILL=1` makes the daemon fill the answer after showing it.
fn autofill_enabled() -> bool {
    matches!(
        std::env::var("MIMIR_AUTOFILL").as_deref(),
        Ok("1") | Ok("true") | Ok("yes")
    )
}

/// Full solve pipeline: extract → IPC solve → show → optional fill.
async fn run_solve_pipeline() -> anyhow::Result<()> {
    use anyhow::Context;

    info!("hotkey fired — extracting question from focused app");

    let extraction = ax_extractor::extract_focused_question()
        .map_err(|e| anyhow::anyhow!(e))
        .context("AX extraction failed")?;

    info!(
        "extracted: type={:?} choices={} text={}…",
        extraction.field_type,
        extraction.choices.len(),
        &extraction.text[..extraction.text.len().min(60)]
    );

    let response = ipc::solve(&extraction)
        .await
        .map_err(|e| anyhow::anyhow!(e))
        .context("IPC solve failed")?;

    info!(
        "answer: {} ({}% confidence, backend={})",
        response.answer, response.confidence, response.backend
    );

    // The overlay always shows the answer. Filling is an explicit opt-in and
    // behaves the same regardless of who else can see the screen.
    overlay::show(&extraction, &response).await;

    if autofill_enabled() && !response.answer.is_empty() {
        info!("auto-fill enabled → applying answer");
        filler::apply(&extraction, &response)
            .map_err(|e| anyhow::anyhow!(e))
            .context("fill failed")?;
    }

    Ok(())
}

fn check_permissions() {
    let ax_ok = ax_extractor::check_accessibility_permission();

    if !ax_ok {
        warn!("Accessibility permission missing — opening System Preferences");
        open_sys_prefs(
            "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
        );
    } else {
        info!("Accessibility: ✓");
    }
}

fn open_sys_prefs(url: &str) {
    let _ = Command::new("open").arg(url).spawn();
}
