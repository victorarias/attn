use std::{
    collections::{HashMap, HashSet},
    rc::Rc,
    sync::Arc,
    time::Duration,
};

use attn_protocol::{
    BootstrapWorkspaceInitialSession, BootstrapWorkspaceMessage, BrowseDirectoryMessage,
    CreateWorktreeMessage, DeleteWorktreeMessage, DetachSessionMessage, DirectoryEntry,
    GetRecentLocationsMessage, GetRepoInfoMessage, InspectPathMessage, LayoutNode, MuteMessage,
    RecentLocation, RepoInfo, ServerEvent, SessionAgent, SetSettingMessage, SpawnSessionMessage,
    WorkspaceLayout, WorkspaceLayoutClosePaneMessage, WorkspaceLayoutFocusPaneMessage,
    WorkspaceLayoutPane, WorkspaceLayoutPaneKind, WorkspaceLayoutSplitDirection,
    WorkspaceLayoutSplitPaneMessage,
};
use gpui::{
    actions, div, ease_in_out, prelude::*, px, relative, Animation, AnimationExt as _, AnyElement,
    App, Context, Entity, Focusable, MouseButton, ParentElement, Render, SharedString, Window,
};
use gpui_component::input::{Input, InputEvent, InputState};
use serde_json::{json, Value};

use crate::{
    adapters::{
        automation,
        daemon::{DaemonClient, DaemonEvent},
        ghostty::GhosttyRuntime,
    },
    state::{store::ClientStore, terminal_model::TerminalModel},
    theme,
    views::terminal_view::{TerminalView, TerminalViewEvent},
};

actions!(
    workspace_navigation,
    [
        PreviousPane,
        NextPane,
        AddPaneVertical,
        AddPaneHorizontal,
        SplitShellVertical,
        SplitShellHorizontal,
        CloseLauncher
    ]
);

pub fn bind_keys(cx: &mut App) {
    cx.bind_keys([
        gpui::KeyBinding::new("cmd-left", PreviousPane, None),
        gpui::KeyBinding::new("cmd-up", PreviousPane, None),
        gpui::KeyBinding::new("cmd-right", NextPane, None),
        gpui::KeyBinding::new("cmd-down", NextPane, None),
        gpui::KeyBinding::new("cmd-n", AddPaneVertical, None),
        gpui::KeyBinding::new("cmd-shift-n", AddPaneHorizontal, None),
        gpui::KeyBinding::new("cmd-d", SplitShellVertical, None),
        gpui::KeyBinding::new("cmd-shift-d", SplitShellHorizontal, None),
        gpui::KeyBinding::new("escape", CloseLauncher, None),
    ]);
}

