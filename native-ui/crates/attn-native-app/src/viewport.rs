/// World ↔ screen coordinate transform for the canvas.
///
/// `origin` is the world-space point visible at screen (0, 0). Screen
/// position = `(world - origin) * zoom`. Sizes scale the same way — both
/// position and extents follow zoom so the canvas feels like tldraw.
use gpui::{point, px, Pixels};

/// World-space grid step. The visible grid was removed after the perf
/// spike (paint cost dominated at zoom-out); this constant survives as
/// the snap-to target for future panel-drag snapping.
#[allow(dead_code)] // unused until snapping lands; see docs/plans/2026-04-28-canvas-perf-spike.md
pub const GRID_SPACING: f32 = 50.0;

const ZOOM_MIN: f32 = 0.15;
const ZOOM_MAX: f32 = 5.0;

/// Extract the underlying `f32` from a `Pixels` value. `Pixels` wraps a
/// crate-private float, so callers reach for `f32::from(p)` constantly —
/// this just spells it shorter.
#[inline]
pub fn pf(p: Pixels) -> f32 {
    f32::from(p)
}

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

    /// Zoom toward a screen-space point, holding the world point under
    /// the cursor in place. Clamps to `[ZOOM_MIN, ZOOM_MAX]`.
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
