/// Workspace canvas — pan/zoom/drag/resize/focus on top of the panels
/// stored in the selected `Entity<Workspace>`. The mechanics (viewport
/// transform, hit testing, drag-state machine) are the spike-4 design
/// lifted onto a peer-shared workspace entity instead of a fixed Vec.
///
/// Mutation flow: drag/resize update panel position/size by reaching into
/// the selected workspace via `update`. Terminal cell sizing and PTY
/// reflow happen inside `TerminalView` itself — when the panel resizes,
/// `set_content_size` propagates the new dims and the view emits the
/// PtyResize on its next render.
use std::cell::Cell;
use std::rc::Rc;

use gpui::{
    canvas, div, point, prelude::*, px, rgb, AnyElement, App, Bounds, Context, Entity, FocusHandle,
    Focusable, MouseButton, MouseDownEvent, MouseMoveEvent, MouseUpEvent, ParentElement, Pixels,
    Render, ScrollDelta, ScrollWheelEvent, SharedString, Subscription, Window,
};

use crate::canvas_view::{pf, GridElement, Viewport};
use crate::daemon_client::DaemonClient;
use crate::panel::{Panel, PanelContent};
use crate::workspace::Workspace;

// ── Layout constants ─────────────────────────────────────────────────────────

const TITLE_HEIGHT: f32 = 24.0; // world-space units
const HANDLE_SIZE: f32 = 8.0; // screen-space pixels (fixed, not scaled)
const PANEL_MIN_W: f32 = 120.0; // world-space
const PANEL_MIN_H: f32 = 80.0; // world-space

// ── Drag/resize state ─────────────────────────────────────────────────────────

#[derive(Clone, Copy, Debug)]
enum ResizeHandle {
    TopLeft,
    TopRight,
    BottomLeft,
    BottomRight,
}

#[derive(Clone, Debug)]
enum DragState {
    Idle,
    PanningCanvas { last_screen: gpui::Point<Pixels> },
    DraggingPanel { panel_id: usize, last_screen: gpui::Point<Pixels> },
    ResizingPanel { panel_id: usize, handle: ResizeHandle, last_screen: gpui::Point<Pixels> },
}

#[derive(Debug)]
enum HitResult {
    Canvas,
    PanelBody(usize),
    TitleBar(usize),
    ResizeHandle(usize, ResizeHandle),
}

pub struct Spike5Canvas {
    #[allow(dead_code)]
    daemon: Entity<DaemonClient>,
    selected: Option<Entity<Workspace>>,
    /// Drop = unsubscribe. Replaced when selection changes so re-renders
    /// only fire for the workspace currently on screen.
    _selected_subscription: Option<Subscription>,
    viewport: Viewport,
    drag_state: DragState,
    focused_panel: Option<usize>,
    needs_focus_panel: Option<usize>,
    focus_handle: FocusHandle,
    /// Window-relative bounds of the canvas's root element, captured each
    /// frame via a `canvas()` prepaint callback. Mouse events arrive with
    /// window-relative coordinates, so hit testing and zoom focal points
    /// must subtract this origin to land in canvas-local space. Without
    /// this, the sidebar's 240px width breaks every panel hit test.
    bounds: Rc<Cell<Option<Bounds<Pixels>>>>,
}

