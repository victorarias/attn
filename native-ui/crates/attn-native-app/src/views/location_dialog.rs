use attn_protocol::{
    BrowseDirectoryMessage, BrowseDirectoryResultMessage, CreateWorktreeMessage,
    CreateWorktreeResultMessage, DirectoryEntry, GetRepoInfoMessage, GetRepoInfoResultMessage,
    InspectPathMessage, InspectPathResultMessage, RepoInfo,
};
use gpui::{
    div, hsla, point, prelude::*, px, BoxShadow, Context, Entity, FocusHandle, Focusable,
    FontWeight, KeyDownEvent, MouseButton, ParentElement, Render, SharedString, Window,
};

use crate::adapters::automation::events;
use crate::adapters::daemon::{DaemonClient, DaemonEvent};
use crate::theme;

#[derive(Clone, Debug)]
pub enum LocationDialogMode {
    NewSession {
        workspace_id: SharedString,
        initial_directory: SharedString,
        initial_agent: SharedString,
    },
    NewWorkspace {
        initial_directory: Option<SharedString>,
    },
}

#[derive(Clone, Debug)]
pub enum LocationDialogOutcome {
    SpawnSession {
        workspace_id: SharedString,
        directory: String,
        agent: SharedString,
    },
    RegisterWorkspace {
        directory: String,
    },
}

type SubmitHandler = dyn Fn(LocationDialogOutcome, &mut gpui::App) -> Result<(), String> + 'static;
type CloseHandler = dyn Fn(&mut gpui::App) + 'static;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum DialogStep {
    Path,
    Repo,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum WorktreeBase {
    Current,
    Default,
}

pub struct LocationDialog {
    mode: LocationDialogMode,
    daemon: Entity<DaemonClient>,
    focus_handle: FocusHandle,
    step: DialogStep,
    input: String,
    directory: String,
    entries: Vec<DirectoryEntry>,
    selected_entry: usize,
    home_path: Option<String>,
    selected_path: Option<String>,
    repo_info: Option<RepoInfo>,
    repo_focus: usize,
    new_worktree_open: bool,
    new_worktree_name: String,
    worktree_base: WorktreeBase,
    agent: SharedString,
    request_seq: u64,
    active_browse_request: Option<String>,
    active_inspect_request: Option<String>,
    creating_worktree: bool,
    loading_label: Option<SharedString>,
    error: Option<SharedString>,
    on_submit: Box<SubmitHandler>,
    on_close: Box<CloseHandler>,
}

impl LocationDialog {
    pub fn new(
        mode: LocationDialogMode,
        daemon: Entity<DaemonClient>,
        on_submit: impl Fn(LocationDialogOutcome, &mut gpui::App) -> Result<(), String> + 'static,
        on_close: impl Fn(&mut gpui::App) + 'static,
        cx: &mut Context<Self>,
    ) -> Self {
        let (input, agent) = match &mode {
            LocationDialogMode::NewSession {
                initial_directory,
                initial_agent,
                ..
            } => (initial_directory.to_string(), initial_agent.clone()),
            LocationDialogMode::NewWorkspace { initial_directory } => (
                initial_directory
                    .as_ref()
                    .map(|s| s.to_string())
                    .unwrap_or_else(|| "~".to_string()),
                SharedString::from("claude"),
            ),
        };

        let mut dialog = Self {
            mode,
            daemon,
            focus_handle: cx.focus_handle(),
            step: DialogStep::Path,
            input,
            directory: String::new(),
            entries: Vec::new(),
            selected_entry: 0,
            home_path: None,
            selected_path: None,
            repo_info: None,
            repo_focus: 0,
            new_worktree_open: false,
            new_worktree_name: String::new(),
            worktree_base: WorktreeBase::Current,
            agent,
            request_seq: 0,
            active_browse_request: None,
            active_inspect_request: None,
            creating_worktree: false,
            loading_label: None,
            error: None,
            on_submit: Box::new(on_submit),
            on_close: Box::new(on_close),
        };
        dialog.request_browse(cx);
        dialog
    }

    pub fn focus(&self, window: &mut Window) {
        self.focus_handle.clone().focus(window);
    }

    pub fn handle_daemon_event(&mut self, event: &DaemonEvent, cx: &mut Context<Self>) {
        match event {
            DaemonEvent::BrowseDirectoryResult(msg) => self.apply_browse_result(msg, cx),
            DaemonEvent::InspectPathResult(msg) => self.apply_inspect_result(msg, cx),
            DaemonEvent::GetRepoInfoResult(msg) => self.apply_repo_info_result(msg, cx),
            DaemonEvent::CreateWorktreeResult(msg) => self.apply_create_worktree_result(msg, cx),
            _ => {}
        }
    }

    fn next_request_id(&mut self, prefix: &str) -> String {
        self.request_seq += 1;
        format!("native-{prefix}-{}", self.request_seq)
    }

    fn request_browse(&mut self, cx: &mut Context<Self>) {
        let request_id = self.next_request_id("browse");
        let msg = BrowseDirectoryMessage::new(self.input.clone(), request_id.clone());
        match self.daemon.read(cx).send_cmd(&msg) {
            Ok(()) => {
                self.active_browse_request = Some(request_id);
            }
            Err(error) => {
                self.error = Some(SharedString::from(error));
            }
        }
    }

