use serde::{Deserialize, Serialize};
use std::collections::HashMap;

pub type SettingsMap = HashMap<String, String>;

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq, Default)]
#[serde(rename_all = "snake_case")]
pub enum AttachPolicy {
    #[default]
    FreshSpawn,
    RelaunchRestore,
    SameAppRemount,
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

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum WorkspaceStatus {
    Launching,
    Working,
    WaitingInput,
    PendingApproval,
    Idle,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum SessionAgent {
    Claude,
    Codex,
    Copilot,
    Pi,
    Shell,
}

impl std::fmt::Display for SessionAgent {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", format!("{self:?}").to_lowercase())
    }
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum WorkspaceLayoutPaneKind {
    Agent,
    Shell,
}

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum WorkspaceLayoutSplitDirection {
    Vertical,
    Horizontal,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Session {
    pub id: String,
    pub label: String,
    pub agent: SessionAgent,
    pub directory: String,
    pub state: SessionState,
    pub state_since: String,
    pub state_updated_at: String,
    pub last_seen: String,
    pub muted: bool,
    #[serde(default)]
    pub workspace_id: Option<String>,
    #[serde(default)]
    pub todos: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Workspace {
    pub id: String,
    pub title: String,
    pub directory: String,
    pub status: WorkspaceStatus,
    #[serde(default)]
    pub layout: Option<WorkspaceLayout>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct RecentLocation {
    pub path: String,
    pub label: String,
    pub last_seen: String,
    pub use_count: i32,
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
    #[serde(default)]
    pub home_path: Option<String>,
    pub exists: bool,
    pub is_directory: bool,
    #[serde(default)]
    pub repo_root: Option<String>,
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
    #[serde(default)]
    pub commit_hash: Option<String>,
    #[serde(default)]
    pub commit_time: Option<String>,
    #[serde(default)]
    pub is_current: Option<bool>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct RepoInfo {
    pub repo: String,
    pub current_branch: String,
    pub current_commit_hash: String,
    pub current_commit_time: String,
    pub default_branch: String,
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
pub struct WorkspaceLayoutPane {
    pub pane_id: String,
    #[serde(default)]
    pub runtime_id: Option<String>,
    #[serde(default)]
    pub workspace_id: Option<String>,
    #[serde(default)]
    pub session_id: Option<String>,
    pub kind: WorkspaceLayoutPaneKind,
    pub title: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkspaceLayout {
    pub workspace_id: String,
    pub active_pane_id: String,
    pub layout_json: String,
    #[serde(default)]
    pub panes: Vec<WorkspaceLayoutPane>,
    #[serde(default)]
    pub updated_at: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct LayoutNode {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub pane_id: Option<String>,
    #[serde(default)]
    pub split_id: Option<String>,
    #[serde(default)]
    pub direction: Option<WorkspaceLayoutSplitDirection>,
    #[serde(default)]
    pub ratio: Option<f32>,
    #[serde(default)]
    pub children: Vec<LayoutNode>,
}

impl WorkspaceLayout {
    pub fn root(&self) -> Result<LayoutNode, serde_json::Error> {
        serde_json::from_str(&self.layout_json)
    }

    pub fn pane(&self, pane_id: &str) -> Option<&WorkspaceLayoutPane> {
        self.panes.iter().find(|pane| pane.pane_id == pane_id)
    }
}

#[derive(Debug, Clone, Deserialize)]
pub struct ReplaySegment {
    pub cols: i32,
    pub rows: i32,
    pub data: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_layout_tree_and_shell_pane() {
        let layout: WorkspaceLayout = serde_json::from_str(
            r#"{"workspace_id":"w","active_pane_id":"shell","layout_json":"{\"type\":\"split\",\"direction\":\"vertical\",\"ratio\":0.5,\"children\":[{\"type\":\"pane\",\"pane_id\":\"main\"},{\"type\":\"pane\",\"pane_id\":\"shell\"}]}","panes":[{"pane_id":"main","runtime_id":"s1","session_id":"s1","kind":"agent","title":"Agent"},{"pane_id":"shell","runtime_id":"rt1","kind":"shell","title":"Shell 1"}]}"#,
        )
        .expect("deserialize layout");
        let root = layout.root().expect("parse layout_json");
        assert_eq!(root.kind, "split");
        assert_eq!(
            layout.pane("shell").unwrap().kind,
            WorkspaceLayoutPaneKind::Shell
        );
    }

    #[test]
    fn keeps_shell_session_agent_in_wire_model() {
        let session: Session = serde_json::from_str(
            r#"{"id":"s","label":"fish","agent":"shell","directory":"~","state":"idle","state_since":"","state_updated_at":"","last_seen":"","muted":false}"#,
        )
        .expect("deserialize shell session");
        assert_eq!(session.agent, SessionAgent::Shell);
    }
}
