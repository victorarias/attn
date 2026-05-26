//! Native Settings surface. It mirrors the daemon-backed settings exposed
//! by the Tauri client while keeping native-only canvas controls nearby.

use attn_protocol::{
    AddEndpointMessage, AuthorState, BootstrapEndpointMessage, EndpointInfo, ListEndpointsMessage,
    RemoveEndpointMessage, RepoState, SetEndpointRemoteWebMessage, SetSettingMessage, SettingsMap,
    ToggleAuthorMuteMessage, ToggleRepoMuteMessage, UpdateEndpointMessage,
};
use gpui::{
    div, hsla, point, prelude::*, px, BoxShadow, Context, Entity, FocusHandle, Focusable,
    FontWeight, KeyDownEvent, MouseButton, ParentElement, Render, SharedString, Window,
};
use serde_json::Value;

use crate::adapters::daemon::DaemonClient;
use crate::theme;

const AGENTS: [&str; 4] = ["claude", "codex", "copilot", "pi"];
const CAPS: [&str; 7] = [
    "hooks",
    "transcript",
    "transcript_watcher",
    "classifier",
    "state_detector",
    "resume",
    "yolo",
];

type CloseHandler = dyn Fn(&mut Window, &mut gpui::App) + 'static;
type ToggleSidebarHandler = dyn Fn(&mut Window, &mut gpui::App) -> bool + 'static;

#[derive(Clone, Debug, Default)]
pub struct SettingsPageState {
    pub settings: SettingsMap,
    pub endpoints: Vec<EndpointInfo>,
    pub github_hosts: Vec<String>,
    pub repos: Vec<RepoState>,
    pub authors: Vec<AuthorState>,
}

