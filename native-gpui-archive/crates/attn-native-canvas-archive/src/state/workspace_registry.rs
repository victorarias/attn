//! Owner of the live workspace map and the in-flight wire-ack tracking
//! that goes with it. Plain data + methods — not a GPUI entity. The
//! coordinator (`NativeApp`) holds one of these and consumes its
//! outcomes to fan changes out to the sidebar and canvas.
//!
//! Why this exists: `NativeApp` previously owned `workspaces_by_id`,
//! `selected_id`, `pending_select_workspace_ids`, and `pending_spawns`
//! directly, plus all the methods that mutate them. That made the root
//! view simultaneously a coordinator, a workspace registry, and a
//! pending-ack tracker — too many jobs for one struct. Pulling the data
//! and pure mutations here leaves `NativeApp` doing only what root
//! views should: wiring adapters to state, and state to views.
use std::collections::{HashMap, HashSet};

use attn_protocol::Workspace as ProtocolWorkspace;
use gpui::{App, AppContext, Context, Entity, SharedString};

use crate::app::NativeApp;
use crate::domain::panel_placement::Rect;
use crate::state::workspace::Workspace;

/// In-flight spawn metadata. We track just enough to attribute a
/// `SpawnResult` failure back to the workspace + agent the user
/// selected when they triggered the spawn.
#[derive(Debug, Clone)]
pub struct PendingSpawn {
    pub workspace_id: SharedString,
    pub agent: SharedString,
    pub initial_placement: Option<Rect>,
    pub focus_after_spawn: bool,
}

/// Outcome of an `upsert` — distinguishes "we just learned about this
/// workspace" (caller needs to attach it to the sidebar, possibly
/// auto-select) from "we updated a workspace we already knew about".
pub enum UpsertOutcome {
    NewlyInserted(Entity<Workspace>),
    UpdatedExisting,
}

#[derive(Default)]
pub struct WorkspaceRegistry {
    workspaces_by_id: HashMap<SharedString, Entity<Workspace>>,
    selected_id: Option<SharedString>,
    /// Workspace ids the user (or an automation action) asked to select
    /// before the workspace had registered yet. When the corresponding
    /// `WorkspaceRegistered` lands, the coordinator drains the pending id
    /// and calls its select path.
    pending_select: HashSet<SharedString>,
    /// Spawns we've issued but the daemon hasn't acked yet. Keyed by
    /// session id (the same one we sent on the wire). Consumed on
    /// `SpawnResult` to attribute the outcome back to a workspace + agent.
    pending_spawns: HashMap<SharedString, PendingSpawn>,
    /// Successful spawns whose daemon panel has not appeared locally yet.
    /// Kept separate from `pending_spawns` because the workspace broadcast
    /// and spawn result can arrive in either order on reconnect-heavy paths.
    pending_panel_placements: HashMap<SharedString, PendingSpawn>,
    placed_spawn_panels: HashSet<SharedString>,
}

impl WorkspaceRegistry {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn workspace(&self, id: &str) -> Option<Entity<Workspace>> {
        self.workspaces_by_id
            .get(&SharedString::from(id.to_string()))
            .cloned()
    }

    pub fn workspaces(&self) -> impl Iterator<Item = &Entity<Workspace>> {
        self.workspaces_by_id.values()
    }

    pub fn len(&self) -> usize {
        self.workspaces_by_id.len()
    }

    pub fn selected_id(&self) -> Option<&SharedString> {
        self.selected_id.as_ref()
    }

    pub fn set_selected(&mut self, id: Option<SharedString>) {
        self.selected_id = id;
    }

