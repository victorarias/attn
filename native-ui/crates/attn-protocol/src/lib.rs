//! Wire types for the attn daemon ↔ client websocket protocol.
//!
//! These mirror the Go types in `internal/protocol/generated.go` and the
//! TypeSpec schema. Only the subset needed by the native UI is defined here;
//! extend as new spikes need more events/commands.

mod commands;
mod events;
mod types;

pub use commands::*;
pub use events::*;
pub use types::*;

/// Current protocol version — must match `ProtocolVersion` in
/// `internal/protocol/constants.go`.
pub const PROTOCOL_VERSION: &str = "57";

/// Capability strings advertised in `ClientHelloMessage.capabilities`.
/// Mirror of `protocol.Capability*` in `internal/protocol/constants.go`.
pub const CAPABILITY_SHELL_AS_SESSION: &str = "shell_as_session";
