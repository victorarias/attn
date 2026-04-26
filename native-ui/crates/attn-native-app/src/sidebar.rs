/// Fixed-width left rail. One row per workspace. Status badge in front of
/// the title. Clicking a row asks the parent (`Spike5App`) to switch
/// selection. The sidebar holds cloned `Entity<Workspace>` handles and
/// observes each — when a workspace's status changes, only that row
/// re-renders.
use attn_protocol::WorkspaceStatus;
use gpui::{
    div, prelude::*, px, rgb, Context, Entity, FocusHandle, Focusable, MouseButton, ParentElement,
    Render, SharedString, Window,
};

use crate::workspace::Workspace;

pub const SIDEBAR_WIDTH: f32 = 240.0;

pub struct Sidebar {
    workspaces: Vec<Entity<Workspace>>,
    selected_id: Option<SharedString>,
    /// Callback fired when the user clicks a row. Set up by `Spike5App`
    /// at construction time so the app can swap the canvas's selected
    /// workspace handle.
    on_select: Box<dyn Fn(SharedString, &mut Window, &mut gpui::App) + 'static>,
    focus_handle: FocusHandle,
}

impl Sidebar {
    pub fn new(
        workspaces: Vec<Entity<Workspace>>,
        on_select: impl Fn(SharedString, &mut Window, &mut gpui::App) + 'static,
        cx: &mut Context<Self>,
    ) -> Self {
        // Re-render this whole view when any member workspace updates.
        // Cheap: the row count is small and rendering is just a div tree.
        for ws in &workspaces {
            cx.observe(ws, |_, _, cx| cx.notify()).detach();
        }
        Self {
            workspaces,
            selected_id: None,
            on_select: Box::new(on_select),
            focus_handle: cx.focus_handle(),
        }
    }

    /// Add a workspace handle. Called by `Spike5App` on `WorkspaceRegistered`.
    /// `Spike5App` guards duplicates upstream (same id → same `Entity<Workspace>`
    /// reused), so this is a pure insert — re-inserting the same id is a no-op.
    pub fn upsert_workspace(&mut self, ws: Entity<Workspace>, cx: &mut Context<Self>) {
        let id = ws.read(cx).id.clone();
        if self.workspaces.iter().any(|existing| existing.read(cx).id == id) {
            return;
        }
        cx.observe(&ws, |_, _, cx| cx.notify()).detach();
        self.workspaces.push(ws);
        cx.notify();
    }

    pub fn remove_workspace(&mut self, id: &str, cx: &mut Context<Self>) {
        self.workspaces.retain(|ws| ws.read(cx).id.as_ref() != id);
        if self.selected_id.as_ref().map(|s| s.as_ref()) == Some(id) {
            self.selected_id = None;
        }
        cx.notify();
    }

    pub fn set_selected(&mut self, id: Option<SharedString>, cx: &mut Context<Self>) {
        if self.selected_id != id {
            self.selected_id = id;
            cx.notify();
        }
    }
}

impl Focusable for Sidebar {
    fn focus_handle(&self, _cx: &gpui::App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for Sidebar {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let rows: Vec<gpui::AnyElement> = self
            .workspaces
            .iter()
            .map(|ws_entity| {
                let ws = ws_entity.read(cx);
                let id = ws.id.clone();
                let title = ws.title.clone();
                let status = ws.status;
                let selected = self.selected_id.as_ref() == Some(&id);
                let click_id = id.clone();
                row(id.clone(), title, status, selected)
                    .on_mouse_down(MouseButton::Left, cx.listener(move |this, _, window, cx| {
                        let id = click_id.clone();
                        (this.on_select)(id.clone(), window, cx);
                        this.set_selected(Some(id), cx);
                    }))
                    .into_any_element()
            })
            .collect();

        div()
            .w(px(SIDEBAR_WIDTH))
            .h_full()
            .bg(rgb(0x1a1a22))
            .border_r_1()
            .border_color(rgb(0x2a2a35))
            .flex()
            .flex_col()
            .child(
                div()
                    .px_4()
                    .py_3()
                    .text_color(rgb(0x8a8a95))
                    .text_size(px(11.))
                    .child(SharedString::from("WORKSPACES")),
            )
            .children(rows)
    }
}

/// One row. Pulled out so the click handler in `render` stays readable.
fn row(
    _id: SharedString,
    title: SharedString,
    status: WorkspaceStatus,
    selected: bool,
) -> gpui::Div {
    let bg = if selected { rgb(0x2a2a3a) } else { rgb(0x1a1a22) };
    div()
        .w_full()
        .px_4()
        .py_2()
        .flex()
        .items_center()
        .gap_2()
        .bg(bg)
        .text_color(rgb(0xe0e0eb))
        .text_size(px(13.))
        .child(status_badge(status))
        .child(title)
}

/// Coloured dot reflecting the workspace's rolled-up status. The colour
/// vocabulary mirrors the Tauri sidebar's session badges so attn feels
/// consistent across frontends.
fn status_badge(status: WorkspaceStatus) -> impl IntoElement {
    let color = match status {
        WorkspaceStatus::Working => rgb(0x4caf50),       // green
        WorkspaceStatus::WaitingInput => rgb(0xffc107),  // amber
        WorkspaceStatus::PendingApproval => rgb(0xff9800), // orange
        WorkspaceStatus::Idle => rgb(0x6a6a78),          // grey
        WorkspaceStatus::Launching => rgb(0x2196f3),     // blue
    };
    div().w(px(8.)).h(px(8.)).rounded_full().bg(color)
}
