use serde::Deserialize;

use crate::types::{ReplaySegment, Session, Workspace};

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
struct EventPeek {
    event: String,
}

#[derive(Debug, Clone)]
pub enum ServerEvent {
    InitialState(InitialStateMessage),
    SessionRegistered(SessionRegisteredMessage),
    SessionUnregistered(SessionUnregisteredMessage),
    SessionStateChanged(SessionStateChangedMessage),
    SessionsUpdated(SessionsUpdatedMessage),
    WorkspaceRegistered(WorkspaceRegisteredMessage),
    WorkspaceUnregistered(WorkspaceUnregisteredMessage),
    WorkspaceStateChanged(WorkspaceStateChangedMessage),
    AttachResult(AttachResultMessage),
    PtyOutput(PtyOutputMessage),
    PtyDesync(PtyDesyncMessage),
    PtyResized(PtyResizedMessage),
    SessionExited(SessionExitedMessage),
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
            other => Ok(Self::Unknown(other.to_string())),
        }
    }
}