    fn request_inspect(&mut self, path: String, cx: &mut Context<Self>) {
        let request_id = self.next_request_id("inspect");
        let msg = InspectPathMessage::new(path, request_id.clone());
        match self.daemon.read(cx).send_cmd(&msg) {
            Ok(()) => {
                self.active_inspect_request = Some(request_id);
                self.loading_label = Some(SharedString::from("Inspecting path"));
                self.error = None;
            }
            Err(error) => {
                self.error = Some(SharedString::from(error));
            }
        }
    }

    fn request_repo_info(&mut self, repo: String, cx: &mut Context<Self>) {
        let msg = GetRepoInfoMessage::new(repo);
        match self.daemon.read(cx).send_cmd(&msg) {
            Ok(()) => {
                self.loading_label = Some(SharedString::from("Reading repository"));
                self.error = None;
            }
            Err(error) => {
                self.loading_label = None;
                self.error = Some(SharedString::from(error));
            }
        }
    }

    fn request_create_worktree(&mut self, cx: &mut Context<Self>) {
        let Some(repo) = self.repo_info.as_ref() else {
            return;
        };
        let branch = self.new_worktree_name.trim().to_string();
        if branch.is_empty() || self.creating_worktree {
            return;
        }
        let starting_from = match self.worktree_base {
            WorktreeBase::Current => selected_destination_branch(repo, self.repo_focus),
            WorktreeBase::Default => format!("origin/{}", repo.default_branch),
        };
        let msg = CreateWorktreeMessage::new(repo.repo.clone(), branch.clone(), starting_from);
        match self.daemon.read(cx).send_cmd(&msg) {
            Ok(()) => {
                self.creating_worktree = true;
                self.loading_label = Some(SharedString::from("Creating worktree"));
                self.error = None;
                events::record(
                    "native_location_worktree_create_submitted",
                    serde_json::json!({
                        "repo": repo.repo.as_str(),
                        "branch": branch.as_str(),
                    }),
                );
            }
            Err(error) => {
                self.error = Some(SharedString::from(error));
            }
        }
    }

    fn apply_browse_result(&mut self, msg: &BrowseDirectoryResultMessage, cx: &mut Context<Self>) {
        if msg.request_id.as_ref() != self.active_browse_request.as_ref() {
            return;
        }
        self.active_browse_request = None;
        if let Some(home) = &msg.home_path {
            self.home_path = Some(home.clone());
        }
        if msg.success {
            self.directory = msg.directory.clone();
            self.entries = msg.entries.clone();
            self.selected_entry = self
                .selected_entry
                .min(self.entries.len().saturating_sub(1));
            if self.entries.is_empty() {
                self.selected_entry = 0;
            }
        } else {
            self.entries.clear();
            self.error = msg
                .error
                .as_ref()
                .map(|error| SharedString::from(error.clone()));
        }
        cx.notify();
    }

    fn apply_inspect_result(&mut self, msg: &InspectPathResultMessage, cx: &mut Context<Self>) {
        if msg.request_id.as_ref() != self.active_inspect_request.as_ref() {
            return;
        }
        self.active_inspect_request = None;
        self.loading_label = None;
        if !msg.success {
            self.error = Some(SharedString::from(
                msg.error
                    .clone()
                    .unwrap_or_else(|| "Path inspection failed".to_string()),
            ));
            cx.notify();
            return;
        }

        let Some(inspection) = msg.inspection.as_ref() else {
            self.error = Some(SharedString::from("No path inspection returned"));
            cx.notify();
            return;
        };
        if let Some(home) = &inspection.home_path {
            self.home_path = Some(home.clone());
        }
        if !inspection.exists || !inspection.is_directory {
            self.error = Some(SharedString::from(format!(
                "Directory not found: {}",
                inspection.input_path
            )));
            cx.notify();
            return;
        }

        self.selected_path = Some(inspection.resolved_path.clone());
        if let Some(repo_root) = &inspection.repo_root {
            self.request_repo_info(repo_root.clone(), cx);
        } else {
            self.submit_path(inspection.resolved_path.clone(), cx);
        }
        cx.notify();
    }

    fn apply_repo_info_result(&mut self, msg: &GetRepoInfoResultMessage, cx: &mut Context<Self>) {
        self.loading_label = None;
        if msg.success {
            self.repo_info = msg.info.clone();
            self.step = DialogStep::Repo;
            self.repo_focus =
                selected_repo_index(self.repo_info.as_ref(), self.selected_path.as_deref());
            self.new_worktree_open = false;
            self.new_worktree_name.clear();
        } else {
            self.error =
                Some(SharedString::from(msg.error.clone().unwrap_or_else(|| {
                    "Repository inspection failed".to_string()
                })));
        }
        cx.notify();
    }

    fn apply_create_worktree_result(
        &mut self,
        msg: &CreateWorktreeResultMessage,
        cx: &mut Context<Self>,
    ) {
        if !self.creating_worktree {
            return;
        }
        self.creating_worktree = false;
        self.loading_label = None;
        if msg.success {
            if let Some(path) = &msg.path {
                self.submit_path(path.clone(), cx);
            }
        } else {
            self.error = Some(SharedString::from(
                msg.error
                    .clone()
                    .unwrap_or_else(|| "Worktree creation failed".to_string()),
            ));
        }
        cx.notify();
    }