impl SettingsPageState {
    pub fn from_wire(
        settings: SettingsMap,
        endpoints: Vec<EndpointInfo>,
        github_hosts: Vec<String>,
        repos: Vec<RepoState>,
        authors: Vec<AuthorState>,
    ) -> Self {
        Self {
            settings,
            endpoints,
            github_hosts,
            repos,
            authors,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum SettingsSection {
    General,
    Agents,
    Review,
    Network,
    Filters,
    System,
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum EditableField {
    Setting(&'static str),
    AgentExecutable(&'static str),
    NewEndpointName,
    NewEndpointTarget,
    NewEndpointProfile,
    EditEndpointName(String),
    EditEndpointTarget(String),
    EditEndpointProfile(String),
}

pub struct SettingsPage {
    daemon: Entity<DaemonClient>,
    state: SettingsPageState,
    sidebar_collapsed: bool,
    active_section: SettingsSection,
    active_field: Option<EditableField>,
    draft: String,
    draft_cursor: usize,
    new_endpoint_name: String,
    new_endpoint_target: String,
    new_endpoint_profile: String,
    editing_endpoint_id: Option<String>,
    editing_endpoint_name: String,
    editing_endpoint_target: String,
    editing_endpoint_profile: String,
    notice: Option<SharedString>,
    on_close: Box<CloseHandler>,
    on_toggle_sidebar: Box<ToggleSidebarHandler>,
    focus_handle: FocusHandle,
}

impl SettingsPage {
    pub fn new(
        daemon: Entity<DaemonClient>,
        state: SettingsPageState,
        sidebar_collapsed: bool,
        on_close: impl Fn(&mut Window, &mut gpui::App) + 'static,
        on_toggle_sidebar: impl Fn(&mut Window, &mut gpui::App) -> bool + 'static,
        cx: &mut Context<Self>,
    ) -> Self {
        let page = Self {
            daemon,
            state,
            sidebar_collapsed,
            active_section: SettingsSection::General,
            active_field: None,
            draft: String::new(),
            draft_cursor: 0,
            new_endpoint_name: String::new(),
            new_endpoint_target: String::new(),
            new_endpoint_profile: String::new(),
            editing_endpoint_id: None,
            editing_endpoint_name: String::new(),
            editing_endpoint_target: String::new(),
            editing_endpoint_profile: String::new(),
            notice: None,
            on_close: Box::new(on_close),
            on_toggle_sidebar: Box::new(on_toggle_sidebar),
            focus_handle: cx.focus_handle(),
        };
        page.request_fresh_state(cx);
        page
    }

    pub fn apply_state(&mut self, state: SettingsPageState, cx: &mut Context<Self>) {
        self.state = state;
        cx.notify();
    }

    pub fn apply_settings_update(
        &mut self,
        settings: SettingsMap,
        changed_key: Option<String>,
        success: Option<bool>,
        error: Option<String>,
        cx: &mut Context<Self>,
    ) {
        self.state.settings = settings;
        self.notice = match (success, error) {
            (Some(false), Some(error)) => Some(SharedString::from(error)),
            (Some(true), _) => changed_key.map(|key| SharedString::from(format!("Saved {key}"))),
            _ => None,
        };
        cx.notify();
    }

    pub fn apply_endpoints(&mut self, endpoints: Vec<EndpointInfo>, cx: &mut Context<Self>) {
        self.state.endpoints = endpoints;
        cx.notify();
    }

    pub fn apply_github_hosts(&mut self, hosts: Vec<String>, cx: &mut Context<Self>) {
        self.state.github_hosts = hosts;
        cx.notify();
    }

    pub fn apply_repos(&mut self, repos: Vec<RepoState>, cx: &mut Context<Self>) {
        self.state.repos = repos;
        cx.notify();
    }

    pub fn apply_authors(&mut self, authors: Vec<AuthorState>, cx: &mut Context<Self>) {
        self.state.authors = authors;
        cx.notify();
    }

    fn request_fresh_state(&self, cx: &mut Context<Self>) {
        let daemon = self.daemon.read(cx);
        let _ = daemon.send_cmd(&attn_protocol::GetSettingsMessage::new());
        let _ = daemon.send_cmd(&ListEndpointsMessage::new());
    }

    fn close(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        (self.on_close)(window, cx);
    }

    pub fn click_close_for_automation(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.close(window, cx);
    }

    fn toggle_sidebar(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.sidebar_collapsed = (self.on_toggle_sidebar)(window, cx);
        cx.notify();
    }

    pub fn click_sidebar_mode_for_automation(
        &mut self,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.toggle_sidebar(window, cx);
    }

    pub fn select_section_for_automation(
        &mut self,
        section: &str,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        self.active_section = section_from_name(section)
            .ok_or_else(|| format!("unknown settings section: {section}"))?;
        self.active_field = None;
        self.draft.clear();
        self.draft_cursor = 0;
        cx.notify();
        Ok(())
    }

    fn set_section(&mut self, section: SettingsSection, cx: &mut Context<Self>) {
        self.active_section = section;
        self.active_field = None;
        self.draft.clear();
        self.draft_cursor = 0;
        self.notice = None;
        cx.notify();
    }

    fn focus_field(&mut self, field: EditableField, cx: &mut Context<Self>) {
        self.active_field = Some(field.clone());
        self.draft = self.field_value(&field);
        self.draft_cursor = self.draft.len();
        self.notice = None;
        cx.notify();
    }

    fn field_value(&self, field: &EditableField) -> String {
        match field {
            EditableField::Setting(key) => setting(&self.state.settings, key).to_string(),
            EditableField::AgentExecutable(agent) => {
                setting(&self.state.settings, &format!("{agent}_executable")).to_string()
            }
            EditableField::NewEndpointName => self.new_endpoint_name.clone(),
            EditableField::NewEndpointTarget => self.new_endpoint_target.clone(),
            EditableField::NewEndpointProfile => self.new_endpoint_profile.clone(),
            EditableField::EditEndpointName(_) => self.editing_endpoint_name.clone(),
            EditableField::EditEndpointTarget(_) => self.editing_endpoint_target.clone(),
            EditableField::EditEndpointProfile(_) => self.editing_endpoint_profile.clone(),
        }
    }

    fn write_draft_to_active_field(&mut self) {
        match self.active_field {
            Some(EditableField::NewEndpointName) => self.new_endpoint_name = self.draft.clone(),
            Some(EditableField::NewEndpointTarget) => self.new_endpoint_target = self.draft.clone(),
            Some(EditableField::NewEndpointProfile) => {
                self.new_endpoint_profile = self.draft.clone()
            }
            Some(EditableField::EditEndpointName(_)) => {
                self.editing_endpoint_name = self.draft.clone()
            }
            Some(EditableField::EditEndpointTarget(_)) => {
                self.editing_endpoint_target = self.draft.clone()
            }
            Some(EditableField::EditEndpointProfile(_)) => {
                self.editing_endpoint_profile = self.draft.clone()
            }
            _ => {}
        }
    }

    fn set_setting(
        &mut self,
        key: impl Into<String>,
        value: impl Into<String>,
        cx: &mut Context<Self>,
    ) {
        let key = key.into();
        let value = value.into();
        match self
            .daemon
            .read(cx)
            .send_cmd(&SetSettingMessage::new(key.clone(), value.clone()))
        {
            Ok(()) => {
                self.state.settings.insert(key, value);
                self.notice = Some(SharedString::from("Saving"));
            }
            Err(error) => self.notice = Some(SharedString::from(error)),
        }
        cx.notify();
    }

    fn commit_active_field(&mut self, cx: &mut Context<Self>) {
        let Some(field) = self.active_field.clone() else {
            return;
        };
        match field {
            EditableField::Setting(key) => self.set_setting(key, self.draft.trim().to_string(), cx),
            EditableField::AgentExecutable(agent) => self.set_setting(
                format!("{agent}_executable"),
                self.draft.trim().to_string(),
                cx,
            ),
            EditableField::NewEndpointName
            | EditableField::NewEndpointTarget
            | EditableField::NewEndpointProfile
            | EditableField::EditEndpointName(_)
            | EditableField::EditEndpointTarget(_)
            | EditableField::EditEndpointProfile(_) => {}
        }
    }

    fn send_cmd<T: serde::Serialize>(&mut self, msg: &T, label: &str, cx: &mut Context<Self>) {
        self.notice = match self.daemon.read(cx).send_cmd(msg) {
            Ok(()) => Some(SharedString::from(label.to_string())),
            Err(error) => Some(SharedString::from(error)),
        };
        cx.notify();
    }

    fn add_endpoint(&mut self, cx: &mut Context<Self>) {
        self.write_draft_to_active_field();
        if self.new_endpoint_name.trim().is_empty() || self.new_endpoint_target.trim().is_empty() {
            self.notice = Some(SharedString::from(
                "Endpoint name and SSH target are required",
            ));
            cx.notify();
            return;
        }
        let name = self.new_endpoint_name.trim().to_string();
        let target = self.new_endpoint_target.trim().to_string();
        let profile = self.new_endpoint_profile.trim().to_string();
        self.send_cmd(
            &AddEndpointMessage::new(name, target, profile),
            "Adding endpoint",
            cx,
        );
        self.new_endpoint_name.clear();
        self.new_endpoint_target.clear();
        self.new_endpoint_profile.clear();
    }

    fn begin_edit_endpoint(&mut self, endpoint: EndpointInfo, cx: &mut Context<Self>) {
        self.editing_endpoint_id = Some(endpoint.id);
        self.editing_endpoint_name = endpoint.name;
        self.editing_endpoint_target = endpoint.ssh_target;
        self.editing_endpoint_profile = endpoint.profile.unwrap_or_default();
        self.active_field = None;
        self.draft.clear();
        self.draft_cursor = 0;
        self.notice = None;
        cx.notify();
    }

    fn cancel_edit_endpoint(&mut self, cx: &mut Context<Self>) {
        self.editing_endpoint_id = None;
        self.editing_endpoint_name.clear();
        self.editing_endpoint_target.clear();
        self.editing_endpoint_profile.clear();
        self.active_field = None;
        self.draft.clear();
        self.draft_cursor = 0;
        cx.notify();
    }

    fn save_edit_endpoint(&mut self, endpoint_id: String, cx: &mut Context<Self>) {
        self.write_draft_to_active_field();
        if self.editing_endpoint_name.trim().is_empty()
            || self.editing_endpoint_target.trim().is_empty()
        {
            self.notice = Some(SharedString::from(
                "Endpoint name and SSH target are required",
            ));
            cx.notify();
            return;
        }
        let msg = UpdateEndpointMessage {
            cmd: "update_endpoint",
            endpoint_id,
            name: Some(self.editing_endpoint_name.trim().to_string()),
            ssh_target: Some(self.editing_endpoint_target.trim().to_string()),
            enabled: None,
            profile: Some(self.editing_endpoint_profile.trim().to_string()),
        };
        self.send_cmd(&msg, "Saving endpoint", cx);
        self.cancel_edit_endpoint(cx);
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        cx.stop_propagation();
        if self.active_field.is_some() {
            self.on_text_field_key_down(event, cx);
            return;
        }

        match event.keystroke.key.as_str() {
            "escape" => self.close(window, cx),
            "b" if event.keystroke.modifiers.platform => self.toggle_sidebar(window, cx),
            "1" => self.set_section(SettingsSection::General, cx),
            "2" => self.set_section(SettingsSection::Agents, cx),
            "3" => self.set_section(SettingsSection::Review, cx),
            "4" => self.set_section(SettingsSection::Network, cx),
            "5" => self.set_section(SettingsSection::Filters, cx),
            "6" => self.set_section(SettingsSection::System, cx),
            _ => {}
        }
    }

    fn on_text_field_key_down(&mut self, event: &KeyDownEvent, cx: &mut Context<Self>) {
        match event.keystroke.key.as_str() {
            "escape" => {
                self.active_field = None;
                self.draft.clear();
                self.draft_cursor = 0;
                cx.notify();
            }
            "enter" => {
                self.write_draft_to_active_field();
                self.commit_active_field(cx);
                self.active_field = None;
                self.draft.clear();
                self.draft_cursor = 0;
                cx.notify();
            }
            "backspace" => {
                if backspace_at_cursor(&mut self.draft, &mut self.draft_cursor) {
                    self.write_draft_to_active_field();
                    cx.notify();
                }
            }
            "delete" => {
                if delete_at_cursor(&mut self.draft, &mut self.draft_cursor) {
                    self.write_draft_to_active_field();
                    cx.notify();
                }
            }
            "left" => {
                self.draft_cursor = previous_char_boundary(&self.draft, self.draft_cursor);
                cx.notify();
            }
            "right" => {
                self.draft_cursor = next_char_boundary(&self.draft, self.draft_cursor);
                cx.notify();
            }
            "home" => {
                self.draft_cursor = 0;
                cx.notify();
            }
            "end" => {
                self.draft_cursor = self.draft.len();
                cx.notify();
            }
            _ => {
                if event.keystroke.modifiers.platform
                    || event.keystroke.modifiers.control
                    || event.keystroke.modifiers.alt
                {
                    return;
                }
                if let Some(key_char) = &event.keystroke.key_char {
                    if !key_char.is_empty() {
                        insert_at_cursor(&mut self.draft, &mut self.draft_cursor, key_char);
                        self.write_draft_to_active_field();
                        cx.notify();
                    }
                }
            }
        }
    }
}

impl Focusable for SettingsPage {
    fn focus_handle(&self, _cx: &gpui::App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl Render for SettingsPage {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if !self.focus_handle.is_focused(window) {
            self.focus_handle.focus(window);
        }

        let content = match self.active_section {
            SettingsSection::General => self.render_general(cx),
            SettingsSection::Agents => self.render_agents(cx),
            SettingsSection::Review => self.render_review(cx),
            SettingsSection::Network => self.render_network(cx),
            SettingsSection::Filters => self.render_filters(cx),
            SettingsSection::System => self.render_system(cx),
        };

        let panel = div()
            .w(px(980.0))
            .h(px(700.0))
            .rounded(px(theme::radius::R2))
            .bg(theme::ink::nocturne())
            .border_1()
            .border_color(theme::line::firm())
            .overflow_hidden()
            .shadow(vec![
                BoxShadow {
                    color: hsla(0.0, 0.0, 0.0, 0.60),
                    offset: point(px(0.0), px(28.0)),
                    blur_radius: px(70.0),
                    spread_radius: px(-8.0),
                },
                BoxShadow {
                    color: theme::sodium::soft().into(),
                    offset: point(px(0.0), px(0.0)),
                    blur_radius: px(0.0),
                    spread_radius: px(1.0),
                },
            ])
            .track_focus(&self.focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .flex()
            .child(self.render_nav(cx))
            .child(
                div()
                    .flex_1()
                    .min_w(px(0.0))
                    .h_full()
                    .flex()
                    .flex_col()
                    .child(header_bar(section_title(self.active_section)).child(
                        close_button().on_mouse_down(
                            MouseButton::Left,
                            cx.listener(|this, _, window, cx| {
                                cx.stop_propagation();
                                this.close(window, cx);
                            }),
                        ),
                    ))
                    .child(content)
                    .child(self.render_footer()),
            );

        div()
            .absolute()
            .size_full()
            .bg(theme::ink::veil())
            .flex()
            .items_center()
            .justify_center()
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| this.close(window, cx)),
            )
            .child(panel.on_mouse_down(
                MouseButton::Left,
                cx.listener(|_, _, _, cx| cx.stop_propagation()),
            ))
    }
}

impl SettingsPage {
    fn render_nav(&self, cx: &mut Context<Self>) -> gpui::Div {
        let mut nav = div()
            .w(px(188.0))
            .h_full()
            .bg(theme::ink::void())
            .border_r_1()
            .border_color(theme::line::weak())
            .p_4()
            .flex()
            .flex_col()
            .gap_3()
            .child(
                div()
                    .flex()
                    .flex_col()
                    .gap_1()
                    .child(
                        div()
                            .text_size(px(10.0))
                            .text_color(theme::sodium::vapor())
                            .font_weight(FontWeight::MEDIUM)
                            .child(SharedString::from("ATTN")),
                    )
                    .child(
                        div()
                            .text_size(px(19.0))
                            .text_color(theme::moon::moonstone())
                            .font_weight(FontWeight::MEDIUM)
                            .child(SharedString::from("Orchestrator")),
                    )
                    .child(
                        div()
                            .text_size(px(10.0))
                            .text_color(theme::moon::ash())
                            .child(SharedString::from("Settings")),
                    ),
            )
            .child(div().h(px(1.0)).w_full().bg(theme::line::weak()));

        for section in [
            SettingsSection::General,
            SettingsSection::Agents,
            SettingsSection::Review,
            SettingsSection::Network,
            SettingsSection::Filters,
            SettingsSection::System,
        ] {
            nav = nav.child(
                nav_item(section, self.active_section == section).on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.set_section(section, cx);
                    }),
                ),
            );
        }

        nav.child(div().flex_1()).child(summary_strip(&self.state))
    }

    fn render_general(&self, cx: &mut Context<Self>) -> gpui::Div {
        let theme_value = setting(&self.state.settings, "theme");
        let scale_value = setting(&self.state.settings, "uiScale");
        let scale = if scale_value.is_empty() {
            "1.0"
        } else {
            scale_value
        };
        div()
            .flex_1()
            .p_5()
            .flex()
            .gap_4()
            .child(
                div()
                    .flex_1()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(
                        card("Appearance", "Shared with the Tauri client")
                            .child(segment_row(
                                &["dark", "light", "system"],
                                if theme_value.is_empty() {
                                    "dark"
                                } else {
                                    theme_value
                                },
                                "theme",
                                cx,
                            ))
                            .child(segment_row(
                                &["0.8", "1.0", "1.2", "1.5"],
                                scale,
                                "uiScale",
                                cx,
                            )),
                    )
                    .child(
                        card("Projects", "Where PR worktrees and repositories live").child(
                            self.setting_text_field(
                                "Projects directory",
                                EditableField::Setting("projects_directory"),
                                "Absolute path. Daemon creates it if needed.",
                                cx,
                            ),
                        ),
                    )
                    .child(
                        card("Native canvas", "Client-specific layout controls").child(
                            sidebar_mode_control(self.sidebar_collapsed).on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|this, _, window, cx| {
                                    cx.stop_propagation();
                                    this.toggle_sidebar(window, cx);
                                }),
                            ),
                        ),
                    ),
            )
            .child(sidebar_preview(self.sidebar_collapsed))
    }

    fn render_agents(&self, cx: &mut Context<Self>) -> gpui::Div {
        let default_agent = setting(&self.state.settings, "new_session_agent");
        let active_agent = if default_agent.is_empty() {
            "claude"
        } else {
            default_agent
        };
        let mut body = div().flex_1().p_5().flex().gap_4();
        let mut left = div().flex_1().flex().flex_col().gap_4().child(
            card(
                "Default session agent",
                "Used when creating new sessions and opening PRs",
            )
            .child(agent_segment_row(active_agent, &self.state.settings, cx)),
        );

        let mut exec_card = card(
            "Executables",
            "Override launch commands. Empty uses PATH defaults",
        );
        for agent in AGENTS {
            exec_card = exec_card.child(self.setting_text_field(
                agent_label(agent),
                EditableField::AgentExecutable(agent),
                availability_label(agent, &self.state.settings),
                cx,
            ));
        }
        exec_card = exec_card.child(self.setting_text_field(
            "Editor",
            EditableField::Setting("editor_executable"),
            "Command used when opening files from reviews",
            cx,
        ));
        left = left.child(exec_card);

        let mut caps = card(
            "Capabilities",
            "Effective integration support reported by daemon",
        );
        for agent in AGENTS {
            caps = caps.child(capability_row(agent, &self.state.settings));
        }
        body = body.child(left).child(div().w(px(300.0)).child(caps));
        body
    }

    fn render_review(&self, cx: &mut Context<Self>) -> gpui::Div {
        let presets =
            review_preset_names(setting(&self.state.settings, "review_loop_prompt_presets"));
        let mut preset_card = card(
            "Review loop prompts",
            "Saved custom prompts used by loop controls",
        )
        .child(metric_row("Saved prompts", presets.len().to_string()));
        if presets.is_empty() {
            preset_card = preset_card.child(empty_row("No saved custom prompts"));
        } else {
            for name in presets.into_iter().take(5) {
                preset_card = preset_card.child(token_row(&name, "preset"));
            }
        }

        div()
            .flex_1()
            .p_5()
            .flex()
            .gap_4()
            .child(
                div()
                    .flex_1()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(
                        card("Review models", "Claude SDK model overrides")
                            .child(self.setting_text_field(
                                "Review loop model",
                                EditableField::Setting("review_loop_model"),
                                "Empty uses the built-in default",
                                cx,
                            ))
                            .child(self.setting_text_field(
                                "Reviewer model",
                                EditableField::Setting("reviewer_model"),
                                "Empty uses the built-in default",
                                cx,
                            )),
                    )
                    .child(preset_card),
            )
            .child(
                div().w(px(340.0)).child(
                    card("Preset data", "Raw JSON setting for full parity")
                        .child(self.setting_text_field(
                            "review_loop_prompt_presets",
                            EditableField::Setting("review_loop_prompt_presets"),
                            "Enter saves the raw preset payload",
                            cx,
                        ))
                        .child(self.setting_text_field(
                            "Last preset",
                            EditableField::Setting("review_loop_last_preset"),
                            "Most recently used prompt preset",
                            cx,
                        ))
                        .child(self.setting_text_field(
                            "Last iterations",
                            EditableField::Setting("review_loop_last_iterations"),
                            "Stored loop iteration count",
                            cx,
                        )),
                ),
            )
    }

    fn render_network(&self, cx: &mut Context<Self>) -> gpui::Div {
        let tailscale_enabled = setting(&self.state.settings, "tailscale_enabled") == "true";
        let mut mobile = card(
            "Mobile web client",
            "Expose this daemon through Tailscale Serve",
        )
        .child(toggle_row(
            "Tailscale Serve",
            if tailscale_enabled {
                "enabled"
            } else {
                "disabled"
            },
            tailscale_enabled,
            "tailscale_enabled",
            cx,
        ))
        .child(metric_row(
            "Status",
            nonempty(
                setting(&self.state.settings, "tailscale_status"),
                "disabled",
            ),
        ));
        for (label, key) in [
            ("Device DNS", "tailscale_domain"),
            ("Web URL", "tailscale_url"),
            ("Auth URL", "tailscale_auth_url"),
            ("Error", "tailscale_error"),
        ] {
            let value = setting(&self.state.settings, key);
            if !value.is_empty() {
                mobile = mobile.child(metric_row(label, value));
            }
        }

        let mut endpoints = card(
            "Remote endpoints",
            "SSH peers the local daemon keeps connected",
        )
        .child(self.endpoint_field("Name", EditableField::NewEndpointName, cx))
        .child(self.endpoint_field("SSH target", EditableField::NewEndpointTarget, cx))
        .child(self.endpoint_field("Profile", EditableField::NewEndpointProfile, cx))
        .child(action_button("Add endpoint").on_mouse_down(
            MouseButton::Left,
            cx.listener(|this, _, _, cx| {
                cx.stop_propagation();
                this.add_endpoint(cx);
            }),
        ));

        if self.state.endpoints.is_empty() {
            endpoints = endpoints.child(empty_row("No remote endpoints configured"));
        } else {
            for endpoint in &self.state.endpoints {
                endpoints = endpoints.child(self.render_endpoint_card(endpoint, cx));
            }
        }

        let mut hosts = card(
            "GitHub hosts",
            "Authenticated hosts registered by the daemon",
        );
        if self.state.github_hosts.is_empty() {
            hosts = hosts.child(empty_row("No authenticated hosts detected"));
        } else {
            for host in &self.state.github_hosts {
                hosts = hosts.child(token_row(host, "host"));
            }
        }

        div()
            .flex_1()
            .p_5()
            .flex()
            .gap_4()
            .child(
                div()
                    .flex_1()
                    .flex()
                    .flex_col()
                    .gap_4()
                    .child(mobile)
                    .child(hosts),
            )
            .child(div().w(px(430.0)).child(endpoints))
    }

    fn render_filters(&self, cx: &mut Context<Self>) -> gpui::Div {
        let muted_repos: Vec<_> = self.state.repos.iter().filter(|repo| repo.muted).collect();
        let muted_authors: Vec<_> = self
            .state
            .authors
            .iter()
            .filter(|author| author.muted)
            .collect();

        let mut repos = card("Muted repositories", "Hidden from attention queues");
        if muted_repos.is_empty() {
            repos = repos.child(empty_row("No muted repositories"));
        } else {
            for repo in muted_repos {
                let name = repo.repo.clone();
                repos = repos.child(filter_row(&repo.repo, "repo").on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.send_cmd(
                            &ToggleRepoMuteMessage::new(name.clone()),
                            "Unmuting repo",
                            cx,
                        );
                    }),
                ));
            }
        }

        let mut authors = card("Muted authors", "Bot and author filters");
        if muted_authors.is_empty() {
            authors = authors.child(empty_row("No muted authors"));
        } else {
            for author in muted_authors {
                let name = author.author.clone();
                authors = authors.child(filter_row(&author.author, "author").on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.send_cmd(
                            &ToggleAuthorMuteMessage::new(name.clone()),
                            "Unmuting author",
                            cx,
                        );
                    }),
                ));
            }
        }

        div()
            .flex_1()
            .p_5()
            .flex()
            .gap_4()
            .child(div().flex_1().child(repos))
            .child(div().flex_1().child(authors))
    }

    fn render_system(&self, _cx: &mut Context<Self>) -> gpui::Div {
        let pty = setting(&self.state.settings, "pty_backend_mode");
        let pty_label = match pty {
            "worker" => "External worker sidecar",
            "embedded" => "Embedded in daemon",
            _ => "Unknown",
        };
        let mut card_el = card("Runtime", "Low-level orchestration state")
            .child(metric_row("PTY backend", pty_label))
            .child(metric_row(
                "Endpoint count",
                self.state.endpoints.len().to_string(),
            ))
            .child(metric_row(
                "Authenticated hosts",
                self.state.github_hosts.len().to_string(),
            ))
            .child(metric_row(
                "Settings loaded",
                self.state.settings.len().to_string(),
            ));

        if let Some(notice) = &self.notice {
            card_el = card_el.child(status_row(notice.as_ref()));
        }

        div()
            .flex_1()
            .p_5()
            .flex()
            .gap_4()
            .child(div().flex_1().child(card_el))
            .child(
                div().w(px(360.0)).child(
                    card("Keyboard", "Native settings shortcuts")
                        .child(metric_row("Open settings", "Cmd+,"))
                        .child(metric_row("Toggle sidebar", "Cmd+B"))
                        .child(metric_row("Save field", "Enter"))
                        .child(metric_row("Close", "Esc")),
                ),
            )
    }

    fn render_footer(&self) -> gpui::Div {
        let text = self
            .notice
            .as_ref()
            .map(|notice| notice.to_string())
            .unwrap_or_else(|| {
                if self.active_field.is_some() {
                    "Editing field - Enter saves, Esc cancels".to_string()
                } else {
                    "1-6 switch sections, Cmd+B toggles sidebar, Esc closes".to_string()
                }
            });
        div()
            .h(px(34.0))
            .px_5()
            .border_t_1()
            .border_color(theme::line::weak())
            .bg(theme::ink::void())
            .flex()
            .items_center()
            .justify_between()
            .child(
                div()
                    .text_size(px(10.0))
                    .text_color(if self.notice.is_some() {
                        theme::sodium::vapor()
                    } else {
                        theme::moon::ash()
                    })
                    .child(SharedString::from(text)),
            )
            .child(
                div()
                    .text_size(px(10.0))
                    .text_color(theme::moon::ash())
                    .child(SharedString::from("daemon-backed")),
            )
    }

    fn setting_text_field(
        &self,
        label: &'static str,
        field: EditableField,
        hint: impl Into<String>,
        cx: &mut Context<Self>,
    ) -> gpui::Div {
        let value = if self.active_field.as_ref() == Some(&field) {
            self.draft.clone()
        } else {
            self.field_value(&field)
        };
        text_field(
            label,
            value,
            hint.into(),
            (self.active_field.as_ref() == Some(&field)).then_some(self.draft_cursor),
        )
        .on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, _, _, cx| {
                cx.stop_propagation();
                this.focus_field(field.clone(), cx);
            }),
        )
    }

    fn endpoint_field(
        &self,
        label: &'static str,
        field: EditableField,
        cx: &mut Context<Self>,
    ) -> gpui::Div {
        let value = if self.active_field.as_ref() == Some(&field) {
            self.draft.clone()
        } else {
            self.field_value(&field)
        };
        text_field(
            label,
            value,
            "New endpoint".to_string(),
            (self.active_field.as_ref() == Some(&field)).then_some(self.draft_cursor),
        )
        .on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, _, _, cx| {
                cx.stop_propagation();
                this.focus_field(field.clone(), cx);
            }),
        )
    }

    fn edit_endpoint_field(
        &self,
        label: &'static str,
        field: EditableField,
        cx: &mut Context<Self>,
    ) -> gpui::Div {
        let value = if self.active_field.as_ref() == Some(&field) {
            self.draft.clone()
        } else {
            self.field_value(&field)
        };
        text_field(
            label,
            value,
            "Editing endpoint".to_string(),
            (self.active_field.as_ref() == Some(&field)).then_some(self.draft_cursor),
        )
        .on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, _, _, cx| {
                cx.stop_propagation();
                this.focus_field(field.clone(), cx);
            }),
        )
    }

    fn render_endpoint_card(
        &self,
        endpoint: &EndpointInfo,
        cx: &mut Context<SettingsPage>,
    ) -> gpui::Div {
        let endpoint_id = endpoint.id.clone();
        let endpoint_id_enable = endpoint.id.clone();
        let endpoint_id_bootstrap = endpoint.id.clone();
        let endpoint_id_remove = endpoint.id.clone();
        let endpoint_id_edit = endpoint.id.clone();
        let endpoint_for_edit = endpoint.clone();
        let enabled = endpoint.enabled.unwrap_or(true);
        let remote_web = endpoint
            .capabilities
            .as_ref()
            .and_then(|caps| caps.tailscale_enabled)
            .unwrap_or(false);
        let editing = self.editing_endpoint_id.as_deref() == Some(endpoint.id.as_str());

        let mut card = div()
            .rounded(px(theme::radius::R0))
            .bg(theme::ink::midnight())
            .border_1()
            .border_color(if editing {
                theme::sodium::deep()
            } else {
                theme::line::weak()
            })
            .p_3()
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .gap_3()
                    .child(
                        div()
                            .flex_1()
                            .min_w(px(0.0))
                            .flex()
                            .flex_col()
                            .gap_1()
                            .child(
                                div()
                                    .truncate()
                                    .text_size(px(12.0))
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::moon::moonstone())
                                    .child(SharedString::from(endpoint.name.clone())),
                            )
                            .child(
                                div()
                                    .truncate()
                                    .text_size(px(10.0))
                                    .text_color(theme::moon::ash())
                                    .child(SharedString::from(endpoint.ssh_target.clone())),
                            ),
                    )
                    .child(status_chip(&endpoint.status)),
            );

        if editing {
            return card
                .child(self.edit_endpoint_field(
                    "Name",
                    EditableField::EditEndpointName(endpoint.id.clone()),
                    cx,
                ))
                .child(self.edit_endpoint_field(
                    "SSH target",
                    EditableField::EditEndpointTarget(endpoint.id.clone()),
                    cx,
                ))
                .child(self.edit_endpoint_field(
                    "Profile",
                    EditableField::EditEndpointProfile(endpoint.id.clone()),
                    cx,
                ))
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(endpoint_action("Save").on_mouse_down(
                            MouseButton::Left,
                            cx.listener(move |this, _, _, cx| {
                                cx.stop_propagation();
                                this.save_edit_endpoint(endpoint_id_edit.clone(), cx);
                            }),
                        ))
                        .child(endpoint_action("Cancel").on_mouse_down(
                            MouseButton::Left,
                            cx.listener(|this, _, _, cx| {
                                cx.stop_propagation();
                                this.cancel_edit_endpoint(cx);
                            }),
                        )),
                );
        }

        card = card
            .child(metric_row(
                "profile",
                endpoint.profile.as_deref().unwrap_or("default"),
            ))
            .child(metric_row(
                "sessions",
                endpoint.session_count.unwrap_or_default().to_string(),
            ));
        if let Some(caps) = &endpoint.capabilities {
            card = card
                .child(metric_row("protocol", caps.protocol_version.clone()))
                .child(metric_row(
                    "remote web",
                    caps.tailscale_status.as_deref().unwrap_or(if remote_web {
                        "enabled"
                    } else {
                        "disabled"
                    }),
                ));
            if let Some(url) = &caps.tailscale_url {
                card = card.child(metric_row("url", url.clone()));
            }
        }

        card.child(
            div()
                .flex()
                .gap_2()
                .child(endpoint_action("Edit").on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.begin_edit_endpoint(endpoint_for_edit.clone(), cx);
                    }),
                ))
                .child(
                    endpoint_action(if enabled { "Disable" } else { "Enable" }).on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, _, cx| {
                            cx.stop_propagation();
                            this.send_cmd(
                                &UpdateEndpointMessage::enabled(
                                    endpoint_id_enable.clone(),
                                    !enabled,
                                ),
                                "Updating endpoint",
                                cx,
                            );
                        }),
                    ),
                )
                .child(endpoint_action("Bootstrap").on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.send_cmd(
                            &BootstrapEndpointMessage::new(endpoint_id_bootstrap.clone()),
                            "Bootstrapping endpoint",
                            cx,
                        );
                    }),
                ))
                .child(
                    endpoint_action(if remote_web { "Web off" } else { "Web on" }).on_mouse_down(
                        MouseButton::Left,
                        cx.listener(move |this, _, _, cx| {
                            cx.stop_propagation();
                            this.send_cmd(
                                &SetEndpointRemoteWebMessage::new(endpoint_id.clone(), !remote_web),
                                "Updating web access",
                                cx,
                            );
                        }),
                    ),
                )
                .child(endpoint_action("Remove").on_mouse_down(
                    MouseButton::Left,
                    cx.listener(move |this, _, _, cx| {
                        cx.stop_propagation();
                        this.send_cmd(
                            &RemoveEndpointMessage::new(endpoint_id_remove.clone()),
                            "Removing endpoint",
                            cx,
                        );
                    }),
                )),
        )
    }
}

