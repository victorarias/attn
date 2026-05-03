use serde::Serialize;

use crate::types::AttachPolicy;

#[derive(Debug, Serialize)]
pub struct QueryMessage {
    pub cmd: &'static str,
}

#[derive(Debug, Serialize)]
pub struct GetSettingsMessage {
    pub cmd: &'static str,
}

impl GetSettingsMessage {
    pub fn new() -> Self {
        Self {
            cmd: "get_settings",
        }
    }
}

#[derive(Debug, Serialize)]
pub struct ListEndpointsMessage {
    pub cmd: &'static str,
}

impl ListEndpointsMessage {
    pub fn new() -> Self {
        Self {
            cmd: "list_endpoints",
        }
    }
}

#[derive(Debug, Serialize)]
pub struct SetSettingMessage {
    pub cmd: &'static str,
    pub key: String,
    pub value: String,
}

impl SetSettingMessage {
    pub fn new(key: impl Into<String>, value: impl Into<String>) -> Self {
        Self {
            cmd: "set_setting",
            key: key.into(),
            value: value.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct UpdateEndpointMessage {
    pub cmd: &'static str,
    pub endpoint_id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ssh_target: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub enabled: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub profile: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct AddEndpointMessage {
    pub cmd: &'static str,
    pub name: String,
    pub ssh_target: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub profile: Option<String>,
}

impl AddEndpointMessage {
    pub fn new(
        name: impl Into<String>,
        ssh_target: impl Into<String>,
        profile: impl Into<String>,
    ) -> Self {
        let profile = profile.into();
        Self {
            cmd: "add_endpoint",
            name: name.into(),
            ssh_target: ssh_target.into(),
            profile: (!profile.trim().is_empty()).then_some(profile),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct RemoveEndpointMessage {
    pub cmd: &'static str,
    pub endpoint_id: String,
}

impl RemoveEndpointMessage {
    pub fn new(endpoint_id: impl Into<String>) -> Self {
        Self {
            cmd: "remove_endpoint",
            endpoint_id: endpoint_id.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct BootstrapEndpointMessage {
    pub cmd: &'static str,
    pub endpoint_id: String,
}

impl BootstrapEndpointMessage {
    pub fn new(endpoint_id: impl Into<String>) -> Self {
        Self {
            cmd: "bootstrap_endpoint",
            endpoint_id: endpoint_id.into(),
        }
    }
}

impl UpdateEndpointMessage {
    pub fn enabled(endpoint_id: impl Into<String>, enabled: bool) -> Self {
        Self {
            cmd: "update_endpoint",
            endpoint_id: endpoint_id.into(),
            name: None,
            ssh_target: None,
            enabled: Some(enabled),
            profile: None,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct SetEndpointRemoteWebMessage {
    pub cmd: &'static str,
    pub endpoint_id: String,
    pub enabled: bool,
}

impl SetEndpointRemoteWebMessage {
    pub fn new(endpoint_id: impl Into<String>, enabled: bool) -> Self {
        Self {
            cmd: "set_endpoint_remote_web",
            endpoint_id: endpoint_id.into(),
            enabled,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct ToggleRepoMuteMessage {
    pub cmd: &'static str,
    pub repo: String,
}

impl ToggleRepoMuteMessage {
    pub fn new(repo: impl Into<String>) -> Self {
        Self {
            cmd: "mute_repo",
            repo: repo.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct ToggleAuthorMuteMessage {
    pub cmd: &'static str,
    pub author: String,
}

impl ToggleAuthorMuteMessage {
    pub fn new(author: impl Into<String>) -> Self {
        Self {
            cmd: "mute_author",
            author: author.into(),
        }
    }
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

#[derive(Debug, Serialize)]
pub struct BrowseDirectoryMessage {
    pub cmd: &'static str,
    pub input_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request_id: Option<String>,
}

impl BrowseDirectoryMessage {
    pub fn new(input_path: impl Into<String>, request_id: impl Into<String>) -> Self {
        Self {
            cmd: "browse_directory",
            input_path: input_path.into(),
            request_id: Some(request_id.into()),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct InspectPathMessage {
    pub cmd: &'static str,
    pub path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request_id: Option<String>,
}

impl InspectPathMessage {
    pub fn new(path: impl Into<String>, request_id: impl Into<String>) -> Self {
        Self {
            cmd: "inspect_path",
            path: path.into(),
            request_id: Some(request_id.into()),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct GetRepoInfoMessage {
    pub cmd: &'static str,
    pub repo: String,
}

impl GetRepoInfoMessage {
    pub fn new(repo: impl Into<String>) -> Self {
        Self {
            cmd: "get_repo_info",
            repo: repo.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct CreateWorktreeMessage {
    pub cmd: &'static str,
    pub main_repo: String,
    pub branch: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub starting_from: Option<String>,
}

impl CreateWorktreeMessage {
    pub fn new(
        main_repo: impl Into<String>,
        branch: impl Into<String>,
        starting_from: impl Into<String>,
    ) -> Self {
        Self {
            cmd: "create_worktree",
            main_repo: main_repo.into(),
            branch: branch.into(),
            path: None,
            starting_from: Some(starting_from.into()),
        }
    }
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
