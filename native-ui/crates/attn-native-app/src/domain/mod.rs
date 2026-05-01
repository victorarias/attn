//! Pure logic. No `Window`, `Context`, or entity types — anything in this
//! module must be unit-testable without spinning up a GPUI app.
//!
//! Allowed dependencies: GPUI value types (`Pixels`, `Point`, `Bounds`,
//! `Size`). Not allowed: `Entity`, `Context`, `Window`, `App`,
//! `cx.subscribe`, `cx.notify`.

pub mod panel_navigation;
pub mod panel_placement;
pub mod viewport;
