use serde::Deserialize;

use crate::types::{
    AuthorState, DirectoryEntry, EndpointInfo, PathInspection, PullRequestSummary, ReplaySegment,
    RepoInfo, RepoState, Session, SettingsMap, Workspace,
};

#[derive(Debug, Clone, Deserialize)]
pub struct InitialStateMessage {
    pub event: String,
    #[serde(default)]
    pub protocol_version: Option<String>,
    #[serde(default)]
    pub daemon_instance_id: Option<String>,
    #[serde(default)]
    pub sessions: Vec<Session>,
    #[serde(default)]
    pub workspaces: Vec<Workspace>,
    #[serde(default)]
    pub endpoints: Vec<EndpointInfo>,
    #[serde(default)]
    pub prs: Vec<PullRequestSummary>,
    #[serde(default)]
    pub repos: Vec<RepoState>,
    #[serde(default)]
    pub authors: Vec<AuthorState>,
    #[serde(default)]
    pub github_hosts: Vec<String>,
    #[serde(default)]
    pub settings: SettingsMap,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SettingsUpdatedMessage {
    pub event: String,
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
pub struct GitHubHostsUpdatedMessage {
    pub event: String,
    pub github_hosts: Vec<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct EndpointsUpdatedMessage {
    pub event: String,
    #[serde(default)]
    pub endpoints: Vec<EndpointInfo>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct EndpointStatusChangedMessage {
    pub event: String,
    pub endpoint: EndpointInfo,
}

#[derive(Debug, Clone, Deserialize)]
pub struct ReposUpdatedMessage {
    pub event: String,
    #[serde(default)]
    pub repos: Vec<RepoState>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AuthorsUpdatedMessage {
    pub event: String,
    #[serde(default)]
    pub authors: Vec<AuthorState>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct PRsUpdatedMessage {
    pub event: String,
    #[serde(default)]
    pub prs: Vec<PullRequestSummary>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionRegisteredMessage {
    pub event: String,
    pub session: Session,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionUnregisteredMessage {
    pub event: String,
    pub session: Session,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionStateChangedMessage {
    pub event: String,
    pub session: Session,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionsUpdatedMessage {
    pub event: String,
    pub sessions: Vec<Session>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkspaceRegisteredMessage {
    pub event: String,
    pub workspace: Workspace,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkspaceUnregisteredMessage {
    pub event: String,
    pub workspace: Workspace,
}

#[derive(Debug, Clone, Deserialize)]
pub struct WorkspaceStateChangedMessage {
    pub event: String,
    pub workspace: Workspace,
}

/// Daemon ack for a `spawn_session` command. Carries success+error so the
/// client can surface failures (invalid agent, executable missing, PTY
/// spawn failure) without inferring them from "no session ever appeared".
#[derive(Debug, Clone, Deserialize)]
pub struct SpawnResultMessage {
    pub event: String,
    pub id: String,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct AttachResultMessage {
    pub event: String,
    pub id: String,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
    /// Base64-encoded ANSI replay of visible screen at attach time.
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
    pub event: String,
    pub id: String,
    /// Base64-encoded PTY bytes.
    pub data: String,
    pub seq: i32,
}

#[derive(Debug, Clone, Deserialize)]
pub struct PtyDesyncMessage {
    pub event: String,
    pub id: String,
    pub reason: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct PtyResizedMessage {
    pub event: String,
    pub id: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone, Deserialize)]
pub struct SessionExitedMessage {
    pub event: String,
    pub id: String,
    pub exit_code: i32,
    #[serde(default)]
    pub signal: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct BrowseDirectoryResultMessage {
    pub event: String,
    pub input_path: String,
    pub directory: String,
    #[serde(default)]
    pub entries: Vec<DirectoryEntry>,
    #[serde(default)]
    pub request_id: Option<String>,
    #[serde(default)]
    pub home_path: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct InspectPathResultMessage {
    pub event: String,
    #[serde(default)]
    pub inspection: Option<PathInspection>,
    #[serde(default)]
    pub request_id: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct GetRepoInfoResultMessage {
    pub event: String,
    #[serde(default)]
    pub info: Option<RepoInfo>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct CreateWorktreeResultMessage {
    pub event: String,
    #[serde(default)]
    pub path: Option<String>,
    pub success: bool,
    #[serde(default)]
    pub error: Option<String>,
}

#[derive(Debug, Clone, Deserialize)]
struct EventPeek {
    event: String,
}

#[derive(Debug, Clone)]
pub enum ServerEvent {
    InitialState(InitialStateMessage),
    SettingsUpdated(SettingsUpdatedMessage),
    GitHubHostsUpdated(GitHubHostsUpdatedMessage),
    EndpointsUpdated(EndpointsUpdatedMessage),
    EndpointStatusChanged(EndpointStatusChangedMessage),
    ReposUpdated(ReposUpdatedMessage),
    AuthorsUpdated(AuthorsUpdatedMessage),
    PRsUpdated(PRsUpdatedMessage),
    SessionRegistered(SessionRegisteredMessage),
    SessionUnregistered(SessionUnregisteredMessage),
    SessionStateChanged(SessionStateChangedMessage),
    SessionsUpdated(SessionsUpdatedMessage),
    WorkspaceRegistered(WorkspaceRegisteredMessage),
    WorkspaceUnregistered(WorkspaceUnregisteredMessage),
    WorkspaceStateChanged(WorkspaceStateChangedMessage),
    AttachResult(AttachResultMessage),
    SpawnResult(SpawnResultMessage),
    PtyOutput(PtyOutputMessage),
    PtyDesync(PtyDesyncMessage),
    PtyResized(PtyResizedMessage),
    SessionExited(SessionExitedMessage),
    BrowseDirectoryResult(BrowseDirectoryResultMessage),
    InspectPathResult(InspectPathResultMessage),
    GetRepoInfoResult(GetRepoInfoResultMessage),
    CreateWorktreeResult(CreateWorktreeResultMessage),
    Unknown(String),
}

impl ServerEvent {
    pub fn parse(data: &str) -> Result<Self, serde_json::Error> {
        let peek: EventPeek = serde_json::from_str(data)?;
        match peek.event.as_str() {
            "initial_state" => {
                let msg: InitialStateMessage = serde_json::from_str(data)?;
                Ok(Self::InitialState(msg))
            }
            "settings_updated" => {
                let msg: SettingsUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::SettingsUpdated(msg))
            }
            "github_hosts_updated" => {
                let msg: GitHubHostsUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::GitHubHostsUpdated(msg))
            }
            "endpoints_updated" => {
                let msg: EndpointsUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::EndpointsUpdated(msg))
            }
            "endpoint_status_changed" => {
                let msg: EndpointStatusChangedMessage = serde_json::from_str(data)?;
                Ok(Self::EndpointStatusChanged(msg))
            }
            "repos_updated" => {
                let msg: ReposUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::ReposUpdated(msg))
            }
            "authors_updated" => {
                let msg: AuthorsUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::AuthorsUpdated(msg))
            }
            "prs_updated" => {
                let msg: PRsUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::PRsUpdated(msg))
            }
            "session_registered" => {
                let msg: SessionRegisteredMessage = serde_json::from_str(data)?;
                Ok(Self::SessionRegistered(msg))
            }
            "session_unregistered" => {
                let msg: SessionUnregisteredMessage = serde_json::from_str(data)?;
                Ok(Self::SessionUnregistered(msg))
            }
            "session_state_changed" => {
                let msg: SessionStateChangedMessage = serde_json::from_str(data)?;
                Ok(Self::SessionStateChanged(msg))
            }
            "sessions_updated" => {
                let msg: SessionsUpdatedMessage = serde_json::from_str(data)?;
                Ok(Self::SessionsUpdated(msg))
            }
            "workspace_registered" => {
                let msg: WorkspaceRegisteredMessage = serde_json::from_str(data)?;
                Ok(Self::WorkspaceRegistered(msg))
            }
            "workspace_unregistered" => {
                let msg: WorkspaceUnregisteredMessage = serde_json::from_str(data)?;
                Ok(Self::WorkspaceUnregistered(msg))
            }
            "workspace_state_changed" => {
                let msg: WorkspaceStateChangedMessage = serde_json::from_str(data)?;
                Ok(Self::WorkspaceStateChanged(msg))
            }
            "attach_result" => {
                let msg: AttachResultMessage = serde_json::from_str(data)?;
                Ok(Self::AttachResult(msg))
            }
            "spawn_result" => {
                let msg: SpawnResultMessage = serde_json::from_str(data)?;
                Ok(Self::SpawnResult(msg))
            }
            "pty_output" => {
                let msg: PtyOutputMessage = serde_json::from_str(data)?;
                Ok(Self::PtyOutput(msg))
            }
            "pty_desync" => {
                let msg: PtyDesyncMessage = serde_json::from_str(data)?;
                Ok(Self::PtyDesync(msg))
            }
            "pty_resized" => {
                let msg: PtyResizedMessage = serde_json::from_str(data)?;
                Ok(Self::PtyResized(msg))
            }
            "session_exited" => {
                let msg: SessionExitedMessage = serde_json::from_str(data)?;
                Ok(Self::SessionExited(msg))
            }
            "browse_directory_result" => {
                let msg: BrowseDirectoryResultMessage = serde_json::from_str(data)?;
                Ok(Self::BrowseDirectoryResult(msg))
            }
            "inspect_path_result" => {
                let msg: InspectPathResultMessage = serde_json::from_str(data)?;
                Ok(Self::InspectPathResult(msg))
            }
            "get_repo_info_result" => {
                let msg: GetRepoInfoResultMessage = serde_json::from_str(data)?;
                Ok(Self::GetRepoInfoResult(msg))
            }
            "create_worktree_result" => {
                let msg: CreateWorktreeResultMessage = serde_json::from_str(data)?;
                Ok(Self::CreateWorktreeResult(msg))
            }
            other => Ok(Self::Unknown(other.to_string())),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::ServerEvent;

    #[test]
    fn parses_github_hosts_from_initial_state() {
        let event =
            ServerEvent::parse(r#"{"event":"initial_state","github_hosts":["github.example"]}"#)
                .expect("parse initial_state");

        match event {
            ServerEvent::InitialState(message) => {
                assert_eq!(message.github_hosts, vec!["github.example"]);
            }
            other => panic!("unexpected event: {other:?}"),
        }
    }

    #[test]
    fn parses_github_hosts_update() {
        let event = ServerEvent::parse(
            r#"{"event":"github_hosts_updated","github_hosts":["github.example"]}"#,
        )
        .expect("parse github_hosts_updated");

        match event {
            ServerEvent::GitHubHostsUpdated(message) => {
                assert_eq!(message.github_hosts, vec!["github.example"]);
            }
            other => panic!("unexpected event: {other:?}"),
        }
    }
}