#[derive(Clone)]
enum LauncherMode {
    NewWorkspace,
    AddPane {
        workspace_id: String,
        target_pane_id: String,
        direction: WorkspaceLayoutSplitDirection,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PaneChoice {
    Shell,
    Agent(SessionAgent),
}

enum PendingSubmission {
    Bootstrap { workspace_id: String },
    Spawn { session_id: String },
    Split,
}

#[derive(Clone)]
enum RepositoryOperation {
    Loading,
    Refreshing,
    Creating,
    Deleting(String),
}

struct LauncherDraft {
    mode: LauncherMode,
    path_input: Option<Entity<InputState>>,
    path_value: String,
    choice: PaneChoice,
    yolo_mode: bool,
    recent_locations: Vec<RecentLocation>,
    entries: Vec<DirectoryEntry>,
    request_seq: u64,
    browse_request: Option<String>,
    inspect_request: Option<String>,
    repo_root_path: Option<String>,
    repo_info: Option<RepoInfo>,
    selected_path: Option<String>,
    repository_operation: Option<RepositoryOperation>,
    create_worktree_input: Option<Entity<InputState>>,
    create_from_default: bool,
    pending_delete_path: Option<String>,
    pending_submission: Option<PendingSubmission>,
    error: Option<String>,
}

pub struct NativeApp {
    daemon: Entity<DaemonClient>,
    ghostty: Rc<GhosttyRuntime>,
    store: ClientStore,
    selected_workspace_id: Option<String>,
    terminal_views: HashMap<String, Entity<TerminalView>>,
    hovered_pane_id: Option<String>,
    launcher: Option<LauncherDraft>,
    connection_error: Option<String>,
    _automation: Option<automation::server::Handle>,
}

impl NativeApp {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        cx.subscribe(&daemon, |this, _, event: &DaemonEvent, cx| {
            this.on_daemon_event(event, cx);
        })
        .detach();
        let automation = if automation::automation_enabled() {
            start_automation(cx)
        } else {
            None
        };
        Self {
            daemon,
            ghostty: GhosttyRuntime::new().expect("initialize embedded Ghostty renderer"),
            store: ClientStore::default(),
            selected_workspace_id: None,
            terminal_views: HashMap::new(),
            hovered_pane_id: None,
            launcher: None,
            connection_error: None,
            _automation: automation,
        }
    }

    fn on_daemon_event(&mut self, event: &DaemonEvent, cx: &mut Context<Self>) {
        match event {
            DaemonEvent::Connected => {
                automation::events::record("daemon_connected", json!({}));
                self.connection_error = None;
                self.reattach_visible_terminals(cx);
            }
            DaemonEvent::Disconnected(error) => {
                automation::events::record("daemon_disconnected", json!({"error": error}));
                self.connection_error = Some(error.clone());
            }
            DaemonEvent::Message(message) => {
                if !matches!(message, ServerEvent::PtyOutput(_)) {
                    let payload = match message {
                        ServerEvent::Unknown(event) => {
                            json!({"kind": "unknown", "event": event})
                        }
                        _ => json!({"kind": server_event_kind(message)}),
                    };
                    automation::events::record("daemon_event", payload);
                }
                match message {
                    ServerEvent::InitialState(initial) => {
                        if initial.protocol_version.as_deref()
                            != Some(DaemonClient::protocol_version())
                        {
                            self.connection_error = Some(format!(
                                "Protocol mismatch: daemon={}, native={}",
                                initial.protocol_version.as_deref().unwrap_or("unknown"),
                                DaemonClient::protocol_version()
                            ));
                            cx.notify();
                            return;
                        }
                        self.store.reset(
                            initial.sessions.clone(),
                            initial.workspaces.clone(),
                            initial.settings.clone(),
                        );
                        if !automation::start_empty()
                            && self
                                .selected_workspace_id
                                .as_ref()
                                .is_none_or(|selected| self.store.workspace(selected).is_none())
                        {
                            self.selected_workspace_id = self
                                .store
                                .workspaces
                                .first()
                                .map(|workspace| workspace.id.clone());
                        }
                        self.sync_visible_terminals(cx);
                    }
                    ServerEvent::SessionRegistered(message)
                    | ServerEvent::SessionStateChanged(message)
                    | ServerEvent::SessionTodosUpdated(message) => {
                        self.store.upsert_session(message.session.clone());
                    }
                    ServerEvent::SessionUnregistered(message) => {
                        self.store.remove_session(&message.session.id);
                    }
                    ServerEvent::SessionsUpdated(message) => {
                        self.store.sessions = message
                            .sessions
                            .iter()
                            .cloned()
                            .map(|session| (session.id.clone(), session))
                            .collect();
                    }
                    ServerEvent::WorkspaceRegistered(message)
                    | ServerEvent::WorkspaceStateChanged(message) => {
                        self.store.upsert_workspace(message.workspace.clone());
                        if self.selected_workspace_id.is_none() && !automation::start_empty() {
                            self.selected_workspace_id = Some(message.workspace.id.clone());
                        }
                        self.sync_visible_terminals(cx);
                    }
                    ServerEvent::WorkspaceUnregistered(message) => {
                        self.store.remove_workspace(&message.workspace.id);
                        if self.selected_workspace_id.as_deref()
                            == Some(message.workspace.id.as_str())
                        {
                            self.selected_workspace_id = if automation::start_empty() {
                                None
                            } else {
                                self.store
                                    .workspaces
                                    .first()
                                    .map(|workspace| workspace.id.clone())
                            };
                        }
                        self.sync_visible_terminals(cx);
                    }
                    ServerEvent::WorkspaceLayout(message)
                    | ServerEvent::WorkspaceLayoutUpdated(message) => {
                        self.store.set_layout(message.workspace_layout.clone());
                        self.sync_visible_terminals(cx);
                    }
                    ServerEvent::RecentLocationsResult(message) => {
                        if let Some(launcher) = self.launcher.as_mut() {
                            if message.success {
                                launcher.recent_locations = message.recent_locations.clone();
                            }
                        }
                    }
                    ServerEvent::BrowseDirectoryResult(message) => {
                        if let Some(launcher) = self.launcher.as_mut() {
                            if launcher.browse_request == message.request_id {
                                launcher.browse_request = None;
                                if message.success {
                                    launcher.entries = message.entries.clone();
                                } else {
                                    launcher.error = message.error.clone();
                                }
                            }
                        }
                    }
                    ServerEvent::InspectPathResult(message) => {
                        let matches_request = self
                            .launcher
                            .as_ref()
                            .is_some_and(|launcher| launcher.inspect_request == message.request_id);
                        if matches_request {
                            if message.success {
                                if let Some(inspection) = &message.inspection {
                                    if inspection.exists && inspection.is_directory {
                                        if let Some(repo_root) = inspection.repo_root.as_ref() {
                                            if let Some(launcher) = self.launcher.as_mut() {
                                                launcher.inspect_request = None;
                                                launcher.repo_root_path = Some(repo_root.clone());
                                                launcher.selected_path =
                                                    Some(inspection.resolved_path.clone());
                                                launcher.repository_operation =
                                                    Some(RepositoryOperation::Loading);
                                            }
                                            if let Err(error) = self
                                                .daemon
                                                .read(cx)
                                                .send(&GetRepoInfoMessage::local(repo_root.clone()))
                                            {
                                                if let Some(launcher) = self.launcher.as_mut() {
                                                    launcher.repository_operation = None;
                                                    launcher.error = Some(error);
                                                }
                                            }
                                        } else {
                                            self.submit_launcher_path(
                                                inspection.resolved_path.clone(),
                                                cx,
                                            );
                                        }
                                    } else if let Some(launcher) = self.launcher.as_mut() {
                                        launcher.error =
                                            Some("Selected path is not a directory".into());
                                        launcher.inspect_request = None;
                                    }
                                }
                            } else if let Some(launcher) = self.launcher.as_mut() {
                                launcher.error = message.error.clone();
                                launcher.inspect_request = None;
                            }
                        }
                    }
                    ServerEvent::SettingsUpdated(message) => {
                        if message.success.unwrap_or(true) {
                            self.store.settings = message.settings.clone();
                        } else if let Some(launcher) = self.launcher.as_mut() {
                            launcher.error = message.error.clone();
                        }
                    }
                    ServerEvent::GetRepoInfoResult(result) => {
                        if let Some(launcher) = self.launcher.as_mut() {
                            if launcher
                                .repository_operation
                                .as_ref()
                                .is_some_and(|operation| {
                                    matches!(
                                        operation,
                                        RepositoryOperation::Loading
                                            | RepositoryOperation::Refreshing
                                    )
                                })
                            {
                                launcher.repository_operation = None;
                                if result.success {
                                    launcher.repo_info = result.info.clone();
                                } else {
                                    launcher.error = result.error.clone();
                                }
                            }
                        }
                    }
                    ServerEvent::CreateWorktreeResult(result) => {
                        let creating = self.launcher.as_ref().is_some_and(|launcher| {
                            matches!(
                                launcher.repository_operation,
                                Some(RepositoryOperation::Creating)
                            )
                        });
                        if creating {
                            if result.success {
                                if let Some(path) = result.path.as_ref() {
                                    self.submit_launcher_path(path.clone(), cx);
                                }
                            } else if let Some(launcher) = self.launcher.as_mut() {
                                launcher.repository_operation = None;
                                launcher.error = result.error.clone();
                            }
                        }
                    }
                    ServerEvent::DeleteWorktreeResult(result) => {
                        let deleting = self.launcher.as_ref().is_some_and(|launcher| {
                            matches!(
                                launcher.repository_operation.as_ref(),
                                Some(RepositoryOperation::Deleting(path)) if path == &result.path
                            )
                        });
                        if deleting {
                            if result.success {
                                if let Some(launcher) = self.launcher.as_mut() {
                                    launcher.pending_delete_path = None;
                                    launcher.repository_operation =
                                        Some(RepositoryOperation::Refreshing);
                                }
                                self.request_launcher_repo_info(cx);
                            } else if let Some(launcher) = self.launcher.as_mut() {
                                launcher.repository_operation = None;
                                launcher.error = result.error.clone();
                            }
                        }
                    }
                    ServerEvent::BootstrapWorkspaceResult(result) => {
                        let pending_workspace = self.launcher.as_ref().and_then(|launcher| {
                            match launcher.pending_submission.as_ref() {
                                Some(PendingSubmission::Bootstrap { workspace_id }) => {
                                    Some(workspace_id.clone())
                                }
                                _ => None,
                            }
                        });
                        if pending_workspace.as_deref() == Some(result.workspace_id.as_str()) {
                            if result.success {
                                self.selected_workspace_id = Some(result.workspace_id.clone());
                                self.launcher = None;
                                self.sync_visible_terminals(cx);
                            } else if let Some(launcher) = self.launcher.as_mut() {
                                launcher.pending_submission = None;
                                launcher.error = result.error.clone();
                            }
                        }
                    }
                    ServerEvent::SpawnResult(result) => {
                        let matches_spawn = self.launcher.as_ref().is_some_and(|launcher| {
                            matches!(
                                launcher.pending_submission.as_ref(),
                                Some(PendingSubmission::Spawn { session_id })
                                    if session_id == &result.id
                            )
                        });
                        if matches_spawn {
                            if result.success {
                                self.launcher = None;
                            } else if let Some(launcher) = self.launcher.as_mut() {
                                launcher.pending_submission = None;
                                launcher.error = result.error.clone();
                            }
                        }
                    }
                    ServerEvent::WorkspaceLayoutActionResult(result) => {
                        if self.launcher.as_ref().is_some_and(|launcher| {
                            matches!(launcher.pending_submission, Some(PendingSubmission::Split))
                        }) && result.action == "workspace_layout_split_pane"
                        {
                            if result.success {
                                self.launcher = None;
                            } else if let Some(launcher) = self.launcher.as_mut() {
                                launcher.pending_submission = None;
                                launcher.error = result.error.clone();
                            }
                        }
                    }
                    _ => {}
                }
            }
        }
        cx.notify();
    }

    fn select_workspace(&mut self, workspace_id: String, cx: &mut Context<Self>) {
        if self.selected_workspace_id.as_deref() == Some(workspace_id.as_str()) {
            return;
        }
        self.selected_workspace_id = Some(workspace_id.clone());
        self.sync_visible_terminals(cx);
        cx.notify();
    }

    fn open_launcher(
        &mut self,
        mode: LauncherMode,
        initial_path: String,
        focus_input: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let input = cx.new(|cx| InputState::new(window, cx).default_value(initial_path));
        cx.subscribe(&input, |this, input, event: &InputEvent, cx| match event {
            InputEvent::Change => {
                let path = input.read(cx).value().to_string();
                this.request_launcher_browse(path, cx);
            }
            InputEvent::PressEnter { .. } => this.inspect_launcher_path(cx),
            _ => {}
        })
        .detach();
        self.launcher = Some(LauncherDraft {
            mode,
            path_input: Some(input.clone()),
            path_value: input.read(cx).value().to_string(),
            choice: launcher_initial_choice(&self.store.settings),
            yolo_mode: launcher_initial_yolo(&self.store.settings),
            recent_locations: Vec::new(),
            entries: Vec::new(),
            request_seq: 0,
            browse_request: None,
            inspect_request: None,
            repo_root_path: None,
            repo_info: None,
            selected_path: None,
            repository_operation: None,
            create_worktree_input: None,
            create_from_default: false,
            pending_delete_path: None,
            pending_submission: None,
            error: None,
        });
        let _ = self
            .daemon
            .read(cx)
            .send(&GetRecentLocationsMessage::new(12));
        self.request_launcher_browse(input.read(cx).value().to_string(), cx);
        if focus_input {
            input.read(cx).focus_handle(cx).focus(window);
        }
        cx.notify();
    }

    fn open_new_workspace_with_focus(
        &mut self,
        focus_input: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_launcher(
            LauncherMode::NewWorkspace,
            self.store
                .settings
                .get("projects_directory")
                .cloned()
                .unwrap_or_else(|| "~".to_string()),
            focus_input,
            window,
            cx,
        );
    }

    fn open_new_workspace(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.open_new_workspace_with_focus(true, window, cx);
    }

    fn open_add_pane_with_focus(
        &mut self,
        direction: WorkspaceLayoutSplitDirection,
        focus_input: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(layout) = self.visible_layout() else {
            return;
        };
        let Some(workspace) = self.store.workspace(&layout.workspace_id) else {
            return;
        };
        self.open_launcher(
            LauncherMode::AddPane {
                workspace_id: layout.workspace_id.clone(),
                target_pane_id: layout.active_pane_id.clone(),
                direction,
            },
            workspace.directory.clone(),
            focus_input,
            window,
            cx,
        );
    }

    fn open_add_pane(
        &mut self,
        direction: WorkspaceLayoutSplitDirection,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_add_pane_with_focus(direction, true, window, cx);
    }

    fn request_launcher_browse(&mut self, path: String, cx: &mut Context<Self>) {
        let Some(launcher) = self.launcher.as_mut() else {
            return;
        };
        launcher.request_seq += 1;
        launcher.path_value = path.clone();
        let request_id = format!("native-launcher-browse-{}", launcher.request_seq);
        launcher.browse_request = Some(request_id.clone());
        launcher.error = None;
        let _ = self
            .daemon
            .read(cx)
            .send(&BrowseDirectoryMessage::new(path, request_id));
    }

    fn inspect_launcher_path(&mut self, cx: &mut Context<Self>) {
        let Some(launcher) = self.launcher.as_mut() else {
            return;
        };
        if launcher.pending_submission.is_some() || launcher.inspect_request.is_some() {
            return;
        }
        launcher.request_seq += 1;
        let request_id = format!("native-launcher-inspect-{}", launcher.request_seq);
        let path = launcher
            .path_input
            .as_ref()
            .map(|input| input.read(cx).value().to_string())
            .unwrap_or_else(|| launcher.path_value.clone());
        launcher.inspect_request = Some(request_id.clone());
        launcher.error = None;
        if let Err(error) = self
            .daemon
            .read(cx)
            .send(&InspectPathMessage::new(path, request_id))
        {
            launcher.inspect_request = None;
            launcher.error = Some(error);
        }
        cx.notify();
    }

    fn request_launcher_repo_info(&mut self, cx: &mut Context<Self>) {
        let Some(repo_root) = self
            .launcher
            .as_ref()
            .and_then(|launcher| launcher.repo_root_path.clone())
        else {
            return;
        };
        if let Err(error) = self
            .daemon
            .read(cx)
            .send(&GetRepoInfoMessage::local(repo_root))
        {
            if let Some(launcher) = self.launcher.as_mut() {
                launcher.repository_operation = None;
                launcher.error = Some(error);
            }
        }
        cx.notify();
    }

    fn choose_launcher_pane(&mut self, choice: PaneChoice, cx: &mut Context<Self>) {
        if !launcher_choice_available(choice, &self.store.settings) {
            return;
        }
        if let Some(launcher) = self.launcher.as_mut() {
            launcher.choice = choice;
            if !launcher_choice_yolo_supported(choice, &self.store.settings) {
                launcher.yolo_mode = false;
            }
        }
        let value = match choice {
            PaneChoice::Shell => "shell".to_string(),
            PaneChoice::Agent(agent) => {
                let _ = self.daemon.read(cx).send(&SetSettingMessage::new(
                    "new_session_agent",
                    agent.to_string(),
                ));
                agent.to_string()
            }
        };
        let _ = self.daemon.read(cx).send(&SetSettingMessage::new(
            "native_launcher_pane_choice",
            value,
        ));
        cx.notify();
    }

    fn toggle_launcher_yolo(&mut self, cx: &mut Context<Self>) {
        let Some(launcher) = self.launcher.as_mut() else {
            return;
        };
        if !launcher_choice_yolo_supported(launcher.choice, &self.store.settings) {
            return;
        }
        launcher.yolo_mode = !launcher.yolo_mode;
        let value = launcher.yolo_mode.to_string();
        let _ = self
            .daemon
            .read(cx)
            .send(&SetSettingMessage::new("new_session_yolo", value));
        cx.notify();
    }

    fn back_launcher_stage(&mut self, cx: &mut Context<Self>) {
        if let Some(launcher) = self.launcher.as_mut() {
            launcher.repo_root_path = None;
            launcher.repo_info = None;
            launcher.selected_path = None;
            launcher.repository_operation = None;
            launcher.create_worktree_input = None;
            launcher.pending_delete_path = None;
            launcher.error = None;
        }
        cx.notify();
    }

    fn open_create_worktree(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let input = cx.new(|cx| InputState::new(window, cx));
        cx.subscribe(&input, |this, _, event: &InputEvent, cx| {
            if matches!(event, InputEvent::PressEnter { .. }) {
                this.submit_create_worktree(cx);
            }
        })
        .detach();
        if let Some(launcher) = self.launcher.as_mut() {
            launcher.pending_delete_path = None;
            launcher.create_from_default = false;
            launcher.create_worktree_input = Some(input.clone());
        }
        input.read(cx).focus_handle(cx).focus(window);
        cx.notify();
    }

    fn submit_create_worktree(&mut self, cx: &mut Context<Self>) {
        let Some(launcher) = self.launcher.as_mut() else {
            return;
        };
        let Some(repo_info) = launcher.repo_info.as_ref() else {
            return;
        };
        let Some(input) = launcher.create_worktree_input.as_ref() else {
            return;
        };
        let branch = input.read(cx).value().trim().to_string();
        if branch.is_empty() {
            return;
        }
        let starting_from = if launcher.create_from_default {
            format!("origin/{}", repo_info.default_branch)
        } else {
            repo_info.current_branch.clone()
        };
        let message = CreateWorktreeMessage {
            cmd: "create_worktree",
            main_repo: repo_info.repo.clone(),
            branch,
            path: None,
            endpoint_id: None,
            starting_from: Some(starting_from),
        };
        match self.daemon.read(cx).send(&message) {
            Ok(()) => launcher.repository_operation = Some(RepositoryOperation::Creating),
            Err(error) => launcher.error = Some(error),
        }
        cx.notify();
    }

    fn confirm_delete_worktree(&mut self, path: String, cx: &mut Context<Self>) {
        let message = DeleteWorktreeMessage {
            cmd: "delete_worktree",
            path: path.clone(),
            endpoint_id: None,
        };
        if let Some(launcher) = self.launcher.as_mut() {
            launcher.pending_delete_path = None;
            match self.daemon.read(cx).send(&message) {
                Ok(()) => launcher.repository_operation = Some(RepositoryOperation::Deleting(path)),
                Err(error) => launcher.error = Some(error),
            }
        }
        cx.notify();
    }

    fn submit_launcher_path(&mut self, path: String, cx: &mut Context<Self>) {
        let Some(launcher) = self.launcher.as_mut() else {
            return;
        };
        launcher.inspect_request = None;
        let choice = launcher.choice;
        let yolo_mode = launcher.yolo_mode;
        let pending = match &launcher.mode {
            LauncherMode::NewWorkspace => {
                let workspace_id = new_native_id("workspace");
                let session_id = new_native_id("session");
                let (kind, agent) = pane_choice_wire(choice);
                let title = path
                    .rsplit('/')
                    .find(|part| !part.is_empty())
                    .unwrap_or("Workspace")
                    .to_string();
                let message = BootstrapWorkspaceMessage {
                    cmd: "bootstrap_workspace",
                    id: workspace_id.clone(),
                    title,
                    directory: path.clone(),
                    endpoint_id: None,
                    initial_session: BootstrapWorkspaceInitialSession {
                        id: session_id,
                        cwd: path,
                        kind,
                        agent,
                        cols: 100,
                        rows: 36,
                        label: None,
                        yolo_mode: (choice != PaneChoice::Shell).then_some(yolo_mode),
                        executable: None,
                    },
                };
                if let Err(error) = self.daemon.read(cx).send(&message) {
                    launcher.error = Some(error);
                    return;
                }
                PendingSubmission::Bootstrap { workspace_id }
            }
            LauncherMode::AddPane {
                workspace_id,
                target_pane_id,
                direction,
            } => match choice {
                PaneChoice::Shell => {
                    let message = WorkspaceLayoutSplitPaneMessage {
                        cmd: "workspace_layout_split_pane",
                        workspace_id: workspace_id.clone(),
                        target_pane_id: target_pane_id.clone(),
                        direction: *direction,
                        cwd: Some(path),
                    };
                    if let Err(error) = self.daemon.read(cx).send(&message) {
                        launcher.error = Some(error);
                        return;
                    }
                    PendingSubmission::Split
                }
                PaneChoice::Agent(agent) => {
                    let session_id = new_native_id("session");
                    let mut message = SpawnSessionMessage::new(
                        session_id.clone(),
                        path,
                        workspace_id.clone(),
                        agent.to_string(),
                        100,
                        36,
                    );
                    message.target_pane_id = Some(target_pane_id.clone());
                    message.direction = Some(*direction);
                    message.yolo_mode = Some(yolo_mode);
                    if let Err(error) = self.daemon.read(cx).send(&message) {
                        launcher.error = Some(error);
                        return;
                    }
                    PendingSubmission::Spawn { session_id }
                }
            },
        };
        launcher.pending_submission = Some(pending);
        cx.notify();
    }

    fn dismiss_launcher_layer(&mut self, cx: &mut Context<Self>) {
        if self
            .launcher
            .as_ref()
            .is_some_and(|launcher| launcher.pending_delete_path.is_some())
        {
            if let Some(launcher) = self.launcher.as_mut() {
                launcher.pending_delete_path = None;
            }
            cx.notify();
        } else if self
            .launcher
            .as_ref()
            .is_some_and(|launcher| launcher.create_worktree_input.is_some())
        {
            if let Some(launcher) = self.launcher.as_mut() {
                launcher.create_worktree_input = None;
            }
            cx.notify();
        } else if self
            .launcher
            .as_ref()
            .is_some_and(|launcher| launcher.repo_info.is_some())
        {
            self.back_launcher_stage(cx);
        } else if self.launcher.is_some() {
            self.launcher = None;
            cx.notify();
        }
    }

    fn dismiss_launcher_layer_with_focus(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let had_launcher = self.launcher.is_some();
        self.dismiss_launcher_layer(cx);
        if had_launcher && self.launcher.is_none() {
            self.focus_active_terminal(window, cx);
        }
    }

    fn close_launcher(&mut self, _: &CloseLauncher, window: &mut Window, cx: &mut Context<Self>) {
        self.dismiss_launcher_layer_with_focus(window, cx);
    }

    fn add_pane_vertical(
        &mut self,
        _: &AddPaneVertical,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_add_pane(WorkspaceLayoutSplitDirection::Vertical, window, cx);
    }

    fn add_pane_horizontal(
        &mut self,
        _: &AddPaneHorizontal,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_add_pane(WorkspaceLayoutSplitDirection::Horizontal, window, cx);
    }

    fn split_shell(&self, direction: WorkspaceLayoutSplitDirection, cx: &mut Context<Self>) {
        if let Some(layout) = self.visible_layout() {
            let _ = self.daemon.read(cx).send(&WorkspaceLayoutSplitPaneMessage {
                cmd: "workspace_layout_split_pane",
                workspace_id: layout.workspace_id.clone(),
                target_pane_id: layout.active_pane_id.clone(),
                direction,
                cwd: None,
            });
        }
    }

    fn split_shell_vertical(
        &mut self,
        _: &SplitShellVertical,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.split_shell(WorkspaceLayoutSplitDirection::Vertical, cx);
    }

    fn split_shell_horizontal(
        &mut self,
        _: &SplitShellHorizontal,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.split_shell(WorkspaceLayoutSplitDirection::Horizontal, cx);
    }

    pub(crate) fn automation_select_workspace(
        &mut self,
        workspace_id: &str,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        if self.store.workspace(workspace_id).is_none() {
            return Err(format!("unknown workspace id: {workspace_id}"));
        }
        self.select_workspace(workspace_id.to_string(), cx);
        Ok(())
    }

    fn open_background_launcher(
        &mut self,
        mode: LauncherMode,
        initial_path: String,
        cx: &mut Context<Self>,
    ) {
        self.launcher = Some(LauncherDraft {
            mode,
            path_input: None,
            path_value: initial_path.clone(),
            choice: launcher_initial_choice(&self.store.settings),
            yolo_mode: launcher_initial_yolo(&self.store.settings),
            recent_locations: Vec::new(),
            entries: Vec::new(),
            request_seq: 0,
            browse_request: None,
            inspect_request: None,
            repo_root_path: None,
            repo_info: None,
            selected_path: None,
            repository_operation: None,
            create_worktree_input: None,
            create_from_default: false,
            pending_delete_path: None,
            pending_submission: None,
            error: None,
        });
        let _ = self
            .daemon
            .read(cx)
            .send(&GetRecentLocationsMessage::new(12));
        self.request_launcher_browse(initial_path, cx);
        cx.notify();
    }

    pub(crate) fn automation_open_new_workspace(&mut self, cx: &mut Context<Self>) {
        let path = self
            .store
            .settings
            .get("projects_directory")
            .cloned()
            .unwrap_or_else(|| "~".to_string());
        self.open_background_launcher(LauncherMode::NewWorkspace, path, cx);
    }

    pub(crate) fn automation_open_add_pane(
        &mut self,
        direction: WorkspaceLayoutSplitDirection,
        cx: &mut Context<Self>,
    ) {
        let Some(layout) = self.visible_layout() else {
            return;
        };
        let Some(workspace) = self.store.workspace(&layout.workspace_id) else {
            return;
        };
        self.open_background_launcher(
            LauncherMode::AddPane {
                workspace_id: layout.workspace_id.clone(),
                target_pane_id: layout.active_pane_id.clone(),
                direction,
            },
            workspace.directory.clone(),
            cx,
        );
    }

    pub(crate) fn automation_cancel_launcher(&mut self, cx: &mut Context<Self>) {
        self.dismiss_launcher_layer(cx);
    }

    pub(crate) fn automation_set_launcher_path(
        &mut self,
        path: String,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        if self.launcher.is_none() {
            return Err("launcher is not open".into());
        }
        self.request_launcher_browse(path, cx);
        Ok(())
    }

    pub(crate) fn automation_inspect_launcher_path(
        &mut self,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        if self.launcher.is_none() {
            return Err("launcher is not open".into());
        }
        self.inspect_launcher_path(cx);
        Ok(())
    }

    pub(crate) fn automation_launcher_snapshot(&self, _cx: &App) -> Value {
        match self.launcher.as_ref() {
            None => json!({ "open": false }),
            Some(launcher) => {
                let (mode, direction) = match launcher.mode {
                    LauncherMode::NewWorkspace => ("new_workspace", None),
                    LauncherMode::AddPane { direction, .. } => (
                        "add_pane",
                        Some(match direction {
                            WorkspaceLayoutSplitDirection::Vertical => "vertical",
                            WorkspaceLayoutSplitDirection::Horizontal => "horizontal",
                        }),
                    ),
                };
                json!({
                    "open": true,
                    "mode": mode,
                    "direction": direction,
                    "path": launcher.path_value,
                    "choice": match launcher.choice {
                        PaneChoice::Shell => "shell".to_string(),
                        PaneChoice::Agent(agent) => agent.to_string(),
                    },
                    "stage": if launcher.repo_info.is_some() { "repository" } else { "path" },
                    "destinationCount": launcher.repo_info.as_ref().map(|repo| repo.worktrees.len() + 1).unwrap_or(0),
                    "hasPendingSubmission": launcher.pending_submission.is_some(),
                })
            }
        }
    }

    pub(crate) fn daemon(&self) -> &Entity<DaemonClient> {
        &self.daemon
    }

    pub(crate) fn has_visible_runtime(&self, runtime_id: &str) -> bool {
        self.terminal_views.contains_key(runtime_id)
    }

    pub(crate) fn terminal_view(&self, runtime_id: &str) -> Option<Entity<TerminalView>> {
        self.terminal_views.get(runtime_id).cloned()
    }

    pub(crate) fn runtime_for_pane(&self, workspace_id: &str, pane_id: &str) -> Option<String> {
        self.store
            .layouts
            .get(workspace_id)?
            .pane(pane_id)?
            .runtime_id
            .clone()
    }

    pub(crate) fn automation_sessions(&self) -> Value {
        let mut sessions = self.store.sessions.values().cloned().collect::<Vec<_>>();
        sessions.sort_by(|left, right| left.id.cmp(&right.id));
        serde_json::to_value(sessions).unwrap_or(Value::Null)
    }

    pub(crate) fn automation_snapshot(&self, cx: &App) -> Value {
        let mut runtime_ids = self.terminal_views.keys().cloned().collect::<Vec<_>>();
        runtime_ids.sort();
        let layouts = self.store.layouts.values().cloned().collect::<Vec<_>>();
        let daemon = self.daemon.read(cx);
        json!({
            "selected_workspace_id": self.selected_workspace_id,
            "workspaces": self.store.workspaces,
            "layouts": layouts,
            "sessions": self.automation_sessions(),
            "visible_terminal_runtime_ids": runtime_ids,
            "hovered_pane_id": self.hovered_pane_id,
            "daemon": {
                "connected": daemon.connected(),
                "error": daemon.error(),
            },
        })
    }

    pub(crate) fn automation_structured_snapshot(&self, cx: &App, include_text: bool) -> Value {
        let selected_workspace_id = self.selected_workspace_id.clone();
        let panes = self
            .visible_layout()
            .map(|layout| {
                layout
                    .panes
                    .iter()
                    .map(|pane| {
                        let terminal = pane
                            .runtime_id
                            .as_ref()
                            .and_then(|runtime_id| self.terminal_views.get(runtime_id));
                        let text = if include_text {
                            terminal.and_then(|view| view.read(cx).screen_text())
                        } else {
                            None
                        };
                        let size = terminal.map(|view| {
                            let (cols, rows) = view.read(cx).terminal_size(cx);
                            json!({ "cols": cols, "rows": rows })
                        });
                        let attached = terminal
                            .map(|view| view.read(cx).attached(cx))
                            .unwrap_or(false);
                        json!({
                            "paneId": pane.pane_id,
                            "runtimeId": pane.runtime_id,
                            "sessionId": pane.session_id,
                            "title": pane.title,
                            "kind": pane.kind,
                            "visible": true,
                            "attached": attached,
                            "size": size,
                            "text": text,
                        })
                    })
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
        json!({
            "selectedWorkspaceId": selected_workspace_id,
            "workspaces": self.store.workspaces,
            "panes": panes,
        })
    }

    pub(crate) fn automation_render_health(&self, cx: &App) -> Value {
        let panes = self
            .visible_layout()
            .map(|layout| {
                layout
                    .panes
                    .iter()
                    .map(|pane| {
                        let terminal = pane
                            .runtime_id
                            .as_ref()
                            .and_then(|runtime_id| self.terminal_views.get(runtime_id));
                        let ready = terminal
                            .map(|view| view.read(cx).attached(cx))
                            .unwrap_or(false);
                        json!({
                            "paneId": pane.pane_id,
                            "runtimeId": pane.runtime_id,
                            "flags": {
                                "visible": true,
                                "terminalReady": ready,
                            },
                        })
                    })
                    .collect::<Vec<_>>()
            })
            .unwrap_or_default();
        json!({
            "selectedWorkspaceId": self.selected_workspace_id,
            "panes": panes,
        })
    }

    fn visible_layout(&self) -> Option<&WorkspaceLayout> {
        let workspace_id = self.selected_workspace_id.as_ref()?;
        self.store.layouts.get(workspace_id)
    }

    fn sync_visible_terminals(&mut self, cx: &mut Context<Self>) {
        let desired: HashSet<String> = self
            .visible_layout()
            .into_iter()
            .flat_map(|layout| layout.panes.iter())
            .filter_map(|pane| pane.runtime_id.clone())
            .collect();
        let removed: Vec<String> = self
            .terminal_views
            .keys()
            .filter(|runtime_id| !desired.contains(*runtime_id))
            .cloned()
            .collect();
        for runtime_id in removed {
            self.terminal_views.remove(&runtime_id);
            let _ = self
                .daemon
                .read(cx)
                .send(&DetachSessionMessage::new(runtime_id));
        }
        for runtime_id in desired {
            if self.terminal_views.contains_key(&runtime_id) {
                continue;
            }
            let model =
                cx.new(|cx| TerminalModel::new(runtime_id.clone(), 100, 36, &self.daemon, cx));
            let view = cx
                .new(|cx| TerminalView::new(model, self.daemon.clone(), self.ghostty.clone(), cx));
            cx.subscribe(
                &view,
                |this, _, event: &TerminalViewEvent, cx| match event {
                    TerminalViewEvent::FocusRequested(runtime_id) => {
                        let target = this.visible_layout().and_then(|layout| {
                            layout
                                .panes
                                .iter()
                                .find(|pane| pane.runtime_id.as_deref() == Some(runtime_id))
                                .map(|pane| (layout.workspace_id.clone(), pane.pane_id.clone()))
                        });
                        if let Some((workspace_id, pane_id)) = target {
                            let _ = this
                                .daemon
                                .read(cx)
                                .send(&WorkspaceLayoutFocusPaneMessage::new(workspace_id, pane_id));
                        }
                    }
                },
            )
            .detach();
            view.update(cx, |view, cx| view.attach(cx));
            self.terminal_views.insert(runtime_id, view);
        }
    }

    fn reattach_visible_terminals(&self, cx: &mut Context<Self>) {
        for view in self.terminal_views.values() {
            view.update(cx, |view, cx| view.attach(cx));
        }
    }

    fn focus_terminal_runtime(
        &self,
        runtime_id: Option<&str>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(runtime_id) = runtime_id else {
            return;
        };
        if let Some(view) = self.terminal_views.get(runtime_id) {
            view.update(cx, |view, _| view.focus_for_input(window));
        }
    }

    fn focus_active_terminal(&self, window: &mut Window, cx: &mut Context<Self>) {
        let runtime_id = self.visible_layout().and_then(|layout| {
            layout
                .pane(&layout.active_pane_id)
                .and_then(|pane| pane.runtime_id.as_deref())
        });
        self.focus_terminal_runtime(runtime_id, window, cx);
    }

    fn focus_pane(
        &mut self,
        workspace_id: String,
        pane_id: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let runtime_id = self.runtime_for_pane(&workspace_id, &pane_id);
        let _ = self
            .daemon
            .read(cx)
            .send(&WorkspaceLayoutFocusPaneMessage::new(workspace_id, pane_id));
        self.focus_terminal_runtime(runtime_id.as_deref(), window, cx);
    }

    fn step_pane(&mut self, delta: isize, window: &mut Window, cx: &mut Context<Self>) {
        let Some(layout) = self.visible_layout() else {
            return;
        };
        let current = layout
            .panes
            .iter()
            .position(|pane| pane.pane_id == layout.active_pane_id)
            .unwrap_or_default();
        if layout.panes.is_empty() {
            return;
        }
        let next = (current as isize + delta).rem_euclid(layout.panes.len() as isize) as usize;
        let workspace_id = layout.workspace_id.clone();
        let pane_id = layout.panes[next].pane_id.clone();
        self.focus_pane(workspace_id, pane_id, window, cx);
    }

    fn previous_pane(&mut self, _: &PreviousPane, window: &mut Window, cx: &mut Context<Self>) {
        self.step_pane(-1, window, cx);
    }

    fn next_pane(&mut self, _: &NextPane, window: &mut Window, cx: &mut Context<Self>) {
        self.step_pane(1, window, cx);
    }

    fn render_sidebar(&self, cx: &mut Context<Self>) -> AnyElement {
        let mut groups = Vec::new();
        for workspace in &self.store.workspaces {
            let selected = self.selected_workspace_id.as_deref() == Some(workspace.id.as_str());
            let workspace_id = workspace.id.clone();
            let mut group = div().w_full().flex().flex_col().mb_2().child(
                div()
                    .w_full()
                    .px_4()
                    .py_2()
                    .flex()
                    .items_center()
                    .gap_2()
                    .bg(if selected {
                        theme::sodium::soft()
                    } else {
                        theme::ink::nocturne()
                    })
                    .border_l_2()
                    .border_color(if selected {
                        theme::sodium::vapor()
                    } else {
                        theme::ink::nocturne()
                    })
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, window, cx| {
                            this.select_workspace(workspace_id.clone(), cx);
                            this.focus_active_terminal(window, cx);
                        }),
                    )
                    .child(
                        div()
                            .w(px(8.))
                            .h(px(8.))
                            .rounded_full()
                            .bg(theme::workspace_state_color(workspace.status)),
                    )
                    .child(
                        div()
                            .flex_1()
                            .text_color(theme::moon::primary())
                            .child(SharedString::from(workspace.title.clone())),
                    ),
            );

            if selected {
                if let Some(layout) = self.store.layouts.get(&workspace.id) {
                    for pane in &layout.panes {
                        group = group.child(self.render_pane_row(layout, pane, cx));
                    }
                }
            }
            groups.push(group.into_any_element());
        }

        div()
            .w(px(270.))
            .h_full()
            .bg(theme::ink::nocturne())
            .border_r_1()
            .border_color(theme::ink::firm())
            .pt_5()
            .child(
                div()
                    .px_4()
                    .pb_4()
                    .flex()
                    .items_center()
                    .text_size(px(12.))
                    .text_color(theme::moon::dim())
                    .child(div().flex_1().child("WORKSPACES"))
                    .child(
                        div()
                            .px_2()
                            .py_1()
                            .rounded_sm()
                            .hover(|element| element.bg(theme::ink::shade()))
                            .child("+")
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |this, _, window, cx| {
                                    this.open_new_workspace(window, cx);
                                }),
                            ),
                    ),
            )
            .children(groups)
            .into_any_element()
    }

    fn render_launcher(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let launcher = self.launcher.as_ref()?;
        let (title, subtitle) = match &launcher.mode {
            LauncherMode::NewWorkspace => (
                "New Workspace",
                "Choose a directory and the initial pane for this workspace".to_string(),
            ),
            LauncherMode::AddPane { direction, .. } => (
                "Add Pane",
                format!(
                    "Insert a {} split in the selected workspace",
                    match direction {
                        WorkspaceLayoutSplitDirection::Vertical => "vertical",
                        WorkspaceLayoutSplitDirection::Horizontal => "horizontal",
                    }
                ),
            ),
        };
        let choice = launcher.choice;
        let busy = launcher.inspect_request.is_some()
            || launcher.pending_submission.is_some()
            || launcher.repository_operation.is_some();
        let options = [
            (PaneChoice::Shell, "Terminal"),
            (PaneChoice::Agent(SessionAgent::Claude), "Claude"),
            (PaneChoice::Agent(SessionAgent::Codex), "Codex"),
            (PaneChoice::Agent(SessionAgent::Copilot), "Copilot"),
            (PaneChoice::Agent(SessionAgent::Pi), "Pi"),
        ];
        let mut suggestions = Vec::new();
        for location in launcher.recent_locations.iter().take(4) {
            suggestions.push((location.path.clone(), location.label.clone()));
        }
        for entry in launcher.entries.iter().take(5) {
            if !suggestions.iter().any(|(path, _)| path == &entry.path) {
                suggestions.push((entry.path.clone(), entry.name.clone()));
            }
        }
        let path_input = launcher.path_input.clone();
        let error = launcher.error.clone();
        let yolo = launcher.yolo_mode;
        let show_yolo = launcher_choice_yolo_supported(choice, &self.store.settings);
        let repo_info = launcher.repo_info.clone();
        let repo_root_path = launcher.repo_root_path.clone();
        let create_input = launcher.create_worktree_input.clone();
        let is_creating_worktree = create_input.is_some();
        let create_from_default = launcher.create_from_default;
        let pending_delete_path = launcher.pending_delete_path.clone();
        let repository_status =
            launcher
                .repository_operation
                .as_ref()
                .map(|operation| match operation {
                    RepositoryOperation::Loading => "Reading repository...".to_string(),
                    RepositoryOperation::Refreshing => "Refreshing destinations...".to_string(),
                    RepositoryOperation::Creating => "Creating worktree...".to_string(),
                    RepositoryOperation::Deleting(path) => format!("Removing {path}..."),
                });
        let protected_path = match &launcher.mode {
            LauncherMode::AddPane { workspace_id, .. } => self
                .store
                .workspace(workspace_id)
                .map(|workspace| workspace.directory.clone()),
            LauncherMode::NewWorkspace => None,
        };
        let dialog = div()
            .w(px(680.))
            .rounded_lg()
            .border_1()
            .border_color(theme::ink::firm())
            .bg(theme::ink::nocturne())
            .p_5()
            .flex()
            .flex_col()
            .gap_4()
            .on_mouse_down(MouseButton::Left, |_event, _window, cx| {
                cx.stop_propagation();
            })
            .child(
                div()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .child(
                        div()
                            .text_size(px(20.))
                            .text_color(theme::moon::primary())
                            .child(title),
                    )
                    .child(
                        div()
                            .text_size(px(12.))
                            .text_color(theme::moon::secondary())
                            .child(subtitle),
                    ),
            )
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_3()
                    .child(
                        div()
                            .text_size(px(11.))
                            .text_color(theme::moon::dim())
                            .child("TARGET"),
                    )
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .rounded_sm()
                            .border_1()
                            .border_color(theme::sodium::vapor())
                            .text_color(theme::moon::primary())
                            .child("Local  this machine"),
                    ),
            )
            .child(div().flex().gap_2().children(options.into_iter().map(
                |(next_choice, label)| {
                    let available = launcher_choice_available(next_choice, &self.store.settings);
                    div()
                        .px_3()
                        .py_2()
                        .rounded_sm()
                        .border_1()
                        .border_color(if choice == next_choice {
                            theme::sodium::vapor()
                        } else {
                            theme::ink::firm()
                        })
                        .text_color(if available {
                            theme::moon::primary()
                        } else {
                            theme::moon::dim()
                        })
                        .child(if available {
                            label.to_string()
                        } else {
                            format!("{label} unavailable")
                        })
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |this, _, _, cx| {
                                if available {
                                    this.choose_launcher_pane(next_choice, cx);
                                }
                            }),
                        )
                },
            )))
            .when(repo_info.is_none(), |element| {
                element
                    .child(
                        div()
                            .flex()
                            .flex_col()
                            .gap_2()
                            .child(
                                div()
                                    .text_size(px(11.))
                                    .text_color(theme::moon::dim())
                                    .child("DIRECTORY"),
                            )
                            .when_some(path_input.clone(), |element, input| {
                                element.child(Input::new(&input).cleanable(true))
                            })
                            .when(path_input.is_none(), |element| {
                                element.child(
                                    div()
                                        .px_3()
                                        .py_2()
                                        .border_1()
                                        .border_color(theme::ink::firm())
                                        .child(launcher.path_value.clone()),
                                )
                            }),
                    )
                    .when(!suggestions.is_empty(), |element| {
                        element.child(div().flex().flex_col().gap_1().children(
                            suggestions.into_iter().map(|(path, label)| {
                                let fill = path.clone();
                                div()
                                    .px_3()
                                    .py_2()
                                    .rounded_sm()
                                    .text_size(px(12.))
                                    .hover(|element| element.bg(theme::ink::shade()))
                                    .child(format!("{label}  {path}"))
                                    .on_mouse_down(
                                        MouseButton::Left,
                                        cx.listener(move |this, _, window, cx| {
                                            if let Some(launcher) = this.launcher.as_mut() {
                                                launcher.path_value = fill.clone();
                                                if let Some(input) = launcher.path_input.as_ref() {
                                                    input.update(cx, |input, cx| {
                                                        input.set_value(fill.clone(), window, cx);
                                                    });
                                                }
                                            }
                                        }),
                                    )
                            }),
                        ))
                    })
            })
            .when_some(repo_info, |element, repo| {
                let repo_path = repo.repo.clone();
                let branch = repo.current_branch.clone();
                let hash = repo.current_commit_hash.chars().take(7).collect::<String>();
                let main_path = repo.repo.clone();
                let worktrees = repo.worktrees.clone();
                element
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .child(
                                div()
                                    .flex_1()
                                    .text_size(px(11.))
                                    .text_color(theme::moon::dim())
                                    .child("DESTINATIONS"),
                            )
                            .child(
                                div()
                                    .px_2()
                                    .py_1()
                                    .text_size(px(12.))
                                    .text_color(theme::moon::secondary())
                                    .child("Back")
                                    .on_mouse_down(
                                        MouseButton::Left,
                                        cx.listener(move |this, _, _, cx| {
                                            this.back_launcher_stage(cx);
                                        }),
                                    ),
                            )
                            .child(
                                div()
                                    .px_2()
                                    .py_1()
                                    .text_size(px(12.))
                                    .text_color(theme::moon::secondary())
                                    .child("Refresh")
                                    .on_mouse_down(
                                        MouseButton::Left,
                                        cx.listener(move |this, _, _, cx| {
                                            if let Some(launcher) = this.launcher.as_mut() {
                                                launcher.repository_operation =
                                                    Some(RepositoryOperation::Refreshing);
                                            }
                                            this.request_launcher_repo_info(cx);
                                        }),
                                    ),
                            ),
                    )
                    .child(
                        div()
                            .px_3()
                            .py_2()
                            .rounded_sm()
                            .border_1()
                            .border_color(theme::sodium::vapor())
                            .hover(|element| element.bg(theme::ink::shade()))
                            .child(format!("{branch}  {hash}  {repo_path}"))
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(move |this, _, _, cx| {
                                    this.submit_launcher_path(main_path.clone(), cx);
                                }),
                            ),
                    )
                    .children(worktrees.into_iter().map(|worktree| {
                        let open_path = worktree.path.clone();
                        let arm_path = worktree.path.clone();
                        let delete_path = worktree.path.clone();
                        let path_label = worktree.path.clone();
                        let confirming =
                            pending_delete_path.as_deref() == Some(worktree.path.as_str());
                        let can_delete = protected_path.as_deref() != Some(worktree.path.as_str());
                        div()
                            .px_3()
                            .py_2()
                            .rounded_sm()
                            .border_1()
                            .border_color(theme::ink::firm())
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(
                                div()
                                    .flex_1()
                                    .hover(|element| element.bg(theme::ink::shade()))
                                    .child(format!("{}  {}", worktree.branch, path_label))
                                    .on_mouse_down(
                                        MouseButton::Left,
                                        cx.listener(move |this, _, _, cx| {
                                            this.submit_launcher_path(open_path.clone(), cx);
                                        }),
                                    ),
                            )
                            .when(can_delete && !confirming, |element| {
                                element.child(
                                    div()
                                        .text_size(px(11.))
                                        .text_color(theme::moon::dim())
                                        .child("Remove")
                                        .on_mouse_down(
                                            MouseButton::Left,
                                            cx.listener(move |this, _, _, cx| {
                                                if let Some(launcher) = this.launcher.as_mut() {
                                                    launcher.pending_delete_path =
                                                        Some(arm_path.clone());
                                                    launcher.create_worktree_input = None;
                                                }
                                                cx.notify();
                                            }),
                                        ),
                                )
                            })
                            .when(can_delete && confirming, |element| {
                                element
                                    .child(
                                        div()
                                            .text_size(px(11.))
                                            .text_color(theme::moon::secondary())
                                            .child("Confirm")
                                            .on_mouse_down(
                                                MouseButton::Left,
                                                cx.listener(move |this, _, _, cx| {
                                                    this.confirm_delete_worktree(
                                                        delete_path.clone(),
                                                        cx,
                                                    );
                                                }),
                                            ),
                                    )
                                    .child(
                                        div()
                                            .text_size(px(11.))
                                            .text_color(theme::moon::dim())
                                            .child("Cancel")
                                            .on_mouse_down(
                                                MouseButton::Left,
                                                cx.listener(move |this, _, _, cx| {
                                                    if let Some(launcher) = this.launcher.as_mut() {
                                                        launcher.pending_delete_path = None;
                                                    }
                                                    cx.notify();
                                                }),
                                            ),
                                    )
                            })
                    }))
                    .child(
                        div()
                            .text_size(px(11.))
                            .text_color(theme::moon::dim())
                            .child("ACTIONS"),
                    )
                    .when_some(create_input, |element, input| {
                        let start_label = if create_from_default {
                            format!("from origin/{}", repo.default_branch)
                        } else {
                            format!("from {}", repo.current_branch)
                        };
                        element.child(Input::new(&input).cleanable(true)).child(
                            div()
                                .flex()
                                .items_center()
                                .gap_3()
                                .child(
                                    div()
                                        .px_3()
                                        .py_2()
                                        .border_1()
                                        .border_color(theme::ink::firm())
                                        .child(start_label)
                                        .on_mouse_down(
                                            MouseButton::Left,
                                            cx.listener(move |this, _, _, cx| {
                                                if let Some(launcher) = this.launcher.as_mut() {
                                                    launcher.create_from_default =
                                                        !launcher.create_from_default;
                                                }
                                                cx.notify();
                                            }),
                                        ),
                                )
                                .child(
                                    div()
                                        .px_3()
                                        .py_2()
                                        .bg(theme::sodium::soft())
                                        .border_1()
                                        .border_color(theme::sodium::vapor())
                                        .child("Create and open")
                                        .on_mouse_down(
                                            MouseButton::Left,
                                            cx.listener(move |this, _, _, cx| {
                                                this.submit_create_worktree(cx);
                                            }),
                                        ),
                                ),
                        )
                    })
                    .when(!is_creating_worktree, |element| {
                        element.child(
                            div()
                                .px_3()
                                .py_2()
                                .border_1()
                                .border_color(theme::ink::firm())
                                .child("Create worktree...")
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |this, _, window, cx| {
                                        this.open_create_worktree(window, cx);
                                    }),
                                ),
                        )
                    })
            })
            .when(show_yolo, |element| {
                element.child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .text_size(px(12.))
                        .text_color(theme::moon::secondary())
                        .child(
                            div()
                                .px_3()
                                .py_2()
                                .rounded_sm()
                                .border_1()
                                .border_color(if yolo {
                                    theme::sodium::vapor()
                                } else {
                                    theme::ink::firm()
                                })
                                .child(if yolo { "YOLO on" } else { "YOLO off" })
                                .on_mouse_down(
                                    MouseButton::Left,
                                    cx.listener(move |this, _, _, cx| {
                                        this.toggle_launcher_yolo(cx);
                                    }),
                                ),
                        )
                        .child("Agent permissive mode"),
                )
            })
            .when_some(repository_status, |element, message| {
                element.child(
                    div()
                        .text_size(px(12.))
                        .text_color(theme::moon::secondary())
                        .child(message),
                )
            })
            .when_some(error, |element, message| {
                element.child(
                    div()
                        .text_size(px(12.))
                        .text_color(theme::session_state_color(
                            attn_protocol::SessionState::PendingApproval,
                        ))
                        .child(message),
                )
            })
            .when(
                repo_root_path.is_none() || launcher.repo_info.is_none(),
                |element| {
                    element.child(
                        div()
                            .flex()
                            .justify_end()
                            .gap_2()
                            .child(
                                div()
                                    .px_4()
                                    .py_2()
                                    .rounded_sm()
                                    .text_color(theme::moon::secondary())
                                    .child("Cancel")
                                    .on_mouse_down(
                                        MouseButton::Left,
                                        cx.listener(move |this, _, window, cx| {
                                            this.dismiss_launcher_layer_with_focus(window, cx);
                                        }),
                                    ),
                            )
                            .child(
                                div()
                                    .px_4()
                                    .py_2()
                                    .rounded_sm()
                                    .bg(theme::sodium::soft())
                                    .border_1()
                                    .border_color(theme::sodium::vapor())
                                    .text_color(theme::moon::primary())
                                    .child(if busy { "Working..." } else { "Open" })
                                    .on_mouse_down(
                                        MouseButton::Left,
                                        cx.listener(move |this, _, _, cx| {
                                            if !busy {
                                                this.inspect_launcher_path(cx);
                                            }
                                        }),
                                    ),
                            ),
                    )
                },
            );
        Some(
            div()
                .absolute()
                .inset_0()
                .bg(gpui::hsla(0., 0., 0., 0.62))
                .flex()
                .items_center()
                .justify_center()
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|this, _, window, cx| {
                        this.dismiss_launcher_layer_with_focus(window, cx);
                    }),
                )
                .child(dialog)
                .into_any_element(),
        )
    }

    fn render_pane_row(
        &self,
        layout: &WorkspaceLayout,
        pane: &WorkspaceLayoutPane,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let active = pane.pane_id == layout.active_pane_id;
        let session = pane
            .session_id
            .as_ref()
            .and_then(|session_id| self.store.sessions.get(session_id));
        let workspace_id = layout.workspace_id.clone();
        let pane_id = pane.pane_id.clone();
        let hover_pane_id = pane.pane_id.clone();
        let reveal_actions = self.hovered_pane_id.as_deref() == Some(pane.pane_id.as_str());
        let mut row = div()
            .ml_4()
            .mr_2()
            .px_3()
            .py_2()
            .flex()
            .items_center()
            .gap_2()
            .bg(if active {
                theme::ink::shade()
            } else {
                theme::ink::nocturne()
            })
            .rounded_sm()
            .id(SharedString::from(format!("pane-row-{}", pane.pane_id)))
            .text_size(px(13.))
            .text_color(if active {
                theme::moon::primary()
            } else {
                theme::moon::secondary()
            })
            .hover(|element| element.bg(theme::ink::shade()))
            .on_hover(cx.listener(move |this, hovered, _, cx| {
                if *hovered {
                    this.hovered_pane_id = Some(hover_pane_id.clone());
                } else if this.hovered_pane_id.as_deref() == Some(hover_pane_id.as_str()) {
                    this.hovered_pane_id = None;
                }
                cx.notify();
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |this, _, window, cx| {
                    this.focus_pane(workspace_id.clone(), pane_id.clone(), window, cx);
                }),
            )
            .child(div().flex_1().child(SharedString::from(pane.title.clone())));

        let closable = pane.pane_id != "main";
        if reveal_actions && (session.is_some() || closable) {
            let mut actions = div()
                .h(px(22.))
                .flex()
                .items_center()
                .justify_end()
                .gap_1()
                .overflow_hidden();
            if let Some(session) = session {
                let session_id = session.id.clone();
                actions = actions.child(
                    div()
                        .px_2()
                        .py_1()
                        .rounded_sm()
                        .bg(theme::ink::border())
                        .text_color(theme::moon::dim())
                        .child(if session.muted { "unmute" } else { "mute" })
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |this, _, _, cx| {
                                cx.stop_propagation();
                                let _ = this
                                    .daemon
                                    .read(cx)
                                    .send(&MuteMessage::new(session_id.clone()));
                            }),
                        ),
                );
            }
            if closable {
                let workspace_id = layout.workspace_id.clone();
                let pane_id = pane.pane_id.clone();
                actions = actions.child(
                    div()
                        .px_2()
                        .py_1()
                        .rounded_sm()
                        .bg(theme::ink::border())
                        .text_color(theme::moon::dim())
                        .child("x")
                        .on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |this, _, _, cx| {
                                cx.stop_propagation();
                                let _ = this.daemon.read(cx).send(
                                    &WorkspaceLayoutClosePaneMessage::new(
                                        workspace_id.clone(),
                                        pane_id.clone(),
                                    ),
                                );
                            }),
                        ),
                );
            }
            if let Some(session) = session {
                actions = actions.child(
                    div()
                        .ml_1()
                        .w(px(8.))
                        .h(px(8.))
                        .rounded_full()
                        .bg(theme::session_state_color(session.state)),
                );
            }
            let expanded_width = match (session.is_some(), closable) {
                (true, true) => 106.,
                (true, false) => 77.,
                (false, true) => 27.,
                (false, false) => 0.,
            };
            row = row.child(actions.with_animation(
                "pane-action-tray",
                Animation::new(Duration::from_millis(140)).with_easing(ease_in_out),
                move |element, delta| element.w(px(expanded_width * delta)).opacity(delta),
            ));
        } else if let Some(session) = session {
            row = row.child(
                div()
                    .w(px(8.))
                    .h(px(8.))
                    .rounded_full()
                    .bg(theme::session_state_color(session.state)),
            );
        }
        row.into_any_element()
    }

    fn render_layout(&self, node: &LayoutNode, layout: &WorkspaceLayout) -> AnyElement {
        match node.kind.as_str() {
            "split" if node.children.len() >= 2 => {
                let first = self.render_layout(&node.children[0], layout);
                let second = self.render_layout(&node.children[1], layout);
                let vertical =
                    node.direction == Some(attn_protocol::WorkspaceLayoutSplitDirection::Vertical);
                let ratio = node.ratio.unwrap_or(0.5).clamp(0.08, 0.92);
                let container = div()
                    .size_full()
                    .flex()
                    .when(!vertical, |element| element.flex_col())
                    .child(
                        div()
                            .flex_none()
                            .flex_basis(relative(ratio))
                            .overflow_hidden()
                            .child(first),
                    )
                    .child(
                        div()
                            .when(vertical, |element| element.w(px(1.)).h_full())
                            .when(!vertical, |element| element.h(px(1.)).w_full())
                            .bg(theme::ink::firm()),
                    )
                    .child(
                        div()
                            .flex_none()
                            .flex_basis(relative(1.0 - ratio))
                            .overflow_hidden()
                            .child(second),
                    );
                container.into_any_element()
            }
            "pane" => {
                let pane = node
                    .pane_id
                    .as_ref()
                    .and_then(|pane_id| layout.pane(pane_id));
                match pane.and_then(|pane| pane.runtime_id.as_ref()) {
                    Some(runtime_id) => self
                        .terminal_views
                        .get(runtime_id)
                        .cloned()
                        .map(|terminal| terminal.into_any_element())
                        .unwrap_or_else(|| self.empty_pane("Attaching terminal")),
                    None => self.empty_pane("Pane runtime unavailable"),
                }
            }
            _ => self.empty_pane("Invalid workspace layout"),
        }
    }

    fn empty_pane(&self, message: &str) -> AnyElement {
        div()
            .size_full()
            .flex()
            .items_center()
            .justify_center()
            .bg(theme::ink::midnight())
            .text_color(theme::moon::dim())
            .child(SharedString::from(message.to_string()))
            .into_any_element()
    }
}