impl Spike5Canvas {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        Self {
            daemon,
            selected: None,
            _selected_subscription: None,
            viewport: Viewport::default(),
            drag_state: DragState::Idle,
            focused_panel: None,
            needs_focus_panel: None,
            focus_handle: cx.focus_handle(),
            bounds: Rc::new(Cell::new(None)),
        }
    }

    /// Translate a window-relative position into canvas-local space using
    /// the most recently captured bounds. Falls back to the input when
    /// bounds aren't known yet (first frame before paint).
    fn local_pos(&self, screen: gpui::Point<Pixels>) -> gpui::Point<Pixels> {
        match self.bounds.get() {
            Some(b) => point(screen.x - b.origin.x, screen.y - b.origin.y),
            None => screen,
        }
    }

    pub fn set_selected(&mut self, ws: Option<Entity<Workspace>>, cx: &mut Context<Self>) {
        self._selected_subscription = ws.as_ref().map(|w| cx.observe(w, |_, _, cx| cx.notify()));
        self.selected = ws;
        self.drag_state = DragState::Idle;
        self.focused_panel = None;
        cx.notify();
    }

    /// Panel snapshot for hit testing and rendering. Cloning is cheap
    /// (entity handles + small fields) and decouples the immutable read
    /// from any mutating update later in the same frame.
    fn panels_snapshot(&self, cx: &App) -> Vec<Panel> {
        match self.selected.as_ref() {
            Some(ws) => ws.read(cx).panels.clone(),
            None => Vec::new(),
        }
    }

    // ── Hit testing ──────────────────────────────────────────────────────────

    fn hit_test(&self, screen_pos: gpui::Point<Pixels>, cx: &App) -> HitResult {
        let mx = pf(screen_pos.x);
        let my = pf(screen_pos.y);
        let zoom = self.viewport.zoom;
        let panels = self.panels_snapshot(cx);

        // Check panels in reverse so the topmost (last rendered) wins.
        for panel in panels.iter().rev() {
            let sp = self.viewport.world_to_screen(point(panel.world_x, panel.world_y));
            let sx = pf(sp.x);
            let sy = pf(sp.y);
            let sw = panel.width * zoom;
            let sh = panel.height * zoom;

            // Resize corner zones — fixed 8+2px hit area in screen space.
            let half = HANDLE_SIZE / 2.0 + 2.0;
            if (mx - sx).abs() <= half && (my - sy).abs() <= half {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::TopLeft);
            }
            if (mx - (sx + sw)).abs() <= half && (my - sy).abs() <= half {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::TopRight);
            }
            if (mx - sx).abs() <= half && (my - (sy + sh)).abs() <= half {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::BottomLeft);
            }
            if (mx - (sx + sw)).abs() <= half && (my - (sy + sh)).abs() <= half {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::BottomRight);
            }

            if mx >= sx && mx <= sx + sw && my >= sy && my <= sy + sh {
                let title_h = TITLE_HEIGHT * zoom;
                if my <= sy + title_h {
                    return HitResult::TitleBar(panel.id);
                }
                return HitResult::PanelBody(panel.id);
            }
        }

        HitResult::Canvas
    }

    // ── Mouse handlers ────────────────────────────────────────────────────────

    fn on_mouse_down(
        &mut self,
        event: &MouseDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let pos = self.local_pos(event.position);
        let hit = self.hit_test(pos, cx);
        match hit {
            HitResult::TitleBar(id) => {
                self.focused_panel = Some(id);
                self.drag_state = DragState::DraggingPanel { panel_id: id, last_screen: pos };
            }
            HitResult::ResizeHandle(id, handle) => {
                self.drag_state =
                    DragState::ResizingPanel { panel_id: id, handle, last_screen: pos };
            }
            HitResult::PanelBody(id) => {
                self.focused_panel = Some(id);
                if let Some(ws) = self.selected.as_ref() {
                    if let Some(panel) = ws.read(cx).panels.iter().find(|p| p.id == id) {
                        if let PanelContent::Terminal { view, .. } = &panel.content {
                            view.read(cx).focus_handle.clone().focus(window);
                        }
                    }
                }
                self.drag_state = DragState::Idle;
            }
            HitResult::Canvas => {
                self.drag_state = DragState::PanningCanvas { last_screen: pos };
            }
        }
        cx.notify();
    }

    fn on_mouse_move(
        &mut self,
        event: &MouseMoveEvent,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        // Window-relative is fine here — every drag-state arm consumes
        // deltas, and any constant offset cancels out when subtracting.
        let pos = event.position;
        match self.drag_state.clone() {
            DragState::Idle => return,

            DragState::PanningCanvas { last_screen } => {
                let dx = pf(pos.x) - pf(last_screen.x);
                let dy = pf(pos.y) - pf(last_screen.y);
                self.viewport.origin.x -= dx / self.viewport.zoom;
                self.viewport.origin.y -= dy / self.viewport.zoom;
                self.drag_state = DragState::PanningCanvas { last_screen: pos };
            }

            DragState::DraggingPanel { panel_id, last_screen } => {
                let dx = (pf(pos.x) - pf(last_screen.x)) / self.viewport.zoom;
                let dy = (pf(pos.y) - pf(last_screen.y)) / self.viewport.zoom;
                if let Some(ws) = self.selected.as_ref() {
                    ws.update(cx, |ws, cx| {
                        if let Some(p) = ws.panels.iter_mut().find(|p| p.id == panel_id) {
                            p.world_x += dx;
                            p.world_y += dy;
                            cx.notify();
                        }
                    });
                }
                self.drag_state = DragState::DraggingPanel { panel_id, last_screen: pos };
            }

            DragState::ResizingPanel { panel_id, handle, last_screen } => {
                // Screen delta → world delta so resize feels zoom-invariant.
                let dx = (pf(pos.x) - pf(last_screen.x)) / self.viewport.zoom;
                let dy = (pf(pos.y) - pf(last_screen.y)) / self.viewport.zoom;
                if let Some(ws) = self.selected.as_ref() {
                    ws.update(cx, |ws, cx| {
                        if let Some(p) = ws.panels.iter_mut().find(|p| p.id == panel_id) {
                            apply_resize(p, handle, dx, dy);
                            cx.notify();
                        }
                    });
                }
                self.drag_state =
                    DragState::ResizingPanel { panel_id, handle, last_screen: pos };
            }
        }
        cx.notify();
    }

    fn on_mouse_up(
        &mut self,
        _event: &MouseUpEvent,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.drag_state = DragState::Idle;
        cx.notify();
    }

    fn on_scroll_wheel(
        &mut self,
        event: &ScrollWheelEvent,
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (dx, dy) = match event.delta {
            ScrollDelta::Pixels(p) => (pf(p.x), pf(p.y)),
            ScrollDelta::Lines(p) => (p.x * 20.0, p.y * 20.0),
        };

        if event.modifiers.platform {
            // Cmd+scroll → zoom toward cursor. Translate to canvas-local
            // space so the focal point lands under the actual cursor and
            // not 240px to its right.
            let factor = if dy > 0.0 { 1.08 } else { 1.0 / 1.08 };
            self.viewport = self.viewport.zoom_toward(self.local_pos(event.position), factor);
        } else {
            // Regular scroll → pan.
            self.viewport.origin.x -= dx / self.viewport.zoom;
            self.viewport.origin.y -= dy / self.viewport.zoom;
        }
        cx.notify();
    }
}

