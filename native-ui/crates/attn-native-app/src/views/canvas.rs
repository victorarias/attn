/// Workspace canvas — pan/zoom/drag/resize/focus on top of the panels
/// stored in the selected `Entity<Workspace>`.
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
    Focusable, KeyDownEvent, MouseButton, MouseDownEvent, MouseMoveEvent, MouseUpEvent,
    ParentElement, Pixels, Render, ScrollDelta, ScrollWheelEvent, SharedString, Subscription,
    Window,
};

/// Callback invoked when the user picks an agent from the canvas's
/// "+ Session" toolbar. The canvas only knows the selected workspace
/// and the chosen agent label; resolving the cwd and minting a session
/// id happen on the `NativeApp` side.
pub type SpawnSessionHandler =
    dyn Fn(SharedString, SharedString, &mut Window, &mut gpui::App) + 'static;

/// Callback invoked when the user clicks a panel's close button. The
/// canvas hands off the session id; `NativeApp` sends the daemon
/// `unregister`, which cascades to `session_unregistered` and the panel
/// is pruned by `sync_terminal_panels`.
pub type CloseSessionHandler = dyn Fn(SharedString, &mut Window, &mut gpui::App) + 'static;

/// Callback invoked when a drag/resize gesture ends. The canvas keeps
/// transient geometry local while the pointer is moving, then commits the
/// final daemon panel id + geometry to the app coordinator.
pub type PanelGeometryCommitHandler =
    dyn Fn(SharedString, SharedString, f32, f32, f32, f32, &mut Window, &mut gpui::App) + 'static;

use serde_json::{json, Value};

use crate::domain::panel_navigation::{navigate_panel, NavigationDirection, PanelNavItem};
use crate::domain::viewport::{pf, Viewport};
use crate::state::panel::{Panel, TITLE_HEIGHT};
use crate::state::workspace::Workspace;
use crate::views::fps_overlay::{self, FpsCounter};

// ── Layout constants ─────────────────────────────────────────────────────────

const HANDLE_SIZE: f32 = 8.0; // screen-space pixels (fixed, not scaled)
const PANEL_MIN_W: f32 = 120.0; // world-space
const PANEL_MIN_H: f32 = 80.0; // world-space
const TITLE_SCREEN_MIN_H: f32 = 18.0; // screen-space; keeps title dragging usable when zoomed out

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
    PanningCanvas {
        last_screen: gpui::Point<Pixels>,
    },
    DraggingPanel {
        panel_id: usize,
        last_screen: gpui::Point<Pixels>,
    },
    ResizingPanel {
        panel_id: usize,
        handle: ResizeHandle,
        last_screen: gpui::Point<Pixels>,
    },
}

#[derive(Debug)]
enum HitResult {
    Canvas,
    PanelBody(usize),
    TitleBar(usize),
    ResizeHandle(usize, ResizeHandle),
}

pub struct WorkspaceCanvas {
    selected: Option<Entity<Workspace>>,
    /// Drop = unsubscribe. Replaced when selection changes so re-renders
    /// only fire for the workspace currently on screen.
    _selected_subscription: Option<Subscription>,
    viewport: Viewport,
    drag_state: DragState,
    selected_panel: Option<usize>,
    input_focused_panel: Option<usize>,
    focus_handle: FocusHandle,
    /// Window-relative bounds of the canvas's root element, captured each
    /// frame via a `canvas()` prepaint callback. Mouse events arrive with
    /// window-relative coordinates, so hit testing and zoom focal points
    /// must subtract this origin to land in canvas-local space. Without
    /// this, the sidebar's 240px width breaks every panel hit test.
    bounds: Rc<Cell<Option<Bounds<Pixels>>>>,
    /// Frame-time overlay. `Some` only when `ATTN_NATIVE_FPS=1` was set
    /// at startup. Off by default — no record cost, no overlay paint.
    fps: Option<FpsCounter>,
    /// True while the agent picker chip strip is expanded under the
    /// "+ Session" pill. Click outside or pick an agent to dismiss.
    spawn_picker_open: bool,
    on_spawn: Box<SpawnSessionHandler>,
    on_close_session: Box<CloseSessionHandler>,
    on_panel_geometry_commit: Box<PanelGeometryCommitHandler>,
}

