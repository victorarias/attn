/// Fixed-width left rail. One row per workspace. Status badge in front of
/// the title. Clicking a row asks the parent (`NativeApp`) to switch
/// selection. The sidebar holds cloned `Entity<Workspace>` handles and
/// observes each — when a workspace's status changes, only that row
/// re-renders.
use attn_protocol::WorkspaceStatus;
use gpui::{
    div, prelude::*, px, Context, Entity, FocusHandle, Focusable, MouseButton, ParentElement,
    Render, SharedString, Window,
};

use crate::state::workspace::Workspace;
use crate::theme;

pub const SIDEBAR_WIDTH: f32 = 240.0;
pub const SIDEBAR_COLLAPSED_WIDTH: f32 = 52.0;

type SelectHandler = dyn Fn(SharedString, &mut Window, &mut gpui::App) + 'static;
type CreateHandler = dyn Fn(&mut Window, &mut gpui::App) + 'static;
type SettingsHandler = dyn Fn(bool, &mut Window, &mut gpui::App) + 'static;
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
    /// Callback fired by the bottom cog. `NativeApp` owns the settings
    /// surface; the sidebar only exposes the affordance.
    on_open_settings: Box<SettingsHandler>,
    /// Callback fired after the user confirms a row's delete affordance.
    /// Sends `unregister_workspace` for the given id. Daemon cascades to
    /// member sessions, so callers don't need a separate session-cleanup
    /// step.
    on_destroy: Box<DestroyHandler>,
    collapsed: bool,
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
        on_open_settings: impl Fn(bool, &mut Window, &mut gpui::App) + 'static,
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
            on_open_settings: Box::new(on_open_settings),
            on_destroy: Box::new(on_destroy),
            collapsed: false,
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

    pub fn is_collapsed(&self) -> bool {
        self.collapsed
    }

    pub fn set_collapsed(&mut self, collapsed: bool, cx: &mut Context<Self>) {
        if self.collapsed != collapsed {
            self.collapsed = collapsed;
            if collapsed {
                self.confirm_destroy_id = None;
                self.destroy_error = None;
            }
            cx.notify();
        }
    }

    pub fn toggle_collapsed(&mut self, cx: &mut Context<Self>) -> bool {
        let collapsed = !self.collapsed;
        self.set_collapsed(collapsed, cx);
        collapsed
    }

    pub fn automation_snapshot(&self) -> serde_json::Value {
        serde_json::json!({
            "collapsed": self.collapsed,
            "width": if self.collapsed {
                SIDEBAR_COLLAPSED_WIDTH
            } else {
                SIDEBAR_WIDTH
            },
        })
    }

    pub fn click_settings_for_automation(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        (self.on_open_settings)(self.collapsed, window, cx);
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
                let mut row_div = if self.collapsed {
                    workspace_row_collapsed(status, selected, confirming, destroying)
                } else {
                    workspace_row(title, status, selected, confirming, destroying)
                }
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

                if self.collapsed {
                    // Destructive actions stay in the expanded rail where
                    // the confirm/cancel copy has room to be explicit.
                } else if confirming {
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

        let count = self.workspaces.len();
        let width = if self.collapsed {
            SIDEBAR_COLLAPSED_WIDTH
        } else {
            SIDEBAR_WIDTH
        };
        let root = div()
            .w(px(width))
            .h_full()
            .bg(theme::ink::nocturne())
            .border_r_1()
            .border_color(theme::ink::firm())
            .flex()
            .flex_col()
            .child(sidebar_header(count, self.collapsed).on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, _, cx| {
                    this.toggle_collapsed(cx);
                }),
            ))
            .child(div().flex_1().flex().flex_col().children(rows));

        let create = create_row(self.collapsed).on_mouse_down(
            MouseButton::Left,
            cx.listener(|this, _, window, cx| {
                (this.on_create)(window, cx);
            }),
        );
        let settings = settings_button(self.collapsed).on_mouse_down(
            MouseButton::Left,
            cx.listener(|this, _, window, cx| {
                (this.on_open_settings)(this.collapsed, window, cx);
            }),
        );

        if self.collapsed {
            root.child(
                div()
                    .w_full()
                    .pb_3()
                    .flex()
                    .flex_col()
                    .items_center()
                    .gap_2()
                    .border_t_1()
                    .border_color(theme::line::weak())
                    .child(create)
                    .child(settings),
            )
        } else {
            root.child(create).child(
                div()
                    .w_full()
                    .px_3()
                    .py_3()
                    .border_t_1()
                    .border_color(theme::line::weak())
                    .flex()
                    .items_center()
                    .justify_end()
                    .child(settings),
            )
        }
    }
}

