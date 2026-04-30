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

impl Default for QueryMessage {
    fn default() -> Self {
        Self::new()
    }
}

#[derive(Debug, Serialize)]
pub struct ClientHelloMessage {
    pub cmd: &'static str,
    pub client_kind: String,
    pub version: String,
    pub capabilities: Vec<String>,
}

impl ClientHelloMessage {
    pub fn new(
        client_kind: impl Into<String>,
        version: impl Into<String>,
        capabilities: Vec<String>,
    ) -> Self {
        Self {
            cmd: "client_hello",
            client_kind: client_kind.into(),
            version: version.into(),
            capabilities,
        }
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
        Self {
            cmd: "attach_session",
            id: session_id.into(),
            attach_policy: None,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct DetachSessionMessage {
    pub cmd: &'static str,
    pub id: String,
}

impl DetachSessionMessage {
    pub fn new(session_id: impl Into<String>) -> Self {
        Self {
            cmd: "detach_session",
            id: session_id.into(),
        }
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
        Self {
            cmd: "pty_input",
            id: session_id.into(),
            data: data.into(),
        }
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
        Self {
            cmd: "pty_resize",
            id: session_id.into(),
            cols,
            rows,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct RegisterWorkspaceMessage {
    pub cmd: &'static str,
    pub id: String,
    pub title: String,
    pub directory: String,
}

impl RegisterWorkspaceMessage {
    pub fn new(
        id: impl Into<String>,
        title: impl Into<String>,
        directory: impl Into<String>,
    ) -> Self {
        Self {
            cmd: "register_workspace",
            id: id.into(),
            title: title.into(),
            directory: directory.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct UnregisterWorkspaceMessage {
    pub cmd: &'static str,
    pub id: String,
}

impl UnregisterWorkspaceMessage {
    pub fn new(id: impl Into<String>) -> Self {
        Self {
            cmd: "unregister_workspace",
            id: id.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct UpdateWorkspacePanelGeometryMessage {
    pub cmd: &'static str,
    pub workspace_id: String,
    pub panel_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub world_x: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub world_y: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub width: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub height: Option<f32>,
}

impl UpdateWorkspacePanelGeometryMessage {
    pub fn new(
        workspace_id: impl Into<String>,
        panel_id: impl Into<String>,
        world_x: Option<f32>,
        world_y: Option<f32>,
        width: Option<f32>,
        height: Option<f32>,
    ) -> Self {
        Self {
            cmd: "update_workspace_panel_geometry",
            workspace_id: workspace_id.into(),
            panel_id: panel_id.into(),
            world_x,
            world_y,
            width,
            height,
        }
    }
}

/// Ask the daemon to spawn a new session inside an existing workspace.
/// Mirrors `spawn_session` in `internal/protocol/schema/main.tsp`. Only the
/// fields the native canvas needs are modelled — the legacy executable
/// override fields are skipped (clients that need them can extend later).
#[derive(Debug, Serialize)]
pub struct SpawnSessionMessage {
    pub cmd: &'static str,
    pub id: String,
    pub cwd: String,
    pub workspace_id: String,
    pub agent: String,
    pub cols: u16,
    pub rows: u16,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
}

impl SpawnSessionMessage {
    pub fn new(
        id: impl Into<String>,
        cwd: impl Into<String>,
        workspace_id: impl Into<String>,
        agent: impl Into<String>,
        cols: u16,
        rows: u16,
    ) -> Self {
        Self {
            cmd: "spawn_session",
            id: id.into(),
            cwd: cwd.into(),
            workspace_id: workspace_id.into(),
            agent: agent.into(),
            cols,
            rows,
            label: None,
        }
    }

    pub fn with_label(mut self, label: impl Into<String>) -> Self {
        self.label = Some(label.into());
        self
    }
}

/// Tear down a session: SIGTERM the PTY, drop the daemon's session record,
/// broadcast `session_unregistered`. Same wire shape the Tauri app uses.
#[derive(Debug, Serialize)]
pub struct UnregisterSessionMessage {
    pub cmd: &'static str,
    pub id: String,
}

impl UnregisterSessionMessage {
    pub fn new(id: impl Into<String>) -> Self {
        Self {
            cmd: "unregister",
            id: id.into(),
        }
    }
}
