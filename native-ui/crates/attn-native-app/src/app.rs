/// Root view. Owns the live `Vec<Entity<Workspace>>` (the authoritative
/// list — sidebar and canvas just hold cloned handles), and subscribes to
/// `DaemonClient` to grow/shrink it as workspaces and sessions appear and
/// vanish on the wire.
///
/// Layout: sidebar pinned left at fixed width, canvas fills the rest.
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use attn_protocol::{
    AttachSessionMessage, RegisterWorkspaceMessage, Session, SpawnSessionMessage,
    UnregisterSessionMessage, UnregisterWorkspaceMessage, Workspace as ProtocolWorkspace,
};
use gpui::{
    div, prelude::*, rgb, App, Context, Entity, ParentElement, PathPromptOptions, Render,
    SharedString, Window,
};
use serde_json::{json, Value};

use crate::adapters::automation;
use crate::adapters::automation::actions::generate_workspace_id;
use crate::adapters::automation::events;
use crate::adapters::daemon::{DaemonClient, DaemonEvent};
use crate::state::panel::{Panel, TITLE_HEIGHT};
use crate::state::session_registry::SessionRegistry;
use crate::state::terminal_model::TerminalModel;
use crate::state::workspace::Workspace;
use crate::state::workspace_registry::{PendingSpawn, UpsertOutcome, WorkspaceRegistry};
use crate::views::canvas::WorkspaceCanvas;
use crate::views::sidebar::Sidebar;
use crate::views::terminal_view::TerminalView;

/// Initial terminal panel size in world-space units. ~380×240 gives
/// ~48 cols × ~12 rows once the title bar is subtracted.
const TERMINAL_W: f32 = 380.0;
const TERMINAL_H: f32 = 240.0;

pub struct NativeApp {
    daemon: Entity<DaemonClient>,
    /// Authoritative store for workspace entities, current selection,
    /// and pending wire-acks (select, spawn). All workspace-data
    /// mutations go through here; `NativeApp` only fans changes out to
    /// the sidebar and canvas.
    registry: WorkspaceRegistry,
    /// Authoritative session list. Populated from `DaemonEvent` and
    /// consumed by `sync_terminal_panels` + the automation snapshot.
    /// Lives here (state) instead of inside `DaemonClient` (adapter)
    /// so the daemon stays a pure I/O adapter.
    sessions: SessionRegistry,
    sidebar: Entity<Sidebar>,
    canvas: Entity<WorkspaceCanvas>,
    /// Live automation server handle. Drop deletes the manifest. `None`
    /// when automation is disabled for this launch (default in prod) or
    /// when bind/start failed — in which case we still want the app to
    /// run, just without the test sidecar.
    _automation: Option<automation::server::Handle>,
}

