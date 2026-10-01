//! ax_extractor.rs — macOS Accessibility API text extraction
//!
//! Walks the AXUIElement tree of the currently focused application to extract:
//!   - Question/challenge text (paragraphs, static text, headings)
//!   - MCQ choices (radio groups, lists, checkboxes)
//!
//! Requires Accessibility permission (kTCCServiceAccessibility).

use log::debug;

/// The type of field/question detected.
#[derive(Debug, Clone, PartialEq)]
pub enum FieldType {
    Mcq,
    Text,
}

/// Result of extracting question content from the focused app.
#[derive(Debug, Clone)]
pub struct ExtractionResult {
    pub text: String,
    pub choices: Vec<String>,
    pub field_type: FieldType,
    /// PID of the focused application (for AX-based fill).
    pub ax_pid: Option<i32>,
}

/// Check if Accessibility permission is granted.
pub fn check_accessibility_permission() -> bool {
    #[cfg(target_os = "macos")]
    unsafe {
        accessibility_sys::AXIsProcessTrusted()
    }
    #[cfg(not(target_os = "macos"))]
    false
}

/// Extract the question / challenge from the currently focused macOS application.
pub fn extract_focused_question() -> Result<ExtractionResult, String> {
    #[cfg(target_os = "macos")]
    {
        extract_via_ax()
    }
    #[cfg(not(target_os = "macos"))]
    Err("AX extraction only supported on macOS".to_string())
}

#[cfg(target_os = "macos")]
fn extract_via_ax() -> Result<ExtractionResult, String> {
    use accessibility_sys::*;
    use core_foundation_sys::base::CFRelease;
    use std::ptr;

    unsafe {
        let system_el = AXUIElementCreateSystemWide();
        if system_el.is_null() {
            return Err("AXUIElementCreateSystemWide returned null".to_string());
        }

        let focused_attr = str_to_cfstring(kAXFocusedApplicationAttribute)
            .ok_or("failed to create kAXFocusedApplicationAttribute CFString")?;

        let mut focused_app: AXUIElementRef = ptr::null_mut();
        let err = AXUIElementCopyAttributeValue(
            system_el,
            focused_attr,
            &mut focused_app as *mut AXUIElementRef as *mut _,
        );
        CFRelease(system_el as _);
        CFRelease(focused_attr as _);

        if err != kAXErrorSuccess || focused_app.is_null() {
            return Err(format!("cannot get focused app: AXError {}", err));
        }

        let mut pid: i32 = 0;
        let _ = AXUIElementGetPid(focused_app, &mut pid);

        let mut collector = TextCollector::new();
        walk_ax_element(focused_app, 0, &mut collector);
        CFRelease(focused_app as _);

        if collector.question_text.is_empty() {
            return Err("no readable text found in focused app's AX tree".to_string());
        }

        let field_type = if collector.choices.is_empty() {
            FieldType::Text
        } else {
            FieldType::Mcq
        };

        Ok(ExtractionResult {
            text: collector.question_text,
            choices: collector.choices,
            field_type,
            ax_pid: if pid != 0 { Some(pid) } else { None },
        })
    }
}

/// Convert a `&str` to a CFStringRef (caller must CFRelease).
#[cfg(target_os = "macos")]
pub(crate) unsafe fn str_to_cfstring(s: &str) -> Option<core_foundation_sys::string::CFStringRef> {
    use core_foundation_sys::string::*;
    use std::ffi::CString;

    let c = CString::new(s).ok()?;
    let cf = CFStringCreateWithCString(std::ptr::null(), c.as_ptr(), kCFStringEncodingUTF8);
    if cf.is_null() { None } else { Some(cf) }
}

#[cfg(target_os = "macos")]
struct TextCollector {
    question_text: String,
    choices: Vec<String>,
}

#[cfg(target_os = "macos")]
impl TextCollector {
    fn new() -> Self {
        Self {
            question_text: String::new(),
            choices: Vec::new(),
        }
    }
}

const MAX_DEPTH: usize = 8;
const MAX_CHILDREN: usize = 50;

