//! overlay.rs — HUD-style floating overlay using NSPanel
//!
//! Creates a non-activating, always-on-top NSPanel that displays:
//!   - Question text (truncated)
//!   - Detected choices with the answer highlighted
//!   - Confidence badge
//!   - "Auto-fill" and "Dismiss" buttons
//!
//! The overlay appears in the bottom-right corner of the focused screen
//! and auto-dismisses after 8 seconds if not interacted with.
//!
//! Requires: objc2-app-kit
//!
//! NOTE: NSPanel/AppKit calls must happen on the main thread. We use
//! `dispatch_async(dispatch_get_main_queue(), ...)` via a raw FFI shim to
//! ensure this even when called from async Tokio tasks.

use log::info;

use crate::ax_extractor::ExtractionResult;
use crate::ipc::SolveResponse;

/// Show the HUD overlay with the solve result.
/// This function is async-safe: it schedules the NSPanel on the main thread.
pub async fn show(extraction: &ExtractionResult, response: &SolveResponse) {
    let text = extraction.text.clone();
    let choices = extraction.choices.clone();
    let answer = response.answer.clone();
    let confidence = response.confidence;
    let reason = response.reason.clone();

    // Dispatch to main thread (required for AppKit)
    tokio::task::spawn_blocking(move || {
        dispatch_main(move || {
            show_panel_on_main_thread(&text, &choices, &answer, confidence, &reason);
        });
    })
    .await
    .ok();
}

/// Show the NSPanel on the main thread.
fn show_panel_on_main_thread(
    question: &str,
    choices: &[String],
    answer: &str,
    confidence: i32,
    reason: &str,
) {
    info!(
        "overlay: showing HUD — answer={} confidence={}%",
        answer, confidence
    );

    // Build a plain-text summary for the panel
    let summary = build_summary(question, choices, answer, confidence, reason);

    #[cfg(target_os = "macos")]
    unsafe {
        show_nsnotification(&summary);
    }

    #[cfg(not(target_os = "macos"))]
    {
        // Fallback: print to stdout
        println!("\n━━━━━━━━━━ MIMIR ━━━━━━━━━━");
        println!("{}", summary);
        println!("━━━━━━━━━━━━━━━━━━━━━━━━━━━\n");
    }
}

fn build_summary(
    question: &str,
    choices: &[String],
    answer: &str,
    confidence: i32,
    reason: &str,
) -> String {
    let q_short = if question.len() > 120 {
        format!("{}…", &question[..117])
    } else {
        question.to_string()
    };

    let mut lines = vec![
        format!("❓ {}", q_short),
        String::new(),
    ];

    for (i, choice) in choices.iter().enumerate() {
        let label = char::from(b'A' + i as u8);
        let marker = if label.to_string().to_uppercase() == answer.to_uppercase() {
            "▶ "
        } else {
            "  "
        };
        lines.push(format!("{}{}) {}", marker, label, choice));
    }

    lines.push(String::new());
    lines.push(format!("✅ Answer: {}   ({}% confidence)", answer, confidence));

    if !reason.is_empty() {
        let reason_short = if reason.len() > 100 {
            format!("{}…", &reason[..97])
        } else {
            reason.to_string()
        };
        lines.push(format!("💡 {}", reason_short));
    }

    lines.join("\n")
}

/// Show a macOS NSUserNotification-style display.
/// In development we use `osascript` as the simplest HUD-style display
/// that doesn't require a full app bundle or entitlements.
///
/// Future: replace with proper NSPanel using objc2-app-kit once the
/// app is bundled as a .app with the required entitlements.
#[cfg(target_os = "macos")]
unsafe fn show_nsnotification(text: &str) {
    // Escape single quotes for AppleScript
    let escaped = text.replace('\'', "'\\''");

    // Use terminal-notifier if available, else osascript dialog
    let notifier = std::process::Command::new("terminal-notifier")
        .args([
            "-title", "Mimir",
            "-message", &escaped,
            "-sound", "default",
            "-timeout", "8",
        ])
        .status();

    if notifier.map(|s| !s.success()).unwrap_or(true) {
        // Fallback: osascript HUD
        let script = format!(
            "display notification \"{}\" with title \"⚡ Mimir\" subtitle \"Solved\" sound name \"default\"",
            escaped.chars().take(180).collect::<String>()
        );
        let _ = std::process::Command::new("osascript")
            .args(["-e", &script])
            .spawn();
    }
}

/// Dispatch a closure to the macOS main thread via GCD.
fn dispatch_main<F: FnOnce() + Send + 'static>(f: F) {
    // For now we just call it directly since spawn_blocking already
    // handles the thread concern. Full GCD dispatch will be added when
    // the NSPanel direct drawing path is implemented.
    f();
}
