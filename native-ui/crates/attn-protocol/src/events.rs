use serde::Deserialize;

use crate::{ReplaySegment, Session, SettingsMap, Workspace, WorkspaceLayout};

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
}
