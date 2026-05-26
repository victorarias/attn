/// World ↔ screen coordinate transform for the canvas.
///
/// `origin` is the world-space point visible at screen (0, 0). Screen
/// position = `(world - origin) * zoom`. Sizes scale the same way — both
/// position and extents follow zoom so the canvas feels like tldraw.
use gpui::{point, px, Pixels};

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

#[derive(Clone, Copy, Debug)]
pub struct WorldRect {
    pub x: f32,
    pub y: f32,
    pub width: f32,
    pub height: f32,
}

impl Default for Viewport {
    fn default() -> Self {
        Viewport {
            origin: point(0.0_f32, 0.0_f32),
            zoom: 1.0,
        }
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
        point(
            pf(screen.x) / self.zoom + self.origin.x,
            pf(screen.y) / self.zoom + self.origin.y,
        )
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

    pub fn pan_view_by_screen_delta(&self, dx: f32, dy: f32) -> Viewport {
        Viewport {
            origin: point(
                self.origin.x + dx / self.zoom,
                self.origin.y + dy / self.zoom,
            ),
            zoom: self.zoom,
        }
    }

    pub fn fit_world_rect(
        &self,
        rect: WorldRect,
        screen_width: f32,
        screen_height: f32,
        margin: f32,
    ) -> Viewport {
        if rect.width <= 0.0 || rect.height <= 0.0 || screen_width <= 0.0 || screen_height <= 0.0 {
            return *self;
        }
        let available_width = (screen_width - margin * 2.0).max(1.0);
        let available_height = (screen_height - margin * 2.0).max(1.0);
        let zoom = (available_width / rect.width)
            .min(available_height / rect.height)
            .clamp(ZOOM_MIN, ZOOM_MAX);
        let world_center_x = rect.x + rect.width / 2.0;
        let world_center_y = rect.y + rect.height / 2.0;
        Viewport {
            origin: point(
                world_center_x - screen_width / (2.0 * zoom),
                world_center_y - screen_height / (2.0 * zoom),
            ),
            zoom,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keyboard_pan_is_zoom_invariant_in_screen_space() {
        let viewport = Viewport {
            origin: point(10.0, 20.0),
            zoom: 2.0,
        };
        let panned = viewport.pan_view_by_screen_delta(160.0, -80.0);
        assert_eq!(panned.origin.x, 90.0);
        assert_eq!(panned.origin.y, -20.0);
        assert_eq!(panned.zoom, 2.0);
    }

    #[test]
    fn fit_world_rect_centers_rect_and_uses_limiting_axis() {
        let viewport = Viewport::default();
        let fitted = viewport.fit_world_rect(
            WorldRect {
                x: 100.0,
                y: 120.0,
                width: 500.0,
                height: 200.0,
            },
            1000.0,
            800.0,
            32.0,
        );
        assert!((fitted.zoom - 1.872).abs() < 0.001);
        assert!(((100.0 - fitted.origin.x) * fitted.zoom - 32.0).abs() < 0.001);
        assert!(((120.0 - fitted.origin.y) * fitted.zoom - 212.8).abs() < 0.01);
    }
}
