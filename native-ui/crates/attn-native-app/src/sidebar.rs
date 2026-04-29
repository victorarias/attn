/// Fixed-width left rail. One row per workspace. Status badge in front of
/// the title. Clicking a row asks the parent (`NativeApp`) to switch
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

type SelectHandler = dyn Fn(SharedString, &mut Window, &mut gpui::App) + 'static;
type CreateHandler = dyn Fn(&mut Window, &mut gpui::App) + 'static;
type DestroyHandler =
    dyn Fn(SharedString, &mut Window, &mut gpui::App) -> Result<(), String> + 'static;

pub struct Sidebar {
    workspaces: Vec<Entity<Workspace>>,
    selected_id: Option<SharedString>,
    /// Callback fired when the user clicks a row. Set up by `NativeApp`
    /// at construction time so the app can swap the canvas's selected
    /// workspace handle.
    on_select: Box<SelectHandler>,
    /// Callback fired when the user clicks "+ New Workspace". Owns the
    /// directory picker → daemon `register_workspace` flow on the app
    /// side; the sidebar just dispatches.
    on_create: Box<CreateHandler>,
    /// Callback fired after the user confirms a row's delete affordance.
    /// Sends `unregister_workspace` for the given id. Daemon cascades to
    /// member sessions, so callers don't need a separate session-cleanup
    /// step.
    on_destroy: Box<DestroyHandler>,
    confirm_destroy_id: Option<SharedString>,
    destroying_id: Option<SharedString>,
    destroy_error: Option<(SharedString, SharedString)>,
    focus_handle: FocusHandle,
}

impl Sidebar {
    pub fn new(
        workspaces: Vec<Entity<Workspace>>,
        on_select: impl Fn(SharedString, &mut Window, &mut gpui::App) + 'static,
        on_create: impl Fn(&mut Window, &mut gpui::App) + 'static,
        on_destroy: impl Fn(SharedString, &mut Window, &mut gpui::App) -> Result<(), String> + 'static,
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
            on_create: Box::new(on_create),
            on_destroy: Box::new(on_destroy),
            confirm_destroy_id: None,
            destroying_id: None,
            destroy_error: None,
            focus_handle: cx.focus_handle(),
        }
    }

    /// Add a workspace handle. Called by `NativeApp` on `WorkspaceRegistered`.
    /// `NativeApp` guards duplicates upstream (same id → same `Entity<Workspace>`
    /// reused), so this is a pure insert — re-inserting the same id is a no-op.
    pub fn upsert_workspace(&mut self, ws: Entity<Workspace>, cx: &mut Context<Self>) {
        let id = ws.read(cx).id.clone();
        if self
            .workspaces
            .iter()
            .any(|existing| existing.read(cx).id == id)
        {
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
        self.clear_destroy_state_for(id);
        cx.notify();
    }

    pub fn set_selected(&mut self, id: Option<SharedString>, cx: &mut Context<Self>) {
        if self.selected_id != id {
            self.selected_id = id;
            cx.notify();
        }
    }

    fn clear_destroy_state_for(&mut self, id: &str) {
        if self
            .confirm_destroy_id
            .as_ref()
            .map(|pending| pending.as_ref())
            == Some(id)
        {
            self.confirm_destroy_id = None;
        }
        if self.destroying_id.as_ref().map(|pending| pending.as_ref()) == Some(id) {
            self.destroying_id = None;
        }
        if self
            .destroy_error
            .as_ref()
            .map(|(error_id, _)| error_id.as_ref())
            == Some(id)
        {
            self.destroy_error = None;
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
                let confirming = self.confirm_destroy_id.as_ref() == Some(&id);
                let destroying = self.destroying_id.as_ref() == Some(&id);
                let error = self.destroy_error.as_ref().and_then(|(error_id, message)| {
                    if error_id == &id {
                        Some(message.clone())
                    } else {
                        None
                    }
                });
                let click_id = id.clone();
                let mut row_div = workspace_row(title, status, selected, confirming, destroying)
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, window, cx| {
                            let id = click_id.clone();
                            let cleared_destroy_state =
                                this.confirm_destroy_id.is_some() || this.destroy_error.is_some();
                            this.confirm_destroy_id = None;
                            this.destroy_error = None;
                            (this.on_select)(id.clone(), window, cx);
                            this.set_selected(Some(id), cx);
                            if cleared_destroy_state {
                                cx.notify();
                            }
                        }),
                    );

                if confirming {
                    let cancel_id = id.clone();
                    let confirm_id = id.clone();
                    row_div = row_div
                        .child(cancel_delete_button().on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |this, _, _, cx| {
                                cx.stop_propagation();
                                this.clear_destroy_state_for(cancel_id.as_ref());
                                cx.notify();
                            }),
                        ))
                        .child(confirm_delete_button().on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |this, _, window, cx| {
                                cx.stop_propagation();
                                match (this.on_destroy)(confirm_id.clone(), window, cx) {
                                    Ok(()) => {
                                        this.confirm_destroy_id = None;
                                        this.destroying_id = Some(confirm_id.clone());
                                        this.destroy_error = None;
                                    }
                                    Err(error) => {
                                        this.confirm_destroy_id = Some(confirm_id.clone());
                                        this.destroying_id = None;
                                        this.destroy_error =
                                            Some((confirm_id.clone(), SharedString::from(error)));
                                    }
                                }
                                cx.notify();
                            }),
                        ));
                } else {
                    let destroy_id = id.clone();
                    row_div = row_div.child(delete_button(destroying).on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, _, cx| {
                            // Stop the row's `on_select` from firing too —
                            // clicking delete shouldn't also select the
                            // workspace it's about to delete.
                            cx.stop_propagation();
                            if this.destroying_id.as_ref() == Some(&destroy_id) {
                                return;
                            }
                            this.confirm_destroy_id = Some(destroy_id.clone());
                            this.destroy_error = None;
                            cx.notify();
                        }),
                    ));
                }

                if let Some(error) = error {
                    div()
                        .w_full()
                        .flex()
                        .flex_col()
                        .child(row_div)
                        .child(destroy_error_row(error))
                        .into_any_element()
                } else {
                    row_div.into_any_element()
                }
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
            .child(create_row().on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| {
                    (this.on_create)(window, cx);
                }),
            ))
    }
}

