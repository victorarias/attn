//! Everything that talks to the outside world: websocket, TCP sidecar,
//! fake input sources. Adapters emit events outward and expose command
//! methods callers invoke. They never import from `state/` or `views/`.

pub mod automation;
pub mod daemon;
pub mod synthetic;