fn apply_resize(p: &mut Panel, handle: ResizeHandle, dx: f32, dy: f32) {
    match handle {
        ResizeHandle::TopLeft => {
            let new_w = (p.width - dx).max(PANEL_MIN_W);
            let new_h = (p.height - dy).max(PANEL_MIN_H);
            p.world_x += p.width - new_w;
            p.world_y += p.height - new_h;
            p.width = new_w;
            p.height = new_h;
        }
        ResizeHandle::TopRight => {
            let new_h = (p.height - dy).max(PANEL_MIN_H);
            p.world_y += p.height - new_h;
            p.height = new_h;
            p.width = (p.width + dx).max(PANEL_MIN_W);
        }
        ResizeHandle::BottomLeft => {
            let new_w = (p.width - dx).max(PANEL_MIN_W);
            p.world_x += p.width - new_w;
            p.width = new_w;
            p.height = (p.height + dy).max(PANEL_MIN_H);
        }
        ResizeHandle::BottomRight => {
            p.width = (p.width + dx).max(PANEL_MIN_W);
            p.height = (p.height + dy).max(PANEL_MIN_H);
        }
    }
}

impl Focusable for Spike5Canvas {
    fn focus_handle(&self, _cx: &App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for Spike5Canvas {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let viewport = self.viewport;
        let window_size = window.viewport_size();
        let ws_w = pf(window_size.width);
        let ws_h = pf(window_size.height);
        let focus_handle = self.focus_handle.clone();
        let focused_panel = self.focused_panel;

        let bounds_capture = self.bounds.clone();
        let mut root = div()
            .size_full()
            .bg(rgb(0x0e0e14))
            .track_focus(&focus_handle)
            .on_mouse_down(MouseButton::Left, cx.listener(Self::on_mouse_down))
            .on_mouse_move(cx.listener(Self::on_mouse_move))
            .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
            .on_scroll_wheel(cx.listener(Self::on_scroll_wheel))
            // Stamp the canvas's window-relative bounds into a shared
            // cell so mouse handlers can translate event.position into
            // canvas-local coords. The paint callback is a no-op.
            .child(
                canvas(
                    move |new_bounds, _, _| bounds_capture.set(Some(new_bounds)),
                    |_, _, _, _| {},
                )
                .absolute()
                .size_full(),
            )
            .child(GridElement { viewport });

        let Some(selected) = self.selected.clone() else {
            return root.child(empty_state(SharedString::from("Select a workspace")));
        };

        let panels = selected.read(cx).panels.clone();
        if panels.is_empty() {
            return root.child(empty_state(SharedString::from("Workspace has no panels yet")));
        }

        // Apply pending focus request now that we have window access.
        if let Some(fid) = self.needs_focus_panel.take() {
            if let Some(panel) = panels.iter().find(|p| p.id == fid) {
                if let PanelContent::Terminal { view, .. } = &panel.content {
                    view.read(cx).focus_handle.clone().focus(window);
                }
            }
        }

        // Push content_size + zoom into every TerminalView so their next
        // render syncs cell dims and emits PtyResize on actual change.
        let zoom = viewport.zoom;
        for panel in &panels {
            if let PanelContent::Terminal { view, .. } = &panel.content {
                let content_w = panel.width;
                let content_h = (panel.height - TITLE_HEIGHT).max(0.0);
                view.update(cx, |tv, inner_cx| {
                    let size_changed = tv.set_content_size(content_w, content_h);
                    let zoom_changed = tv.set_zoom(zoom);
                    if size_changed || zoom_changed {
                        inner_cx.notify();
                    }
                });
            }
        }

        for panel in &panels {
            let sp = viewport.world_to_screen(point(panel.world_x, panel.world_y));
            let sx = pf(sp.x);
            let sy = pf(sp.y);
            let sw = panel.width * viewport.zoom;
            let sh = panel.height * viewport.zoom;
            let title_h = TITLE_HEIGHT * viewport.zoom;

            // Viewport culling — skip fully off-screen panels.
            if sx + sw < 0.0 || sy + sh < 0.0 || sx > ws_w || sy > ws_h {
                continue;
            }

            let is_focused = focused_panel == Some(panel.id);
            let border_color = if is_focused { rgb(0x4a9eff) } else { rgb(0x2a2a35) };
            let content_h = (sh - title_h).max(0.0);
            let title = panel.title.clone();

            let body: AnyElement = match &panel.content {
                PanelContent::Placeholder(view) => view.clone().into_any_element(),
                PanelContent::Terminal { view, .. } => view.clone().into_any_element(),
            };

            let panel_div = div()
                .absolute()
                .left(px(sx))
                .top(px(sy))
                .w(px(sw))
                .h(px(sh))
                .bg(rgb(0x1c1c26))
                .border_1()
                .border_color(border_color)
                .child(
                    div()
                        .w_full()
                        .h(px(title_h))
                        .bg(rgb(0x252535))
                        .flex()
                        .items_center()
                        .pl(px(8.0))
                        .child(div().text_xs().text_color(rgb(0xa0a0b0)).child(title)),
                )
                .child(div().w_full().h(px(content_h)).overflow_hidden().child(body))
                .child(corner_handle(0.0, 0.0))
                .child(corner_handle(sw - HANDLE_SIZE, 0.0))
                .child(corner_handle(0.0, sh - HANDLE_SIZE))
                .child(corner_handle(sw - HANDLE_SIZE, sh - HANDLE_SIZE));

            root = root.child(panel_div);
        }

        root
    }
}

fn empty_state(label: SharedString) -> impl IntoElement {
    div()
        .absolute()
        .size_full()
        .flex()
        .items_center()
        .justify_center()
        .text_color(rgb(0x6a6a78))
        .text_size(px(13.))
        .child(label)
}

fn corner_handle(left: f32, top: f32) -> impl IntoElement {
    div()
        .absolute()
        .left(px(left))
        .top(px(top))
        .w(px(HANDLE_SIZE))
        .h(px(HANDLE_SIZE))
        .bg(rgb(0x5a5a6a))
        .rounded(px(1.0))
}
