//! In-memory ring buffer of structured events for diagnosing UI behavior
//! end-to-end. Externally observable via the automation `tail_events`
//! action.
//!
//! ## Why this exists
//!
//! When something behaves wrong end-to-end (a session spawns but no panel
//! attaches, a workspace switch doesn't take effect, a typed command
//! doesn't echo), the only way to figure out where in the chain it broke
//! is to see what each layer saw and what it did. `eprintln!` would do for
//! one debugging session but doesn't survive into the real app — we need a
//! structured stream that test scripts AND production debugging both
//! consume. This is the same pattern Playwright tracing or Chrome DevTools'
//! performance log expose.
//!
//! ## Design
//!
//! - Single global ring buffer (bounded; oldest dropped on overflow).
//! - Monotonic 64-bit ids; consumers poll with a `since_id` cursor.
//! - Mutex-protected. Lock contention is fine for our volume — emission
//!   sites fire at the rate of UI state changes (low hundreds/sec at peak).
//! - Always-on. The buffer's memory cost is bounded (~150 KB at full
//!   capacity), and the cost of an emission is one `Mutex` acquire +
//!   `VecDeque::push_back`.
//!
//! ## Categories
//!
//! Categories are stable string identifiers — pick from this enumerated
//! list when adding new emission sites so consumers can switch on them.
//!
//! - `daemon_event` — payload `{kind, ...}` for every inbound wire event
//!   from the daemon. Excludes `pty_output` (would dominate the buffer).
//! - `sessions_changed_observed` — Spike5App reacted to a SessionsChanged
//!   emit; carries the post-reaction session count.
//! - `workspace_registered_observed`, `workspace_unregistered_observed`,
//!   `workspace_state_changed_observed` — Spike5App processed the matching
//!   daemon event and updated its workspaces map.
//! - `workspace_selected` — payload `{id}`. Sidebar click or automation.
//! - `panel_added` — payload
//!   `{workspace_id, panel_id, session_id, kind}`. Fired by
//!   `sync_terminal_panels` when a new Terminal panel is created.
//! - `panel_pruned` — payload
//!   `{workspace_id, panel_id, session_id}`. Fired when
//!   `sync_terminal_panels` drops a panel whose session disappeared.
//! - `panel_updated` — payload `{workspace_id, panel_id, world_x, world_y,
//!   width, height}`. Fired by `Workspace::update_panel`.
//! - `terminal_attach_processed` — payload `{session_id, success,
//!   has_snapshot, replay_segments}`. Fired when `TerminalModel` finishes
//!   processing an `AttachResult`.

use std::collections::VecDeque;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{LazyLock, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Serialize;
use serde_json::{json, Value};

/// Maximum events kept in the ring buffer. ~2k events covers a long
/// scenario or several seconds of peak UI activity; older events fall out
/// the front.
const CAPACITY: usize = 2048;

static NEXT_ID: AtomicU64 = AtomicU64::new(1);
static LOG: LazyLock<Mutex<VecDeque<Event>>> =
    LazyLock::new(|| Mutex::new(VecDeque::with_capacity(CAPACITY)));

#[derive(Debug, Clone, Serialize)]
pub struct Event {
    /// Monotonic across the process lifetime. Used as the `tail_since`
    /// cursor.
    pub id: u64,
    /// Wall-clock milliseconds since the Unix epoch. Useful for ordering
    /// against external observers (e.g. the daemon's own log).
    pub ts_ms: u64,
    pub category: String,
    pub payload: Value,
}

/// Append an event. Drops the oldest record when the buffer is full.
pub fn record(category: impl Into<String>, payload: Value) {
    let id = NEXT_ID.fetch_add(1, Ordering::Relaxed);
    let ts_ms = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0);
    let event = Event {
        id,
        ts_ms,
        category: category.into(),
        payload,
    };
    if let Ok(mut log) = LOG.lock() {
        if log.len() >= CAPACITY {
            log.pop_front();
        }
        log.push_back(event);
    }
}

/// Pull every event with `id > cursor`, oldest-to-newest.
pub fn tail_since(cursor: u64) -> Vec<Event> {
    match LOG.lock() {
        Ok(log) => log.iter().filter(|e| e.id > cursor).cloned().collect(),
        Err(_) => Vec::new(),
    }
}

/// Build the JSON response for the `tail_events` action. Returns the
/// matching events plus the cursor a caller should send back next time.
pub fn tail_events_response(cursor: u64) -> Value {
    let events = tail_since(cursor);
    let next_cursor = events.last().map(|e| e.id).unwrap_or(cursor);
    json!({
        "events": events,
        "next_cursor": next_cursor,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn current_id() -> u64 {
        NEXT_ID.load(Ordering::Relaxed)
    }

    #[test]
    fn record_then_tail_returns_only_new_events() {
        let cursor = current_id() - 1;
        record("test_record_a", json!({"x": 1}));
        record("test_record_b", json!({"x": 2}));
        let events = tail_since(cursor);
        // Filter to our categories — other tests may have emitted too.
        let ours: Vec<_> = events
            .into_iter()
            .filter(|e| e.category == "test_record_a" || e.category == "test_record_b")
            .collect();
        assert!(ours.len() >= 2);
        // Ids must be strictly increasing.
        for pair in ours.windows(2) {
            assert!(pair[1].id > pair[0].id);
        }
    }

    #[test]
    fn ring_buffer_is_bounded() {
        for i in 0..(CAPACITY + 100) {
            record("test_bounded", json!({"i": i}));
        }
        let log_len = LOG.lock().unwrap().len();
        assert!(
            log_len <= CAPACITY,
            "log grew past capacity: {} > {}",
            log_len,
            CAPACITY
        );
    }

    #[test]
    fn tail_events_response_advances_cursor() {
        let before = current_id() - 1;
        record("test_cursor", json!({}));
        let response = tail_events_response(before);
        let events = response["events"].as_array().unwrap();
        let next_cursor = response["next_cursor"].as_u64().unwrap();
        assert!(!events.is_empty());
        assert!(next_cursor > before);

        // Calling again with the new cursor should return no `test_cursor`
        // events (other tests may still slip in).
        let again = tail_events_response(next_cursor);
        let again_events = again["events"].as_array().unwrap();
        let our_again: Vec<_> = again_events
            .iter()
            .filter(|e| e["category"] == "test_cursor")
            .collect();
        assert!(our_again.is_empty());
    }

    #[test]
    fn empty_tail_keeps_cursor() {
        let response = tail_events_response(u64::MAX);
        let events = response["events"].as_array().unwrap();
        assert!(events.is_empty());
        assert_eq!(response["next_cursor"].as_u64().unwrap(), u64::MAX);
    }
}
