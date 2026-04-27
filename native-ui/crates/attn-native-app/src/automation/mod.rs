/// UI automation sidecar for the native canvas app. See module docs in
/// `profile.rs` for the gating rule, and `docs/plans/native-gpui-canvas-ui.md`
/// (Spike 6) for the broader design.
///
/// The sidecar runs an in-process TCP server that external test scripts
/// connect to. Wire format and manifest layout match the Tauri bridge so
/// `app/scripts/real-app-harness/uiAutomationClient.mjs` works against
/// either app with only the manifest path swapped out.
pub mod actions;
pub mod events;
pub mod manifest;
pub mod profile;
pub mod protocol;
pub mod server;

pub use profile::{automation_enabled, manifest_path};