    fn submit_path(&mut self, path: String, cx: &mut Context<Self>) {
        let outcome = match &self.mode {
            LocationDialogMode::NewSession { workspace_id, .. } => {
                LocationDialogOutcome::SpawnSession {
                    workspace_id: workspace_id.clone(),
                    directory: path,
                    agent: self.agent.clone(),
                }
            }
            LocationDialogMode::NewWorkspace { .. } => {
                LocationDialogOutcome::RegisterWorkspace { directory: path }
            }
        };

        match (self.on_submit)(outcome, cx) {
            Ok(()) => {
                (self.on_close)(cx);
            }
            Err(error) => {
                self.error = Some(SharedString::from(error));
            }
        }
    }

    fn close(&mut self, cx: &mut Context<Self>) {
        (self.on_close)(cx);
    }

    fn set_input(&mut self, next: String, cx: &mut Context<Self>) {
        self.input = next;
        self.step = DialogStep::Path;
        self.repo_info = None;
        self.selected_path = None;
        self.error = None;
        self.request_browse(cx);
        cx.notify();
    }

    fn activate_path_selection(&mut self, cx: &mut Context<Self>) {
        let path = self
            .entries
            .get(self.selected_entry)
            .map(|entry| entry.path.clone())
            .unwrap_or_else(|| self.input.trim().to_string());
        if path.trim().is_empty() {
            return;
        }
        self.request_inspect(path, cx);
    }

    fn activate_repo_selection(&mut self, cx: &mut Context<Self>) {
        if self.new_worktree_open {
            self.request_create_worktree(cx);
            return;
        }
        let Some(repo) = self.repo_info.as_ref() else {
            return;
        };
        if self.repo_focus == repo.worktrees.len() + 1 {
            self.new_worktree_open = true;
            self.new_worktree_name.clear();
            cx.notify();
            return;
        }
        let path = if self.repo_focus == 0 {
            repo.repo.clone()
        } else {
            repo.worktrees
                .get(self.repo_focus - 1)
                .map(|worktree| worktree.path.clone())
                .unwrap_or_else(|| repo.repo.clone())
        };
        self.submit_path(path, cx);
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, _window: &mut Window, cx: &mut Context<Self>) {
        cx.stop_propagation();
        match self.step {
            DialogStep::Path => self.on_path_key_down(event, cx),
            DialogStep::Repo => self.on_repo_key_down(event, cx),
        }
    }

    fn on_path_key_down(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let key = event.keystroke.key.as_str();
        match key {
            "escape" => self.close(cx),
            "enter" => self.activate_path_selection(cx),
            "up" => {
                if !self.entries.is_empty() {
                    self.selected_entry = self.selected_entry.saturating_sub(1);
                    cx.notify();
                }
            }
            "down" => {
                if !self.entries.is_empty() {
                    self.selected_entry = (self.selected_entry + 1).min(self.entries.len() - 1);
                    cx.notify();
                }
            }
            "tab" => {
                if let Some(entry) = self.entries.get(self.selected_entry) {
                    self.set_input(entry.path.clone(), cx);
                }
            }
            "backspace" => {
                let mut next = self.input.clone();
                next.pop();
                self.set_input(next, cx);
            }
            _ => {
                if event.keystroke.modifiers.platform {
                    self.apply_platform_shortcut(key, cx);
                    return;
                }
                if let Some(key_char) = &event.keystroke.key_char {
                    if !event.keystroke.modifiers.control
                        && !event.keystroke.modifiers.alt
                        && !key_char.is_empty()
                    {
                        let mut next = self.input.clone();
                        next.push_str(key_char);
                        self.set_input(next, cx);
                    }
                }
            }
        }
    }

    fn on_repo_key_down(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        let key = event.keystroke.key.as_str();
        if self.new_worktree_open {
            match key {
                "escape" => {
                    self.new_worktree_open = false;
                    self.new_worktree_name.clear();
                    cx.notify();
                }
                "enter" => self.request_create_worktree(cx),
                "tab" => {
                    self.worktree_base = match self.worktree_base {
                        WorktreeBase::Current => WorktreeBase::Default,
                        WorktreeBase::Default => WorktreeBase::Current,
                    };
                    cx.notify();
                }
                "backspace" => {
                    self.new_worktree_name.pop();
                    cx.notify();
                }
                _ => {
                    if let Some(key_char) = &event.keystroke.key_char {
                        if !event.keystroke.modifiers.control
                            && !event.keystroke.modifiers.alt
                            && !key_char.is_empty()
                        {
                            self.new_worktree_name.push_str(key_char);
                            cx.notify();
                        }
                    }
                }
            }
            return;
        }

        let item_count = self
            .repo_info
            .as_ref()
            .map(|repo| repo.worktrees.len() + 2)
            .unwrap_or(0);
        match key {
            "escape" => {
                self.step = DialogStep::Path;
                self.repo_info = None;
                self.error = None;
                cx.notify();
            }
            "enter" => self.activate_repo_selection(cx),
            "up" => {
                self.repo_focus = self.repo_focus.saturating_sub(1);
                cx.notify();
            }
            "down" => {
                if item_count > 0 {
                    self.repo_focus = (self.repo_focus + 1).min(item_count - 1);
                    cx.notify();
                }
            }
            "n" => {
                if !event.keystroke.modifiers.platform {
                    self.new_worktree_open = true;
                    self.new_worktree_name.clear();
                    if item_count > 0 {
                        self.repo_focus = item_count - 1;
                    }
                    cx.notify();
                }
            }
            _ => {
                if event.keystroke.modifiers.platform {
                    self.apply_platform_shortcut(key, cx);
                }
            }
        }
    }