fn setting<'a>(settings: &'a SettingsMap, key: &str) -> &'a str {
    settings.get(key).map(String::as_str).unwrap_or("")
}

fn nonempty<'a>(value: &'a str, fallback: &'a str) -> &'a str {
    if value.trim().is_empty() {
        fallback
    } else {
        value
    }
}

fn clamp_char_boundary(value: &str, index: usize) -> usize {
    let mut index = index.min(value.len());
    while index > 0 && !value.is_char_boundary(index) {
        index -= 1;
    }
    index
}

fn previous_char_boundary(value: &str, index: usize) -> usize {
    let index = clamp_char_boundary(value, index);
    if index == 0 {
        return 0;
    }
    value[..index]
        .char_indices()
        .last()
        .map(|(position, _)| position)
        .unwrap_or(0)
}

fn next_char_boundary(value: &str, index: usize) -> usize {
    let index = clamp_char_boundary(value, index);
    if index >= value.len() {
        return value.len();
    }
    value[index..]
        .char_indices()
        .nth(1)
        .map(|(offset, _)| index + offset)
        .unwrap_or(value.len())
}

fn insert_at_cursor(value: &mut String, cursor: &mut usize, text: &str) {
    let index = clamp_char_boundary(value, *cursor);
    value.insert_str(index, text);
    *cursor = index + text.len();
}

