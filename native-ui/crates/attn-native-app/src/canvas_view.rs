// This module is shared between attn-canvas (spike 3) and attn-spike4.
// Not all items are used by both binaries.
#![allow(dead_code)]
/// Spike 3: Infinite canvas with dummy panels.
///
/// Viewport transforms world ↔ screen coordinates. Panels are positioned in
/// world space; their sizes stay fixed in screen pixels (no scaling on zoom).
/// All mouse handling lives on the root div with manual hit-testing.
use gpui::{
    div, fill, point, prelude::*, px, rgb, size, App, Bounds, ElementId, FocusHandle,
    Focusable, GlobalElementId, InspectorElementId, LayoutId, MouseButton, MouseDownEvent,
    MouseMoveEvent, MouseUpEvent, Pixels, ScrollDelta, ScrollWheelEvent, SharedString, Size,
    Style, Window, Context,
};

// ── Layout constants ─────────────────────────────────────────────────────────

const TITLE_HEIGHT: f32 = 24.0;
const HANDLE_SIZE: f32 = 8.0;
const PANEL_MIN_W: f32 = 80.0;
const PANEL_MIN_H: f32 = 60.0;

const GRID_SPACING: f32 = 50.0;
const ZOOM_MIN: f32 = 0.15;
const ZOOM_MAX: f32 = 5.0;

const CANVAS_BG: u32 = 0x1a1a1a;
const GRID_DOT_COLOR: u32 = 0x333333;

/// Extract the underlying f32 from a Pixels value.
#[inline]
pub fn pf(p: Pixels) -> f32 {
    f32::from(p)
}

// ── Viewport ─────────────────────────────────────────────────────────────────

/// Viewport maps world ↔ screen.
///
/// `origin` is the world-space point visible at screen (0, 0).
/// `screen_pos = (world_pos - origin) * zoom`
#[derive(Clone, Copy, Debug)]
pub struct Viewport {
    pub origin: gpui::Point<f32>,
    pub zoom: f32,
}

impl Default for Viewport {
    fn default() -> Self {
        Viewport { origin: point(0.0_f32, 0.0_f32), zoom: 1.0 }
    }
}

impl Viewport {
    pub fn world_to_screen(&self, world: gpui::Point<f32>) -> gpui::Point<Pixels> {
        point(
            px((world.x - self.origin.x) * self.zoom),
            px((world.y - self.origin.y) * self.zoom),
        )
    }

    pub fn screen_to_world(&self, screen: gpui::Point<Pixels>) -> gpui::Point<f32> {
        point(pf(screen.x) / self.zoom + self.origin.x, pf(screen.y) / self.zoom + self.origin.y)
    }

    /// Zoom toward a screen-space point, maintaining the world point under the cursor.
    pub fn zoom_toward(&self, screen_pt: gpui::Point<Pixels>, factor: f32) -> Viewport {
        let new_zoom = (self.zoom * factor).clamp(ZOOM_MIN, ZOOM_MAX);
        if (new_zoom - self.zoom).abs() < 1e-6 {
            return *self;
        }
        let world_pt = self.screen_to_world(screen_pt);
        Viewport {
            zoom: new_zoom,
            origin: point(
                world_pt.x - pf(screen_pt.x) / new_zoom,
                world_pt.y - pf(screen_pt.y) / new_zoom,
            ),
        }
    }
}

// ── Panel data ────────────────────────────────────────────────────────────────

#[derive(Clone, Debug)]
pub struct PanelData {
    pub id: usize,
    pub title: String,
    pub color: u32,
    pub world_x: f32,
    pub world_y: f32,
    pub width: f32,  // screen pixels — does not change with zoom
    pub height: f32, // screen pixels — does not change with zoom
}

// ── Drag/resize state ─────────────────────────────────────────────────────────

#[derive(Clone, Copy, Debug)]
pub enum ResizeHandle {
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
    PanelBody(#[allow(dead_code)] usize),
    TitleBar(usize),
    ResizeHandle(usize, ResizeHandle),
}

// ── WorkspaceCanvasView ───────────────────────────────────────────────────────

pub struct WorkspaceCanvasView {
    panels: Vec<PanelData>,
    viewport: Viewport,
    drag_state: DragState,
    focus_handle: FocusHandle,
}

impl WorkspaceCanvasView {
    pub fn new(cx: &mut Context<Self>) -> Self {
        WorkspaceCanvasView {
            panels: initial_panels(),
            viewport: Viewport::default(),
            drag_state: DragState::Idle,
            focus_handle: cx.focus_handle(),
        }
    }

    // ── Hit testing ──────────────────────────────────────────────────────────

