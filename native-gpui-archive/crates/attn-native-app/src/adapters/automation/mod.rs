//! Runtime-gated automation sidecar for the workspace-first native client.
//!
//! The TCP wire contract and discovery manifest match Attn's Tauri bridge and
//! the archived canvas prototype. Actions remain specific to the active
//! workspace-layout UI and must not reintroduce canvas ownership concepts.

pub mod actions;
pub mod events;
pub mod manifest;
pub mod profile;
pub mod protocol;
pub mod server;

pub use profile::{automation_enabled, background_window, manifest_path, start_empty};