fn backspace_at_cursor(value: &mut String, cursor: &mut usize) -> bool {
    let index = clamp_char_boundary(value, *cursor);
    if index == 0 {
        *cursor = 0;
        return false;
    }
    let previous = previous_char_boundary(value, index);
    value.replace_range(previous..index, "");
    *cursor = previous;
    true
}

fn delete_at_cursor(value: &mut String, cursor: &mut usize) -> bool {
    let index = clamp_char_boundary(value, *cursor);
    *cursor = index;
    if index >= value.len() {
        return false;
    }
    let next = next_char_boundary(value, index);
    value.replace_range(index..next, "");
    true
}

fn section_from_name(name: &str) -> Option<SettingsSection> {
    match name {
        "general" => Some(SettingsSection::General),
        "agents" => Some(SettingsSection::Agents),
        "review" => Some(SettingsSection::Review),
        "network" => Some(SettingsSection::Network),
        "filters" => Some(SettingsSection::Filters),
        "system" => Some(SettingsSection::System),
        _ => None,
    }
}

fn section_title(section: SettingsSection) -> &'static str {
    match section {
        SettingsSection::General => "General",
        SettingsSection::Agents => "Agents",
        SettingsSection::Review => "Review",
        SettingsSection::Network => "Network",
        SettingsSection::Filters => "Filters",
        SettingsSection::System => "System",
    }
}

