//! `Render` impls. Views observe state entities and produce pixels. They
//! may hold adapter handles only for outbound commands (e.g.
//! `TerminalView` sending `PtyInput`); they never read cached state from
//! an adapter — that's what state entities are for.

pub mod canvas;
pub mod fps_overlay;
pub mod location_dialog;
pub mod settings_page;
pub mod sidebar;
pub mod terminal_view;