    fn apply_platform_shortcut(&mut self, key: &str, cx: &mut Context<Self>) {
        if !self.is_session_mode() {
            return;
        }
        if let Some(agent) = match key {
            "1" => Some("claude"),
            "2" => Some("codex"),
            "3" => Some("shell"),
            _ => None,
        } {
            self.agent = SharedString::from(agent);
            cx.notify();
        }
    }

    fn is_session_mode(&self) -> bool {
        matches!(self.mode, LocationDialogMode::NewSession { .. })
    }
}

impl Focusable for LocationDialog {
    fn focus_handle(&self, _cx: &gpui::App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for LocationDialog {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.focus(window);
        let (eyebrow, title) = match self.mode {
            LocationDialogMode::NewSession { .. } => ("New session", "Open a new observation"),
            LocationDialogMode::NewWorkspace { .. } => ("New workspace", "Bind a working directory"),
        };

        let mut panel = div()
            .w(px(660.0))
            .max_h(px(640.0))
            .rounded(px(theme::radius::R2))
            .bg(theme::ink::nocturne())
            .border_1()
            .border_color(theme::line::firm())
            .shadow(vec![
                BoxShadow {
                    color: hsla(0.0, 0.0, 0.0, 0.55),
                    offset: point(px(0.0), px(28.0)),
                    blur_radius: px(64.0),
                    spread_radius: px(-8.0),
                },
                BoxShadow {
                    color: hsla(0.0, 0.0, 0.0, 0.40),
                    offset: point(px(0.0), px(2.0)),
                    blur_radius: px(8.0),
                    spread_radius: px(0.0),
                },
            ])
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .child(dialog_header(eyebrow, title, self.step));

        if self.is_session_mode() {
            panel = panel.child(self.render_agent_picker(cx));
        }

        panel = match self.step {
            DialogStep::Path => panel.child(self.render_path_step(cx)),
            DialogStep::Repo => panel.child(self.render_repo_step(cx)),
        };

        panel = panel.child(self.render_status_footer());

        div()
            .absolute()
            .size_full()
            .bg(theme::ink::veil())
            .flex()
            .items_center()
            .justify_center()
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, _, cx| this.close(cx)),
            )
            .child(panel.on_mouse_down(
                MouseButton::Left,
                cx.listener(|_, _, _, cx| cx.stop_propagation()),
            ))
    }
}

impl LocationDialog {
    fn render_agent_picker(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let mut row = div()
            .px_5()
            .pt_4()
            .pb_4()
            .flex()
            .gap_2p5()
            .border_b_1()
            .border_color(theme::line::weak());

        for spec in agent_specs() {
            let selected = self.agent.as_ref() == spec.id;
            let id = spec.id;
            row = row.child(agent_tile(spec, selected).on_mouse_down(
                MouseButton::Left,
                cx.listener(move |this, _, _, cx| {
                    cx.stop_propagation();
                    this.agent = SharedString::from(id);
                    cx.notify();
                }),
            ));
        }
        row
    }