fn server_event_kind(event: &ServerEvent) -> &'static str {
    match event {
        ServerEvent::InitialState(_) => "initial_state",
        ServerEvent::SessionRegistered(_) => "session_registered",
        ServerEvent::SessionUnregistered(_) => "session_unregistered",
        ServerEvent::SessionStateChanged(_) => "session_state_changed",
        ServerEvent::SessionTodosUpdated(_) => "session_todos_updated",
        ServerEvent::SessionsUpdated(_) => "sessions_updated",
        ServerEvent::WorkspaceRegistered(_) => "workspace_registered",
        ServerEvent::WorkspaceUnregistered(_) => "workspace_unregistered",
        ServerEvent::WorkspaceStateChanged(_) => "workspace_state_changed",
        ServerEvent::WorkspaceLayout(_) => "workspace_layout",
        ServerEvent::WorkspaceLayoutUpdated(_) => "workspace_layout_updated",
        ServerEvent::WorkspaceLayoutActionResult(_) => "workspace_layout_action_result",
        ServerEvent::BootstrapWorkspaceResult(_) => "bootstrap_workspace_result",
        ServerEvent::SpawnResult(_) => "spawn_result",
        ServerEvent::RecentLocationsResult(_) => "recent_locations_result",
        ServerEvent::BrowseDirectoryResult(_) => "browse_directory_result",
        ServerEvent::InspectPathResult(_) => "inspect_path_result",
        ServerEvent::SettingsUpdated(_) => "settings_updated",
        ServerEvent::GetRepoInfoResult(_) => "get_repo_info_result",
        ServerEvent::CreateWorktreeResult(_) => "create_worktree_result",
        ServerEvent::DeleteWorktreeResult(_) => "delete_worktree_result",
        ServerEvent::AttachResult(_) => "attach_result",
        ServerEvent::PtyOutput(_) => "pty_output",
        ServerEvent::PtyDesync(_) => "pty_desync",
        ServerEvent::PtyResized(_) => "pty_resized",
        ServerEvent::SessionExited(_) => "session_exited",
        ServerEvent::Unknown(_) => "unknown",
    }
}

