use serde::Serialize;

use crate::{AttachPolicy, WorkspaceLayoutPaneKind, WorkspaceLayoutSplitDirection};

#[derive(Debug, Serialize)]
pub struct ClientHelloMessage {
    pub cmd: &'static str,
    pub client_kind: String,
    pub version: String,
    pub capabilities: Vec<String>,
}

impl ClientHelloMessage {
    pub fn native(version: impl Into<String>) -> Self {
        Self {
            cmd: "client_hello",
            client_kind: "native-workspace".to_string(),
            version: version.into(),
            capabilities: Vec::new(),
        }
    }
}

macro_rules! id_command {
    ($name:ident, $cmd:literal) => {
        #[derive(Debug, Serialize)]
        pub struct $name {
            pub cmd: &'static str,
            pub id: String,
        }
        impl $name {
            pub fn new(id: impl Into<String>) -> Self {
                Self {
                    cmd: $cmd,
                    id: id.into(),
                }
            }
        }
    };
}

id_command!(MuteMessage, "mute");
id_command!(DetachSessionMessage, "detach_session");
id_command!(KillSessionMessage, "kill_session");

#[derive(Debug, Serialize)]
pub struct AttachSessionMessage {
    pub cmd: &'static str,
    pub id: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub attach_policy: Option<AttachPolicy>,
}

impl AttachSessionMessage {
    pub fn new(id: impl Into<String>) -> Self {
        Self {
            cmd: "attach_session",
            id: id.into(),
            attach_policy: None,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct PtyInputMessage {
    pub cmd: &'static str,
    pub id: String,
    pub data: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
}

impl PtyInputMessage {
    pub fn new(id: impl Into<String>, data: impl Into<String>) -> Self {
        Self {
            cmd: "pty_input",
            id: id.into(),
            data: data.into(),
            source: Some("native-workspace".to_string()),
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
    pub fn new(id: impl Into<String>, cols: u16, rows: u16) -> Self {
        Self {
            cmd: "pty_resize",
            id: id.into(),
            cols,
            rows,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct WorkspaceLayoutFocusPaneMessage {
    pub cmd: &'static str,
    pub workspace_id: String,
    pub pane_id: String,
}

impl WorkspaceLayoutFocusPaneMessage {
    pub fn new(workspace_id: impl Into<String>, pane_id: impl Into<String>) -> Self {
        Self {
            cmd: "workspace_layout_focus_pane",
            workspace_id: workspace_id.into(),
            pane_id: pane_id.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct WorkspaceLayoutClosePaneMessage {
    pub cmd: &'static str,
    pub workspace_id: String,
    pub pane_id: String,
}

impl WorkspaceLayoutClosePaneMessage {
    pub fn new(workspace_id: impl Into<String>, pane_id: impl Into<String>) -> Self {
        Self {
            cmd: "workspace_layout_close_pane",
            workspace_id: workspace_id.into(),
            pane_id: pane_id.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct WorkspaceLayoutSplitPaneMessage {
    pub cmd: &'static str,
    pub workspace_id: String,
    pub target_pane_id: String,
    pub direction: WorkspaceLayoutSplitDirection,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cwd: Option<String>,
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

id_command!(UnregisterWorkspaceMessage, "unregister_workspace");

#[derive(Debug, Serialize)]
pub struct BootstrapWorkspaceInitialSession {
    pub id: String,
    pub cwd: String,
    pub kind: WorkspaceLayoutPaneKind,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub agent: Option<String>,
    pub cols: u16,
    pub rows: u16,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub yolo_mode: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub executable: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct BootstrapWorkspaceMessage {
    pub cmd: &'static str,
    pub id: String,
    pub title: String,
    pub directory: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint_id: Option<String>,
    pub initial_session: BootstrapWorkspaceInitialSession,
}

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
    #[serde(skip_serializing_if = "Option::is_none")]
    pub executable: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub yolo_mode: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub target_pane_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub direction: Option<WorkspaceLayoutSplitDirection>,
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
            executable: None,
            yolo_mode: None,
            target_pane_id: None,
            direction: None,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct GetRecentLocationsMessage {
    pub cmd: &'static str,
    pub limit: i32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint_id: Option<String>,
}

impl GetRecentLocationsMessage {
    pub fn new(limit: i32) -> Self {
        Self {
            cmd: "get_recent_locations",
            limit,
            endpoint_id: None,
        }
    }
}

#[derive(Debug, Serialize)]
pub struct BrowseDirectoryMessage {
    pub cmd: &'static str,
    pub input_path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint_id: Option<String>,
    pub request_id: String,
}

impl BrowseDirectoryMessage {
    pub fn new(input_path: impl Into<String>, request_id: impl Into<String>) -> Self {
        Self {
            cmd: "browse_directory",
            input_path: input_path.into(),
            endpoint_id: None,
            request_id: request_id.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct InspectPathMessage {
    pub cmd: &'static str,
    pub path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint_id: Option<String>,
    pub request_id: String,
}

impl InspectPathMessage {
    pub fn new(path: impl Into<String>, request_id: impl Into<String>) -> Self {
        Self {
            cmd: "inspect_path",
            path: path.into(),
            endpoint_id: None,
            request_id: request_id.into(),
        }
    }
}

#[derive(Debug, Serialize)]
pub struct GetRepoInfoMessage {
    pub cmd: &'static str,
    pub repo: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint_id: Option<String>,
}

impl GetRepoInfoMessage {
    pub fn local(repo: impl Into<String>) -> Self {
        Self {
            cmd: "get_repo_info",
            repo: repo.into(),
            endpoint_id: None,
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
    pub endpoint_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub starting_from: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct DeleteWorktreeMessage {
    pub cmd: &'static str,
    pub path: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint_id: Option<String>,
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
