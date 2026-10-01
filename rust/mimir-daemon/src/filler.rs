//! filler.rs — apply the solver answer to the focused UI element
//!
//! Strategy:
//!   1. For MCQ (radio/checkbox): try AXUIElementPerformAction(kAXPressAction)
//!      on the matching choice element. Falls back to clipboard paste.
//!   2. For text fields: write answer to clipboard, then simulate Cmd+A Cmd+V.

use log::{debug, info, warn};

use crate::ax_extractor::{ExtractionResult, FieldType};
use crate::ipc::SolveResponse;

/// Apply the answer from `response` to the active UI element.
pub fn apply(extraction: &ExtractionResult, response: &SolveResponse) -> Result<(), String> {
    match extraction.field_type {
        FieldType::Mcq => fill_mcq(extraction, response),
        FieldType::Text => fill_text(response),
    }
}

fn fill_mcq(extraction: &ExtractionResult, response: &SolveResponse) -> Result<(), String> {
    let answer = response.answer.trim().to_uppercase();
    let target_text = find_choice_text(extraction, &answer);

    if let Some(ref text) = target_text {
        debug!("filler MCQ: trying AX press for: {}", text);
        if ax_press_choice(extraction, text).is_ok() {
            info!("filler: AX pressed choice '{}'", text);
            return Ok(());
        }
        warn!("filler: AX press failed, falling back to clipboard");
    }

    copy_to_clipboard(&answer)?;
    info!("filler: answer '{}' copied to clipboard (MCQ fallback)", answer);
    Ok(())
}

fn fill_text(response: &SolveResponse) -> Result<(), String> {
    copy_to_clipboard(&response.answer)?;
    simulate_select_all_paste()?;
    info!("filler: pasted text answer via Cmd+A Cmd+V");
    Ok(())
}

fn copy_to_clipboard(text: &str) -> Result<(), String> {
    let mut ctx = arboard::Clipboard::new()
        .map_err(|e| format!("clipboard init: {}", e))?;
    ctx.set_text(text.to_string())
        .map_err(|e| format!("clipboard set: {}", e))?;
    debug!("filler: clipboard set to '{}'", text);
    Ok(())
}

fn find_choice_text(extraction: &ExtractionResult, answer_label: &str) -> Option<String> {
    for (i, choice) in extraction.choices.iter().enumerate() {
        let label = char::from(b'A' + i as u8).to_string();
        if label == answer_label {
            return Some(choice.clone());
        }
        if choice.trim().to_uppercase() == answer_label.to_uppercase() {
            return Some(choice.clone());
        }
    }
    None
}

fn ax_press_choice(extraction: &ExtractionResult, choice_text: &str) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    unsafe {
        use accessibility_sys::*;
        use core_foundation_sys::base::CFRelease;

        let pid = extraction.ax_pid.ok_or("no pid available for AX press")?;
        let app_el = AXUIElementCreateApplication(pid);
        if app_el.is_null() {
            return Err("AXUIElementCreateApplication returned null".to_string());
        }

        let found = find_and_press_element(app_el, choice_text, 0);
        CFRelease(app_el as _);
        if found {
            Ok(())
        } else {
            Err(format!("choice '{}' not found in AX tree", choice_text))
        }
    }
    #[cfg(not(target_os = "macos"))]
    Err("AX press only supported on macOS".to_string())
}

#[cfg(target_os = "macos")]
unsafe fn find_and_press_element(
    el: accessibility_sys::AXUIElementRef,
    target_text: &str,
    depth: usize,
) -> bool {
    use accessibility_sys::*;
    use core_foundation_sys::base::CFRelease;
    use crate::ax_extractor::{get_ax_attr_str, get_ax_children, str_to_cfstring};

    if depth > 8 || el.is_null() {
        return false;
    }

    let role = get_ax_attr_str(el, kAXRoleAttribute);
    let title = get_ax_attr_str(el, kAXTitleAttribute);
    let value = get_ax_attr_str(el, kAXValueAttribute);

    let text_match = [&title, &value].iter().any(|s| {
        s.as_ref().map(|t| t.contains(target_text)).unwrap_or(false)
    });

    if text_match {
        match role.as_deref() {
            Some("AXRadioButton") | Some("AXCheckBox") | Some("AXButton") => {
                if let Some(cf_action) = str_to_cfstring(kAXPressAction) {
                    let err = AXUIElementPerformAction(el, cf_action);
                    CFRelease(cf_action as _);
                    return err == kAXErrorSuccess;
                }
            }
            _ => {}
        }
    }

    for child in get_ax_children(el).into_iter().take(50) {
        if find_and_press_element(child, target_text, depth + 1) {
            return true;
        }
    }
    false
}

fn simulate_select_all_paste() -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        use core_graphics::event::{CGEvent, CGEventFlags, CGEventType, CGKeyCode};
        use core_graphics::event_source::{CGEventSource, CGEventSourceStateID};

        let source = CGEventSource::new(CGEventSourceStateID::HIDSystemState)
            .map_err(|_| "CGEventSource::new failed".to_string())?;

        // keycodes: 'a' = 0, 'v' = 9
        let key_a: CGKeyCode = 0;
        let key_v: CGKeyCode = 9;

        macro_rules! key_event {
            ($key:expr, $down:expr, $flags:expr) => {{
                let ev = CGEvent::new_keyboard_event(source.clone(), $key, $down)
                    .map_err(|_| "CGEvent create failed".to_string())?;
                ev.set_flags($flags);
                ev.post(core_graphics::event::CGEventTapLocation::HID);
            }};
        }

        // Cmd+A
        key_event!(key_a, true, CGEventFlags::CGEventFlagCommand);
        key_event!(key_a, false, CGEventFlags::CGEventFlagCommand);
        std::thread::sleep(std::time::Duration::from_millis(50));
        // Cmd+V
        key_event!(key_v, true, CGEventFlags::CGEventFlagCommand);
        key_event!(key_v, false, CGEventFlags::CGEventFlagNull);

        Ok(())
    }
    #[cfg(not(target_os = "macos"))]
    Err("CGEvent only supported on macOS".to_string())
}
