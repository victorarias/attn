/// Spike 4: Terminal panels on an infinite canvas.
/// Merges spike 2 (terminal surface element) with spike 3 (infinite canvas).
///
/// Key behaviours proven here:
/// - Focus routing: click terminal body → keyboard goes to that terminal
/// - Resize → PTY reflow: drag resize handle → cols/rows update → daemon PtyResize
/// - Zoom-aware sizing: terminal content stays fixed-pixel; more cells visible when zoomed in
/// - Multiple sessions: up to MAX_PANELS live terminals on the canvas simultaneously
use std::collections::HashSet;

use gpui::{
    div, prelude::*, px, rgb, point, App, Entity, FocusHandle, Focusable, MouseButton,
    MouseDownEvent, MouseMoveEvent, MouseUpEvent, Pixels, ScrollDelta, ScrollWheelEvent,
    SharedString, Window, Context,
};

use attn_protocol::{AttachSessionMessage, PtyResizeMessage};

use crate::canvas_view::{GridElement, Viewport, pf};
use crate::daemon_client::{DaemonClient, DaemonEvent};
use crate::terminal_model::TerminalModel;
use crate::terminal_view::{TerminalView, CHAR_WIDTH, ROW_HEIGHT};

// ── Layout constants ─────────────────────────────────────────────────────────

const TITLE_HEIGHT: f32 = 24.0; // world-space units
const HANDLE_SIZE: f32 = 8.0; // screen-space pixels (fixed, not scaled)
const PANEL_MIN_W: f32 = 120.0; // world-space
const PANEL_MIN_H: f32 = 80.0; // world-space
const MAX_PANELS: usize = 3;

// ── Panel ─────────────────────────────────────────────────────────────────────

struct TerminalPanel {
    id: usize,
    title: SharedString,
    world_x: f32,
    world_y: f32,
    width: f32,  // world space
    height: f32, // world space
    view: Entity<TerminalView>,
}

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

// ── TerminalCanvasView ────────────────────────────────────────────────────────

pub struct TerminalCanvasView {
    daemon: Entity<DaemonClient>,
    panels: Vec<TerminalPanel>,
    next_panel_id: usize,
    attached: HashSet<String>,
    viewport: Viewport,
    drag_state: DragState,
    focused_panel: Option<usize>,
    needs_focus_panel: Option<usize>,
    focus_handle: FocusHandle,
}