fn start_automation(cx: &mut Context<NativeApp>) -> Option<automation::server::Handle> {
    let listener = match automation::server::bind() {
        Ok(listener) => listener,
        Err(error) => {
            eprintln!("[native automation] bind failed: {error}");
            return None;
        }
    };
    let (dispatcher, receiver) = automation::actions::make_dispatcher();
    let executor = cx.background_executor().clone();
    let spawner: automation::server::Spawner = Arc::new(move |future| {
        executor.spawn(future).detach();
    });
    let handle =
        match automation::server::start(listener, automation::manifest_path(), dispatcher, spawner)
        {
            Ok(handle) => handle,
            Err(error) => {
                eprintln!("[native automation] start failed: {error}");
                return None;
            }
        };
    let app = cx.entity().downgrade();
    cx.spawn(async move |_, cx| {
        automation::actions::pump_actions(receiver, app, cx.clone()).await;
    })
    .detach();
    eprintln!(
        "[native automation] listening; manifest={}",
        handle.manifest_path().display()
    );
    Some(handle)
}

impl Render for NativeApp {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let main = match self.visible_layout() {
            Some(layout) => match layout.root() {
                Ok(root) => self.render_layout(&root, layout),
                Err(_) => self.empty_pane("Cannot parse daemon layout"),
            },
            None => self.empty_pane("Waiting for workspace layout"),
        };
        let status = self.connection_error.clone().map(|message| {
            div()
                .absolute()
                .bottom_4()
                .left(px(286.))
                .px_3()
                .py_2()
                .rounded_sm()
                .bg(theme::ink::shade())
                .text_color(theme::moon::secondary())
                .child(message)
        });
        let launcher = self.render_launcher(cx);
        div()
            .size_full()
            .relative()
            .flex()
            .bg(theme::ink::midnight())
            .text_color(theme::moon::primary())
            .on_action(cx.listener(Self::previous_pane))
            .on_action(cx.listener(Self::next_pane))
            .on_action(cx.listener(Self::add_pane_vertical))
            .on_action(cx.listener(Self::add_pane_horizontal))
            .on_action(cx.listener(Self::split_shell_vertical))
            .on_action(cx.listener(Self::split_shell_horizontal))
            .on_action(cx.listener(Self::close_launcher))
            .child(self.render_sidebar(cx))
            .child(div().flex_1().overflow_hidden().child(main))
            .children(status)
            .children(launcher)
    }
}