    fn hit_test(&self, screen_pos: gpui::Point<Pixels>) -> HitResult {
        let mx = pf(screen_pos.x);
        let my = pf(screen_pos.y);
        let zoom = self.viewport.zoom;

        // Iterate panels in reverse so the topmost (last rendered) is checked first.
        for panel in self.panels.iter().rev() {
            let sp = self.viewport.world_to_screen(point(panel.world_x, panel.world_y));
            let sx = pf(sp.x);
            let sy = pf(sp.y);
            // Screen-space size scales with zoom.
            let sw = panel.width * zoom;
            let sh = panel.height * zoom;

            // Resize handle zones — fixed 8px hit area in screen space.
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

            // Panel bounds check.
            if mx >= sx && mx <= sx + sw && my >= sy && my <= sy + sh {
                if my <= sy + TITLE_HEIGHT * zoom {
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
        _window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let pos = event.position;
        self.drag_state = match self.hit_test(pos) {
            HitResult::TitleBar(id) => {
                DragState::DraggingPanel { panel_id: id, last_screen: pos }
            }
            HitResult::ResizeHandle(id, handle) => {
                DragState::ResizingPanel { panel_id: id, handle, last_screen: pos }
            }
            HitResult::Canvas | HitResult::PanelBody(_) => {
                DragState::PanningCanvas { last_screen: pos }
            }
        };
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
                // Pan: drag right → origin decreases → canvas content moves right.
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
                // Convert screen delta → world delta so resize is zoom-invariant.
                let dx = (pf(pos.x) - pf(last_screen.x)) / self.viewport.zoom;
                let dy = (pf(pos.y) - pf(last_screen.y)) / self.viewport.zoom;
                if let Some(p) = self.panels.iter_mut().find(|p| p.id == panel_id) {
                    match handle {
                        ResizeHandle::TopLeft => {
                            let new_w = (p.width - dx).max(PANEL_MIN_W);
                            let new_h = (p.height - dy).max(PANEL_MIN_H);
                            // world_x/y move by the amount we couldn't shrink further.
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
                self.drag_state = DragState::ResizingPanel { panel_id, handle, last_screen: pos };
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

impl Focusable for WorkspaceCanvasView {
    fn focus_handle(&self, _cx: &gpui::App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for WorkspaceCanvasView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let viewport = self.viewport;
        let window_size = window.viewport_size();
        let ws_w = pf(window_size.width);
        let ws_h = pf(window_size.height);
        let focus_handle = self.focus_handle.clone();

        let mut root = div()
            .size_full()
            .bg(rgb(CANVAS_BG))
            .track_focus(&focus_handle)
            .on_mouse_down(MouseButton::Left, cx.listener(Self::on_mouse_down))
            .on_mouse_move(cx.listener(Self::on_mouse_move))
            .on_mouse_up(MouseButton::Left, cx.listener(Self::on_mouse_up))
            .on_scroll_wheel(cx.listener(Self::on_scroll_wheel))
            .child(GridElement { viewport });

        for panel in &self.panels {
            let sp = viewport.world_to_screen(point(panel.world_x, panel.world_y));
            let sx = pf(sp.x);
            let sy = pf(sp.y);
            // World-space size scaled to screen space.
            let sw = panel.width * viewport.zoom;
            let sh = panel.height * viewport.zoom;
            let title_h = TITLE_HEIGHT * viewport.zoom;

            // Viewport culling — skip fully off-screen panels.
            if sx + sw < 0.0 || sy + sh < 0.0 || sx > ws_w || sy > ws_h {
                continue;
            }

            let title: SharedString = panel.title.clone().into();
            let header_color = darken(panel.color);
            let body_color = panel.color;

            let panel_elem = div()
                .absolute()
                .left(px(sx))
                .top(px(sy))
                .w(px(sw))
                .h(px(sh))
                .border_1()
                .border_color(rgb(0x555555))
                // Title bar
                .child(
                    div()
                        .w_full()
                        .h(px(title_h))
                        .bg(rgb(header_color))
                        .flex()
                        .items_center()
                        .pl(px(8.0))
                        .child(div().text_sm().text_color(rgb(0xffffff)).child(title)),
                )
                // Body
                .child(div().w_full().flex_1().bg(rgb(body_color)))
                // Corner resize handles — fixed 8px in screen space, positioned at corners.
                .child(
                    div()
                        .absolute()
                        .left(px(0.0))
                        .top(px(0.0))
                        .w(px(HANDLE_SIZE))
                        .h(px(HANDLE_SIZE))
                        .bg(rgb(0xcccccc))
                        .rounded(px(1.0)),
                )
                .child(
                    div()
                        .absolute()
                        .left(px(sw - HANDLE_SIZE))
                        .top(px(0.0))
                        .w(px(HANDLE_SIZE))
                        .h(px(HANDLE_SIZE))
                        .bg(rgb(0xcccccc))
                        .rounded(px(1.0)),
                )
                .child(
                    div()
                        .absolute()
                        .left(px(0.0))
                        .top(px(sh - HANDLE_SIZE))
                        .w(px(HANDLE_SIZE))
                        .h(px(HANDLE_SIZE))
                        .bg(rgb(0xcccccc))
                        .rounded(px(1.0)),
                )
                .child(
                    div()
                        .absolute()
                        .left(px(sw - HANDLE_SIZE))
                        .top(px(sh - HANDLE_SIZE))
                        .w(px(HANDLE_SIZE))
                        .h(px(HANDLE_SIZE))
                        .bg(rgb(0xcccccc))
                        .rounded(px(1.0)),
                );

            root = root.child(panel_elem);
        }

        root
    }
}

// ── Grid background element ───────────────────────────────────────────────────

pub struct GridElement {
    pub viewport: Viewport,
}

pub struct GridPrepaint {
    pub bounds: Bounds<Pixels>,
}

impl Element for GridElement {
    type RequestLayoutState = ();
    type PrepaintState = GridPrepaint;

    fn id(&self) -> Option<ElementId> {
        None
    }

    fn source_location(&self) -> Option<&'static std::panic::Location<'static>> {
        None
    }

    fn request_layout(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        window: &mut Window,
        cx: &mut App,
    ) -> (LayoutId, ()) {
        let style = Style { size: Size::full(), ..Default::default() };
        (window.request_layout(style, [], cx), ())
    }

    fn prepaint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        bounds: Bounds<Pixels>,
        _request_layout: &mut (),
        _window: &mut Window,
        _cx: &mut App,
    ) -> GridPrepaint {
        GridPrepaint { bounds }
    }

    fn paint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        _bounds: Bounds<Pixels>,
        _request_layout: &mut (),
        prepaint: &mut GridPrepaint,
        window: &mut Window,
        _cx: &mut App,
    ) {
        let b = prepaint.bounds;
        let vp = &self.viewport;

        let grid_screen_spacing = GRID_SPACING * vp.zoom;

        // Skip grid rendering when dots would be too dense (< 8px apart).
        if grid_screen_spacing < 8.0 {
            return;
        }

        let ox = pf(b.origin.x);
        let oy = pf(b.origin.y);
        let bw = pf(b.size.width);
        let bh = pf(b.size.height);

        // First grid world coordinate (snapped to GRID_SPACING) visible on screen.
        let first_wx = (vp.origin.x / GRID_SPACING).floor() * GRID_SPACING;
        let first_wy = (vp.origin.y / GRID_SPACING).floor() * GRID_SPACING;

        // Convert to screen space.
        let first_sx = ox + (first_wx - vp.origin.x) * vp.zoom;
        let first_sy = oy + (first_wy - vp.origin.y) * vp.zoom;

        let dot_r = if grid_screen_spacing > 25.0 { 1.5_f32 } else { 1.0_f32 };

        let mut sx = first_sx;
        while sx < ox + bw + grid_screen_spacing {
            let mut sy = first_sy;
            while sy < oy + bh + grid_screen_spacing {
                if sx >= ox - dot_r
                    && sy >= oy - dot_r
                    && sx <= ox + bw + dot_r
                    && sy <= oy + bh + dot_r
                {
                    window.paint_quad(fill(
                        Bounds::new(
                            point(px(sx - dot_r), px(sy - dot_r)),
                            size(px(dot_r * 2.0), px(dot_r * 2.0)),
                        ),
                        rgb(GRID_DOT_COLOR),
                    ));
                }
                sy += grid_screen_spacing;
            }
            sx += grid_screen_spacing;
        }
    }
}

impl IntoElement for GridElement {
    type Element = Self;
    fn into_element(self) -> Self {
        self
    }
}

// ── Helpers ───────────────────────────────────────────────────────────────────

fn darken(color: u32) -> u32 {
    let r = ((color >> 16) & 0xff) * 2 / 3;
    let g = ((color >> 8) & 0xff) * 2 / 3;
    let b = (color & 0xff) * 2 / 3;
    (r << 16) | (g << 8) | b
}

fn initial_panels() -> Vec<PanelData> {
    let panels = [
        ("Agent 1",   50.0_f32,  50.0_f32, 320.0, 200.0, 0x2d4a7a_u32),
        ("Agent 2",  420.0,      50.0,     320.0, 200.0, 0x4a2d7a),
        ("Shell",     50.0,     300.0,     320.0, 180.0, 0x2d7a4a),
        ("Todo",     420.0,     300.0,     280.0, 180.0, 0x7a4a2d),
        ("Browser",  750.0,      50.0,     320.0, 240.0, 0x7a2d4a),
        ("Notes",   -200.0,      80.0,     280.0, 200.0, 0x2d7a7a),
        ("Terminal", 750.0,     340.0,     320.0, 180.0, 0x7a7a2d),
        ("Search",  -200.0,     330.0,     280.0, 180.0, 0x4a7a2d),
    ];
    panels
        .iter()
        .enumerate()
        .map(|(i, (title, wx, wy, w, h, color))| PanelData {
            id: i,
            title: title.to_string(),
            color: *color,
            world_x: *wx,
            world_y: *wy,
            width: *w,
            height: *h,
        })
        .collect()
}
