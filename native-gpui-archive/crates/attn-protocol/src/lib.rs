//! Native client's intentionally small subset of the daemon websocket protocol.
//! The TypeSpec schema remains canonical; add wire models here only as native
//! panes begin to consume them.

mod commands;
mod events;
mod types;

pub use commands::*;
pub use events::*;
pub use types::*;

pub const PROTOCOL_VERSION: &str = "66";