impl WorkspaceCanvas {
    pub fn new(
        cx: &mut Context<Self>,
        on_spawn: impl Fn(SharedString, SharedString, &mut Window, &mut gpui::App) + 'static,
        on_close_session: impl Fn(SharedString, &mut Window, &mut gpui::App) + 'static,
        on_panel_geometry_commit: impl Fn(SharedString, SharedString, f32, f32, f32, f32, &mut Window, &mut gpui::App)
            + 'static,
    ) -> Self {
        let fps = if env_flag("ATTN_NATIVE_FPS") {
            Some(FpsCounter::new())
        } else {
            None
        };
        Self {
            selected: None,
            _selected_subscription: None,
            viewport: Viewport::default(),
            drag_state: DragState::Idle,
            selected_panel: None,
            input_focused_panel: None,
            focus_handle: cx.focus_handle(),
            bounds: Rc::new(Cell::new(None)),
            fps,
            spawn_picker_open: false,
            on_spawn: Box::new(on_spawn),
            on_close_session: Box::new(on_close_session),
            on_panel_geometry_commit: Box::new(on_panel_geometry_commit),
        }
    }

    /// Apply a zoom level to the canvas, centered on the canvas
    /// midpoint. Used by the automation `set_zoom` action so headless
    /// scripts can drive perf measurements at known zoom levels.
    /// `reset_fps=true` clears the FPS counter (when enabled) so
    /// post-change samples reflect the new steady state.
    pub fn set_zoom_centered(&mut self, target_zoom: f32, reset_fps: bool, cx: &mut Context<Self>) {
        let center = match self.bounds.get() {
            Some(b) => point(
                b.origin.x + b.size.width / 2.0,
                b.origin.y + b.size.height / 2.0,
            ),
            None => point(gpui::px(500.0), gpui::px(400.0)),
        };
        let local = self.local_pos(center);
        let factor = target_zoom / self.viewport.zoom;
        self.viewport = self.viewport.zoom_toward(local, factor);
        if reset_fps {
            if let Some(fps) = self.fps.as_mut() {
                fps.reset();
            }
        }
        cx.notify();
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

    /// JSON view used by the UI automation server. Captures viewport,
    /// focus, the canvas's window-relative bounds, and the latest FPS
    /// readout (when `ATTN_NATIVE_FPS=1`) so test scripts can translate
    /// world coordinates into screen pixels for OS-level input and read
    /// frame timing without inducing an extra render.
    pub fn automation_snapshot(&self) -> Value {
        let bounds = self.bounds.get().map(|b| {
            json!({
                "x": pf(b.origin.x),
                "y": pf(b.origin.y),
                "width": pf(b.size.width),
                "height": pf(b.size.height),
            })
        });
        let fps = self.fps.as_ref().map(|f| {
            let r = f.last_readout();
            json!({"fps": r.fps, "avg_ms": r.avg_ms, "last_ms": r.last_ms})
        });
        json!({
            "viewport": {
                "origin_x": self.viewport.origin.x,
                "origin_y": self.viewport.origin.y,
                "zoom": self.viewport.zoom,
            },
            // focused_panel_id is retained for older scripts; new callers
            // should read selected_panel_id + input_focused_panel_id.
            "focused_panel_id": self.selected_panel,
            "selected_panel_id": self.selected_panel,
            "input_focused_panel_id": self.input_focused_panel,
            "bounds": bounds,
            "fps": fps,
        })
    }

    pub fn set_selected(&mut self, ws: Option<Entity<Workspace>>, cx: &mut Context<Self>) {
        self._selected_subscription = ws.as_ref().map(|w| cx.observe(w, |_, _, cx| cx.notify()));
        self.selected = ws;
        self.drag_state = DragState::Idle;
        self.selected_panel = None;
        self.input_focused_panel = None;
        cx.notify();
    }

    /// Select a panel by session id and optionally route keyboard input
    /// into its terminal. Used by automation and NativeApp-level focus
    /// commands so tests exercise the same canvas state as pointer input.
    pub fn set_panel_focus_by_session(
        &mut self,
        session_id: &str,
        input_focus: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        let Some(ws) = self.selected.as_ref() else {
            return Err("no selected workspace".to_string());
        };
        let panel = ws
            .read(cx)
            .panels
            .iter()
            .find(|panel| panel.session_id.as_ref() == session_id)
            .cloned()
            .ok_or_else(|| format!("no panel for session in selected workspace: {session_id}"))?;

        self.select_panel(panel.id);
        if input_focus {
            self.focus_panel_input(&panel, window, cx);
        } else {
            self.release_input_focus(window);
        }
        cx.notify();
        Ok(())
    }

    fn select_panel(&mut self, id: usize) {
        self.selected_panel = Some(id);
    }

    fn navigate_selected_panel(&mut self, direction: NavigationDirection, cx: &mut Context<Self>) {
        let panels: Vec<PanelNavItem> = self
            .panels_snapshot(cx)
            .into_iter()
            .map(|panel| PanelNavItem {
                id: panel.id,
                world_x: panel.world_x,
                world_y: panel.world_y,
                width: panel.width,
                height: panel.height,
            })
            .collect();
        if let Some(next_id) = navigate_panel(&panels, self.selected_panel, direction) {
            self.selected_panel = Some(next_id);
        }
    }

    fn release_input_focus(&mut self, window: &mut Window) {
        self.input_focused_panel = None;
        self.focus_handle.clone().focus(window);
    }

    fn focus_panel_input(&mut self, panel: &Panel, window: &mut Window, cx: &App) {
        self.selected_panel = Some(panel.id);
        self.input_focused_panel = Some(panel.id);
        panel.view.read(cx).focus_handle.clone().focus(window);
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
            let sp = self
                .viewport
                .world_to_screen(point(panel.world_x, panel.world_y));
            let sx = pf(sp.x);
            let sy = pf(sp.y);
            let sw = panel.width * zoom;
            let sh = panel.height * zoom;
            let title_h = title_screen_height(zoom);

            // Resize corner zones — fixed 8+2px hit area in screen space.
            let half = HANDLE_SIZE / 2.0 + 2.0;
            if top_resize_handles_enabled(zoom)
                && (mx - sx).abs() <= half
                && (my - sy).abs() <= half
            {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::TopLeft);
            }
            if top_resize_handles_enabled(zoom)
                && (mx - (sx + sw)).abs() <= half
                && (my - sy).abs() <= half
            {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::TopRight);
            }
            if (mx - sx).abs() <= half && (my - (sy + sh)).abs() <= half {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::BottomLeft);
            }
            if (mx - (sx + sw)).abs() <= half && (my - (sy + sh)).abs() <= half {
                return HitResult::ResizeHandle(panel.id, ResizeHandle::BottomRight);
            }

            if mx >= sx && mx <= sx + sw && my >= sy && my <= sy + sh {
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
        // hit_test takes canvas-local coords (panel screen positions are
        // computed from world*zoom with no canvas offset). DragState's
        // last_screen stays window-relative so it matches on_mouse_move,
        // where deltas are computed against event.position.
        let hit = self.hit_test(self.local_pos(event.position), cx);
        let pos = event.position;
        match hit {
            HitResult::TitleBar(id) => {
                self.select_panel(id);
                self.release_input_focus(window);
                self.drag_state = DragState::DraggingPanel {
                    panel_id: id,
                    last_screen: pos,
                };
            }
            HitResult::ResizeHandle(id, handle) => {
                self.select_panel(id);
                self.release_input_focus(window);
                self.drag_state = DragState::ResizingPanel {
                    panel_id: id,
                    handle,
                    last_screen: pos,
                };
            }
            HitResult::PanelBody(id) => {
                if let Some(ws) = self.selected.as_ref() {
                    let panel = ws.read(cx).panels.iter().find(|p| p.id == id).cloned();
                    if let Some(panel) = panel {
                        self.focus_panel_input(&panel, window, cx);
                    }
                }
                self.drag_state = DragState::Idle;
            }
            HitResult::Canvas => {
                self.release_input_focus(window);
                self.drag_state = DragState::PanningCanvas { last_screen: pos };
            }
        }
        cx.notify();
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        match event.keystroke.key.as_str() {
            "escape" if self.input_focused_panel.is_some() => {
                cx.stop_propagation();
                self.release_input_focus(window);
                cx.notify();
            }
            _ if self.input_focused_panel.is_some() => {}
            "tab" if self.focus_handle.is_focused(window) => {
                cx.stop_propagation();
                if event.keystroke.modifiers.shift {
                    self.navigate_selected_panel(NavigationDirection::Previous, cx);
                } else {
                    self.navigate_selected_panel(NavigationDirection::Next, cx);
                }
                cx.notify();
            }
            key @ ("up" | "down" | "left" | "right" | "h" | "j" | "k" | "l")
                if self.focus_handle.is_focused(window) =>
            {
                cx.stop_propagation();
                let direction = match key {
                    "up" | "k" => NavigationDirection::Up,
                    "down" | "j" => NavigationDirection::Down,
                    "left" | "h" => NavigationDirection::Left,
                    "right" | "l" => NavigationDirection::Right,
                    _ => unreachable!(),
                };
                self.navigate_selected_panel(direction, cx);
                cx.notify();
            }
            "enter" if self.focus_handle.is_focused(window) => {
                let Some(selected_id) = self.selected_panel else {
                    return;
                };
                let Some(ws) = self.selected.as_ref() else {
                    return;
                };
                let panel = ws
                    .read(cx)
                    .panels
                    .iter()
                    .find(|panel| panel.id == selected_id)
                    .cloned();
                if let Some(panel) = panel {
                    cx.stop_propagation();
                    self.focus_panel_input(&panel, window, cx);
                    cx.notify();
                }
            }
            _ => {}
        }
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

            DragState::DraggingPanel {
                panel_id,
                last_screen,
            } => {
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
                self.drag_state = DragState::DraggingPanel {
                    panel_id,
                    last_screen: pos,
                };
            }

            DragState::ResizingPanel {
                panel_id,
                handle,
                last_screen,
            } => {
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
                self.drag_state = DragState::ResizingPanel {
                    panel_id,
                    handle,
                    last_screen: pos,
                };
            }
        }
        cx.notify();
    }

    fn on_mouse_up(&mut self, _event: &MouseUpEvent, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(panel_id) = match &self.drag_state {
            DragState::DraggingPanel { panel_id, .. }
            | DragState::ResizingPanel { panel_id, .. } => Some(*panel_id),
            _ => None,
        } {
            if let Some(ws) = self.selected.as_ref() {
                let workspace_id = ws.read(cx).id.clone();
                if let Some(panel) = ws.read(cx).panels.iter().find(|p| p.id == panel_id) {
                    (self.on_panel_geometry_commit)(
                        workspace_id,
                        panel.daemon_panel_id.clone(),
                        panel.world_x,
                        panel.world_y,
                        panel.width,
                        panel.height,
                        window,
                        cx,
                    );
                }
            }
        }
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
            self.viewport = self
                .viewport
                .zoom_toward(self.local_pos(event.position), factor);
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

fn title_screen_height(zoom: f32) -> f32 {
    (TITLE_HEIGHT * zoom).max(TITLE_SCREEN_MIN_H)
}

fn top_resize_handles_enabled(zoom: f32) -> bool {
    TITLE_HEIGHT * zoom >= TITLE_SCREEN_MIN_H
}

fn reconcile_panel_focus(
    selected_panel: &mut Option<usize>,
    input_focused_panel: &mut Option<usize>,
    panel_ids: impl IntoIterator<Item = usize>,
) -> bool {
    let panel_ids: std::collections::HashSet<usize> = panel_ids.into_iter().collect();
    let mut cleared_input_focus = false;
    if selected_panel.is_some_and(|id| !panel_ids.contains(&id)) {
        *selected_panel = None;
    }
    if input_focused_panel.is_some_and(|id| !panel_ids.contains(&id)) {
        *input_focused_panel = None;
        cleared_input_focus = true;
    }
    cleared_input_focus
}

impl Focusable for WorkspaceCanvas {
    fn focus_handle(&self, _cx: &App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for WorkspaceCanvas {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        // Tick the FPS counter once per render when enabled. Skipped
        // entirely when `ATTN_NATIVE_FPS` is unset — the field is `None`
        // and there's no per-frame cost.
        let readout = self.fps.as_mut().map(|f| f.record_frame());
        let viewport = self.viewport;
        let window_size = window.viewport_size();
        let ws_w = pf(window_size.width);
        let ws_h = pf(window_size.height);
        let focus_handle = self.focus_handle.clone();

        let bounds_capture = self.bounds.clone();
        // Grid dots intentionally omitted: snapping (when added) reads
        // `viewport::GRID_SPACING` directly in code, no visible grid
        // required. Removing the per-frame paint_quad-per-dot work keeps
        // the frame budget clear at every zoom level.
        let mut root = div()
            .size_full()
            .bg(rgb(0x0e0e14))
            .track_focus(&focus_handle)
            .capture_key_down(cx.listener(Self::on_key_down))
            .on_key_down(cx.listener(Self::on_key_down))
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
            );

        let Some(selected) = self.selected.clone() else {
            let mut r = root.child(empty_state(SharedString::from("Select a workspace")));
            if let Some(readout) = readout {
                r = r.child(fps_overlay::overlay(readout, 0, viewport.zoom));
            }
            return r;
        };

        let workspace_id = selected.read(cx).id.clone();
        let panels = selected.read(cx).panels.clone();
        if reconcile_panel_focus(
            &mut self.selected_panel,
            &mut self.input_focused_panel,
            panels.iter().map(|panel| panel.id),
        ) {
            self.focus_handle.clone().focus(window);
        }
        let selected_panel = self.selected_panel;
        let input_focused_panel = self.input_focused_panel;

        // Push content_size + zoom into every TerminalView so their next
        // render syncs cell dims and emits PtyResize on actual change.
        let zoom = viewport.zoom;
        for panel in &panels {
            let content_w = panel.width;
            let content_h = (panel.height - TITLE_HEIGHT).max(0.0);
            let input_enabled = input_focused_panel == Some(panel.id);
            panel.view.update(cx, |tv, inner_cx| {
                let size_changed = tv.set_content_size(content_w, content_h);
                let zoom_changed = tv.set_zoom(zoom);
                let input_changed = tv.set_input_enabled(input_enabled);
                if size_changed || zoom_changed || input_changed {
                    inner_cx.notify();
                }
            });
        }

        for panel in &panels {
            let sp = viewport.world_to_screen(point(panel.world_x, panel.world_y));
            let sx = pf(sp.x);
            let sy = pf(sp.y);
            let sw = panel.width * viewport.zoom;
            let sh = panel.height * viewport.zoom;
            let title_h = title_screen_height(viewport.zoom);

            // Viewport culling — skip fully off-screen panels.
            if sx + sw < 0.0 || sy + sh < 0.0 || sx > ws_w || sy > ws_h {
                continue;
            }

            let has_input_focus = input_focused_panel == Some(panel.id);
            let is_selected = selected_panel == Some(panel.id);
            let border_color = if has_input_focus {
                rgb(0x4a9eff)
            } else if is_selected {
                rgb(0xc59b45)
            } else {
                rgb(0x2a2a35)
            };
            let content_h = (sh - title_h).max(0.0);
            let title = panel.title.clone();

            let body: AnyElement = panel.view.clone().into_any_element();

            let mut title_bar = div()
                .w_full()
                .h(px(title_h))
                .bg(rgb(0x252535))
                .flex()
                .items_center()
                .pl(px(8.0))
                .pr(px(4.0))
                .child(
                    div()
                        .flex_1()
                        .truncate()
                        .text_xs()
                        .text_color(rgb(0xa0a0b0))
                        .child(title),
                );
            title_bar = title_bar.child(panel_close_button(panel.session_id.clone(), cx));

            let panel_div = div()
                .absolute()
                .left(px(sx))
                .top(px(sy))
                .w(px(sw))
                .h(px(sh))
                .bg(rgb(0x1c1c26))
                .border_1()
                .border_color(border_color)
                .child(title_bar)
                .child(
                    div()
                        .w_full()
                        .h(px(content_h))
                        .overflow_hidden()
                        .child(body),
                )
                .child(corner_handle(0.0, 0.0))
                .child(corner_handle(sw - HANDLE_SIZE, 0.0))
                .child(corner_handle(0.0, sh - HANDLE_SIZE))
                .child(corner_handle(sw - HANDLE_SIZE, sh - HANDLE_SIZE));

            root = root.child(panel_div);
        }

        if panels.is_empty() {
            root = root.child(empty_state(SharedString::from(
                "Workspace has no panels yet — pick an agent above to start one",
            )));
        }

        // Render the spawn toolbar last so it sits on top of panels and
        // empty-state copy. Always visible when a workspace is selected so
        // the entry point to dogfood is unmissable.
        root = root.child(self.render_spawn_toolbar(workspace_id, cx));

        if let Some(readout) = readout {
            root = root.child(fps_overlay::overlay(readout, panels.len(), viewport.zoom));
        }
        root
    }
}

impl WorkspaceCanvas {
    /// Top-left "+ Session" pill plus an inline agent picker (Claude /
    /// Codex / Shell) that expands when the pill is clicked. All chips
    /// stop propagation so clicks don't also pan the canvas underneath.
    fn render_spawn_toolbar(
        &self,
        workspace_id: SharedString,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let mut row = div()
            .absolute()
            .top(px(12.0))
            .left(px(12.0))
            .flex()
            .flex_row()
            .items_center()
            .gap(px(6.0))
            .child(spawn_pill(self.spawn_picker_open).on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, _, cx| {
                    cx.stop_propagation();
                    this.spawn_picker_open = !this.spawn_picker_open;
                    cx.notify();
                }),
            ));

        if self.spawn_picker_open {
            for (label, agent_id) in SPAWNABLE_AGENTS {
                let agent = SharedString::from(*agent_id);
                let workspace_id = workspace_id.clone();
                row = row.child(agent_chip(SharedString::from(*label)).on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, window, cx| {
                        cx.stop_propagation();
                        let workspace_id = workspace_id.clone();
                        let agent = agent.clone();
                        this.spawn_picker_open = false;
                        (this.on_spawn)(workspace_id, agent, window, cx);
                        cx.notify();
                    }),
                ));
            }
        }

        row.into_any_element()
    }
}

