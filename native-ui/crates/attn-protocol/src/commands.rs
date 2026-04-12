use serde::Serialize;

use crate::types::AttachPolicy;

#[derive(Debug, Serialize)]
pub struct QueryMessage {
    pub cmd: &'static str,
}

impl QueryMessage {
    pub fn new() -> Self {
        Self { cmd: "query" }
    }
}

#[derive(Debug, Serialize)]
pub struct AttachSessionMessage {
    pub cmd: &'static str,
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub attach_policy: Option<AttachPolicy>,
}

impl AttachSessionMessage {
    pub fn new(session_id: impl Into<String>) -> Self {
        Self { cmd: "attach_session", id: session_id.into(), attach_policy: None }
    }
}

#[derive(Debug, Serialize)]
pub struct DetachSessionMessage {
    pub cmd: &'static str,
    pub id: String,
}

impl DetachSessionMessage {
    pub fn new(session_id: impl Into<String>) -> Self {
        Self { cmd: "detach_session", id: session_id.into() }
    }
}

#[derive(Debug, Serialize)]
pub struct PtyInputMessage {
    pub cmd: &'static str,
    pub id: String,
    pub data: String,
}

impl PtyInputMessage {
    pub fn new(session_id: impl Into<String>, data: impl Into<String>) -> Self {
        Self { cmd: "pty_input", id: session_id.into(), data: data.into() }
    }
}

#[derive(Debug, Serialize)]
pub struct PtyResizeMessage {
    pub cmd: &'static str,
    pub id: String,
    pub cols: u16,
    pub rows: u16,
}

impl PtyResizeMessage {
    pub fn new(session_id: impl Into<String>, cols: u16, rows: u16) -> Self {
        Self { cmd: "pty_resize", id: session_id.into(), cols, rows }
    }
}