/// One workspace row. Title + status badge on the left. Caller appends
/// the delete affordance — pulled out so the click handler in `render`
/// stays readable.
fn workspace_row(
    title: SharedString,
    status: WorkspaceStatus,
    selected: bool,
    confirming_delete: bool,
    destroying: bool,
) -> gpui::Div {
    let bg = if confirming_delete {
        rgb(0x3a2428)
    } else if destroying {
        rgb(0x24242e)
    } else if selected {
        rgb(0x2a2a3a)
    } else {
        rgb(0x1a1a22)
    };
    let title_color = if destroying {
        rgb(0x8a8a95)
    } else {
        rgb(0xe0e0eb)
    };
    div()
        .w_full()
        .px_4()
        .py_2()
        .flex()
        .items_center()
        .gap_2()
        .bg(bg)
        .text_color(title_color)
        .text_size(px(13.))
        .child(status_badge(status))
        .child(div().flex_1().truncate().child(title))
}

/// Trailing delete affordance on each row. Always visible (no hover gate
/// yet — that's a follow-up once we have a hover-state pattern in this
/// crate). Dim by default so the eye lands on titles, not crosses.
fn delete_button(disabled: bool) -> gpui::Div {
    let label = if disabled { "..." } else { "x" };
    div()
        .w(px(20.))
        .h(px(20.))
        .flex_shrink_0()
        .flex()
        .items_center()
        .justify_center()
        .text_color(rgb(0x6a6a78))
        .text_size(px(14.))
        .child(SharedString::from(label))
}

fn cancel_delete_button() -> gpui::Div {
    div()
        .px_2()
        .h(px(20.))
        .flex_shrink_0()
        .flex()
        .items_center()
        .justify_center()
        .text_color(rgb(0x9a9aa8))
        .text_size(px(11.))
        .child(SharedString::from("Cancel"))
}

fn confirm_delete_button() -> gpui::Div {
    div()
        .px_2()
        .h(px(20.))
        .flex_shrink_0()
        .flex()
        .items_center()
        .justify_center()
        .bg(rgb(0x7a2f35))
        .rounded_sm()
        .text_color(rgb(0xf0dadd))
        .text_size(px(11.))
        .child(SharedString::from("Delete"))
}

fn destroy_error_row(message: SharedString) -> gpui::Div {
    div()
        .w_full()
        .px_4()
        .pb_2()
        .text_color(rgb(0xff8a8a))
        .text_size(px(11.))
        .line_clamp(2)
        .child(message)
}

/// "+ New Workspace" entry below the workspace list. Visually distinct
/// from real workspaces so the eye reads it as an action, not a row to
/// select.
fn create_row() -> gpui::Div {
    div()
        .w_full()
        .px_4()
        .py_2()
        .flex()
        .items_center()
        .gap_2()
        .text_color(rgb(0x8a8a95))
        .text_size(px(13.))
        .child(SharedString::from("+ New Workspace"))
}

/// Coloured dot reflecting the workspace's rolled-up status. The colour
/// vocabulary mirrors the Tauri sidebar's session badges so attn feels
/// consistent across frontends.
fn status_badge(status: WorkspaceStatus) -> impl IntoElement {
    let color = match status {
        WorkspaceStatus::Working => rgb(0x4caf50),         // green
        WorkspaceStatus::WaitingInput => rgb(0xffc107),    // amber
        WorkspaceStatus::PendingApproval => rgb(0xff9800), // orange
        WorkspaceStatus::Idle => rgb(0x6a6a78),            // grey
        WorkspaceStatus::Launching => rgb(0x2196f3),       // blue
    };
    div().w(px(8.)).h(px(8.)).rounded_full().bg(color)
}
