/// Workspace root view. Owns the live `Vec<Entity<Workspace>>` (the
/// authoritative list, sidebar and canvas just hold cloned handles), and
/// subscribes to `DaemonClient` to grow/shrink it as workspaces and
/// sessions appear and vanish on the wire.
///
/// Layout: sidebar pinned left at fixed width, canvas fills the rest.
use std::collections::HashMap;
use std::sync::atomic::{AtomicUsize, Ordering};

use attn_protocol::{AttachSessionMessage, Session};
use gpui::{div, prelude::*, rgb, Context, Entity, ParentElement, Render, SharedString, Window};

use crate::daemon_client::{DaemonClient, DaemonEvent};
use crate::panel::{Panel, PanelContent};
use crate::sidebar::Sidebar;
use crate::spike5_canvas::Spike5Canvas;
use crate::terminal_model::TerminalModel;
use crate::terminal_view::TerminalView;
use crate::workspace::Workspace;

/// Initial terminal panel size in world-space units. Roughly 380×240
/// matches the spike-4 default and gives ~48 cols × ~12 rows once the
/// title bar is subtracted.
const TERMINAL_W: f32 = 380.0;
const TERMINAL_H: f32 = 240.0;

pub struct Spike5App {
    daemon: Entity<DaemonClient>,
    workspaces_by_id: HashMap<SharedString, Entity<Workspace>>,
    sidebar: Entity<Sidebar>,
    canvas: Entity<Spike5Canvas>,
    selected_id: Option<SharedString>,
}

impl Spike5App {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        let canvas = cx.new(|cx| Spike5Canvas::new(daemon.clone(), cx));
        let canvas_for_select = canvas.clone();
        let app_handle = cx.entity().downgrade();
        let sidebar = cx.new(|cx| {
            Sidebar::new(
                Vec::new(),
                move |id, _window, cx| {
                    let app_handle = app_handle.clone();
                    let canvas = canvas_for_select.clone();
                    let _ = app_handle.update(cx, |app: &mut Spike5App, cx| {
                        let ws = app.workspaces_by_id.get(&id).cloned();
                        canvas.update(cx, |canvas, cx| canvas.set_selected(ws, cx));
                        app.selected_id = Some(id);
                    });
                },
                cx,
            )
        });

        cx.subscribe(&daemon, |this, _client, event: &DaemonEvent, cx| match event {
            DaemonEvent::WorkspaceRegistered { workspace } => {
                this.upsert_workspace(workspace.clone(), cx);
                this.sync_terminal_panels(cx);
            }
            DaemonEvent::WorkspaceUnregistered { workspace_id } => {
                this.remove_workspace(workspace_id.clone(), cx);
            }
            DaemonEvent::WorkspaceStateChanged { workspace } => {
                this.apply_workspace_snapshot(workspace.clone(), cx);
            }
            DaemonEvent::SessionsChanged | DaemonEvent::Connected => {
                this.sync_terminal_panels(cx);
            }
            _ => {}
        })
        .detach();

