use std::{
    collections::{HashMap, HashSet},
    rc::Rc,
    sync::Arc,
    time::Duration,
};

use attn_protocol::{
    DetachSessionMessage, LayoutNode, MuteMessage, ServerEvent, WorkspaceLayout,
    WorkspaceLayoutClosePaneMessage, WorkspaceLayoutFocusPaneMessage, WorkspaceLayoutPane,
};
use gpui::{
    actions, div, ease_in_out, prelude::*, px, relative, Animation, AnimationExt as _, AnyElement,
    App, Context, Entity, MouseButton, ParentElement, Render, SharedString, Window,
};
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

actions!(workspace_navigation, [PreviousPane, NextPane]);

pub fn bind_keys(cx: &mut App) {
    cx.bind_keys([
        gpui::KeyBinding::new("cmd-left", PreviousPane, None),
        gpui::KeyBinding::new("cmd-up", PreviousPane, None),
        gpui::KeyBinding::new("cmd-right", NextPane, None),
        gpui::KeyBinding::new("cmd-down", NextPane, None),
    ]);
}

pub struct NativeApp {
    daemon: Entity<DaemonClient>,
    ghostty: Rc<GhosttyRuntime>,
    store: ClientStore,
    selected_workspace_id: Option<String>,
    terminal_views: HashMap<String, Entity<TerminalView>>,
    hovered_pane_id: Option<String>,
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
                        self.store
                            .reset(initial.sessions.clone(), initial.workspaces.clone());
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
                    .text_size(px(12.))
                    .text_color(theme::moon::dim())
                    .child("WORKSPACES"),
            )
            .children(groups)
            .into_any_element()
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
        div()
            .size_full()
            .relative()
            .flex()
            .bg(theme::ink::midnight())
            .text_color(theme::moon::primary())
            .on_action(cx.listener(Self::previous_pane))
            .on_action(cx.listener(Self::next_pane))
            .child(self.render_sidebar(cx))
            .child(div().flex_1().overflow_hidden().child(main))
            .children(status)
    }
}
