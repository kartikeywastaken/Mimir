//! screen_guard.rs — detect active screen monitoring / sharing

use log::debug;

/// Returns true if any external process appears to be recording / sharing
/// the user's screen right now.
pub fn is_screen_monitored() -> bool {
    screen_sharing_via_cgwindow() || screenshare_process_running()
}

/// Check Screen Recording permission via raw CoreGraphics FFI.
pub fn check_screen_recording_permission() -> bool {
    #[cfg(target_os = "macos")]
    unsafe {
        cg_preflight_screen_capture_access()
    }
    #[cfg(not(target_os = "macos"))]
    false
}

#[cfg(target_os = "macos")]
unsafe fn cg_preflight_screen_capture_access() -> bool {
    #[link(name = "CoreGraphics", kind = "framework")]
    extern "C" {
        fn CGPreflightScreenCaptureAccess() -> bool;
    }
    CGPreflightScreenCaptureAccess()
}

/// Check via CGWindowListCopyWindowInfo whether any window is being captured.
fn screen_sharing_via_cgwindow() -> bool {
    #[cfg(target_os = "macos")]
    unsafe {
        use core_foundation::array::CFArray;
        use core_foundation::base::TCFType;
        use core_foundation::dictionary::CFDictionary;
        use core_foundation::string::CFString;
        use core_graphics::display::{CGWindowListCopyWindowInfo, kCGNullWindowID, kCGWindowListOptionAll};

        let window_list = CGWindowListCopyWindowInfo(kCGWindowListOptionAll, kCGNullWindowID);
        if window_list.is_null() {
            return false;
        }

        // kCGWindowSharingState > 0 indicates the window is being captured
        let sharing_key = CFString::new("kCGWindowSharingState");
        let count = core_foundation_sys::array::CFArrayGetCount(window_list as _);

        for i in 0..count {
            let dict_ptr = core_foundation_sys::array::CFArrayGetValueAtIndex(window_list as _, i);
            if dict_ptr.is_null() {
                continue;
            }
            let dict = dict_ptr as core_foundation_sys::dictionary::CFDictionaryRef;
            let mut val_ptr: *const std::ffi::c_void = std::ptr::null();
            let has = core_foundation_sys::dictionary::CFDictionaryGetValueIfPresent(
                dict,
                sharing_key.as_concrete_TypeRef() as _,
                &mut val_ptr,
            );
            if has != 0 && !val_ptr.is_null() {
                // It's a CFNumber; get its int value
                let mut n: i64 = 0;
                if core_foundation_sys::number::CFNumberGetValue(
                    val_ptr as _,
                    core_foundation_sys::number::kCFNumberSInt64Type,
                    &mut n as *mut i64 as *mut _,
                ) && n > 0
                {
                    debug!("screen_guard: window sharing state={}", n);
                    core_foundation::base::CFRelease(window_list as _);
                    return true;
                }
            }
        }
        core_foundation::base::CFRelease(window_list as _);
        false
    }
    #[cfg(not(target_os = "macos"))]
    false
}

/// Known screensharing / video conferencing app process names.
const SCREENSHARE_PROCESSES: &[&str] = &[
    "zoom.us",
    "ZoomOpener",
    "CptHost",
    "Microsoft Teams",
    "Teams",
    "Webex",
    "Google Meet",
    "Slack",
    "Discord",
    "OBS",
    "ScreenFlow",
    "Loom",
];

fn screenshare_process_running() -> bool {
    for proc_name in SCREENSHARE_PROCESSES {
        let output = std::process::Command::new("pgrep")
            .args(["-x", proc_name])
            .output();
        if let Ok(out) = output {
            if out.status.success() && !out.stdout.is_empty() {
                debug!("screen_guard: found process: {}", proc_name);
                return true;
            }
        }
    }
    false
}
