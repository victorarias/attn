use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum AttachPolicy {
    #[default]
    FreshSpawn,
    RelaunchRestore,
    SameAppRemount,
}

/// A segment of PTY replay data returned on attach.
#[derive(Debug, Clone, Deserialize)]
pub struct ReplaySegment {
    pub cols: i32,
    pub rows: i32,
    /// Base64-encoded PTY bytes.
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SessionState {
    Launching,
    Working,
    WaitingInput,
    Idle,
    PendingApproval,
    Unknown,
}

impl std::fmt::Display for SessionState {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Launching => write!(f, "launching"),
            Self::Working => write!(f, "working"),
            Self::WaitingInput => write!(f, "waiting_input"),
            Self::Idle => write!(f, "idle"),
            Self::PendingApproval => write!(f, "pending_approval"),
            Self::Unknown => write!(f, "unknown"),
        }
    }
}

/// Rolled-up status of all sessions in a workspace. Mirrors the daemon's
/// `WorkspaceStatus` enum (no `unknown` value — workspaces always have a
/// directory and a registry entry, so the rollup always lands somewhere).
#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum WorkspaceStatus {
    Launching,
    Working,
    WaitingInput,
    PendingApproval,
    Idle,
}

impl std::fmt::Display for WorkspaceStatus {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Launching => write!(f, "launching"),
            Self::Working => write!(f, "working"),
            Self::WaitingInput => write!(f, "waiting_input"),
            Self::PendingApproval => write!(f, "pending_approval"),
            Self::Idle => write!(f, "idle"),
        }
    }
}

/// A workspace as the daemon broadcasts it: the directory + the rolled-up
/// status of its member sessions. Panels and other UI state are local to
/// the canvas client; only the wire-visible fields live here.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Workspace {
    pub id: String,
    pub title: String,
    pub directory: String,
    pub status: WorkspaceStatus,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SessionAgent {
    Claude,
    Codex,
    Copilot,
    Pi,
}

impl std::fmt::Display for SessionAgent {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Claude => write!(f, "claude"),
            Self::Codex => write!(f, "codex"),
            Self::Copilot => write!(f, "copilot"),
            Self::Pi => write!(f, "pi"),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Session {
    pub id: String,
    pub label: String,
    pub agent: SessionAgent,
    pub state: SessionState,
    pub directory: String,
    pub last_seen: String,
    pub state_since: String,
    pub state_updated_at: String,
    pub muted: bool,
    #[serde(default)]
    pub branch: Option<String>,
    #[serde(default)]
    pub endpoint_id: Option<String>,
    #[serde(default)]
    pub workspace_id: Option<String>,
    #[serde(default)]
    pub is_worktree: Option<bool>,
    #[serde(default)]
    pub main_repo: Option<String>,
    #[serde(default)]
    pub needs_review_after_long_run: Option<bool>,
    #[serde(default)]
    pub recoverable: Option<bool>,
    #[serde(default)]
    pub todos: Vec<String>,
}