fn pane_choice_wire(choice: PaneChoice) -> (WorkspaceLayoutPaneKind, Option<String>) {
    match choice {
        PaneChoice::Shell => (WorkspaceLayoutPaneKind::Shell, Some("shell".into())),
        PaneChoice::Agent(agent) => (WorkspaceLayoutPaneKind::Agent, Some(agent.to_string())),
    }
}

fn setting_is_true(value: Option<&String>) -> bool {
    value.is_some_and(|value| value.eq_ignore_ascii_case("true"))
}

fn launcher_agent_available(agent: SessionAgent, settings: &attn_protocol::SettingsMap) -> bool {
    if agent == SessionAgent::Shell {
        return true;
    }
    let key = format!("{agent}_available");
    match settings.get(&key) {
        Some(value) => value.eq_ignore_ascii_case("true"),
        None => agent != SessionAgent::Pi,
    }
}

fn launcher_choice_available(choice: PaneChoice, settings: &attn_protocol::SettingsMap) -> bool {
    match choice {
        PaneChoice::Shell => true,
        PaneChoice::Agent(agent) => launcher_agent_available(agent, settings),
    }
}

fn launcher_choice_yolo_supported(
    choice: PaneChoice,
    settings: &attn_protocol::SettingsMap,
) -> bool {
    match choice {
        PaneChoice::Shell => false,
        PaneChoice::Agent(agent) => setting_is_true(settings.get(&format!("{agent}_cap_yolo"))),
    }
}