    fn render_path_step(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let entries: Vec<_> = self.entries.iter().take(10).cloned().collect();
        let entries_len = entries.len();
        let selected_entry = self.selected_entry;

        let mut results = div().max_h(px(280.0)).overflow_hidden().flex().flex_col();
        if entries_len == 0 {
            results = results.child(empty_state(
                "Type a directory path",
                "Tab fills the highlighted entry · Enter opens it",
            ));
        } else {
            for (index, entry) in entries.into_iter().enumerate() {
                let path = entry.path.clone();
                results = results.child(path_row(&entry, index == selected_entry).on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.request_inspect(path.clone(), cx);
                    }),
                ));
            }
        }

        let dir_label = if self.directory.is_empty() {
            None
        } else {
            Some(SharedString::from(format!(
                "Browsing  {}",
                tildify(&self.directory)
            )))
        };

        div()
            .flex()
            .flex_col()
            .child(
                div()
                    .px_5()
                    .pt_4()
                    .pb_2()
                    .child(eyebrow("Working directory")),
            )
            .child(div().px_5().pb_3().child(path_input(self.input.as_str())))
            .when_some(dir_label, |el, label| {
                el.child(
                    div()
                        .px_5()
                        .pb_3()
                        .text_size(px(10.0))
                        .text_color(theme::moon::bone())
                        .child(label),
                )
            })
            .child(divider())
            .child(results)
    }

    fn render_repo_step(&self, cx: &mut Context<Self>) -> impl IntoElement {
        let Some(repo) = self.repo_info.clone() else {
            return div().into_any_element();
        };

        let header = div()
            .px_5()
            .py_4()
            .border_b_1()
            .border_color(theme::line::weak())
            .flex()
            .flex_col()
            .gap_1()
            .child(eyebrow("Repository"))
            .child(
                div()
                    .text_size(px(15.0))
                    .text_color(theme::moon::moonstone())
                    .font_weight(FontWeight::MEDIUM)
                    .child(SharedString::from(tildify(&repo.repo))),
            );

        let mut sections = div().flex().flex_col();

        sections = sections.child(
            div().px_5().pt_4().pb_2().child(
                div()
                    .flex()
                    .items_baseline()
                    .justify_between()
                    .child(eyebrow("Current branch"))
                    .child(
                        div()
                            .text_size(px(9.0))
                            .text_color(theme::moon::ash())
                            .child(SharedString::from(format!(
                                "default · origin/{}",
                                repo.default_branch
                            ))),
                    ),
            ),
        );

        sections = sections.child(
            branch_row(
                SharedString::from(repo.current_branch.clone()),
                SharedString::from(tildify(&repo.repo)),
                self.repo_focus == 0,
                BranchKind::Current,
            )
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, _, cx| {
                    cx.stop_propagation();
                    this.repo_focus = 0;
                    this.activate_repo_selection(cx);
                }),
            ),
        );

        if !repo.worktrees.is_empty() {
            sections = sections.child(
                div()
                    .px_5()
                    .pt_4()
                    .pb_2()
                    .child(eyebrow(&format!("Worktrees · {}", repo.worktrees.len()))),
            );

            for (index, worktree) in repo.worktrees.iter().enumerate() {
                let item_index = index + 1;
                let path = worktree.path.clone();
                sections = sections.child(
                    branch_row(
                        SharedString::from(worktree.branch.clone()),
                        SharedString::from(tildify(&worktree.path)),
                        self.repo_focus == item_index,
                        BranchKind::Worktree,
                    )
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, _, cx| {
                            cx.stop_propagation();
                            this.repo_focus = item_index;
                            this.submit_path(path.clone(), cx);
                        }),
                    ),
                );
            }
        }

        let create_index = repo.worktrees.len() + 1;
        sections = sections
            .child(div().px_5().pt_4().pb_2().child(eyebrow("Create new")))
            .child(if self.new_worktree_open {
                new_worktree_form(
                    self.new_worktree_name.as_str(),
                    self.worktree_base,
                    selected_destination_branch(&repo, self.repo_focus).as_str(),
                    repo.default_branch.as_str(),
                    self.creating_worktree,
                )
                .into_any_element()
            } else {
                create_worktree_row(self.repo_focus == create_index)
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, _, cx| {
                            cx.stop_propagation();
                            this.repo_focus = create_index;
                            this.new_worktree_open = true;
                            cx.notify();
                        }),
                    )
                    .into_any_element()
            });

        div()
            .flex()
            .flex_col()
            .child(header)
            .child(sections)
            .into_any_element()
    }

    fn render_status_footer(&self) -> impl IntoElement {
        if let Some(error) = &self.error {
            return status_footer(error.clone(), StatusKind::Error);
        }
        if let Some(label) = &self.loading_label {
            return status_footer(label.clone(), StatusKind::Loading);
        }
        let hint = match (self.step, self.new_worktree_open) {
            (DialogStep::Path, _) => "↵ select   ⇥ complete   ↑↓ navigate   esc cancel",
            (DialogStep::Repo, false) => "↵ open   N new worktree   ↑↓ navigate   esc back",
            (DialogStep::Repo, true) => "↵ create   ⇥ toggle base   esc cancel",
        };
        status_footer(SharedString::from(hint), StatusKind::Hint)
    }
}

fn selected_repo_index(repo: Option<&RepoInfo>, selected_path: Option<&str>) -> usize {
    let Some(repo) = repo else {
        return 0;
    };
    if selected_path == Some(repo.repo.as_str()) {
        return 0;
    }
    repo.worktrees
        .iter()
        .position(|worktree| selected_path == Some(worktree.path.as_str()))
        .map(|index| index + 1)
        .unwrap_or(0)
}

fn selected_destination_branch(repo: &RepoInfo, focus: usize) -> String {
    if focus == 0 {
        return repo.current_branch.clone();
    }
    repo.worktrees
        .get(focus.saturating_sub(1))
        .map(|worktree| worktree.branch.clone())
        .unwrap_or_else(|| repo.current_branch.clone())
}

fn tildify(path: &str) -> String {
    let parts: Vec<&str> = path.split('/').collect();
    if parts.len() > 3 && (parts.get(1) == Some(&"Users") || parts.get(1) == Some(&"home")) {
        format!("~/{}", parts[3..].join("/"))
    } else {
        path.to_string()
    }
}

// ─────────────────────────────────────────────────────────────────────────
// HEADER
// ─────────────────────────────────────────────────────────────────────────