fn nav_item(section: SettingsSection, active: bool) -> gpui::Div {
    let label = section_title(section);
    let mut item = div()
        .h(px(34.0))
        .rounded(px(theme::radius::R0))
        .px_3()
        .flex()
        .items_center()
        .justify_between()
        .text_size(px(12.0))
        .text_color(if active {
            theme::moon::moonstone()
        } else {
            theme::moon::ash()
        })
        .child(SharedString::from(label));
    if active {
        item = item
            .bg(theme::surface::selected_row())
            .border_1()
            .border_color(theme::sodium::deep())
            .child(
                div()
                    .w(px(6.0))
                    .h(px(6.0))
                    .rounded_full()
                    .bg(theme::sodium::vapor()),
            );
    }
    item
}

fn summary_strip(state: &SettingsPageState) -> gpui::Div {
    div()
        .rounded(px(theme::radius::R1))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(theme::line::weak())
        .p_3()
        .flex()
        .flex_col()
        .gap_2()
        .child(summary_line("settings", state.settings.len()))
        .child(summary_line("endpoints", state.endpoints.len()))
        .child(summary_line("hosts", state.github_hosts.len()))
}

fn summary_line(label: &'static str, count: usize) -> gpui::Div {
    div()
        .flex()
        .items_center()
        .justify_between()
        .text_size(px(10.0))
        .text_color(theme::moon::ash())
        .child(SharedString::from(label))
        .child(
            div()
                .text_color(theme::moon::bone())
                .font_weight(FontWeight::MEDIUM)
                .child(SharedString::from(count.to_string())),
        )
}

