/// Spike 5 canvas — reads panels from one selected `Entity<Workspace>`
/// and renders each via the `PanelContent` enum. Pan/zoom is intentionally
/// out of scope here (proven by spikes 3+4); this canvas focuses on the
/// architectural piece spike 5 is meant to prove: enum-dispatched panel
/// rendering off a peer-shared workspace entity.
///
/// When the selected workspace changes, the parent (`Spike5App`) calls
/// `set_selected` with the new entity handle. Subscribers are re-wired so
/// `cx.notify()` only fires for the workspace currently on screen.
use gpui::{
    div, prelude::*, px, rgb, AnyElement, Context, Entity, ParentElement, Render, SharedString,
    Subscription, Window,
};

use crate::panel::{Panel, PanelContent};
use crate::workspace::Workspace;

pub struct Spike5Canvas {
    selected: Option<Entity<Workspace>>,
    /// Handle to the workspace observation. Drop = unsubscribe; keeping it
    /// lets us replace cleanly when selection changes.
    _selected_subscription: Option<Subscription>,
}

impl Spike5Canvas {
    pub fn new() -> Self {
        Self { selected: None, _selected_subscription: None }
    }

    pub fn set_selected(&mut self, ws: Option<Entity<Workspace>>, cx: &mut Context<Self>) {
        self._selected_subscription = ws.as_ref().map(|w| cx.observe(w, |_, _, cx| cx.notify()));
        self.selected = ws;
        cx.notify();
    }
}

impl Render for Spike5Canvas {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let body: AnyElement = match self.selected.as_ref() {
            None => empty_state().into_any_element(),
            Some(ws_entity) => {
                // Clone the panel list out so the immutable borrow on `cx`
                // ends before `render_panel` takes its own mutable borrow.
                let (title, panels_data) = {
                    let ws = ws_entity.read(cx);
                    (ws.title.clone(), ws.panels.clone())
                };
                let panels: Vec<AnyElement> =
                    panels_data.iter().map(|p| render_panel(p, cx)).collect();
                div()
                    .size_full()
                    .relative()
                    .child(
                        div()
                            .absolute()
                            .left_4()
                            .top_2()
                            .text_color(rgb(0x6a6a78))
                            .text_size(px(11.))
                            .child(format!("workspace · {title}")),
                    )
                    .children(panels)
                    .into_any_element()
            }
        };

        div().size_full().bg(rgb(0x0e0e14)).child(body)
    }
}

fn empty_state() -> impl IntoElement {
    div()
        .size_full()
        .flex()
        .items_center()
        .justify_center()
        .text_color(rgb(0x6a6a78))
        .text_size(px(13.))
        .child(SharedString::from("Select a workspace"))
}

/// Render one panel. Position is `absolute` in screen space — no zoom for
/// the spike. The PanelContent match is the place new panel types plug in.
fn render_panel(panel: &Panel, cx: &mut Context<Spike5Canvas>) -> AnyElement {
    let frame = div()
        .absolute()
        .left(px(panel.world_x))
        .top(px(panel.world_y))
        .w(px(panel.width))
        .h(px(panel.height))
        .bg(rgb(0x1c1c26))
        .border_1()
        .border_color(rgb(0x2a2a35))
        .rounded_md()
        .flex()
        .flex_col()
        .child(
            // Title bar
            div()
                .px_3()
                .py_1()
                .border_b_1()
                .border_color(rgb(0x2a2a35))
                .text_color(rgb(0xa0a0b0))
                .text_size(px(11.))
                .child(panel.title.clone()),
        );

    match &panel.content {
        PanelContent::Placeholder(view) => frame.child(view.clone()).into_any_element(),
        PanelContent::Terminal { session_id } => frame
            .child(terminal_stub(session_id.clone(), cx))
            .into_any_element(),
    }
}

/// Stand-in for a real `TerminalView` — proves the enum dispatches a
/// distinct render branch. Wiring spike-4's terminal rendering into the
/// workspace context is a follow-up.
fn terminal_stub(session_id: SharedString, _cx: &mut Context<Spike5Canvas>) -> impl IntoElement {
    div()
        .flex_1()
        .flex()
        .items_center()
        .justify_center()
        .text_color(rgb(0x6a8a6a))
        .text_size(px(12.))
        .child(format!("terminal · {session_id} (stub)"))
}