fn dialog_header(eyebrow_text: &str, title: &str, step: DialogStep) -> impl IntoElement {
    let (step_no, step_label) = match step {
        DialogStep::Path => ("01", "location"),
        DialogStep::Repo => ("02", "repository"),
    };

    div()
        .px_5()
        .pt_5()
        .pb_4()
        .border_b_1()
        .border_color(theme::line::firm())
        .flex()
        .items_center()
        .justify_between()
        .gap_4()
        .child(
            div()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_size(px(9.0))
                        .text_color(theme::sodium::vapor())
                        .child(SharedString::from(format_eyebrow(eyebrow_text))),
                )
                .child(
                    div()
                        .text_size(px(22.0))
                        .text_color(theme::moon::moonstone())
                        .font_weight(FontWeight::LIGHT)
                        .child(SharedString::from(title.to_string())),
                ),
        )
        .child(step_pill(step_no, step_label))
}

fn step_pill(step_no: &str, step_label: &str) -> gpui::Div {
    div()
        .flex()
        .items_center()
        .gap_2()
        .px_2p5()
        .py_1p5()
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(theme::line::mild())
        .child(
            div()
                .text_size(px(11.0))
                .text_color(theme::sodium::vapor())
                .font_weight(FontWeight::MEDIUM)
                .child(SharedString::from(step_no.to_string())),
        )
        .child(
            div()
                .text_size(px(9.0))
                .text_color(theme::moon::bone())
                .child(SharedString::from(format_eyebrow(step_label))),
        )
}

// ─────────────────────────────────────────────────────────────────────────
// AGENT PICKER
// ─────────────────────────────────────────────────────────────────────────

struct AgentSpec {
    id: &'static str,
    name: &'static str,
    glyph: &'static str,
    role: &'static str,
    shortcut: &'static str,
}

fn agent_specs() -> [AgentSpec; 3] {
    [
        AgentSpec {
            id: "claude",
            name: "Claude",
            glyph: "α",
            role: "Long-form. Plans, reviews.",
            shortcut: "⌘ 1",
        },
        AgentSpec {
            id: "codex",
            name: "Codex",
            glyph: "β",
            role: "Narrow diff. Tight edits.",
            shortcut: "⌘ 2",
        },
        AgentSpec {
            id: "shell",
            name: "Shell",
            glyph: "γ",
            role: "Raw PTY. Your hand.",
            shortcut: "⌘ 3",
        },
    ]
}

fn agent_tile(spec: AgentSpec, selected: bool) -> gpui::Div {
    let star = theme::star::for_agent_id(spec.id);
    let (border, bg) = if selected {
        (theme::sodium::vapor(), theme::sodium::hush())
    } else {
        (theme::line::mild(), theme::ink::shade())
    };
    let shadow = if selected {
        vec![BoxShadow {
            color: theme::sodium::soft().into(),
            offset: point(px(0.0), px(0.0)),
            blur_radius: px(20.0),
            spread_radius: px(0.0),
        }]
    } else {
        Vec::new()
    };

    div()
        .flex_1()
        .min_w(px(0.0))
        .relative()
        .px_3()
        .py_3()
        .rounded(px(theme::radius::R0))
        .bg(bg)
        .border_1()
        .border_color(border)
        .shadow(shadow)
        .flex()
        .flex_col()
        .gap_2()
        .child(
            div()
                .absolute()
                .top(px(8.0))
                .right(px(10.0))
                .text_size(px(9.0))
                .text_color(if selected {
                    theme::sodium::vapor()
                } else {
                    theme::moon::ash()
                })
                .child(SharedString::from(spec.shortcut.to_string())),
        )
        .child(
            div()
                .flex()
                .items_center()
                .gap_2()
                .child(
                    div()
                        .w(px(8.0))
                        .h(px(8.0))
                        .rounded_full()
                        .bg(star)
                        .shadow(vec![BoxShadow {
                            color: star.into(),
                            offset: point(px(0.0), px(0.0)),
                            blur_radius: px(8.0),
                            spread_radius: px(0.0),
                        }]),
                )
                .child(
                    div()
                        .text_size(px(15.0))
                        .text_color(if selected {
                            theme::sodium::vapor()
                        } else {
                            theme::moon::parchment()
                        })
                        .italic()
                        .child(SharedString::from(spec.glyph.to_string())),
                )
                .child(
                    div()
                        .text_size(px(14.0))
                        .text_color(theme::moon::moonstone())
                        .font_weight(FontWeight::MEDIUM)
                        .child(SharedString::from(spec.name.to_string())),
                ),
        )
        .child(
            div()
                .text_size(px(11.0))
                .text_color(theme::moon::bone())
                .child(SharedString::from(spec.role.to_string())),
        )
}

// ─────────────────────────────────────────────────────────────────────────
// PATH STEP
// ─────────────────────────────────────────────────────────────────────────

