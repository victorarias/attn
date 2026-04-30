//! Long-lived observable application state — the ViewModel layer.
//!
//! State entities may subscribe to adapter events (`cx.subscribe(&adapter,
//! ...)`) and call adapter command methods. They never call into views.
//! Views observe state by holding `Entity<T>` handles and reading through
//! them.

pub mod panel;
pub mod terminal_model;
pub mod workspace;
pub mod workspace_registry;