#[cfg(target_os = "macos")]
unsafe fn walk_ax_element(
    el: accessibility_sys::AXUIElementRef,
    depth: usize,
    collector: &mut TextCollector,
) {
    use accessibility_sys::*;

    if depth > MAX_DEPTH || el.is_null() {
        return;
    }

    let role = get_ax_attr_str(el, kAXRoleAttribute);
    let value = get_ax_attr_str(el, kAXValueAttribute);
    let title = get_ax_attr_str(el, kAXTitleAttribute);
    let desc = get_ax_attr_str(el, kAXDescriptionAttribute);

    let text_content: Option<String> = [&value, &title, &desc].iter().find_map(|s| {
        s.as_ref().and_then(|t| {
            let trimmed = t.trim();
            if !trimmed.is_empty() {
                Some(trimmed.to_string())
            } else {
                None
            }
        })
    });

    match role.as_deref() {
        Some("AXStaticText") | Some("AXHeading") => {
            if let Some(t) = text_content {
                if t.len() > 5 {
                    debug!("AX text: {}", &t[..t.len().min(60)]);
                    if !collector.question_text.is_empty() {
                        collector.question_text.push('\n');
                    }
                    collector.question_text.push_str(&t);
                }
            }
        }
        Some("AXRadioButton") | Some("AXCheckBox") => {
            if let Some(t) = text_content {
                collector.choices.push(t);
            }
        }
        _ => {}
    }

    for (i, child) in get_ax_children(el).into_iter().enumerate() {
        if i >= MAX_CHILDREN {
            break;
        }
        walk_ax_element(child, depth + 1, collector);
    }
}

/// Read an AX attribute as a String (attr is one of the kAX* &str constants).
#[cfg(target_os = "macos")]
pub(crate) unsafe fn get_ax_attr_str(
    el: accessibility_sys::AXUIElementRef,
    attr: &str,
) -> Option<String> {
    use accessibility_sys::*;
    use core_foundation_sys::base::{CFGetTypeID, CFRelease, CFTypeRef};
    use core_foundation_sys::string::{
        CFStringGetCStringPtr, CFStringGetLength, CFStringGetTypeID,
        kCFStringEncodingUTF8,
    };

    let cf_attr = str_to_cfstring(attr)?;
    let mut val: CFTypeRef = std::ptr::null();
    let err = AXUIElementCopyAttributeValue(el, cf_attr, &mut val);
    CFRelease(cf_attr as _);

    if err != kAXErrorSuccess || val.is_null() {
        return None;
    }

    // Verify it's a CFString before reading
    if CFGetTypeID(val) != CFStringGetTypeID() {
        CFRelease(val);
        return None;
    }

    // Fast path: get a direct C pointer if the string is in UTF-8 storage
    let c_ptr = CFStringGetCStringPtr(val as _, kCFStringEncodingUTF8);
    let rust_str = if !c_ptr.is_null() {
        std::ffi::CStr::from_ptr(c_ptr).to_string_lossy().into_owned()
    } else {
        // Slow path: allocate a buffer
        let len = CFStringGetLength(val as _);
        if len == 0 {
            CFRelease(val);
            return None;
        }
        let buf_size = (len as usize) * 4 + 1; // UTF-8 max 4 bytes/char
        let mut buf = vec![0u8; buf_size];
        let ok = core_foundation_sys::string::CFStringGetCString(
            val as _,
            buf.as_mut_ptr() as _,
            buf_size as _,
            kCFStringEncodingUTF8,
        );
        if ok == 0 {
            CFRelease(val);
            return None;
        }
        let end = buf.iter().position(|&b| b == 0).unwrap_or(buf.len());
        String::from_utf8_lossy(&buf[..end]).into_owned()
    };

    CFRelease(val);
    if rust_str.is_empty() { None } else { Some(rust_str) }
}

/// Get AX children as a Vec of AXUIElementRef.
#[cfg(target_os = "macos")]
pub(crate) unsafe fn get_ax_children(
    el: accessibility_sys::AXUIElementRef,
) -> Vec<accessibility_sys::AXUIElementRef> {
    use accessibility_sys::*;
    use core_foundation_sys::array::{CFArrayGetCount, CFArrayGetValueAtIndex};
    use core_foundation_sys::base::{CFRelease, CFTypeRef};

    let cf_attr = match str_to_cfstring(kAXChildrenAttribute) {
        Some(s) => s,
        None => return vec![],
    };

    let mut val: CFTypeRef = std::ptr::null();
    let err = AXUIElementCopyAttributeValue(el, cf_attr, &mut val);
    CFRelease(cf_attr as _);

    if err != kAXErrorSuccess || val.is_null() {
        return vec![];
    }

    let count = CFArrayGetCount(val as _);
    let mut children = Vec::with_capacity(count as usize);
    for i in 0..count {
        let child = CFArrayGetValueAtIndex(val as _, i) as AXUIElementRef;
        if !child.is_null() {
            children.push(child);
        }
    }
    CFRelease(val);
    children
}

// ── Public helpers for filler.rs ──────────────────────────────────────────────

#[cfg(target_os = "macos")]
pub unsafe fn get_ax_string_pub(
    el: accessibility_sys::AXUIElementRef,
    attr: &str,
) -> Option<String> {
    get_ax_attr_str(el, attr)
}

#[cfg(target_os = "macos")]
pub unsafe fn get_ax_children_pub(
    el: accessibility_sys::AXUIElementRef,
) -> Vec<accessibility_sys::AXUIElementRef> {
    get_ax_children(el)
}