fn launcher_initial_choice(settings: &attn_protocol::SettingsMap) -> PaneChoice {
    if settings
        .get("native_launcher_pane_choice")
        .is_some_and(|choice| choice == "shell")
    {
        return PaneChoice::Shell;
    }
    let preferred = match settings.get("new_session_agent").map(String::as_str) {
        Some("codex") => SessionAgent::Codex,
        Some("copilot") => SessionAgent::Copilot,
        Some("pi") => SessionAgent::Pi,
        _ => SessionAgent::Claude,
    };
    if launcher_agent_available(preferred, settings) {
        PaneChoice::Agent(preferred)
    } else {
        [
            SessionAgent::Claude,
            SessionAgent::Codex,
            SessionAgent::Copilot,
            SessionAgent::Pi,
        ]
        .into_iter()
        .find(|agent| launcher_agent_available(*agent, settings))
        .map(PaneChoice::Agent)
        .unwrap_or(PaneChoice::Shell)
    }
}

fn launcher_initial_yolo(settings: &attn_protocol::SettingsMap) -> bool {
    setting_is_true(settings.get("new_session_yolo"))
}

fn new_native_id(prefix: &str) -> String {
    let mut bytes = [0u8; 8];
    getrandom::getrandom(&mut bytes).expect("native launcher id randomness unavailable");
    let suffix = bytes
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    format!("{prefix}-{suffix}")
}

