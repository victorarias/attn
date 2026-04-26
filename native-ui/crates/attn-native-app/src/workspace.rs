/// `Workspace` is the GPUI entity that wraps the daemon's wire-level
/// `attn_protocol::Workspace` plus the canvas panels the user has placed
/// inside it. Sidebar and canvas both hold cloned `Entity<Workspace>`
/// handles and observe independently — `cx.notify()` re-renders both.
///
/// Note the name collision: `attn_protocol::Workspace` is the wire data
/// type, `crate::workspace::Workspace` is this GPUI entity. They're
/// distinct on purpose — the entity owns extra UI-only state (panels)
/// the daemon doesn't know or care about.
use attn_protocol::{Workspace as ProtocolWorkspace, WorkspaceStatus};
use gpui::{Context, EventEmitter, SharedString};

use crate::panel::Panel;

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
            panels,
        }
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
        if changed {
            cx.emit(WorkspaceUpdated);
            cx.notify();
        }
    }
}
