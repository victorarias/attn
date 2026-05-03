//! Everything that talks to the outside world: websocket and TCP sidecar.
//! Adapters emit events outward and expose command methods callers invoke.
//! They never import from `state/` or `views/`.

pub mod automation;
pub mod daemon;
pub mod trackpad_zoom;