        Self {
            daemon,
            workspaces_by_id: HashMap::new(),
            sidebar,
            canvas,
            selected_id: None,
        }
    }

    fn upsert_workspace(&mut self, data: attn_protocol::Workspace, cx: &mut Context<Self>) {
        let id = SharedString::from(data.id.clone());
        if let Some(existing) = self.workspaces_by_id.get(&id) {
            existing.update(cx, |ws, cx| ws.apply_snapshot(data.clone(), cx));
            return;
        }
        let entity = cx.new(|_| Workspace::new(data, Vec::new()));
        self.workspaces_by_id.insert(id.clone(), entity.clone());
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.upsert_workspace(entity.clone(), cx));

        // First workspace to appear becomes the canvas's initial selection.
        if self.selected_id.is_none() {
            self.selected_id = Some(id.clone());
            self.canvas
                .update(cx, |canvas, cx| canvas.set_selected(Some(entity), cx));
            self.sidebar
                .update(cx, |sidebar, cx| sidebar.set_selected(Some(id), cx));
        }
    }

    fn apply_workspace_snapshot(
        &mut self,
        data: attn_protocol::Workspace,
        cx: &mut Context<Self>,
    ) {
        let id = SharedString::from(data.id.clone());
        if let Some(existing) = self.workspaces_by_id.get(&id) {
            existing.update(cx, |ws, cx| ws.apply_snapshot(data, cx));
        } else {
            // State change for a workspace we haven't seen — treat as a
            // late registration. Daemon ordering says this shouldn't
            // happen, but the cost of being defensive is one line.
            self.upsert_workspace(data, cx);
        }
    }

    fn remove_workspace(&mut self, id: String, cx: &mut Context<Self>) {
        let id = SharedString::from(id);
        if self.workspaces_by_id.remove(&id).is_none() {
            return;
        }
        let id_str = id.clone();
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.remove_workspace(&id_str, cx));
        if self.selected_id.as_ref() == Some(&id) {
            self.selected_id = None;
            self.canvas
                .update(cx, |canvas, cx| canvas.set_selected(None, cx));
        }
    }

    /// Walk current sessions and ensure every session whose
    /// `workspace_id` matches a known workspace has a corresponding
    /// Terminal panel. Idempotent — duplicates are skipped by id.
    fn sync_terminal_panels(&mut self, cx: &mut Context<Self>) {
        // Snapshot sessions out of the daemon read borrow before we
        // start mutating workspaces.
        let sessions: Vec<Session> = self.daemon.read(cx).sessions().to_vec();

        for session in sessions {
            let Some(ws_id) = session.workspace_id.as_deref() else {
                continue;
            };
            let key = SharedString::from(ws_id.to_string());
            let Some(ws_entity) = self.workspaces_by_id.get(&key).cloned() else {
                continue;
            };

            let already_present = ws_entity.read(cx).panels.iter().any(|p| matches!(
                &p.content,
                PanelContent::Terminal { session_id, .. } if session_id.as_ref() == session.id
            ));
            if already_present {
                continue;
            }

            // Find a non-overlapping x position by counting existing
            // terminal panels in this workspace.
            let existing = ws_entity
                .read(cx)
                .panels
                .iter()
                .filter(|p| matches!(p.content, PanelContent::Terminal { .. }))
                .count();
            let world_x = 30.0 + existing as f32 * (TERMINAL_W + 30.0);
            let world_y = 50.0;

            let session_id = session.id.clone();
            let label = session.label.clone();

            // Default cols/rows derived from world-space size; the
            // canvas re-pushes content_size each frame so these will
            // be corrected on first render if needed.
            let (cols, rows) = panel_terminal_dims(TERMINAL_W, TERMINAL_H);

            let daemon = self.daemon.clone();
            let model = cx.new(|cx| TerminalModel::new(session_id.clone(), cols, rows, &daemon, cx));
            let view = cx.new(|cx| {
                let mut tv = TerminalView::new(model, daemon.clone(), cx);
                tv.set_content_size(TERMINAL_W, (TERMINAL_H - TITLE_HEIGHT).max(0.0));
                tv
            });

            // Send attach. The TerminalView's render path will emit the
            // initial PtyResize once it sees its first content_size.
            self.daemon
                .read(cx)
                .send_cmd(&AttachSessionMessage::new(session_id.clone()));

            let panel = Panel {
                id: next_panel_id(),
                title: SharedString::from(label),
                world_x,
                world_y,
                width: TERMINAL_W,
                height: TERMINAL_H,
                content: PanelContent::Terminal {
                    session_id: SharedString::from(session_id),
                    view,
                },
            };

            ws_entity.update(cx, |ws, cx| {
                ws.panels.push(panel);
                cx.notify();
            });
        }
    }
}

impl Render for Spike5App {
    fn render(&mut self, _window: &mut Window, _cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .size_full()
            .flex()
            .flex_row()
            .bg(rgb(0x0e0e14))
            .child(self.sidebar.clone())
            .child(div().flex_1().child(self.canvas.clone()))
    }
}

/// Process-wide monotonically-increasing panel ID. Panels are keyed by
/// id for hit testing so collisions across workspaces would mis-target
/// drag/resize.
static NEXT_PANEL_ID: AtomicUsize = AtomicUsize::new(1);

fn next_panel_id() -> usize {
    NEXT_PANEL_ID.fetch_add(1, Ordering::Relaxed)
}

/// Mirror of the canvas's title-bar height. Kept in this file so the
/// initial content_size matches the canvas's per-frame value.
const TITLE_HEIGHT: f32 = 24.0;

fn panel_terminal_dims(world_w: f32, world_h: f32) -> (u16, u16) {
    use crate::terminal_view::{CHAR_WIDTH, ROW_HEIGHT};
    let cols = ((world_w / CHAR_WIDTH) as u16).max(1);
    let rows = (((world_h - TITLE_HEIGHT) / ROW_HEIGHT) as u16).max(1);
    (cols, rows)
}