impl TerminalCanvasView {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        cx.subscribe(&daemon, Self::on_daemon_event).detach();
        Self {
            daemon,
            panels: Vec::new(),
            next_panel_id: 0,
            attached: HashSet::new(),
            viewport: Viewport::default(),
            drag_state: DragState::Idle,
            focused_panel: None,
            needs_focus_panel: None,
            focus_handle: cx.focus_handle(),
        }
    }

    fn on_daemon_event(
        &mut self,
        _daemon: Entity<DaemonClient>,
        event: &DaemonEvent,
        cx: &mut Context<Self>,
    ) {
        match event {
            DaemonEvent::Connected | DaemonEvent::SessionsChanged => {
                self.attach_available_sessions(cx);
            }
            _ => {}
        }
    }

    fn attach_available_sessions(&mut self, cx: &mut Context<Self>) {
        let remaining = MAX_PANELS.saturating_sub(self.panels.len());
        if remaining == 0 {
            return;
        }
        let to_attach: Vec<(String, String)> = self
            .daemon
            .read(cx)
            .sessions()
            .iter()
            .filter(|s| !self.attached.contains(&s.id))
            .take(remaining)
            .map(|s| (s.id.clone(), s.label.clone()))
            .collect();

        for (session_id, label) in to_attach {
            self.spawn_panel(session_id, label, cx);
        }
    }

    fn spawn_panel(&mut self, session_id: String, label: String, cx: &mut Context<Self>) {
        let id = self.next_panel_id;
        self.next_panel_id += 1;

        // Stagger panels horizontally so they don't overlap at zoom=1.
        let world_x = 30.0 + id as f32 * 410.0;
        let world_y = 50.0;
        let world_w = 380.0_f32;
        let world_h = 240.0_f32;

        // Terminal dimensions are world-space (zoom-invariant): the panel frame
        // scales with zoom, but the terminal cell size stays fixed in screen pixels.
        let (cols, rows) = panel_terminal_dims(world_w, world_h);
        let content_w = world_w;
        let content_h = (world_h - TITLE_HEIGHT).max(0.0);

        let daemon = self.daemon.clone();
        let terminal =
            cx.new(|cx| TerminalModel::new(session_id.clone(), cols, rows, &daemon, cx));
        let initial_zoom = self.viewport.zoom;
        let view = cx.new(|cx| {
            let mut tv = TerminalView::new(terminal, daemon.clone(), cx);
            tv.set_content_size(content_w, content_h);
            tv.set_zoom(initial_zoom);
            tv
        });

        // Send attach + initial resize.
        daemon.read(cx).send_cmd(&AttachSessionMessage::new(session_id.clone()));
        daemon.read(cx).send_cmd(&PtyResizeMessage::new(session_id.clone(), cols, rows));

        self.attached.insert(session_id.clone());
        self.panels.push(TerminalPanel {
            id,
            title: label.into(),
            world_x,
            world_y,
            width: world_w,
            height: world_h,
            view,
        });

        // Auto-focus the first panel.
        if self.focused_panel.is_none() {
            self.focused_panel = Some(id);
            self.needs_focus_panel = Some(id);
        }

        cx.notify();
    }

    // ── Hit testing ──────────────────────────────────────────────────────────

    fn hit_test(&self, screen_pos: gpui::Point<Pixels>) -> HitResult {
        let mx = pf(screen_pos.x);
        let my = pf(screen_pos.y);
        let zoom = self.viewport.zoom;

        // Panels are checked in reverse so the topmost (last rendered) wins.
        for panel in self.panels.iter().rev() {
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
        let pos = event.position;
        match self.hit_test(pos) {
            HitResult::TitleBar(id) => {
                // Focus the panel when dragging its title bar, but don't
                // forward keyboard focus — that would steal input mid-drag.
                self.focused_panel = Some(id);
                self.drag_state = DragState::DraggingPanel { panel_id: id, last_screen: pos };
            }
            HitResult::ResizeHandle(id, handle) => {
                self.drag_state =
                    DragState::ResizingPanel { panel_id: id, handle, last_screen: pos };
            }
            HitResult::PanelBody(id) => {
                // Click inside the terminal body → focus that terminal.
                self.focused_panel = Some(id);
                if let Some(panel) = self.panels.iter().find(|p| p.id == id) {
                    panel.view.read(cx).focus_handle.clone().focus(window);
                }
                // Don't start a canvas pan — the body belongs to the terminal.
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
                let dx = pf(pos.x) - pf(last_screen.x);
                let dy = pf(pos.y) - pf(last_screen.y);
                if let Some(p) = self.panels.iter_mut().find(|p| p.id == panel_id) {
                    p.world_x += dx / self.viewport.zoom;
                    p.world_y += dy / self.viewport.zoom;
                }
                self.drag_state = DragState::DraggingPanel { panel_id, last_screen: pos };
            }

            DragState::ResizingPanel { panel_id, handle, last_screen } => {
                // Screen delta → world delta so resize feels zoom-invariant.
                let dx = (pf(pos.x) - pf(last_screen.x)) / self.viewport.zoom;
                let dy = (pf(pos.y) - pf(last_screen.y)) / self.viewport.zoom;
                if let Some(p) = self.panels.iter_mut().find(|p| p.id == panel_id) {
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
            // Cmd+scroll → zoom toward cursor.
            let factor = if dy > 0.0 { 1.08 } else { 1.0 / 1.08 };
            self.viewport = self.viewport.zoom_toward(event.position, factor);
        } else {
            // Regular scroll → pan.
            self.viewport.origin.x -= dx / self.viewport.zoom;
            self.viewport.origin.y -= dy / self.viewport.zoom;
        }
        cx.notify();
    }
}

impl Focusable for TerminalCanvasView {
    fn focus_handle(&self, _cx: &App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for TerminalCanvasView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let viewport = self.viewport;
        let window_size = window.viewport_size();
        let ws_w = pf(window_size.width);
        let ws_h = pf(window_size.height);
        let focus_handle = self.focus_handle.clone();
        let focused_panel = self.focused_panel;

        // Apply focus to a panel if requested (e.g. after auto-spawning the first one).
        if let Some(fid) = self.needs_focus_panel.take() {
            if let Some(panel) = self.panels.iter().find(|p| p.id == fid) {
                panel.view.read(cx).focus_handle.clone().focus(window);
            }
        }

        // Push content_size (world-space, zoom-invariant → fixed cols/rows) and
        // zoom (for rendering) into every TerminalView each frame.  Cols/rows
        // only change when the panel is physically resized; the visual scale
        // changes with zoom so the terminal looks like a tldraw canvas node.
        let zoom = viewport.zoom;
        for panel in &self.panels {
            let content_w = panel.width;
            let content_h = (panel.height - TITLE_HEIGHT).max(0.0);
            panel.view.update(cx, |tv, inner_cx| {
                let size_changed = tv.set_content_size(content_w, content_h);
                let zoom_changed = tv.set_zoom(zoom);
                if size_changed || zoom_changed {
                    inner_cx.notify();
                }
            });
        }

        let mut root = div()
            .size_full()
            .bg(rgb(0x1a1a1a))
            .track_focus(&focus_handle)
            .on_mouse_down(MouseButton::Left, cx.listener(Self::on_mouse_down))
            .on_mouse_move(cx.listener(Self::on_mouse_move))
            .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
            .on_scroll_wheel(cx.listener(Self::on_scroll_wheel))
            .child(GridElement { viewport });

        if self.panels.is_empty() {
            root = root.child(
                div()
                    .absolute()
                    .size_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .child(
                        div()
                            .text_color(rgb(0x555555))
                            .child(SharedString::from("Waiting for sessions…")),
                    ),
            );
            return root;
        }

        for panel in &self.panels {
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
            let border_color = if is_focused { rgb(0x4a9eff) } else { rgb(0x444455) };
            let content_h = (sh - title_h).max(0.0);
            let title: SharedString = panel.title.clone();
            let terminal_view = panel.view.clone();

            let panel_div = div()
                .absolute()
                .left(px(sx))
                .top(px(sy))
                .w(px(sw))
                .h(px(sh))
                .border_1()
                .border_color(border_color)
                // Title bar
                .child(
                    div()
                        .w_full()
                        .h(px(title_h))
                        .bg(rgb(0x252535))
                        .flex()
                        .items_center()
                        .pl(px(8.0))
                        .child(div().text_xs().text_color(rgb(0xaaaaaa)).child(title)),
                )
                // Terminal body — overflow_hidden prevents content bleeding.
                .child(
                    div()
                        .w_full()
                        .h(px(content_h))
                        .overflow_hidden()
                        .child(terminal_view),
                )
                // Corner resize handles (fixed screen-space size).
                .child(corner_handle(0.0, 0.0))
                .child(corner_handle(sw - HANDLE_SIZE, 0.0))
                .child(corner_handle(0.0, sh - HANDLE_SIZE))
                .child(corner_handle(sw - HANDLE_SIZE, sh - HANDLE_SIZE));

            root = root.child(panel_div);
        }

        root
    }
}

// ── Helpers ───────────────────────────────────────────────────────────────────

/// Compute (cols, rows) for a terminal panel from its world-space dimensions.
/// Zoom is intentionally excluded: terminal cell size is fixed in screen pixels.
fn panel_terminal_dims(world_w: f32, world_h: f32) -> (u16, u16) {
    let cols = ((world_w / CHAR_WIDTH) as u16).max(1);
    let rows = (((world_h - TITLE_HEIGHT) / ROW_HEIGHT) as u16).max(1);
    (cols, rows)
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
