//! hotkey.rs — global ⌘+Shift+M event listener
//!
//! Uses `rdev` to install a system-wide event tap. Requires Input Monitoring
//! permission (kTCCServiceListenEvent).

use log::{debug, warn};
use rdev::{Event, EventType, Key};
use std::collections::HashSet;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

/// Start listening for global keyboard events. Blocks indefinitely.
/// `on_fire` is called every time ⌘+Shift+M is detected.
pub fn listen<F>(on_fire: F)
where
    F: Fn() + Send + Sync + 'static,
{
    let on_fire = Arc::new(on_fire);
    let mut held: HashSet<Key> = HashSet::new();
    // Store last fire time as millis since epoch to allow Fn (not FnMut) in move closure
    let last_fired_ms = Arc::new(AtomicU64::new(0));

    let callback = move |event: Event| {
        match event.event_type {
            EventType::KeyPress(ref key) => {
                held.insert(key.clone());
                debug!("keypress: {:?}", key);

                let cmd = held.contains(&Key::MetaLeft) || held.contains(&Key::MetaRight);
                let shift = held.contains(&Key::ShiftLeft) || held.contains(&Key::ShiftRight);
                let m = held.contains(&Key::KeyM);

                if cmd && shift && m {
                    // Debounce: ignore repeat fires within 1 second
                    let now_ms = std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .map(|d| d.as_millis() as u64)
                        .unwrap_or(0);
                    let prev = last_fired_ms.load(Ordering::Relaxed);
                    if now_ms.saturating_sub(prev) > 1000 {
                        last_fired_ms.store(now_ms, Ordering::Relaxed);
                        (on_fire)();
                    }
                }
            }
            EventType::KeyRelease(ref key) => {
                held.remove(key);
            }
            _ => {}
        }
    };

    if let Err(e) = rdev::listen(callback) {
        warn!("hotkey listener error: {:?}", e);
        warn!(
            "If you see a permissions error, grant Input Monitoring in \
             System Preferences > Privacy & Security > Input Monitoring"
        );
    }
}