fn path_input(value: &str) -> impl IntoElement {
    let placeholder = value.is_empty();
    let shown = if placeholder {
        SharedString::from("Type path…")
    } else {
        SharedString::from(value.to_string())
    };

    div()
        .px_3p5()
        .py_2p5()
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(theme::sodium::vapor())
        .shadow(vec![
            BoxShadow {
                color: theme::sodium::soft().into(),
                offset: point(px(0.0), px(0.0)),
                blur_radius: px(0.0),
                spread_radius: px(1.0),
            },
            BoxShadow {
                color: theme::sodium::soft().into(),
                offset: point(px(0.0), px(0.0)),
                blur_radius: px(18.0),
                spread_radius: px(0.0),
            },
        ])
        .flex()
        .items_center()
        .gap_3()
        .child(
            div()
                .text_size(px(13.0))
                .text_color(theme::sodium::vapor())
                .child(SharedString::from("⌕")),
        )
        // Text + caret share a flex_1 row so the caret sits flush
        // against the typed value while the row still fills the input
        // visually. (Putting flex_1 on the text alone pushes the caret
        // to the far right edge.)
        .child(
            div()
                .flex_1()
                .flex()
                .items_center()
                .gap(px(2.0))
                .child(
                    div()
                        .text_size(px(13.0))
                        .text_color(if placeholder {
                            theme::moon::ash()
                        } else {
                            theme::moon::moonstone()
                        })
                        .child(shown),
                )
                .child(aurora_caret()),
        )
}

fn aurora_caret() -> gpui::Div {
    div()
        .w(px(7.0))
        .h(px(15.0))
        .bg(theme::sodium::vapor())
        .rounded(px(1.0))
        .shadow(vec![
            BoxShadow {
                color: theme::sodium::glow().into(),
                offset: point(px(0.0), px(0.0)),
                blur_radius: px(10.0),
                spread_radius: px(0.0),
            },
            BoxShadow {
                color: theme::sodium::soft().into(),
                offset: point(px(0.0), px(0.0)),
                blur_radius: px(18.0),
                spread_radius: px(0.0),
            },
        ])
}

fn path_row(entry: &DirectoryEntry, selected: bool) -> gpui::Div {
    let glyph = if selected { "›" } else { "·" };
    row_base(selected)
        .child(
            div()
                .w(px(14.0))
                .text_color(if selected {
                    theme::sodium::vapor()
                } else {
                    theme::moon::ash()
                })
                .text_size(px(13.0))
                .child(SharedString::from(glyph)),
        )
        .child(
            div()
                .flex_1()
                .truncate()
                .child(SharedString::from(entry.name.clone())),
        )
        .child(
            div()
                .w(px(280.0))
                .truncate()
                .text_size(px(11.0))
                .text_color(theme::moon::ash())
                .child(SharedString::from(tildify(&entry.path))),
        )
}

fn empty_state(headline: &str, hint: &str) -> gpui::Div {
    div()
        .px_5()
        .py_8()
        .flex()
        .flex_col()
        .items_center()
        .gap_2()
        .child(
            div()
                .text_size(px(13.0))
                .text_color(theme::moon::parchment())
                .child(SharedString::from(headline.to_string())),
        )
        .child(
            div()
                .text_size(px(10.0))
                .text_color(theme::moon::ash())
                .child(SharedString::from(hint.to_string())),
        )
}

// ─────────────────────────────────────────────────────────────────────────
// REPO STEP
// ─────────────────────────────────────────────────────────────────────────

#[derive(Clone, Copy)]
enum BranchKind {
    Current,
    Worktree,
}

fn branch_row(
    name: SharedString,
    detail: SharedString,
    selected: bool,
    kind: BranchKind,
) -> gpui::Div {
    let glyph = match kind {
        BranchKind::Current => "⎈",
        BranchKind::Worktree => "⎇",
    };
    row_base(selected)
        .child(
            div()
                .w(px(18.0))
                .text_color(if selected {
                    theme::sodium::vapor()
                } else {
                    theme::moon::bone()
                })
                .text_size(px(12.0))
                .child(SharedString::from(glyph)),
        )
        .child(
            div()
                .w(px(200.0))
                .truncate()
                .font_weight(FontWeight::MEDIUM)
                .child(name),
        )
        .child(
            div()
                .flex_1()
                .truncate()
                .text_size(px(11.0))
                .text_color(theme::moon::ash())
                .child(detail),
        )
}

fn create_worktree_row(selected: bool) -> gpui::Div {
    row_base(selected)
        .child(
            div()
                .w(px(18.0))
                .text_color(theme::sodium::vapor())
                .text_size(px(13.0))
                .child(SharedString::from("+")),
        )
        .child(
            div()
                .flex_1()
                .text_color(if selected {
                    theme::moon::moonstone()
                } else {
                    theme::moon::parchment()
                })
                .child(SharedString::from("New worktree")),
        )
        .child(
            div()
                .text_size(px(10.0))
                .text_color(theme::moon::ash())
                .child(SharedString::from("press N")),
        )
}

