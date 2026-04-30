/// Panels are the canvas's spatial terminal objects. Each carries
/// world-space position + size and the terminal view that handles rendering,
/// focus, resize, and PTY input.
use gpui::{Entity, SharedString};

use crate::views::terminal_view::TerminalView;

/// World-space height of the panel title bar. Used by both the canvas
/// (for hit testing + title-bar layout) and `NativeApp` (for initial
/// `content_size` when a panel is created). Single source of truth.
pub const TITLE_HEIGHT: f32 = 24.0;

#[derive(Clone, Debug)]
pub struct Panel {
    pub id: usize,
    pub title: SharedString,
    pub world_x: f32,
    pub world_y: f32,
    pub width: f32,
    pub height: f32,
    pub session_id: SharedString,
    pub view: Entity<TerminalView>,
}