fn header_bar(title: &'static str) -> gpui::Div {
    div()
        .h(px(60.0))
        .px_5()
        .border_b_1()
        .border_color(theme::line::weak())
        .bg(theme::ink::shade())
        .flex()
        .items_center()
        .justify_between()
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .child(
                    div()
                        .w(px(3.0))
                        .h(px(31.0))
                        .rounded(px(theme::radius::R0))
                        .bg(theme::sodium::vapor())
                        .shadow(vec![BoxShadow {
                            color: theme::sodium::glow().into(),
                            offset: point(px(0.0), px(0.0)),
                            blur_radius: px(14.0),
                            spread_radius: px(0.0),
                        }]),
                )
                .child(
                    div()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .text_size(px(17.0))
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::moon::moonstone())
                                .child(SharedString::from(title)),
                        )
                        .child(
                            div()
                                .text_size(px(10.0))
                                .text_color(theme::moon::ash())
                                .child(SharedString::from("Native client settings")),
                        ),
                ),
        )
}

fn close_button() -> gpui::Div {
    div()
        .w(px(28.0))
        .h(px(28.0))
        .flex()
        .items_center()
        .justify_center()
        .rounded(px(theme::radius::R0))
        .border_1()
        .border_color(theme::line::mild())
        .text_color(theme::moon::bone())
        .text_size(px(14.0))
        .child(SharedString::from("x"))
}

fn card(title: &'static str, subtitle: &'static str) -> gpui::Div {
    div()
        .w_full()
        .rounded(px(theme::radius::R1))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(theme::line::mild())
        .p_4()
        .flex()
        .flex_col()
        .gap_3()
        .child(
            div()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_size(px(14.0))
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::moon::moonstone())
                        .child(SharedString::from(title)),
                )
                .child(
                    div()
                        .text_size(px(10.0))
                        .text_color(theme::moon::ash())
                        .child(SharedString::from(subtitle)),
                ),
        )
}

fn text_field(
    label: &'static str,
    value: String,
    hint: String,
    cursor: Option<usize>,
) -> gpui::Div {
    let active = cursor.is_some();
    let placeholder = value.is_empty();
    let cursor = cursor.map(|index| clamp_char_boundary(&value, index));

    let text_and_caret = div()
        .flex_1()
        .min_w(px(0.0))
        .overflow_hidden()
        .flex()
        .items_center()
        .gap(px(2.0));

    let text_and_caret = if let Some(cursor) = cursor {
        let (before, after) = value.split_at(cursor);
        let (before, after) = visible_field_segments(before, after);
        let mut row = text_and_caret
            .child(field_text(&before, placeholder && before.is_empty()))
            .child(settings_caret());
        if after.is_empty() && placeholder {
            row = row.child(field_text("type", true));
        } else {
            row = row.child(field_text(&after, false));
        }
        row
    } else {
        text_and_caret.child(field_text(
            if placeholder { "empty" } else { &value },
            placeholder,
        ))
    };

    let input = div()
        .h(px(32.0))
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::midnight())
        .border_1()
        .border_color(if active {
            theme::sodium::vapor()
        } else {
            theme::line::mild()
        })
        .px_3()
        .flex()
        .items_center()
        .gap_2()
        .child(text_and_caret);
    div()
        .flex()
        .flex_col()
        .gap_1()
        .child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .child(
                    div()
                        .text_size(px(10.0))
                        .text_color(theme::moon::ash())
                        .child(SharedString::from(label)),
                )
                .child(
                    div()
                        .text_size(px(9.0))
                        .text_color(theme::moon::cinder())
                        .child(SharedString::from(hint)),
                ),
        )
        .child(input)
}