/// Section header at the top of the workspace rail. Expanded mode shows
/// the ledger label and count; collapsed mode becomes a compact toggle
/// cap so the narrow rail still has a clear affordance.
fn sidebar_header(count: usize, collapsed: bool) -> gpui::Div {
    if collapsed {
        return div()
            .w_full()
            .h(px(42.0))
            .flex()
            .items_center()
            .justify_center()
            .border_b_1()
            .border_color(theme::line::weak())
            .text_color(theme::moon::bone())
            .text_size(px(16.0))
            .child(SharedString::from(">"));
    }

    div()
        .w_full()
        .px_4()
        .pt_3()
        .pb_2()
        .flex()
        .items_center()
        .justify_between()
        .text_color(theme::moon::bone())
        .text_size(px(10.))
        .child(
            div()
                .flex()
                .items_baseline()
                .gap_2()
                .child(SharedString::from("WORKSPACES"))
                .child(
                    div()
                        .text_color(theme::moon::parchment())
                        .child(SharedString::from(format!("{:02}", count))),
                ),
        )
        .child(
            div()
                .w(px(24.0))
                .h(px(24.0))
                .flex()
                .items_center()
                .justify_center()
                .rounded(px(theme::radius::R0))
                .border_1()
                .border_color(theme::line::mild())
                .text_color(theme::moon::bone())
                .text_size(px(13.0))
                .child(SharedString::from("<")),
        )
}

/// One workspace row. Title + status badge on the left. Caller appends
/// the delete affordance — pulled out so the click handler in `render`
/// stays readable.
///
/// A 2px left-edge accent runs the height of every row, hue-keyed to the
/// row's role: `sodium::vapor` when the row is the active workspace, the
/// row's own background otherwise (so the layout stays static — no
/// 2px-shift on selection). This is the same "selection mark" pattern
/// used in plate 04 of the visual plates.
fn workspace_row(
    title: SharedString,
    status: WorkspaceStatus,
    selected: bool,
    confirming_delete: bool,
    destroying: bool,
) -> gpui::Div {
    let bg = if confirming_delete {
        theme::surface::danger_row()
    } else if destroying {
        theme::surface::pending_row()
    } else if selected {
        theme::surface::selected_row()
    } else {
        theme::ink::nocturne()
    };
    let title_color = if destroying {
        theme::moon::bone()
    } else {
        theme::moon::moonstone()
    };
    let accent = if selected {
        theme::sodium::vapor()
    } else {
        // Match the row's own background so the 2px reservation reads
        // as nothing when the row isn't selected. Layout stays static.
        bg
    };
    div()
        .w_full()
        // pl_3p5 (14px) + border_l_2 (2px) = 16px to content, matching
        // the right padding of the row even when the strip is on.
        .pl_3p5()
        .pr_4()
        .py_2()
        .flex()
        .items_center()
        .gap_2()
        .bg(bg)
        .border_l_2()
        .border_color(accent)
        .text_color(title_color)
        .text_size(px(13.))
        .child(status_badge(status))
        .child(div().flex_1().truncate().child(title))
}