/// Trailing close button rendered on each terminal panel's title bar.
/// Shares the dim grey treatment of corner_handle / sidebar's delete `x`
/// so the eye reads it as an affordance, not a primary action.
fn panel_close_button(session_id: SharedString, cx: &mut Context<WorkspaceCanvas>) -> gpui::Div {
    div()
        .w(px(20.0))
        .h(px(18.0))
        .flex_shrink_0()
        .flex()
        .items_center()
        .justify_center()
        .text_color(rgb(0x6a6a78))
        .text_size(px(13.0))
        .child(SharedString::from("x"))
        .on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, _, window, cx| {
                // Stop propagation so the click doesn't also trigger the
                // canvas's title-bar drag-start hit (which would mid-drag
                // the panel we're about to remove).
                cx.stop_propagation();
                let id = session_id.clone();
                (this.on_close_session)(id, window, cx);
            }),
        )
}

/// Static list of agents the canvas spawn toolbar offers. Strings on the
/// right are the wire-level agent identifiers the daemon understands
/// (`internal/protocol/schema/main.tsp` SessionAgent enum). Adding more
/// is a one-line append once we have icons / labels for them.
const SPAWNABLE_AGENTS: &[(&str, &str)] =
    &[("Claude", "claude"), ("Codex", "codex"), ("Shell", "shell")];