impl NativeApp {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        let app_handle = cx.entity().downgrade();
        let app_handle_canvas_spawn = app_handle.clone();
        let app_handle_canvas_close = app_handle.clone();
        let canvas = cx.new(|cx| {
            WorkspaceCanvas::new(
                cx,
                move |workspace_id, agent, _window, cx| {
                    let app_handle = app_handle_canvas_spawn.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        let _ = app.spawn_session_in_workspace(workspace_id, agent, cx);
                    });
                },
                move |session_id, _window, cx| {
                    let app_handle = app_handle_canvas_close.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.unregister_session_by_id(session_id, cx);
                    });
                },
            )
        });
        let app_handle_create = app_handle.clone();
        let app_handle_destroy = app_handle.clone();
        let sidebar = cx.new(|cx| {
            Sidebar::new(
                Vec::new(),
                move |id, _window, cx| {
                    let app_handle = app_handle.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.select_workspace_from_sidebar(id, cx);
                    });
                },
                move |_window, cx| {
                    let app_handle = app_handle_create.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.prompt_and_register_workspace(cx);
                    });
                },
                move |id, _window, cx| {
                    let app_handle = app_handle_destroy.clone();
                    let id_for_event = id.clone();
                    match app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.unregister_workspace_by_id(id, cx)
                    }) {
                        Ok(Ok(())) => Ok(()),
                        Ok(Err(error)) => {
                            events::record(
                                "workspace_destroy_failed",
                                json!({
                                    "id": id_for_event.as_ref(),
                                    "error": error.as_str(),
                                }),
                            );
                            Err(error)
                        }
                        Err(error) => {
                            let error = format!("update app: {error}");
                            events::record(
                                "workspace_destroy_failed",
                                json!({
                                    "id": id_for_event.as_ref(),
                                    "error": error.as_str(),
                                }),
                            );
                            Err(error)
                        }
                    }
                },
                cx,
            )
        });

        cx.subscribe(
            &daemon,
            |this, _client, event: &DaemonEvent, cx| match event {
                DaemonEvent::InitialState {
                    sessions,
                    workspaces,
                } => {
                    events::record(
                        "initial_state_observed",
                        json!({
                            "session_count": sessions.len(),
                            "workspace_count": workspaces.len(),
                        }),
                    );
                    this.apply_initial_state(sessions.clone(), workspaces.clone(), cx);
                }
                DaemonEvent::WorkspaceRegistered { workspace } => {
                    events::record(
                        "workspace_registered_observed",
                        json!({"workspace_id": workspace.id.as_str()}),
                    );
                    this.upsert_workspace(workspace.clone(), cx);
                    this.select_workspace_if_pending(&workspace.id, cx);
                    this.sync_terminal_panels(cx);
                }
                DaemonEvent::WorkspaceUnregistered { workspace_id } => {
                    events::record(
                        "workspace_unregistered_observed",
                        json!({"workspace_id": workspace_id.as_str()}),
                    );
                    this.remove_workspace(workspace_id.clone(), cx);
                }
                DaemonEvent::WorkspaceStateChanged { workspace } => {
                    events::record(
                        "workspace_state_changed_observed",
                        json!({"workspace_id": workspace.id.as_str()}),
                    );
                    this.apply_workspace_snapshot(workspace.clone(), cx);
                    this.select_workspace_if_pending(&workspace.id, cx);
                }
                DaemonEvent::SessionRegistered { session } => {
                    this.sessions.upsert(session.clone());
                    events::record(
                        "sessions_changed_observed",
                        json!({"session_count": this.sessions.len()}),
                    );
                    this.sync_terminal_panels(cx);
                }
                DaemonEvent::SessionStateChanged { session } => {
                    this.sessions.upsert(session.clone());
                    events::record(
                        "sessions_changed_observed",
                        json!({"session_count": this.sessions.len()}),
                    );
                    this.sync_terminal_panels(cx);
                }
                DaemonEvent::SessionUnregistered { session_id } => {
                    this.sessions.remove(session_id);
                    events::record(
                        "sessions_changed_observed",
                        json!({"session_count": this.sessions.len()}),
                    );
                    this.sync_terminal_panels(cx);
                }
                DaemonEvent::SessionsReplaced { sessions } => {
                    this.sessions.replace_all(sessions.clone());
                    events::record(
                        "sessions_changed_observed",
                        json!({"session_count": this.sessions.len()}),
                    );
                    this.sync_terminal_panels(cx);
                }
                DaemonEvent::SpawnResult {
                    session_id,
                    success,
                    error,
                } => {
                    let key = SharedString::from(session_id.clone());
                    let pending = this.registry.take_pending_spawn(&key);
                    if *success {
                        events::record(
                            "session_spawn_succeeded",
                            json!({
                                "session_id": session_id,
                                "workspace_id": pending.as_ref().map(|p| p.workspace_id.as_ref()),
                                "agent": pending.as_ref().map(|p| p.agent.as_ref()),
                            }),
                        );
                    } else {
                        events::record(
                            "session_spawn_failed",
                            json!({
                                "session_id": session_id,
                                "workspace_id": pending.as_ref().map(|p| p.workspace_id.as_ref()),
                                "agent": pending.as_ref().map(|p| p.agent.as_ref()),
                                "error": error.as_deref(),
                            }),
                        );
                    }
                }
                DaemonEvent::Connected => {
                    events::record("daemon_connected", json!({}));
                    // Daemon tracks PTY attachments per client connection,
                    // so on a fresh socket every existing TerminalView is
                    // detached on the daemon side until we re-issue
                    // AttachSession. Without this, terminals go silent
                    // after a daemon restart and only recover on app
                    // restart.
                    this.reattach_existing_terminals(cx);
                    this.sync_terminal_panels(cx);
                }
                _ => {}
            },
        )
        .detach();

        let automation_handle = if automation::automation_enabled() {
            start_automation(cx)
        } else {
            None
        };

        Self {
            daemon,
            registry: WorkspaceRegistry::new(),
            sessions: SessionRegistry::new(),
            sidebar,
            canvas,
            _automation: automation_handle,
        }
    }

    /// Apply a zoom level to the canvas, centered on the canvas
    /// midpoint. Used by the automation `set_zoom` action so headless
    /// scripts can drive perf measurements at known zoom levels.
    pub fn set_canvas_zoom(&self, zoom: f32, reset_fps: bool, cx: &mut Context<Self>) {
        let canvas = self.canvas.clone();
        canvas.update(cx, |canvas, cx| {
            canvas.set_zoom_centered(zoom, reset_fps, cx)
        });
    }

    /// Read-only handle to the daemon client, exposed so the automation
    /// module can serialize wire-level workspace + session state without
    /// cloning the lists out of `NativeApp`.
    pub fn daemon(&self) -> &Entity<DaemonClient> {
        &self.daemon
    }

    /// Lookup helper used by automation actions to target a specific
    /// workspace by id without exposing the underlying map.
    pub fn workspace(&self, id: &str) -> Option<Entity<Workspace>> {
        self.registry.workspace(id)
    }

    /// Iterator over every live workspace handle. Used by automation
    /// actions that need to scan panels across workspaces (e.g.
    /// `read_pane_text` finding the terminal model for a given session).
    pub fn workspaces(&self) -> impl Iterator<Item = &Entity<Workspace>> {
        self.registry.workspaces()
    }

    /// Snapshot of every live session. Used by the automation
    /// `list_sessions` action and `automation_snapshot`. Reads from the
    /// state registry, not the daemon adapter.
    pub fn sessions_snapshot(&self) -> Vec<Session> {
        self.sessions.snapshot()
    }

    /// Open the platform's directory picker. On selection, derive the
    /// workspace title from the directory's basename and send
    /// `register_workspace` to the daemon. The canvas + sidebar update
    /// reactively when the daemon's `workspace_registered` broadcast lands
    /// — same path the automation `create_workspace` action exercises.
    /// User cancellation (no path picked) is silent.
    pub fn prompt_and_register_workspace(&mut self, cx: &mut Context<Self>) {
        events::record("workspace_create_prompt", json!({}));
        let receiver = cx.prompt_for_paths(PathPromptOptions {
            files: false,
            directories: true,
            multiple: false,
            prompt: Some(SharedString::from("Pick a workspace directory")),
        });
        let app_handle = cx.entity().downgrade();
        cx.spawn(async move |_, cx| {
            let outcome = match receiver.await {
                Ok(Ok(Some(paths))) if !paths.is_empty() => Some(paths[0].clone()),
                Ok(Ok(_)) | Ok(Err(_)) | Err(_) => None,
            };
            let Some(path) = outcome else {
                events::record("workspace_create_cancelled", json!({}));
                return;
            };
            let directory = path.to_string_lossy().to_string();
            let title = path
                .file_name()
                .map(|s| s.to_string_lossy().to_string())
                .unwrap_or_else(|| directory.clone());
            let id = generate_workspace_id();
            let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                match app.register_workspace_and_select(
                    id.clone(),
                    title.clone(),
                    directory.clone(),
                    cx,
                ) {
                    Ok(()) => {
                        events::record(
                            "workspace_create_submitted",
                            json!({
                                "id": id.as_str(),
                                "directory": directory.as_str(),
                            }),
                        );
                    }
                    Err(error) => {
                        events::record(
                            "workspace_create_failed",
                            json!({
                                "id": id.as_str(),
                                "directory": directory.as_str(),
                                "error": error,
                            }),
                        );
                    }
                }
            });
        })
        .detach();
    }

    /// Register a workspace and select it once the daemon confirms it.
    /// The daemon broadcast is still the source of truth for creating or
    /// updating the local workspace entity.
    pub fn register_workspace_and_select(
        &mut self,
        id: String,
        title: String,
        directory: String,
        cx: &Context<Self>,
    ) -> Result<(), String> {
        self.daemon
            .read(cx)
            .send_cmd(&RegisterWorkspaceMessage::new(id.clone(), title, directory))?;
        self.registry.record_pending_select(SharedString::from(id));
        Ok(())
    }

    /// Send `unregister_workspace` for the given id. Daemon cascades
    /// (kills member sessions, broadcasts unregister), so the canvas +
    /// sidebar update reactively once the daemon confirms removal.
    pub fn unregister_workspace_by_id(
        &self,
        id: SharedString,
        cx: &Context<Self>,
    ) -> Result<(), String> {
        let id_string = id.to_string();
        self.daemon
            .read(cx)
            .send_cmd(&UnregisterWorkspaceMessage::new(id_string.clone()))?;
        events::record(
            "workspace_destroy_submitted",
            json!({"id": id_string.as_str()}),
        );
        Ok(())
    }

    /// Spawn a new session inside `workspace_id`, using the workspace's
    /// directory as the cwd and the canvas's default panel-derived
    /// terminal dimensions. The daemon's `session_registered` broadcast
    /// is the source of truth for the panel appearing — `sync_terminal_panels`
    /// picks it up and adds the panel — so we don't optimistically push
    /// anything into the canvas here. Returns the freshly generated session
    /// id on successful queue, an error string on lookup or wire failure.
    pub fn spawn_session_in_workspace(
        &mut self,
        workspace_id: SharedString,
        agent: SharedString,
        cx: &mut Context<Self>,
    ) -> Result<SharedString, String> {
        let Some(ws_entity) = self.registry.workspace(workspace_id.as_ref()) else {
            let known: Vec<String> = self
                .registry
                .workspaces()
                .map(|w| w.read(cx).id.to_string())
                .collect();
            events::record(
                "session_spawn_missed",
                json!({
                    "workspace_id": workspace_id.as_ref(),
                    "reason": "unknown_workspace",
                    "known_workspace_ids": known,
                }),
            );
            return Err(format!("unknown workspace id: {workspace_id}"));
        };
        let directory = ws_entity.read(cx).directory.to_string();
        let session_id = automation::actions::generate_workspace_id();
        let (cols, rows) = panel_terminal_dims(TERMINAL_W, TERMINAL_H);
        let msg = SpawnSessionMessage::new(
            session_id.clone(),
            directory.clone(),
            workspace_id.to_string(),
            agent.to_string(),
            cols,
            rows,
        );
        if let Err(error) = self.daemon.read(cx).send_cmd(&msg) {
            events::record(
                "session_spawn_send_failed",
                json!({
                    "session_id": session_id,
                    "workspace_id": workspace_id.as_ref(),
                    "agent": agent.as_ref(),
                    "error": error,
                }),
            );
            return Err(format!("send spawn_session: {error}"));
        }
        let id_shared = SharedString::from(session_id.clone());
        self.registry.record_pending_spawn(
            id_shared.clone(),
            PendingSpawn {
                workspace_id: workspace_id.clone(),
                agent: agent.clone(),
            },
        );
        events::record(
            "session_spawn_submitted",
            json!({
                "session_id": session_id,
                "workspace_id": workspace_id.as_ref(),
                "agent": agent.as_ref(),
                "directory": directory,
            }),
        );
        Ok(id_shared)
    }

    /// Tear down a session by sending `unregister`. The daemon SIGTERMs
    /// the PTY, drops the session record, and broadcasts
    /// `session_unregistered`; `sync_terminal_panels` prunes the panel.
    pub fn unregister_session_by_id(&mut self, session_id: SharedString, cx: &Context<Self>) {
        let id = session_id.to_string();
        match self
            .daemon
            .read(cx)
            .send_cmd(&UnregisterSessionMessage::new(id.clone()))
        {
            Ok(()) => {
                events::record("session_unregister_submitted", json!({"session_id": id}));
            }
            Err(error) => {
                events::record(
                    "session_unregister_send_failed",
                    json!({"session_id": id, "error": error}),
                );
            }
        }
    }

    /// Switch the canvas + sidebar to the given workspace id. Shared by
    /// the sidebar's click callback and the automation `select_workspace`
    /// action so both paths produce identical state. No-op when `id`
    /// doesn't match a known workspace.
    pub fn select_workspace(&mut self, id: SharedString, cx: &mut Context<Self>) {
        self.select_workspace_impl(id, true, cx);
    }

    /// Sidebar click handlers already hold a mutable lease on the
    /// `Sidebar` entity, so this path must not re-enter `sidebar.update`.
    fn select_workspace_from_sidebar(&mut self, id: SharedString, cx: &mut Context<Self>) {
        self.select_workspace_impl(id, false, cx);
    }

    fn select_workspace_impl(
        &mut self,
        id: SharedString,
        sync_sidebar: bool,
        cx: &mut Context<Self>,
    ) {
        let Some(ws) = self.registry.workspace(id.as_ref()) else {
            events::record(
                "workspace_select_missed",
                json!({"id": id.as_ref(), "reason": "unknown_id"}),
            );
            return;
        };
        events::record("workspace_selected", json!({"id": id.as_ref()}));
        let canvas = self.canvas.clone();
        canvas.update(cx, |canvas, cx| canvas.set_selected(Some(ws), cx));
        self.registry.set_selected(Some(id.clone()));
        if sync_sidebar {
            self.sidebar
                .update(cx, |sidebar, cx| sidebar.set_selected(Some(id), cx));
        }
    }

    fn select_workspace_if_pending(&mut self, id: &str, cx: &mut Context<Self>) {
        if self.registry.take_pending_select(id) {
            self.select_workspace(SharedString::from(id.to_string()), cx);
        }
    }

    /// Build a JSON snapshot of everything an external test script needs
    /// to reason about: live wire state, the canvas's local UI state, and
    /// which workspace is currently selected. Shape is the long-term
    /// contract; new fields are added as automation needs grow.
    pub fn automation_snapshot(&self, cx: &App) -> Value {
        let daemon = self.daemon.read(cx);

        let workspaces: Vec<Value> = self
            .registry
            .workspaces()
            .map(|ws| ws.read(cx).automation_snapshot())
            .collect();

        let canvas = self.canvas.read(cx).automation_snapshot();

        json!({
            "selected_workspace_id": self.registry.selected_id().map(|s| s.to_string()),
            "workspaces": workspaces,
            "sessions": serde_json::to_value(self.sessions.snapshot()).unwrap_or(Value::Null),
            "canvas": canvas,
            "daemon": {
                "connected": daemon.connected(),
                "error": daemon.error(),
            },
        })
    }

    /// Reset workspace and session registries to match a fresh
    /// `InitialState` from the daemon. Workspaces missing from the new
    /// list get the same removal fan-out a live `WorkspaceUnregistered`
    /// would, so a daemon restart with fewer workspaces doesn't leave
    /// stale rows in the sidebar. The diff used to live inside
    /// `DaemonClient` (against its private `workspaces` cache) — moving
    /// it here lets the adapter stay cache-free.
    fn apply_initial_state(
        &mut self,
        sessions: Vec<Session>,
        workspaces: Vec<ProtocolWorkspace>,
        cx: &mut Context<Self>,
    ) {
        self.sessions.replace_all(sessions);

        let new_ids: std::collections::HashSet<String> =
            workspaces.iter().map(|w| w.id.clone()).collect();
        let stale: Vec<String> = self
            .registry
            .workspaces()
            .filter_map(|ws_entity| {
                let id = ws_entity.read(cx).id.to_string();
                (!new_ids.contains(&id)).then_some(id)
            })
            .collect();
        for id in stale {
            self.remove_workspace(id, cx);
        }

        for ws in workspaces {
            let ws_id = ws.id.clone();
            self.upsert_workspace(ws, cx);
            self.select_workspace_if_pending(&ws_id, cx);
        }

        self.sync_terminal_panels(cx);
    }

    fn upsert_workspace(&mut self, data: attn_protocol::Workspace, cx: &mut Context<Self>) {
        let id = SharedString::from(data.id.clone());
        if let UpsertOutcome::NewlyInserted(entity) = self.registry.upsert(data, cx) {
            self.on_new_workspace_entity(id, entity, cx);
        }
    }

    fn apply_workspace_snapshot(&mut self, data: attn_protocol::Workspace, cx: &mut Context<Self>) {
        let id = SharedString::from(data.id.clone());
        if let UpsertOutcome::NewlyInserted(entity) = self.registry.apply_snapshot(data, cx) {
            // Snapshot for a workspace we hadn't seen — fan out the same
            // way an explicit registration would (sidebar row +
            // auto-select if first). Daemon ordering says this shouldn't
            // happen but the cost of being defensive is two lines.
            self.on_new_workspace_entity(id, entity, cx);
        }
    }

    /// Sidebar + canvas fan-out when a workspace entity is newly known
    /// to the registry. The first workspace to appear also becomes the
    /// initial selection.
    fn on_new_workspace_entity(
        &mut self,
        id: SharedString,
        entity: Entity<Workspace>,
        cx: &mut Context<Self>,
    ) {
        self.sidebar.update(cx, |sidebar, cx| {
            sidebar.upsert_workspace(entity.clone(), cx)
        });
        if self.registry.selected_id().is_none() {
            self.registry.set_selected(Some(id.clone()));
            self.canvas
                .update(cx, |canvas, cx| canvas.set_selected(Some(entity), cx));
            self.sidebar
                .update(cx, |sidebar, cx| sidebar.set_selected(Some(id), cx));
        }
    }

    fn remove_workspace(&mut self, id: String, cx: &mut Context<Self>) {
        let id = SharedString::from(id);
        if !self.registry.remove(&id) {
            return;
        }
        let id_str = id.clone();
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.remove_workspace(&id_str, cx));
        if self.registry.selected_id() == Some(&id) {
            self.registry.set_selected(None);
            if let Some(next_id) = self.registry.fallback_id(cx) {
                events::record(
                    "workspace_selected_after_destroy",
                    json!({"destroyed_id": id.as_ref(), "selected_id": next_id.as_ref()}),
                );
                self.select_workspace(next_id, cx);
            } else {
                self.canvas
                    .update(cx, |canvas, cx| canvas.set_selected(None, cx));
                self.sidebar
                    .update(cx, |sidebar, cx| sidebar.set_selected(None, cx));
            }
        }
    }

    /// Walk every workspace's Terminal panels and re-issue
    /// `AttachSession` for each. Called on `Connected` so existing
    /// terminals resume receiving PtyOutput after a daemon restart or
    /// any websocket reconnect.
    fn reattach_existing_terminals(&self, cx: &mut Context<Self>) {
        let daemon = self.daemon.read(cx);
        for ws_entity in self.registry.workspaces() {
            for panel in ws_entity.read(cx).panels.iter() {
                let _ = daemon.send_cmd(&AttachSessionMessage::new(panel.session_id.to_string()));
            }
        }
    }

    /// Reconcile each workspace's Terminal panels with the daemon's
    /// current session list: spawn panels for new sessions, drop panels
    /// whose session has gone away. Idempotent.
    fn sync_terminal_panels(&mut self, cx: &mut Context<Self>) {
        let sessions: Vec<Session> = self.sessions.snapshot();
        let live_session_ids: std::collections::HashSet<&str> =
            sessions.iter().map(|s| s.id.as_str()).collect();

        events::record(
            "sync_terminal_panels_start",
            json!({
                "session_count": sessions.len(),
                "workspace_count": self.registry.len(),
            }),
        );

        // Prune Terminal panels whose session is no longer alive on the
        // daemon. Dropping the panel drops the last handle to its
        // TerminalView, which detaches subscriptions for free.
        for ws_entity in self.registry.workspaces().cloned().collect::<Vec<_>>() {
            ws_entity.update(cx, |ws, cx| {
                let workspace_id = ws.id.to_string();
                let before = ws.panels.len();
                ws.panels.retain(|panel| {
                    let alive = live_session_ids.contains(panel.session_id.as_ref());
                    if !alive {
                        events::record(
                            "panel_pruned",
                            json!({
                                "workspace_id": workspace_id.as_str(),
                                "panel_id": panel.id,
                                "session_id": panel.session_id.as_ref(),
                            }),
                        );
                    }
                    alive
                });
                if ws.panels.len() != before {
                    cx.notify();
                }
            });
        }

        for session in sessions {
            let Some(ws_id) = session.workspace_id.as_deref() else {
                continue;
            };
            let Some(ws_entity) = self.registry.workspace(ws_id) else {
                continue;
            };

            let already_present = ws_entity
                .read(cx)
                .panels
                .iter()
                .any(|p| p.session_id.as_ref() == session.id);
            if already_present {
                continue;
            }

            // Find a non-overlapping x position by counting existing
            // terminal panels in this workspace.
            let existing = ws_entity.read(cx).panels.len();
            let world_x = 30.0 + existing as f32 * (TERMINAL_W + 30.0);
            let world_y = 50.0;

            let session_id = session.id.clone();
            let label = session.label.clone();

            // Default cols/rows derived from world-space size; the
            // canvas re-pushes content_size each frame so these will
            // be corrected on first render if needed.
            let (cols, rows) = panel_terminal_dims(TERMINAL_W, TERMINAL_H);

            let daemon = self.daemon.clone();
            let model =
                cx.new(|cx| TerminalModel::new(session_id.clone(), cols, rows, &daemon, cx));
            let view = cx.new(|cx| {
                let mut tv = TerminalView::new(model, daemon.clone(), cx);
                tv.set_content_size(TERMINAL_W, (TERMINAL_H - TITLE_HEIGHT).max(0.0));
                tv
            });

            // Send attach. The TerminalView's render path will emit the
            // initial PtyResize once it sees its first content_size.
            let _ = self
                .daemon
                .read(cx)
                .send_cmd(&AttachSessionMessage::new(session_id.clone()));

            let panel = Panel {
                id: next_panel_id(),
                title: SharedString::from(label),
                world_x,
                world_y,
                width: TERMINAL_W,
                height: TERMINAL_H,
                session_id: SharedString::from(session_id.clone()),
                view,
            };

            let panel_id = panel.id;
            events::record(
                "panel_added",
                json!({
                    "workspace_id": ws_id,
                    "panel_id": panel_id,
                    "session_id": session_id.as_str(),
                    "kind": "terminal",
                }),
            );

            ws_entity.update(cx, |ws, cx| {
                ws.panels.push(panel);
                cx.notify();
            });
        }
    }
}