fn workspace_row_collapsed(
    status: WorkspaceStatus,
    selected: bool,
    confirming_delete: bool,
    destroying: bool,
) -> gpui::Div {
    let bg = if confirming_delete {
        theme::surface::danger_row()
    } else if destroying {
        theme::surface::pending_row()
    } else if selected {
        theme::surface::selected_row()
    } else {
        theme::ink::nocturne()
    };
    let accent = if selected { theme::sodium::vapor() } else { bg };
    div()
        .w_full()
        .h(px(34.0))
        .flex()
        .items_center()
        .justify_center()
        .bg(bg)
        .border_l_2()
        .border_color(accent)
        .child(
            div()
                .w(px(16.0))
                .h(px(16.0))
                .rounded_full()
                .border_1()
                .border_color(if selected {
                    theme::sodium::deep()
                } else {
                    theme::line::mild()
                })
                .flex()
                .items_center()
                .justify_center()
                .child(status_badge(status)),
        )
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
        .text_color(theme::moon::ash())
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
        .text_color(theme::moon::bone())
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
        .bg(theme::surface::danger_emphasis_bg())
        .rounded_sm()
        .text_color(theme::surface::danger_emphasis_fg())
        .text_size(px(11.))
        .child(SharedString::from("Delete"))
}

fn destroy_error_row(message: SharedString) -> gpui::Div {
    div()
        .w_full()
        .px_4()
        .pb_2()
        .text_color(theme::state::error())
        .text_size(px(11.))
        .line_clamp(2)
        .child(message)
}

/// "+ New Workspace" entry below the workspace list. Visually distinct
/// from real workspaces so the eye reads it as an action, not a row to
/// select. A faint top divider separates the "live workspaces" stack
/// from this "open new" affordance, matching the sidebar plate.
fn create_row(collapsed: bool) -> gpui::Div {
    if collapsed {
        return icon_button("+");
    }

    div()
        .w_full()
        .pt_2()
        .border_t_1()
        .border_color(theme::line::weak())
        .child(
            div()
                .w_full()
                // Match the workspace_row content offset: 14px + 2px =
                // 16px. No accent strip so a transparent reservation is
                // unnecessary; just inset to the column.
                .pl_4()
                .pr_4()
                .py_2()
                .flex()
                .items_center()
                .gap_2()
                .text_color(theme::moon::ash())
                .text_size(px(13.))
                .child(create_glyph())
                .child(SharedString::from("New Workspace")),
        )
}

fn settings_button(collapsed: bool) -> gpui::Div {
    if collapsed {
        icon_button("⚙")
    } else {
        div()
            .w(px(28.0))
            .h(px(28.0))
            .flex_shrink_0()
            .flex()
            .items_center()
            .justify_center()
            .rounded(px(theme::radius::R0))
            .border_1()
            .border_color(theme::line::mild())
            .text_color(theme::moon::bone())
            .text_size(px(15.0))
            .child(SharedString::from("⚙"))
    }
}

fn icon_button(label: &'static str) -> gpui::Div {
    div()
        .w(px(32.0))
        .h(px(32.0))
        .flex_shrink_0()
        .flex()
        .items_center()
        .justify_center()
        .rounded(px(theme::radius::R1))
        .border_1()
        .border_color(theme::line::mild())
        .text_color(theme::moon::bone())
        .text_size(px(16.0))
        .child(SharedString::from(label))
}

/// Leading "+" glyph for the create row. Sits in the column where status
/// badges sit on workspace rows, so the eye reads "this row is doing
/// the same job, but it's an action."
fn create_glyph() -> gpui::Div {
    div()
        .w(px(8.))
        .h(px(8.))
        .flex()
        .items_center()
        .justify_center()
        .text_color(theme::moon::bone())
        .text_size(px(12.))
        .child(SharedString::from("+"))
}

/// Coloured dot reflecting the workspace's rolled-up status. Hue
/// resolution lives in `theme::workspace_status_color` so the same
/// classification reads identically across the sidebar, the canvas
/// status pills, and any future surface.
fn status_badge(status: WorkspaceStatus) -> impl IntoElement {
    div()
        .w(px(8.))
        .h(px(8.))
        .rounded_full()
        .bg(theme::workspace_status_color(status))
}
