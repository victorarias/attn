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

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
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
/// status of its member sessions plus daemon-owned canvas panels.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Workspace {
    pub id: String,
    pub title: String,
    pub directory: String,
    pub status: WorkspaceStatus,
    #[serde(default)]
    pub panels: Vec<WorkspacePanel>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct WorkspacePanel {
    pub id: String,
    pub session_id: String,
    pub kind: String,
    pub title: String,
    pub world_x: f32,
    pub world_y: f32,
    pub width: f32,
    pub height: f32,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct DirectoryEntry {
    pub name: String,
    pub path: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct PathInspection {
    pub input_path: String,
    pub resolved_path: String,
    pub exists: bool,
    pub is_directory: bool,
    #[serde(default)]
    pub repo_root: Option<String>,
    #[serde(default)]
    pub home_path: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Worktree {
    pub path: String,
    pub branch: String,
    pub main_repo: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct Branch {
    pub name: String,
    pub commit_hash: String,
    pub commit_time: String,
    pub is_current: bool,
    pub is_worktree: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct RepoInfo {
    pub repo: String,
    pub current_branch: String,
    pub current_commit_hash: String,
    pub current_commit_time: String,
    pub default_branch: String,
    // The daemon serializes nil Go slices as JSON `null` rather than
    // `[]`, so plain `#[serde(default)]` is not enough — without
    // `deserialize_with`, parsing fails with "invalid type: null,
    // expected a sequence" and the location dialog hangs on
    // "Reading repository". `null_or_vec` accepts either.
    #[serde(default, deserialize_with = "null_or_vec")]
    pub worktrees: Vec<Worktree>,
    #[serde(default, deserialize_with = "null_or_vec")]
    pub branches: Vec<Branch>,
    #[serde(default)]
    pub fetched_at: Option<String>,
}

fn null_or_vec<'de, D, T>(deserializer: D) -> Result<Vec<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: serde::Deserialize<'de>,
{
    Ok(Option::<Vec<T>>::deserialize(deserializer)?.unwrap_or_default())
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SessionAgent {
    Claude,
    Codex,
    Copilot,
    Pi,
    /// Shell agents are first-class sessions for clients that
    /// advertise the `shell_as_session` capability (the native canvas
    /// app). Must mirror the TypeSpec enum and the Go-side daemon —
    /// without this variant, serde drops every event that contains a
    /// shell session and the native client's session/workspace sync
    /// stalls silently.
    Shell,
}

impl std::fmt::Display for SessionAgent {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Claude => write!(f, "claude"),
            Self::Codex => write!(f, "codex"),
            Self::Copilot => write!(f, "copilot"),
            Self::Pi => write!(f, "pi"),
            Self::Shell => write!(f, "shell"),
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

#[cfg(test)]
mod tests {
    use super::*;

    /// The daemon emits Go `nil` slices as JSON `null`. Without the
    /// `null_or_vec` deserializer, the location dialog's "Reading
    /// repository" step hangs forever because the response can't be
    /// parsed and the failure is silent.
    #[test]
    fn repo_info_accepts_null_collections() {
        let json = r#"{
            "repo":"/path",
            "current_branch":"main",
            "current_commit_hash":"abc",
            "current_commit_time":"2024",
            "default_branch":"main",
            "worktrees":null,
            "branches":null
        }"#;
        let info: RepoInfo = serde_json::from_str(json).expect("repo_info parses with nulls");
        assert!(info.worktrees.is_empty());
        assert!(info.branches.is_empty());
    }

    #[test]
    fn repo_info_accepts_missing_collections() {
        let json = r#"{
            "repo":"/path",
            "current_branch":"main",
            "current_commit_hash":"abc",
            "current_commit_time":"2024",
            "default_branch":"main"
        }"#;
        let info: RepoInfo = serde_json::from_str(json).expect("repo_info parses without fields");
        assert!(info.worktrees.is_empty());
        assert!(info.branches.is_empty());
    }
}