#[cfg(test)]
mod tests {
    use super::{
        launcher_choice_yolo_supported, launcher_initial_choice, pane_choice_wire, LauncherMode,
        NativeApp, PaneChoice, RepositoryOperation,
    };
    use crate::adapters::daemon::DaemonClient;
    use async_channel::{unbounded, Receiver};
    use attn_protocol::{
        RepoInfo, SessionAgent, SettingsMap, Workspace, WorkspaceLayout, WorkspaceLayoutPane,
        WorkspaceLayoutPaneKind, WorkspaceLayoutSplitDirection, WorkspaceStatus, Worktree,
    };
    use gpui::{AppContext, Entity, TestAppContext};
    use serde_json::Value;

    fn build_test_app(cx: &mut TestAppContext) -> (Entity<NativeApp>, Receiver<String>) {
        let (commands, receiver) = unbounded();
        let daemon = cx.update(|cx| cx.new(|_| DaemonClient::connected_for_test(commands)));
        let app = cx.update(|cx| cx.new(|cx| NativeApp::new(daemon, cx)));
        (app, receiver)
    }

    fn drain_commands(receiver: &Receiver<String>) -> Vec<Value> {
        let mut messages = Vec::new();
        while let Ok(message) = receiver.try_recv() {
            messages.push(serde_json::from_str(&message).expect("parse captured command"));
        }
        messages
    }