fn visible_field_segments(before: &str, after: &str) -> (String, String) {
    let before = if before.chars().count() > 64 {
        format!("...{}", tail_chars(before, 64))
    } else {
        before.to_string()
    };
    let after = if after.chars().count() > 32 {
        format!("{}...", head_chars(after, 32))
    } else {
        after.to_string()
    };
    (before, after)
}

fn head_chars(value: &str, count: usize) -> String {
    value.chars().take(count).collect()
}

fn tail_chars(value: &str, count: usize) -> String {
    let chars: Vec<char> = value.chars().rev().take(count).collect();
    chars.into_iter().rev().collect()
}

fn field_text(value: &str, placeholder: bool) -> gpui::Div {
    div()
        .text_size(px(11.0))
        .text_color(if placeholder {
            theme::moon::ash()
        } else {
            theme::moon::bone()
        })
        .child(SharedString::from(value.to_string()))
}

fn settings_caret() -> gpui::Div {
    div()
        .w(px(6.0))
        .h(px(15.0))
        .rounded(px(theme::radius::R0))
        .bg(theme::sodium::vapor())
}

fn segment_row(
    options: &[&'static str],
    active: &str,
    key: &'static str,
    cx: &mut Context<SettingsPage>,
) -> gpui::Div {
    let mut row = div()
        .rounded(px(theme::radius::R1))
        .border_1()
        .border_color(theme::line::mild())
        .bg(theme::ink::midnight())
        .p_1()
        .flex()
        .items_center()
        .gap_1();
    for option in options {
        let value = *option;
        row = row.child(mode_segment(value, active == value).on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, _, _, cx| {
                cx.stop_propagation();
                this.set_setting(key, value, cx);
            }),
        ));
    }
    row
}

fn agent_segment_row(
    active: &str,
    settings: &SettingsMap,
    cx: &mut Context<SettingsPage>,
) -> gpui::Div {
    let mut row = div()
        .rounded(px(theme::radius::R1))
        .border_1()
        .border_color(theme::line::mild())
        .bg(theme::ink::midnight())
        .p_1()
        .flex()
        .items_center()
        .gap_1();
    for agent in AGENTS {
        let available = setting(settings, &format!("{agent}_available")) == "true";
        row = row.child(
            agent_segment(agent, active == agent, available).on_mouse_down(
                MouseButton::Left,
                cx.listener(move |this, _, _, cx| {
                    cx.stop_propagation();
                    if setting(&this.state.settings, &format!("{agent}_available")) == "true" {
                        this.set_setting("new_session_agent", agent, cx);
                    }
                }),
            ),
        );
    }
    row
}

fn agent_segment(label: &'static str, active: bool, available: bool) -> gpui::Div {
    let mut segment = mode_segment(agent_label(label), active);
    if !available {
        segment = segment.text_color(theme::moon::cinder());
    }
    segment
}

fn toggle_row(
    label: &'static str,
    status: &'static str,
    active: bool,
    key: &'static str,
    cx: &mut Context<SettingsPage>,
) -> gpui::Div {
    div()
        .flex()
        .items_center()
        .justify_between()
        .gap_3()
        .child(
            div()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .text_size(px(12.0))
                        .text_color(theme::moon::bone())
                        .child(SharedString::from(label)),
                )
                .child(
                    div()
                        .text_size(px(10.0))
                        .text_color(theme::moon::ash())
                        .child(SharedString::from(status)),
                ),
        )
        .child(toggle_pill(active).on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, _, _, cx| {
                cx.stop_propagation();
                let next = if setting(&this.state.settings, key) == "true" {
                    "false"
                } else {
                    "true"
                };
                this.set_setting(key, next, cx);
            }),
        ))
}

fn toggle_pill(active: bool) -> gpui::Div {
    div()
        .w(px(54.0))
        .h(px(26.0))
        .rounded(px(theme::radius::R1))
        .bg(if active {
            theme::sodium::deep()
        } else {
            theme::ink::midnight()
        })
        .border_1()
        .border_color(if active {
            theme::sodium::vapor()
        } else {
            theme::line::mild()
        })
        .p_1()
        .flex()
        .items_center()
        .justify_end()
        .when(!active, |el| el.justify_start())
        .child(div().w(px(18.0)).h(px(18.0)).rounded_full().bg(if active {
            theme::sodium::vapor()
        } else {
            theme::moon::ash()
        }))
}

fn mode_segment(label: &'static str, active: bool) -> gpui::Div {
    let mut segment = div()
        .h(px(28.0))
        .px_3()
        .flex()
        .items_center()
        .justify_center()
        .rounded(px(theme::radius::R0))
        .text_size(px(11.0))
        .text_color(if active {
            theme::moon::moonstone()
        } else {
            theme::moon::ash()
        })
        .child(SharedString::from(label));
    if active {
        segment = segment
            .bg(theme::surface::selected_row())
            .border_1()
            .border_color(theme::sodium::deep());
    }
    segment
}

fn action_button(label: &'static str) -> gpui::Div {
    div()
        .h(px(30.0))
        .rounded(px(theme::radius::R0))
        .bg(theme::sodium::deep())
        .border_1()
        .border_color(theme::sodium::vapor())
        .px_3()
        .flex()
        .items_center()
        .justify_center()
        .text_size(px(11.0))
        .text_color(theme::moon::moonstone())
        .child(SharedString::from(label))
}

fn metric_row(label: impl Into<String>, value: impl Into<String>) -> gpui::Div {
    div()
        .h(px(28.0))
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::midnight())
        .border_1()
        .border_color(theme::line::weak())
        .px_3()
        .flex()
        .items_center()
        .justify_between()
        .gap_3()
        .child(
            div()
                .text_size(px(10.0))
                .text_color(theme::moon::ash())
                .child(SharedString::from(label.into())),
        )
        .child(
            div()
                .flex_1()
                .min_w(px(0.0))
                .text_size(px(11.0))
                .text_color(theme::moon::bone())
                .truncate()
                .child(SharedString::from(value.into())),
        )
}

fn token_row(label: &str, kind: &'static str) -> gpui::Div {
    metric_row(kind, label.to_string())
}

fn empty_row(text: &'static str) -> gpui::Div {
    div()
        .h(px(32.0))
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::midnight())
        .border_1()
        .border_color(theme::line::weak())
        .px_3()
        .flex()
        .items_center()
        .text_size(px(11.0))
        .text_color(theme::moon::ash())
        .child(SharedString::from(text))
}

fn status_row(text: &str) -> gpui::Div {
    div()
        .rounded(px(theme::radius::R0))
        .bg(theme::sodium::hush())
        .border_1()
        .border_color(theme::sodium::deep())
        .p_3()
        .text_size(px(11.0))
        .text_color(theme::sodium::vapor())
        .child(SharedString::from(text.to_string()))
}

fn filter_row(label: &str, kind: &'static str) -> gpui::Div {
    div()
        .h(px(34.0))
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::midnight())
        .border_1()
        .border_color(theme::line::weak())
        .px_3()
        .flex()
        .items_center()
        .justify_between()
        .gap_3()
        .child(
            div()
                .flex_1()
                .min_w(px(0.0))
                .truncate()
                .text_size(px(11.0))
                .text_color(theme::moon::bone())
                .child(SharedString::from(label.to_string())),
        )
        .child(
            div()
                .text_size(px(10.0))
                .text_color(theme::sodium::vapor())
                .child(SharedString::from(format!("unmute {kind}"))),
        )
}

