use std::{
    collections::VecDeque,
    sync::{
        atomic::{AtomicU64, Ordering},
        LazyLock, Mutex,
    },
    time::{SystemTime, UNIX_EPOCH},
};

use serde::Serialize;
use serde_json::{json, Value};

const CAPACITY: usize = 1024;
static NEXT_ID: AtomicU64 = AtomicU64::new(1);
static EVENTS: LazyLock<Mutex<VecDeque<Event>>> =
    LazyLock::new(|| Mutex::new(VecDeque::with_capacity(CAPACITY)));

#[derive(Clone, Debug, Serialize)]
pub struct Event {
    pub id: u64,
    pub ts_ms: u64,
    pub category: String,
    pub payload: Value,
}

pub fn record(category: impl Into<String>, payload: Value) {
    let event = Event {
        id: NEXT_ID.fetch_add(1, Ordering::Relaxed),
        ts_ms: SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_millis() as u64)
            .unwrap_or_default(),
        category: category.into(),
        payload,
    };
    if let Ok(mut events) = EVENTS.lock() {
        if events.len() == CAPACITY {
            events.pop_front();
        }
        events.push_back(event);
    }
}

pub fn tail(cursor: u64) -> Value {
    let events = EVENTS
        .lock()
        .map(|events| {
            events
                .iter()
                .filter(|event| event.id > cursor)
                .cloned()
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    let next_cursor = events.last().map(|event| event.id).unwrap_or(cursor);
    json!({ "events": events, "next_cursor": next_cursor })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tail_advances_past_new_event() {
        let cursor = NEXT_ID.load(Ordering::Relaxed).saturating_sub(1);
        record("test", json!({"ok": true}));
        let result = tail(cursor);
        assert!(result["next_cursor"].as_u64().unwrap() > cursor);
    }
}
