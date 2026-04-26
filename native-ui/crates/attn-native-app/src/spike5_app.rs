/// Spike 5 root view. Owns the live `Vec<Entity<Workspace>>` (the
/// authoritative list, sidebar and canvas just hold cloned handles), and
/// subscribes to `DaemonClient` to grow/shrink it as workspaces appear
/// and vanish on the wire.
///
/// Layout: sidebar pinned left at fixed width, canvas fills the rest.
use std::collections::HashMap;

use gpui::{
    div, prelude::*, rgb, App, Context, Entity, ParentElement, Render, SharedString, Window,
};

use crate::daemon_client::{DaemonClient, DaemonEvent};
use crate::panel::{Panel, PanelContent, PlaceholderView};
use crate::sidebar::Sidebar;
use crate::spike5_canvas::Spike5Canvas;
use crate::workspace::Workspace;

pub struct Spike5App {
    #[allow(dead_code)]
    daemon: Entity<DaemonClient>,
    workspaces_by_id: HashMap<SharedString, Entity<Workspace>>,
    sidebar: Entity<Sidebar>,
    canvas: Entity<Spike5Canvas>,
    selected_id: Option<SharedString>,
}

impl Spike5App {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        let canvas = cx.new(|_| Spike5Canvas::new());
        let canvas_for_select = canvas.clone();
        let app_handle = cx.entity().downgrade();
        let sidebar = cx.new(|cx| {
            Sidebar::new(
                Vec::new(),
                move |id, _window, cx| {
                    // Resolve the workspace entity, hand it to the canvas,
                    // and remember the selection on the app.
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

        // Forward DaemonClient events into our own state. We subscribe to
        // the daemon entity, not its raw stream, so GPUI handles the
        // re-render machinery for us.
        cx.subscribe(&daemon, |this, _client, event: &DaemonEvent, cx| {
            match event {
                DaemonEvent::WorkspaceRegistered { workspace } => {
                    this.upsert_workspace(workspace.clone(), cx);
                }
                DaemonEvent::WorkspaceUnregistered { workspace_id } => {
                    this.remove_workspace(workspace_id.clone(), cx);
                }
                DaemonEvent::WorkspaceStateChanged { workspace } => {
                    this.apply_workspace_snapshot(workspace.clone(), cx);
                }
                _ => {}
            }
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
        // First time we've seen this workspace — seed it with two demo
        // panels so the spike has something to render. A real native UI
        // would build panels from persisted layout state instead.
        let panels = make_demo_panels(&id, cx);
        let entity = cx.new(|_| Workspace::new(data, panels));
        self.workspaces_by_id.insert(id.clone(), entity.clone());
        self.sidebar.update(cx, |sidebar, cx| sidebar.upsert_workspace(entity.clone(), cx));

        // First workspace to appear becomes the canvas's initial selection.
        if self.selected_id.is_none() {
            self.selected_id = Some(id.clone());
            self.canvas.update(cx, |canvas, cx| canvas.set_selected(Some(entity), cx));
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
            // late registration (daemon ordering guarantees say this
            // shouldn't happen, but the cost of being defensive is one
            // line).
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
            self.canvas.update(cx, |canvas, cx| canvas.set_selected(None, cx));
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

/// Two panels for every workspace as it appears: one Placeholder, one
/// Terminal stub. Demonstrates the enum dispatching across distinct
/// render paths without requiring real PTY wiring in this spike.
fn make_demo_panels(workspace_id: &SharedString, cx: &mut App) -> Vec<Panel> {
    let placeholder_label =
        SharedString::from(format!("Todo Panel · {workspace_id}"));
    let placeholder_view = cx.new(|_| PlaceholderView::new(placeholder_label));
    let session_id = SharedString::from(format!("{workspace_id}-demo"));
    vec![
        Panel {
            id: 1,
            title: SharedString::from("Notes"),
            world_x: 60.0,
            world_y: 60.0,
            width: 280.0,
            height: 180.0,
            content: PanelContent::Placeholder(placeholder_view),
        },
        Panel {
            id: 2,
            title: SharedString::from("Agent"),
            world_x: 380.0,
            world_y: 60.0,
            width: 320.0,
            height: 200.0,
            content: PanelContent::Terminal { session_id },
        },
    ]
}