    /// Insert a fresh workspace entity, or apply a snapshot to one we
    /// already have. Returns whether this was a new insertion (caller
    /// fans out to sidebar etc.) or a quiet update.
    ///
    /// The `Context<NativeApp>` parameter is a type-level upward
    /// reference, not a runtime cycle: GPUI's `cx.new`/`Entity::update`
    /// signatures use an associated `Result<T>` type that defeats a
    /// generic-over-`AppContext` formulation. The alternative — making
    /// the caller construct the entity and pass it in — would push the
    /// "is this id already present?" check up to every call site. This
    /// is the smaller compromise.
    pub fn upsert(
        &mut self,
        data: ProtocolWorkspace,
        cx: &mut Context<NativeApp>,
    ) -> UpsertOutcome {
        let id = SharedString::from(data.id.clone());
        if let Some(existing) = self.workspaces_by_id.get(&id) {
            existing.update(cx, |ws, cx| ws.apply_snapshot(data, cx));
            return UpsertOutcome::UpdatedExisting;
        }
        let entity = cx.new(|_| Workspace::new(data, Vec::new()));
        self.workspaces_by_id.insert(id, entity.clone());
        UpsertOutcome::NewlyInserted(entity)
    }

    /// Apply a wire snapshot to an existing workspace; if the workspace
    /// is unknown (out-of-order broadcast), fall back to insertion.
    pub fn apply_snapshot(
        &mut self,
        data: ProtocolWorkspace,
        cx: &mut Context<NativeApp>,
    ) -> UpsertOutcome {
        let id = SharedString::from(data.id.clone());
        if let Some(existing) = self.workspaces_by_id.get(&id) {
            existing.update(cx, |ws, cx| ws.apply_snapshot(data, cx));
            UpsertOutcome::UpdatedExisting
        } else {
            // State change for a workspace we haven't seen — treat as a
            // late registration. Daemon ordering says this shouldn't
            // happen, but the cost of being defensive is one line.
            self.upsert(data, cx)
        }
    }

    /// Drop the workspace with this id. Also clears any pending-select
    /// entry for it. Returns whether it was present.
    pub fn remove(&mut self, id: &SharedString) -> bool {
        let was_present = self.workspaces_by_id.remove(id).is_some();
        if was_present {
            self.pending_select.remove(id);
        }
        was_present
    }

    /// First workspace by title (then id) — used as the new selection
    /// when the currently-selected one is destroyed.
    pub fn fallback_id(&self, cx: &App) -> Option<SharedString> {
        let mut candidates: Vec<(String, String, SharedString)> = self
            .workspaces_by_id
            .values()
            .map(|ws_entity| {
                let ws = ws_entity.read(cx);
                (ws.title.to_string(), ws.id.to_string(), ws.id.clone())
            })
            .collect();
        candidates.sort_by(|a, b| a.0.cmp(&b.0).then_with(|| a.1.cmp(&b.1)));
        candidates.into_iter().next().map(|(_, _, id)| id)
    }

    pub fn record_pending_select(&mut self, id: SharedString) {
        self.pending_select.insert(id);
    }

    /// Returns true if `id` was awaiting selection — caller should run
    /// its select path now.
    pub fn take_pending_select(&mut self, id: &str) -> bool {
        let key = SharedString::from(id.to_string());
        self.pending_select.remove(&key)
    }

    pub fn record_pending_spawn(&mut self, session_id: SharedString, spawn: PendingSpawn) {
        self.pending_spawns.insert(session_id, spawn);
    }

    pub fn take_pending_spawn(&mut self, session_id: &SharedString) -> Option<PendingSpawn> {
        self.pending_spawns.remove(session_id)
    }

    pub fn mark_spawn_succeeded(&mut self, session_id: SharedString, spawn: PendingSpawn) {
        if !self.placed_spawn_panels.contains(&session_id) {
            self.pending_panel_placements.insert(session_id, spawn);
        }
    }

    pub fn pending_spawn_for_panel_placement(
        &self,
        session_id: &SharedString,
    ) -> Option<PendingSpawn> {
        self.pending_panel_placements
            .get(session_id)
            .or_else(|| self.pending_spawns.get(session_id))
            .cloned()
    }

    pub fn mark_panel_placed(&mut self, session_id: SharedString) {
        self.pending_panel_placements.remove(&session_id);
        self.placed_spawn_panels.insert(session_id);
    }
}
