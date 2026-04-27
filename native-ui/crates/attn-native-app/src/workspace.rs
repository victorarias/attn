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
use serde_json::{json, Value};

use crate::automation::events;
use crate::panel::{Panel, PanelContent};

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

    /// JSON view used by the UI automation server. Pulled into the
    /// workspace itself so the snapshot shape lives next to the data;
    /// callers (`Spike5App::automation_snapshot`) just stitch them
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

    /// Apply a partial update to a panel by id. Used by the automation
    /// `move_panel` action. Returns the post-update panel snapshot when
    /// the panel exists, `None` otherwise. Notifies subscribers when any
    /// field actually changes so the canvas re-renders.
    pub fn update_panel(
        &mut self,
        panel_id: usize,
        world_x: Option<f32>,
        world_y: Option<f32>,
        width: Option<f32>,
        height: Option<f32>,
        cx: &mut Context<Self>,
    ) -> Option<Value> {
        let workspace_id = self.id.clone();
        let panel = self.panels.iter_mut().find(|p| p.id == panel_id)?;
        let mut changed = false;
        if let Some(x) = world_x {
            if (panel.world_x - x).abs() > f32::EPSILON {
                panel.world_x = x;
                changed = true;
            }
        }
        if let Some(y) = world_y {
            if (panel.world_y - y).abs() > f32::EPSILON {
                panel.world_y = y;
                changed = true;
            }
        }
        if let Some(w) = width {
            if (panel.width - w).abs() > f32::EPSILON {
                panel.width = w;
                changed = true;
            }
        }
        if let Some(h) = height {
            if (panel.height - h).abs() > f32::EPSILON {
                panel.height = h;
                changed = true;
            }
        }
        if changed {
            events::record(
                "panel_updated",
                json!({
                    "workspace_id": workspace_id.as_ref(),
                    "panel_id": panel.id,
                    "world_x": panel.world_x,
                    "world_y": panel.world_y,
                    "width": panel.width,
                    "height": panel.height,
                }),
            );
            cx.notify();
        }
        Some(panel_snapshot(panel))
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

fn panel_snapshot(panel: &Panel) -> Value {
    let (kind, session_id) = match &panel.content {
        PanelContent::Terminal { session_id, .. } => ("terminal", Some(session_id.to_string())),
        PanelContent::Placeholder(_) => ("placeholder", None),
    };
    json!({
        "id": panel.id,
        "kind": kind,
        "title": panel.title.to_string(),
        "session_id": session_id,
        "world_x": panel.world_x,
        "world_y": panel.world_y,
        "width": panel.width,
        "height": panel.height,
    })
}
