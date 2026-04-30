//! Owner of the live session map. Mirrors `WorkspaceRegistry`: plain
//! data, not a GPUI entity. The coordinator (`NativeApp`) holds one and
//! drives mutations from `DaemonEvent`s; views and automation read
//! through it instead of through the daemon adapter, so the adapter
//! stays cache-free.

use std::collections::HashMap;

use attn_protocol::Session;
use gpui::SharedString;

#[derive(Default)]
pub struct SessionRegistry {
    sessions_by_id: HashMap<SharedString, Session>,
}

impl SessionRegistry {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn len(&self) -> usize {
        self.sessions_by_id.len()
    }

    /// Snapshot as a `Vec<Session>`. Callers consume the list without
    /// holding the registry borrow — `NativeApp` fans it out to
    /// workspaces and the automation snapshot serializes it to JSON.
    pub fn snapshot(&self) -> Vec<Session> {
        self.sessions_by_id.values().cloned().collect()
    }

    /// Insert or update a session by id.
    pub fn upsert(&mut self, session: Session) {
        let id = SharedString::from(session.id.clone());
        self.sessions_by_id.insert(id, session);
    }

    /// Drop the session with this id. Returns whether it was present.
    pub fn remove(&mut self, id: &str) -> bool {
        let key = SharedString::from(id.to_string());
        self.sessions_by_id.remove(&key).is_some()
    }

    /// Replace the entire set. Used for `InitialState` and the daemon's
    /// bulk `SessionsUpdated` broadcast.
    pub fn replace_all(&mut self, sessions: Vec<Session>) {
        self.sessions_by_id.clear();
        for s in sessions {
            self.upsert(s);
        }
    }
}