fn endpoint_action(label: &'static str) -> gpui::Div {
    div()
        .h(px(24.0))
        .px_2()
        .rounded(px(theme::radius::R0))
        .border_1()
        .border_color(theme::line::mild())
        .text_size(px(10.0))
        .text_color(theme::moon::bone())
        .flex()
        .items_center()
        .justify_center()
        .child(SharedString::from(label))
}

fn status_chip(status: &str) -> gpui::Div {
    let color = match status {
        "connected" => theme::state::working(),
        "error" | "failed" => theme::state::error(),
        "disabled" => theme::moon::cinder(),
        _ => theme::state::waiting(),
    };
    div()
        .h(px(20.0))
        .px_2()
        .rounded(px(theme::radius::R0))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(color)
        .text_size(px(10.0))
        .text_color(theme::moon::bone())
        .flex()
        .items_center()
        .child(SharedString::from(status.to_string()))
}

fn capability_row(agent: &'static str, settings: &SettingsMap) -> gpui::Div {
    let mut row = div().flex().flex_col().gap_1().child(
        div()
            .text_size(px(11.0))
            .text_color(theme::moon::bone())
            .font_weight(FontWeight::MEDIUM)
            .child(SharedString::from(agent_label(agent))),
    );
    for cap in CAPS {
        let key = format!("{agent}_cap_{cap}");
        let enabled = setting(settings, &key) == "true";
        row = row.child(
            div()
                .h(px(20.0))
                .rounded(px(theme::radius::R0))
                .bg(if enabled {
                    theme::sodium::hush()
                } else {
                    theme::ink::midnight()
                })
                .border_1()
                .border_color(if enabled {
                    theme::sodium::deep()
                } else {
                    theme::line::weak()
                })
                .px_2()
                .flex()
                .items_center()
                .justify_between()
                .child(
                    div()
                        .text_size(px(9.0))
                        .text_color(theme::moon::ash())
                        .child(SharedString::from(cap_label(cap))),
                )
                .child(
                    div()
                        .text_size(px(9.0))
                        .text_color(if enabled {
                            theme::sodium::vapor()
                        } else {
                            theme::moon::cinder()
                        })
                        .child(SharedString::from(if enabled { "on" } else { "off" })),
                ),
        );
    }
    row
}

fn availability_label(agent: &str, settings: &SettingsMap) -> &'static str {
    if setting(settings, &format!("{agent}_available")) == "true" {
        "Found in PATH"
    } else {
        "Not found in PATH"
    }
}

fn agent_label(agent: &str) -> &'static str {
    match agent {
        "claude" => "Claude",
        "codex" => "Codex",
        "copilot" => "Copilot",
        "pi" => "Pi",
        _ => "Agent",
    }
}

fn cap_label(cap: &'static str) -> &'static str {
    match cap {
        "transcript_watcher" => "watcher",
        "state_detector" => "state",
        other => other,
    }
}

fn review_preset_names(raw: &str) -> Vec<String> {
    let Ok(Value::Array(values)) = serde_json::from_str::<Value>(raw) else {
        return Vec::new();
    };
    values
        .into_iter()
        .filter_map(|value| {
            value
                .get("name")
                .and_then(Value::as_str)
                .map(str::to_string)
        })
        .collect()
}

fn sidebar_mode_control(collapsed: bool) -> gpui::Div {
    div()
        .rounded(px(theme::radius::R1))
        .border_1()
        .border_color(theme::line::mild())
        .bg(theme::ink::midnight())
        .p_1()
        .flex()
        .items_center()
        .gap_1()
        .child(mode_segment("Wide", !collapsed))
        .child(mode_segment("Narrow", collapsed))
}

fn sidebar_preview(collapsed: bool) -> gpui::Div {
    let rail_w = if collapsed { 48.0 } else { 150.0 };
    div()
        .w(px(260.0))
        .rounded(px(theme::radius::R1))
        .bg(theme::ink::shade())
        .border_1()
        .border_color(theme::line::mild())
        .p_4()
        .flex()
        .flex_col()
        .gap_3()
        .child(
            div()
                .text_size(px(14.0))
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::moon::moonstone())
                .child(SharedString::from("Workspace rail")),
        )
        .child(
            div()
                .h(px(230.0))
                .rounded(px(theme::radius::R1))
                .bg(theme::ink::midnight())
                .border_1()
                .border_color(theme::line::weak())
                .p_2()
                .flex()
                .gap_2()
                .child(
                    div()
                        .w(px(rail_w))
                        .h_full()
                        .rounded(px(theme::radius::R0))
                        .bg(theme::ink::nocturne())
                        .border_1()
                        .border_color(theme::line::mild())
                        .p_2()
                        .flex()
                        .flex_col()
                        .gap_2()
                        .child(preview_row(collapsed, theme::state::working(), true))
                        .child(preview_row(collapsed, theme::state::waiting(), false))
                        .child(preview_row(collapsed, theme::state::approval(), false)),
                )
                .child(
                    div()
                        .flex_1()
                        .h_full()
                        .rounded(px(theme::radius::R0))
                        .bg(theme::ink::void())
                        .border_1()
                        .border_color(theme::line::weak()),
                ),
        )
}

fn preview_row(collapsed: bool, color: gpui::Rgba, selected: bool) -> gpui::Div {
    let mut row = div()
        .w_full()
        .h(px(28.0))
        .rounded(px(theme::radius::R0))
        .bg(if selected {
            theme::surface::selected_row()
        } else {
            theme::ink::shade()
        })
        .border_l_2()
        .border_color(if selected {
            theme::sodium::vapor()
        } else {
            theme::ink::shade()
        })
        .flex()
        .items_center()
        .gap_2()
        .px_2()
        .child(div().w(px(7.0)).h(px(7.0)).rounded_full().bg(color));
    if !collapsed {
        row = row.child(
            div()
                .w(px(76.0))
                .h(px(5.0))
                .rounded(px(theme::radius::R0))
                .bg(if selected {
                    theme::moon::bone()
                } else {
                    theme::moon::cinder()
                }),
        );
    }
    row
}

#[cfg(test)]
mod tests {
    use super::{
        backspace_at_cursor, clamp_char_boundary, delete_at_cursor, insert_at_cursor,
        next_char_boundary, previous_char_boundary,
    };

    #[test]
    fn cursor_boundaries_preserve_utf8() {
        let value = "aéz";
        assert_eq!(clamp_char_boundary(value, 2), 1);
        assert_eq!(previous_char_boundary(value, value.len()), 3);
        assert_eq!(next_char_boundary(value, 1), 3);
    }

    #[test]
    fn cursor_editing_inserts_and_deletes_at_cursor() {
        let mut value = "ac".to_string();
        let mut cursor = 1;
        insert_at_cursor(&mut value, &mut cursor, "b");
        assert_eq!(value, "abc");
        assert_eq!(cursor, 2);

        assert!(backspace_at_cursor(&mut value, &mut cursor));
        assert_eq!(value, "ac");
        assert_eq!(cursor, 1);

        assert!(delete_at_cursor(&mut value, &mut cursor));
        assert_eq!(value, "a");
        assert_eq!(cursor, 1);
    }
}
