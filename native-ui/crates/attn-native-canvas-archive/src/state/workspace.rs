/// `Workspace` is the GPUI entity that wraps the daemon's wire-level
/// `attn_protocol::Workspace` plus the live GPUI panel views for the
/// daemon-owned panel geometry. Sidebar and canvas both hold cloned
/// `Entity<Workspace>`
/// handles and observe independently — `cx.notify()` re-renders both.
///
/// Note the name collision: `attn_protocol::Workspace` is the wire data
/// type, `crate::workspace::Workspace` is this GPUI entity. They're
/// distinct on purpose — the entity owns live view handles, while
/// durable panel geometry comes from the daemon snapshot.
use attn_protocol::{Workspace as ProtocolWorkspace, WorkspacePanel, WorkspaceStatus};
use gpui::{Context, EventEmitter, SharedString};
use serde_json::{json, Value};

use crate::state::panel::Panel;

/// Emitted when the workspace's wire-level data changes (rolled-up status,
/// title, directory). Subscribers are the sidebar (status badge) and the
/// canvas (panel border colour, if we add focus styling later).
#[derive(Debug, Clone)]
pub struct WorkspaceUpdated;

pub struct Workspace {
    pub id: SharedString,
    pub title: SharedString,
    pub directory: SharedString,
    pub status: WorkspaceStatus,
    pub daemon_panels: Vec<WorkspacePanel>,
    pub panels: Vec<Panel>,
}

impl EventEmitter<WorkspaceUpdated> for Workspace {}

impl Workspace {
    pub fn new(data: ProtocolWorkspace, panels: Vec<Panel>) -> Self {
        Self {
            id: SharedString::from(data.id),
            title: SharedString::from(data.title),
            directory: SharedString::from(data.directory),
            status: data.status,
            daemon_panels: data.panels,
            panels,
        }
    }

    /// JSON view used by the UI automation server. Pulled into the
    /// workspace itself so the snapshot shape lives next to the data;
    /// callers (`NativeApp::automation_snapshot`) just stitch them
    /// together.
    pub fn automation_snapshot(&self) -> Value {
        let panels: Vec<Value> = self.panels.iter().map(panel_snapshot).collect();
        json!({
            "id": self.id.to_string(),
            "title": self.title.to_string(),
            "directory": self.directory.to_string(),
            "status": self.status.to_string(),
            "panels": panels,
        })
    }

    /// Apply a fresh wire snapshot. Returns true if anything user-visible
    /// changed — caller decides whether to `cx.emit` + `cx.notify`.
    pub fn apply_snapshot(&mut self, data: ProtocolWorkspace, cx: &mut Context<Self>) {
        let mut changed = false;
        if self.title.as_ref() != data.title {
            self.title = SharedString::from(data.title);
            changed = true;
        }
        if self.directory.as_ref() != data.directory {
            self.directory = SharedString::from(data.directory);
            changed = true;
        }
        if self.status != data.status {
            self.status = data.status;
            changed = true;
        }
        if self.daemon_panels != data.panels {
            self.daemon_panels = data.panels;
            changed = true;
        }
        if changed {
            cx.emit(WorkspaceUpdated);
            cx.notify();
        }
    }
}

fn panel_snapshot(panel: &Panel) -> Value {
    json!({
        "id": panel.id,
        "daemon_panel_id": panel.daemon_panel_id.to_string(),
        "kind": "terminal",
        "title": panel.title.to_string(),
        "session_id": panel.session_id.to_string(),
        "world_x": panel.world_x,
        "world_y": panel.world_y,
        "width": panel.width,
        "height": panel.height,
        "session_state": panel.session_state.to_string(),
        "needs_review_after_long_run": panel.needs_review_after_long_run,
    })
}