    fn selected_workspace() -> Workspace {
        Workspace {
            id: "workspace-1".into(),
            title: "Workspace".into(),
            directory: "/tmp/workspace".into(),
            status: WorkspaceStatus::Idle,
            layout: None,
        }
    }

    fn selected_layout() -> WorkspaceLayout {
        WorkspaceLayout {
            workspace_id: "workspace-1".into(),
            active_pane_id: "main".into(),
            layout_json: r#"{"type":"pane","pane_id":"main"}"#.into(),
            panes: vec![WorkspaceLayoutPane {
                pane_id: "main".into(),
                runtime_id: Some("runtime-1".into()),
                workspace_id: Some("workspace-1".into()),
                session_id: Some("session-1".into()),
                kind: WorkspaceLayoutPaneKind::Agent,
                title: "Claude".into(),
            }],
            updated_at: None,
        }
    }

    fn repo_info() -> RepoInfo {
        RepoInfo {
            repo: "/tmp/workspace".into(),
            current_branch: "main".into(),
            current_commit_hash: "abcdef0".into(),
            current_commit_time: String::new(),
            default_branch: "main".into(),
            worktrees: vec![Worktree {
                path: "/tmp/workspace--feature".into(),
                branch: "feature".into(),
                main_repo: "/tmp/workspace".into(),
            }],
            branches: Vec::new(),
            fetched_at: None,
        }
    }

    #[test]
    fn pane_choice_keeps_shell_distinct_from_agent() {
        let (shell_kind, shell_agent) = pane_choice_wire(PaneChoice::Shell);
        let (agent_kind, agent) = pane_choice_wire(PaneChoice::Agent(SessionAgent::Codex));

        assert_eq!(shell_kind, WorkspaceLayoutPaneKind::Shell);
        assert_eq!(shell_agent.as_deref(), Some("shell"));
        assert_eq!(agent_kind, WorkspaceLayoutPaneKind::Agent);
        assert_eq!(agent.as_deref(), Some("codex"));
    }

    #[test]
    fn launcher_preserves_terminal_choice_without_agent_availability() {
        let mut settings = SettingsMap::new();
        settings.insert("native_launcher_pane_choice".into(), "shell".into());
        settings.insert("claude_available".into(), "false".into());
        settings.insert("codex_available".into(), "false".into());
        settings.insert("copilot_available".into(), "false".into());
        settings.insert("pi_available".into(), "false".into());

        assert!(matches!(
            launcher_initial_choice(&settings),
            PaneChoice::Shell
        ));
    }

    #[test]
    fn launcher_falls_back_from_unavailable_saved_agent() {
        let mut settings = SettingsMap::new();
        settings.insert("new_session_agent".into(), "claude".into());
        settings.insert("claude_available".into(), "false".into());
        settings.insert("codex_available".into(), "true".into());

        assert!(matches!(
            launcher_initial_choice(&settings),
            PaneChoice::Agent(SessionAgent::Codex)
        ));
    }

    #[test]
    fn launcher_only_shows_yolo_for_capable_agents() {
        let mut settings = SettingsMap::new();
        settings.insert("codex_cap_yolo".into(), "true".into());

        assert!(launcher_choice_yolo_supported(
            PaneChoice::Agent(SessionAgent::Codex),
            &settings
        ));
        assert!(!launcher_choice_yolo_supported(
            PaneChoice::Agent(SessionAgent::Claude),
            &settings
        ));
        assert!(!launcher_choice_yolo_supported(
            PaneChoice::Shell,
            &settings
        ));
    }

    fn verify_new_workspace_dialog_uses_saved_defaults_and_routes_atomic_bootstrap(
        cx: &mut TestAppContext,
    ) {
        let (app, receiver) = build_test_app(cx);
        app.update(cx, |app, cx| {
            app.store
                .settings
                .insert("projects_directory".into(), "/projects".into());
            app.store
                .settings
                .insert("new_session_agent".into(), "codex".into());
            app.store
                .settings
                .insert("codex_available".into(), "true".into());
            app.store
                .settings
                .insert("new_session_yolo".into(), "true".into());
            app.automation_open_new_workspace(cx);

            let launcher = app.launcher.as_ref().expect("launcher open");
            assert!(matches!(launcher.mode, LauncherMode::NewWorkspace));
            assert_eq!(launcher.path_value, "/projects");
            assert_eq!(launcher.choice, PaneChoice::Agent(SessionAgent::Codex));
            assert!(launcher.yolo_mode);

            app.submit_launcher_path("/projects/repo".into(), cx);
        });

        let bootstrap = drain_commands(&receiver)
            .into_iter()
            .find(|message| message["cmd"] == "bootstrap_workspace")
            .expect("bootstrap command");
        assert_eq!(bootstrap["directory"], "/projects/repo");
        assert_eq!(bootstrap["initial_session"]["kind"], "agent");
        assert_eq!(bootstrap["initial_session"]["agent"], "codex");
        assert_eq!(bootstrap["initial_session"]["yolo_mode"], true);
    }

    fn verify_add_pane_dialog_routes_terminal_and_agent_choices_to_requested_split(
        cx: &mut TestAppContext,
    ) {
        let (app, receiver) = build_test_app(cx);
        app.update(cx, |app, cx| {
            app.store.upsert_workspace(selected_workspace());
            app.store.set_layout(selected_layout());
            app.selected_workspace_id = Some("workspace-1".into());
            app.automation_open_add_pane(WorkspaceLayoutSplitDirection::Horizontal, cx);

            let launcher = app.launcher.as_ref().expect("terminal launcher open");
            assert_eq!(launcher.path_value, "/tmp/workspace");
            assert!(matches!(
                launcher.mode,
                LauncherMode::AddPane {
                    direction: WorkspaceLayoutSplitDirection::Horizontal,
                    ..
                }
            ));
            app.launcher
                .as_mut()
                .expect("terminal launcher open")
                .choice = PaneChoice::Shell;
            app.submit_launcher_path("/tmp/edited-shell".into(), cx);
        });

        let split = drain_commands(&receiver)
            .into_iter()
            .find(|message| message["cmd"] == "workspace_layout_split_pane")
            .expect("shell split command");
        assert_eq!(split["workspace_id"], "workspace-1");
        assert_eq!(split["target_pane_id"], "main");
        assert_eq!(split["direction"], "horizontal");
        assert_eq!(split["cwd"], "/tmp/edited-shell");

        app.update(cx, |app, cx| {
            app.launcher = None;
            app.store
                .settings
                .insert("codex_available".into(), "true".into());
            app.automation_open_add_pane(WorkspaceLayoutSplitDirection::Vertical, cx);
            app.launcher.as_mut().expect("agent launcher open").choice =
                PaneChoice::Agent(SessionAgent::Codex);
            app.submit_launcher_path("/tmp/agent-path".into(), cx);
        });

        let spawn = drain_commands(&receiver)
            .into_iter()
            .find(|message| message["cmd"] == "spawn_session")
            .expect("agent spawn command");
        assert_eq!(spawn["workspace_id"], "workspace-1");
        assert_eq!(spawn["target_pane_id"], "main");
        assert_eq!(spawn["direction"], "vertical");
        assert_eq!(spawn["cwd"], "/tmp/agent-path");
        assert_eq!(spawn["agent"], "codex");
    }

    fn verify_repository_dialog_cancel_unwinds_delete_confirmation_then_stage_then_modal(
        cx: &mut TestAppContext,
    ) {
        let (app, _) = build_test_app(cx);
        app.update(cx, |app, cx| {
            app.automation_open_new_workspace(cx);
            let launcher = app.launcher.as_mut().expect("launcher open");
            launcher.repo_root_path = Some("/tmp/workspace".into());
            launcher.repo_info = Some(repo_info());
            launcher.repository_operation = Some(RepositoryOperation::Refreshing);
            launcher.pending_delete_path = Some("/tmp/workspace--feature".into());

            app.automation_cancel_launcher(cx);
            let launcher = app.launcher.as_ref().expect("delete cancelled only");
            assert!(launcher.pending_delete_path.is_none());
            assert!(launcher.repo_info.is_some());

            app.automation_cancel_launcher(cx);
            let launcher = app.launcher.as_ref().expect("returned to path stage");
            assert!(launcher.repo_info.is_none());
            assert!(launcher.repo_root_path.is_none());

            app.automation_cancel_launcher(cx);
            assert!(app.launcher.is_none());
        });
    }

    #[gpui::test]
    fn launcher_dialog_workflows_route_commands_and_unwind_cancellation(cx: &mut TestAppContext) {
        verify_new_workspace_dialog_uses_saved_defaults_and_routes_atomic_bootstrap(cx);
        verify_add_pane_dialog_routes_terminal_and_agent_choices_to_requested_split(cx);
        verify_repository_dialog_cancel_unwinds_delete_confirmation_then_stage_then_modal(cx);
    }
}