fn new_worktree_form(
    name: &str,
    base: WorktreeBase,
    current_branch: &str,
    default_branch: &str,
    creating: bool,
) -> gpui::Div {
    let placeholder = name.is_empty();
    let value = if creating {
        SharedString::from("Creating worktree…")
    } else if placeholder {
        SharedString::from("Branch name…")
    } else {
        SharedString::from(name.to_string())
    };

    let (current_pill, default_pill) = match base {
        WorktreeBase::Current => (true, false),
        WorktreeBase::Default => (false, true),
    };

    div()
        .mx_5()
        .my_3()
        .p_4()
        .rounded(px(theme::radius::R1))
        .bg(theme::ink::midnight())
        .border_1()
        .border_color(theme::sodium::deep())
        .shadow(vec![BoxShadow {
            color: theme::sodium::soft().into(),
            offset: point(px(0.0), px(0.0)),
            blur_radius: px(20.0),
            spread_radius: px(0.0),
        }])
        .flex()
        .flex_col()
        .gap_3()
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .text_size(px(13.0))
                        .text_color(theme::sodium::vapor())
                        .child(SharedString::from("⎇")),
                )
                .child(
                    div()
                        .flex_1()
                        .text_size(px(14.0))
                        .text_color(if placeholder || creating {
                            theme::moon::ash()
                        } else {
                            theme::moon::moonstone()
                        })
                        .font_weight(FontWeight::MEDIUM)
                        .child(value),
                )
                .when(!creating, |el| el.child(aurora_caret())),
        )
        .child(
            div()
                .flex()
                .items_center()
                .gap_2()
                .child(eyebrow_inline("from"))
                .child(base_pill(
                    SharedString::from(current_branch.to_string()),
                    current_pill,
                ))
                .child(base_pill(
                    SharedString::from(format!("origin/{}", default_branch)),
                    default_pill,
                ))
                .child(
                    div()
                        .ml_auto()
                        .text_size(px(9.0))
                        .text_color(theme::moon::bone())
                        .child(SharedString::from("⇥ toggle")),
                ),
        )
}

fn base_pill(label: SharedString, active: bool) -> gpui::Div {
    let (bg, fg, border) = if active {
        (
            theme::sodium::soft(),
            theme::sodium::vapor(),
            theme::sodium::vapor(),
        )
    } else {
        (
            theme::ink::shade(),
            theme::moon::parchment(),
            theme::line::mild(),
        )
    };
    div()
        .px_2()
        .py_1()
        .rounded(px(theme::radius::R0))
        .bg(bg)
        .border_1()
        .border_color(border)
        .text_size(px(11.0))
        .text_color(fg)
        .child(label)
}

// ─────────────────────────────────────────────────────────────────────────
// SHARED ROW SCAFFOLD
// ─────────────────────────────────────────────────────────────────────────

fn row_base(selected: bool) -> gpui::Div {
    div()
        .px_5()
        .py_2()
        .flex()
        .items_center()
        .gap_3()
        .bg(if selected {
            theme::sodium::hush()
        } else {
            theme::ink::nocturne()
        })
        .border_l_2()
        .border_color(if selected {
            theme::sodium::vapor()
        } else {
            theme::ink::nocturne()
        })
        // Pull content back so the 2px reservation doesn't shift on selection.
        .pl(px(18.0))
        .text_size(px(13.0))
        .text_color(if selected {
            theme::moon::moonstone()
        } else {
            theme::moon::parchment()
        })
}

fn divider() -> gpui::Div {
    div()
        .h(px(1.0))
        .w_full()
        .bg(theme::line::weak())
}

fn eyebrow(label: &str) -> gpui::Div {
    div()
        .text_size(px(9.0))
        .text_color(theme::moon::bone())
        .child(SharedString::from(format_eyebrow(label)))
}

fn eyebrow_inline(label: &str) -> gpui::Div {
    div()
        .text_size(px(9.0))
        .text_color(theme::moon::ash())
        .child(SharedString::from(format_eyebrow(label)))
}

/// Render an eyebrow as uppercased letters separated by a thin space, so
/// the design system's mono letter-spacing reads correctly even without a
/// real letter-spacing CSS property in GPUI.
fn format_eyebrow(label: &str) -> String {
    let mut out = String::with_capacity(label.len() * 2);
    for (i, ch) in label.chars().enumerate() {
        if i > 0 && !ch.is_whitespace() {
            out.push('\u{2009}'); // thin space
        }
        for upper in ch.to_uppercase() {
            out.push(upper);
        }
    }
    out
}

// ─────────────────────────────────────────────────────────────────────────
// FOOTER / STATUS
// ─────────────────────────────────────────────────────────────────────────

#[derive(Clone, Copy)]
enum StatusKind {
    Hint,
    Loading,
    Error,
}

fn status_footer(message: SharedString, kind: StatusKind) -> gpui::Div {
    let (bg, glyph_color, text_color, glyph) = match kind {
        StatusKind::Hint => (
            theme::ink::midnight(),
            theme::sodium::vapor(),
            theme::moon::bone(),
            "·",
        ),
        StatusKind::Loading => (
            theme::ink::midnight(),
            theme::sodium::vapor(),
            theme::moon::parchment(),
            "◌",
        ),
        StatusKind::Error => (
            theme::ink::midnight(),
            theme::state::error(),
            theme::state::error(),
            "✕",
        ),
    };

    div()
        .w_full()
        .px_5()
        .py_3()
        .border_t_1()
        .border_color(theme::line::firm())
        .bg(bg)
        .flex()
        .items_center()
        .gap_3()
        .child(
            div()
                .w(px(12.0))
                .text_size(px(11.0))
                .text_color(glyph_color)
                .child(SharedString::from(glyph.to_string())),
        )
        .child(
            div()
                .flex_1()
                .text_size(px(10.0))
                .text_color(text_color)
                .child(message),
        )
}
