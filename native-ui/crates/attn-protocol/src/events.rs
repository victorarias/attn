use serde::Deserialize;

use crate::types::Session;

#[derive(Debug, Clone, Deserialize)]
pub struct InitialStateMessage {
    pub event: String,
    #[serde(default)]
    pub protocol_version: Option<String>,
    #[serde(default)]
    pub daemon_instance_id: Option<String>,
    #[serde(default)]
    pub sessions: Vec<Session>,
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
            other => Ok(Self::Unknown(other.to_string())),
        }
    }
}
