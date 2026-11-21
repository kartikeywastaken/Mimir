//! mimir-permissions — first-launch macOS permission checker
//!
//! Checks the two permissions required by mimir-daemon:
//!   1. Accessibility (kTCCServiceAccessibility) — read and fill the focused field
//!   2. Input Monitoring (kTCCServiceListenEvent) — global hotkey, checked implicitly by rdev
//!
//! For any missing permission, opens System Preferences to the correct pane
//! and prints a human-readable message.

fn main() {
    println!("mimir-permissions: checking required macOS permissions\n");

    check_accessibility();
    check_input_monitoring();

    println!("\nDone. Restart mimir-daemon after granting permissions.");
}

fn check_accessibility() {
    #[cfg(target_os = "macos")]
    {
        let trusted = unsafe { ax_is_trusted() };
        if trusted {
            println!("✅  Accessibility: granted");
        } else {
            println!("❌  Accessibility: MISSING");
            println!("    → Opening System Preferences > Privacy & Security > Accessibility");
            open_prefs("x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility");
            println!("    Add 'mimir-daemon' and 'mimir-permissions' to the list, then re-run.");
        }
    }
    #[cfg(not(target_os = "macos"))]
    println!("⚠️   Accessibility: not applicable (non-macOS)");
}

fn check_input_monitoring() {
    // Input Monitoring permission cannot be pre-checked via a public API —
    // rdev's event tap will fail silently and log a warning when it starts.
    println!("ℹ️   Input Monitoring: will be requested when mimir-daemon starts its hotkey listener.");
    println!("    If the hotkey doesn't fire, go to System Preferences > Privacy & Security > Input Monitoring");
    println!("    and add 'mimir-daemon' to the list.");
    open_prefs("x-apple.systempreferences:com.apple.preference.security?Privacy_ListenEvent");
}

fn open_prefs(url: &str) {
    let _ = std::process::Command::new("open").arg(url).spawn();
    std::thread::sleep(std::time::Duration::from_millis(500));
}

#[cfg(target_os = "macos")]
unsafe fn ax_is_trusted() -> bool {
    // AXIsProcessTrusted() — returns 1 if trusted
    #[link(name = "ApplicationServices", kind = "framework")]
    extern "C" {
        fn AXIsProcessTrusted() -> bool;
    }
    AXIsProcessTrusted()
}