impl Render for NativeApp {
    fn render(&mut self, _window: &mut Window, _cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .size_full()
            .flex()
            .flex_row()
            .bg(rgb(0x0e0e14))
            .child(self.sidebar.clone())
            .child(div().flex_1().child(self.canvas.clone()))
    }
}

/// Process-wide monotonically-increasing panel ID. Panels are keyed by
/// id for hit testing so collisions across workspaces would mis-target
/// drag/resize.
static NEXT_PANEL_ID: AtomicUsize = AtomicUsize::new(1);

fn next_panel_id() -> usize {
    NEXT_PANEL_ID.fetch_add(1, Ordering::Relaxed)
}

fn panel_terminal_dims(world_w: f32, world_h: f32) -> (u16, u16) {
    use crate::views::terminal_view::{CHAR_WIDTH, ROW_HEIGHT};
    let cols = ((world_w / CHAR_WIDTH) as u16).max(1);
    let rows = (((world_h - TITLE_HEIGHT) / ROW_HEIGHT) as u16).max(1);
    (cols, rows)
}

/// Bring up the UI automation TCP server + dispatch pump. Errors are
/// logged but don't bubble up — automation is a dev/test affordance and
/// shouldn't take down the app if (e.g.) the manifest dir is unwritable.
fn start_automation(cx: &mut Context<NativeApp>) -> Option<automation::server::Handle> {
    let listener = match automation::server::bind() {
        Ok(l) => l,
        Err(error) => {
            eprintln!("[automation] bind failed: {error}");
            return None;
        }
    };

    let manifest_path = automation::manifest_path();
    let (dispatcher, rx) = automation::actions::make_dispatcher();

    // Spawn the wire-protocol layer onto GPUI's background executor. The
    // executor is multi-threaded and `Send`, so the closure stored in
    // the `Spawner` can hand futures to it from anywhere.
    let bg = cx.background_executor().clone();
    let spawner: automation::server::Spawner = Arc::new(move |fut| {
        bg.spawn(fut).detach();
    });

    let handle = match automation::server::start(listener, manifest_path, dispatcher, spawner) {
        Ok(h) => h,
        Err(error) => {
            eprintln!("[automation] start failed: {error}");
            return None;
        }
    };

    // Drive the foreground-side action pump on GPUI's main thread so
    // handlers can read entity state. Detached: it loops until the
    // dispatcher's channel closes (which happens when the server handle
    // is dropped on app shutdown).
    let app_handle = cx.entity().downgrade();
    cx.spawn(async move |_, cx| {
        automation::actions::pump_actions(rx, app_handle, cx.clone()).await;
    })
    .detach();

    eprintln!(
        "[automation] listening — manifest at {}",
        handle.manifest_path().display()
    );
    Some(handle)
}
