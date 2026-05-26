use serde::Deserialize;

use crate::{
    DirectoryEntry, PathInspection, RecentLocation, ReplaySegment, RepoInfo, Session, SettingsMap,
    Workspace, WorkspaceLayout,
};

#[derive(Debug, Clone, Deserialize)]
pub struct InitialStateMessage {
    #[serde(default)]
    pub protocol_version: Option<String>,
    #[serde(default)]
    pub sessions: Vec<Session>,
    #[serde(default)]
    pub workspaces: Vec<Workspace>,
    #[serde(default)]
    pub settings: SettingsMap,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionMessage {
    pub session: Session,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkspaceMessage {
    pub workspace: Workspace,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionsUpdatedMessage {
    #[serde(default)]
    pub sessions: Vec<Session>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkspaceLayoutMessage {
    pub workspace_layout: WorkspaceLayout,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkspaceLayoutActionResultMessage {
    pub action: String,
    pub workspace_id: String,
    #[serde(default)]
    pub pane_id: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BootstrapWorkspaceResultMessage {
    pub workspace_id: String,
    #[serde(default)]
    pub session_id: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SpawnResultMessage {
    pub id: String,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct RecentLocationsResultMessage {
    #[serde(default)]
    pub recent_locations: Vec<RecentLocation>,
    #[serde(default)]
    pub home_path: Option<String>,
    pub success: bool,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BrowseDirectoryResultMessage {
    pub directory: String,
    #[serde(default)]
    pub entries: Vec<DirectoryEntry>,
    #[serde(default)]
    pub request_id: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct InspectPathResultMessage {
    #[serde(default)]
    pub inspection: Option<PathInspection>,
    #[serde(default)]
    pub request_id: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SettingsUpdatedMessage {
    #[serde(default)]
    pub settings: SettingsMap,
    #[serde(default)]
    pub changed_key: Option<String>,
    #[serde(default)]
    pub success: Option<bool>,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct GetRepoInfoResultMessage {
    #[serde(default)]
    pub info: Option<RepoInfo>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct CreateWorktreeResultMessage {
    #[serde(default)]
    pub path: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct DeleteWorktreeResultMessage {
    pub path: String,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AttachResultMessage {
    pub id: String,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub screen_snapshot: Option<String>,
    #[serde(default)]
    pub replay_segments: Option<Vec<ReplaySegment>>,
    #[serde(default)]
    pub last_seq: Option<i32>,
    #[serde(default)]
    pub cols: Option<u16>,
    #[serde(default)]
    pub rows: Option<u16>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct PtyOutputMessage {
    pub id: String,
    pub data: String,
    pub seq: i32,
}

#[derive(Debug, Clone, Deserialize)]
pub struct PtyResizedMessage {
    pub id: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionExitedMessage {
    pub id: String,
    pub exit_code: i32,
}

#[derive(Debug, Clone, Deserialize)]
struct EventPeek {
    event: String,
}

#[derive(Debug, Clone)]
pub enum ServerEvent {
    InitialState(InitialStateMessage),
    SessionRegistered(SessionMessage),
    SessionUnregistered(SessionMessage),
    SessionStateChanged(SessionMessage),
    SessionTodosUpdated(SessionMessage),
    SessionsUpdated(SessionsUpdatedMessage),
    WorkspaceRegistered(WorkspaceMessage),
    WorkspaceUnregistered(WorkspaceMessage),
    WorkspaceStateChanged(WorkspaceMessage),
    WorkspaceLayout(WorkspaceLayoutMessage),
    WorkspaceLayoutUpdated(WorkspaceLayoutMessage),
    WorkspaceLayoutActionResult(WorkspaceLayoutActionResultMessage),
    BootstrapWorkspaceResult(BootstrapWorkspaceResultMessage),
    SpawnResult(SpawnResultMessage),
    RecentLocationsResult(RecentLocationsResultMessage),
    BrowseDirectoryResult(BrowseDirectoryResultMessage),
    InspectPathResult(InspectPathResultMessage),
    SettingsUpdated(SettingsUpdatedMessage),
    GetRepoInfoResult(GetRepoInfoResultMessage),
    CreateWorktreeResult(CreateWorktreeResultMessage),
    DeleteWorktreeResult(DeleteWorktreeResultMessage),
    AttachResult(AttachResultMessage),
    PtyOutput(PtyOutputMessage),
    PtyDesync(String),
    PtyResized(PtyResizedMessage),
    SessionExited(SessionExitedMessage),
    Unknown(String),
}

impl ServerEvent {
    pub fn parse(data: &str) -> Result<Self, serde_json::Error> {
        let event = serde_json::from_str::<EventPeek>(data)?.event;
        Ok(match event.as_str() {
            "initial_state" => Self::InitialState(serde_json::from_str(data)?),
            "session_registered" => Self::SessionRegistered(serde_json::from_str(data)?),
            "session_unregistered" => Self::SessionUnregistered(serde_json::from_str(data)?),
            "session_state_changed" => Self::SessionStateChanged(serde_json::from_str(data)?),
            "session_todos_updated" => Self::SessionTodosUpdated(serde_json::from_str(data)?),
            "sessions_updated" => Self::SessionsUpdated(serde_json::from_str(data)?),
            "workspace_registered" => Self::WorkspaceRegistered(serde_json::from_str(data)?),
            "workspace_unregistered" => Self::WorkspaceUnregistered(serde_json::from_str(data)?),
            "workspace_state_changed" => Self::WorkspaceStateChanged(serde_json::from_str(data)?),
            "workspace_layout" => Self::WorkspaceLayout(serde_json::from_str(data)?),
            "workspace_layout_updated" => Self::WorkspaceLayoutUpdated(serde_json::from_str(data)?),
            "workspace_layout_action_result" => {
                Self::WorkspaceLayoutActionResult(serde_json::from_str(data)?)
            }
            "bootstrap_workspace_result" => {
                Self::BootstrapWorkspaceResult(serde_json::from_str(data)?)
            }
            "spawn_result" => Self::SpawnResult(serde_json::from_str(data)?),
            "recent_locations_result" => Self::RecentLocationsResult(serde_json::from_str(data)?),
            "browse_directory_result" => Self::BrowseDirectoryResult(serde_json::from_str(data)?),
            "inspect_path_result" => Self::InspectPathResult(serde_json::from_str(data)?),
            "settings_updated" => Self::SettingsUpdated(serde_json::from_str(data)?),
            "get_repo_info_result" => Self::GetRepoInfoResult(serde_json::from_str(data)?),
            "create_worktree_result" => Self::CreateWorktreeResult(serde_json::from_str(data)?),
            "delete_worktree_result" => Self::DeleteWorktreeResult(serde_json::from_str(data)?),
            "attach_result" => Self::AttachResult(serde_json::from_str(data)?),
            "pty_output" => Self::PtyOutput(serde_json::from_str(data)?),
            "pty_desync" => {
                #[derive(Deserialize)]
                struct Desync {
                    id: String,
                }
                Self::PtyDesync(serde_json::from_str::<Desync>(data)?.id)
            }
            "pty_resized" => Self::PtyResized(serde_json::from_str(data)?),
            "session_exited" => Self::SessionExited(serde_json::from_str(data)?),
            _ => Self::Unknown(event),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::ServerEvent;

    #[test]
    fn parses_workspace_layout_update() {
        let event = ServerEvent::parse(
            r#"{"event":"workspace_layout_updated","workspace_layout":{"workspace_id":"w","active_pane_id":"main","layout_json":"{\"type\":\"pane\",\"pane_id\":\"main\"}","panes":[]}}"#,
        )
        .expect("parse layout event");
        assert!(matches!(event, ServerEvent::WorkspaceLayoutUpdated(_)));
    }

    #[test]
    fn parses_bootstrap_workspace_result() {
        let event = ServerEvent::parse(
            r#"{"event":"bootstrap_workspace_result","workspace_id":"w","session_id":"s","success":true}"#,
        )
        .expect("parse bootstrap result");
        assert!(matches!(event, ServerEvent::BootstrapWorkspaceResult(_)));
    }

    #[test]
    fn parses_repository_picker_result() {
        let event = ServerEvent::parse(
            r#"{"event":"get_repo_info_result","success":true,"info":{"repo":"/tmp/repo","current_branch":"main","current_commit_hash":"abc1234","current_commit_time":"","default_branch":"main","worktrees":null,"branches":null}}"#,
        )
        .expect("parse repository picker result");
        assert!(matches!(event, ServerEvent::GetRepoInfoResult(_)));
    }
}