/// "+ Session" pill. Visually distinct from agent chips so it reads as
/// the disclosure trigger, not one of the choices. Highlighted when the
/// picker is expanded so it's clear which surface is active.
fn spawn_pill(open: bool) -> gpui::Div {
    let bg = if open { rgb(0x3a3a4a) } else { rgb(0x252535) };
    let text = if open { rgb(0xf0f0f5) } else { rgb(0xc8c8d2) };
    div()
        .px(px(10.0))
        .py(px(4.0))
        .rounded(px(4.0))
        .bg(bg)
        .text_color(text)
        .text_size(px(12.0))
        .child(SharedString::from("+ Session"))
}

fn agent_chip(label: SharedString) -> gpui::Div {
    div()
        .px(px(8.0))
        .py(px(4.0))
        .rounded(px(4.0))
        .bg(rgb(0x2c2c3a))
        .border_1()
        .border_color(rgb(0x3a3a4a))
        .text_color(rgb(0xd8d8e2))
        .text_size(px(12.0))
        .child(label)
}

fn env_flag(name: &str) -> bool {
    matches!(std::env::var(name).as_deref(), Ok("1") | Ok("true"))
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn title_hit_area_has_screen_minimum_when_zoomed_out() {
        assert_eq!(title_screen_height(0.25), TITLE_SCREEN_MIN_H);
        assert!(!top_resize_handles_enabled(0.25));
    }

    #[test]
    fn title_hit_area_scales_normally_when_zoomed_in() {
        assert_eq!(title_screen_height(1.0), TITLE_HEIGHT);
        assert!(top_resize_handles_enabled(1.0));
    }

    #[test]
    fn reconcile_panel_focus_clears_removed_panel_ids() {
        let mut selected = Some(7);
        let mut input = Some(7);

        let cleared_input = reconcile_panel_focus(&mut selected, &mut input, [1, 2, 3]);

        assert!(cleared_input);
        assert_eq!(selected, None);
        assert_eq!(input, None);
    }

    #[test]
    fn reconcile_panel_focus_preserves_live_panel_ids() {
        let mut selected = Some(2);
        let mut input = Some(2);

        let cleared_input = reconcile_panel_focus(&mut selected, &mut input, [1, 2, 3]);

        assert!(!cleared_input);
        assert_eq!(selected, Some(2));
        assert_eq!(input, Some(2));
    }
}
