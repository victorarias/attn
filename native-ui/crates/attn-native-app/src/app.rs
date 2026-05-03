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
    UnregisterSessionMessage, UnregisterWorkspaceMessage, UpdateWorkspacePanelGeometryMessage,
    Workspace as ProtocolWorkspace,
};
use gpui::{
    div, point, prelude::*, px, App, Context, Entity, Focusable, ParentElement, Render,
    SharedString, Subscription, WeakEntity, Window,
};
use serde_json::{json, Value};

use crate::adapters::automation;
use crate::adapters::automation::actions::generate_workspace_id;
use crate::adapters::automation::events;
use crate::adapters::daemon::{DaemonClient, DaemonEvent};
use crate::adapters::trackpad_zoom::{self, TrackpadZoomEvent};
use crate::domain::panel_placement::{
    place_panel, AdjacentPanelDirection, PanelPlacementItem, PanelSize, Rect,
};
use crate::state::panel::{Panel, TITLE_HEIGHT};
use crate::state::session_registry::SessionRegistry;
use crate::state::terminal_model::TerminalModel;
use crate::state::workspace::Workspace;
use crate::state::workspace_registry::{PendingSpawn, UpsertOutcome, WorkspaceRegistry};
use crate::theme;
use crate::views::canvas::WorkspaceCanvas;
use crate::views::location_dialog::{LocationDialog, LocationDialogMode, LocationDialogOutcome};
use crate::views::settings_page::SettingsPage;
use crate::views::sidebar::Sidebar;
use crate::views::terminal_view::TerminalView;

/// Initial terminal panel size in world-space units. ~720×560 gives
/// ~92 cols × ~31 rows once the title bar is subtracted.
const TERMINAL_W: f32 = 720.0;
const TERMINAL_H: f32 = 560.0;

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
    location_dialog: Option<Entity<LocationDialog>>,
    settings_page: Option<Entity<SettingsPage>>,
    /// True from the moment the location dialog closes until the next
    /// render restores focus to the canvas. Without this hand-off, focus
    /// stays on the dialog's dropped focus handle and global shortcuts
    /// like Cmd+N stop firing because no element is receiving key events.
    canvas_needs_refocus: bool,
    _canvas_subscription: Subscription,
    /// Live automation server handle. Drop deletes the manifest. `None`
    /// when automation is disabled for this launch (default in prod) or
    /// when bind/start failed — in which case we still want the app to
    /// run, just without the test sidecar.
    _automation: Option<automation::server::Handle>,
    /// Native macOS magnify gesture bridge. GPUI 0.2 does not surface this
    /// event type, so the adapter forwards it into the canvas explicitly.
    _trackpad_zoom: Option<trackpad_zoom::Handle>,
}

pub struct PanelGeometryUpdate {
    pub workspace_id: SharedString,
    pub panel_id: SharedString,
    pub world_x: f32,
    pub world_y: f32,
    pub width: f32,
    pub height: f32,
}

impl NativeApp {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        let app_handle = cx.entity().downgrade();
        let app_handle_canvas_spawn = app_handle.clone();
        let app_handle_canvas_close = app_handle.clone();
        let app_handle_canvas_split = app_handle.clone();
        let app_handle_canvas_geometry = app_handle.clone();
        let app_handle_canvas_sidebar = app_handle.clone();
        let app_handle_canvas_settings = app_handle.clone();
        let (trackpad_tx, trackpad_rx) = async_channel::unbounded();
        let trackpad_zoom = trackpad_zoom::install(trackpad_tx);
        let canvas = cx.new(|cx| {
            WorkspaceCanvas::new(
                cx,
                move |workspace_id, agent, _window, cx| {
                    let app_handle = app_handle_canvas_spawn.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.open_session_dialog(workspace_id, agent, cx);
                    });
                },
                move |session_id, _window, cx| {
                    let app_handle = app_handle_canvas_close.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.unregister_session_by_id(session_id, cx);
                    });
                },
                move |workspace_id, anchor_session_id, direction, placement, _window, cx| {
                    let app_handle = app_handle_canvas_split.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        let _ = app.spawn_shell_split_in_workspace(
                            workspace_id,
                            anchor_session_id,
                            direction,
                            placement,
                            cx,
                        );
                    });
                },
                move |workspace_id, panel_id, world_x, world_y, width, height, _window, cx| {
                    let app_handle = app_handle_canvas_geometry.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        let _ = app.update_workspace_panel_geometry(
                            PanelGeometryUpdate {
                                workspace_id,
                                panel_id,
                                world_x,
                                world_y,
                                width,
                                height,
                            },
                            cx,
                        );
                    });
                },
                move |_window, cx| {
                    let app_handle = app_handle_canvas_sidebar.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.toggle_sidebar_collapsed(cx);
                    });
                },
                move |_window, cx| {
                    let app_handle = app_handle_canvas_settings.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.open_settings_page_from_app(cx);
                    });
                },
            )
        });
        let canvas_subscription = cx.observe(&canvas, |_, _, cx| cx.notify());
        let app_handle_create = app_handle.clone();
        let app_handle_settings = app_handle.clone();
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
                        app.open_workspace_dialog(cx);
                    });
                },
                move |sidebar_collapsed, _window, cx| {
                    let app_handle = app_handle_settings.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.open_settings_page(sidebar_collapsed, cx);
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

        cx.subscribe(&daemon, |this, _client, event: &DaemonEvent, cx| {
            this.forward_location_dialog_event(event, cx);
            match event {
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
                    this.sync_terminal_panels(cx);
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
                        if let Some(spawn) = pending.clone() {
                            this.registry.mark_spawn_succeeded(key, spawn);
                        }
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
            }
        })
        .detach();

        let automation_handle = if automation::automation_enabled() {
            start_automation(cx)
        } else {
            None
        };
        let weak_app = cx.weak_entity();
        let mut async_cx = cx.to_async();
        cx.foreground_executor()
            .spawn(async move {
                while let Ok(event) = trackpad_rx.recv().await {
                    let Some(app) = weak_app.upgrade() else {
                        break;
                    };
                    let _ = app.update(&mut async_cx, |app: &mut NativeApp, cx| {
                        app.handle_trackpad_zoom(event, cx);
                    });
                }
            })
            .detach();

        Self {
            daemon,
            registry: WorkspaceRegistry::new(),
            sessions: SessionRegistry::new(),
            sidebar,
            canvas,
            location_dialog: None,
            settings_page: None,
            canvas_needs_refocus: false,
            _canvas_subscription: canvas_subscription,
            _automation: automation_handle,
            _trackpad_zoom: trackpad_zoom,
        }
    }

    fn handle_trackpad_zoom(&mut self, event: TrackpadZoomEvent, cx: &mut Context<Self>) {
        let position = point(px(event.window_x), px(event.window_y));
        let factor = crate::views::canvas::magnify_zoom_factor(event.magnification);
        self.canvas.update(cx, |canvas, cx| {
            canvas.zoom_at_window_position(position, factor, cx);
        });
    }

    pub fn set_sidebar_collapsed(&mut self, collapsed: bool, cx: &mut Context<Self>) -> bool {
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.set_collapsed(collapsed, cx));
        cx.notify();
        collapsed
    }

    pub fn toggle_sidebar_collapsed(&mut self, cx: &mut Context<Self>) -> bool {
        let collapsed = self
            .sidebar
            .update(cx, |sidebar, cx| sidebar.toggle_collapsed(cx));
        cx.notify();
        collapsed
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

    /// Select a canvas panel by session id. When `input_focus` is true,
    /// keyboard input routes to the panel's terminal; otherwise the panel
    /// is selected at the canvas level and keybindings stay with the canvas.
    pub fn set_canvas_panel_focus_by_session(
        &self,
        session_id: &str,
        input_focus: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        let canvas = self.canvas.clone();
        canvas.update(cx, |canvas, cx| {
            canvas.set_panel_focus_by_session(session_id, input_focus, window, cx)
        })
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

    pub fn sidebar_entity(&self) -> Entity<Sidebar> {
        self.sidebar.clone()
    }

    pub fn canvas_entity(&self) -> Entity<WorkspaceCanvas> {
        self.canvas.clone()
    }

    pub fn settings_page_entity(&self) -> Option<Entity<SettingsPage>> {
        self.settings_page.clone()
    }

    fn open_session_dialog(
        &mut self,
        workspace_id: SharedString,
        initial_agent: SharedString,
        cx: &mut Context<Self>,
    ) {
        let Some(ws) = self.registry.workspace(workspace_id.as_ref()) else {
            events::record(
                "session_dialog_open_failed",
                json!({"workspace_id": workspace_id.as_ref(), "reason": "unknown_workspace"}),
            );
            return;
        };
        let initial_directory = ws.read(cx).directory.clone();
        let app_handle = cx.entity().downgrade();
        let app_handle_submit = app_handle.clone();
        let app_handle_close = app_handle.clone();
        let daemon = self.daemon.clone();
        let dialog = cx.new(|cx| {
            LocationDialog::new(
                LocationDialogMode::NewSession {
                    workspace_id: workspace_id.clone(),
                    initial_directory,
                    initial_agent,
                },
                daemon,
                move |outcome, cx| {
                    schedule_location_dialog_submit(app_handle_submit.clone(), outcome, cx)
                },
                move |cx| {
                    if let Some(app) = app_handle_close.upgrade() {
                        app.update(cx, |app: &mut NativeApp, cx| {
                            app.location_dialog = None;
                            app.canvas_needs_refocus = true;
                            cx.notify();
                        });
                    }
                },
                cx,
            )
        });
        self.location_dialog = Some(dialog);
        cx.notify();
    }

    fn open_workspace_dialog(&mut self, cx: &mut Context<Self>) {
        let initial_directory = self
            .registry
            .selected_id()
            .and_then(|id| self.registry.workspace(id.as_ref()))
            .map(|ws| ws.read(cx).directory.clone());
        let app_handle = cx.entity().downgrade();
        let app_handle_submit = app_handle.clone();
        let app_handle_close = app_handle.clone();
        let daemon = self.daemon.clone();
        let dialog = cx.new(|cx| {
            LocationDialog::new(
                LocationDialogMode::NewWorkspace { initial_directory },
                daemon,
                move |outcome, cx| {
                    schedule_location_dialog_submit(app_handle_submit.clone(), outcome, cx)
                },
                move |cx| {
                    if let Some(app) = app_handle_close.upgrade() {
                        app.update(cx, |app: &mut NativeApp, cx| {
                            app.location_dialog = None;
                            app.canvas_needs_refocus = true;
                            cx.notify();
                        });
                    }
                },
                cx,
            )
        });
        self.location_dialog = Some(dialog);
        cx.notify();
    }

    fn open_settings_page(&mut self, sidebar_collapsed: bool, cx: &mut Context<Self>) {
        let app_handle = cx.entity().downgrade();
        let app_handle_close = app_handle.clone();
        let app_handle_toggle = app_handle.clone();
        let settings_page = cx.new(|cx| {
            SettingsPage::new(
                sidebar_collapsed,
                move |_window, cx| {
                    if let Some(app) = app_handle_close.upgrade() {
                        app.update(cx, |app: &mut NativeApp, cx| {
                            app.settings_page = None;
                            app.canvas_needs_refocus = true;
                            cx.notify();
                        });
                    }
                },
                move |_window, cx| {
                    app_handle_toggle
                        .update(cx, |app: &mut NativeApp, cx| {
                            let collapsed = !app.sidebar.read(cx).is_collapsed();
                            app.set_sidebar_collapsed(collapsed, cx)
                        })
                        .unwrap_or(sidebar_collapsed)
                },
                cx,
            )
        });
        self.settings_page = Some(settings_page);
        cx.notify();
    }

    fn open_settings_page_from_app(&mut self, cx: &mut Context<Self>) {
        let sidebar_collapsed = self.sidebar.read(cx).is_collapsed();
        self.open_settings_page(sidebar_collapsed, cx);
    }

    fn forward_location_dialog_event(&mut self, event: &DaemonEvent, cx: &mut Context<Self>) {
        if let Some(dialog) = self.location_dialog.clone() {
            dialog.update(cx, |dialog, cx| dialog.handle_daemon_event(event, cx));
        }
    }

    fn handle_location_dialog_outcome(
        &mut self,
        outcome: LocationDialogOutcome,
        cx: &mut Context<Self>,
    ) -> Result<(), String> {
        match outcome {
            LocationDialogOutcome::SpawnSession {
                workspace_id,
                directory,
                agent,
            } => {
                self.spawn_session_in_workspace_at(workspace_id, directory, agent, cx)?;
            }
            LocationDialogOutcome::RegisterWorkspace { directory } => {
                let title = directory_title(&directory);
                let id = generate_workspace_id();
                self.register_workspace_and_select(id.clone(), title, directory.clone(), cx)?;
                events::record(
                    "workspace_create_submitted",
                    json!({"id": id.as_str(), "directory": directory.as_str()}),
                );
            }
        }
        Ok(())
    }

    fn close_location_dialog_after_submit(&mut self, cx: &mut Context<Self>) {
        self.location_dialog = None;
        self.canvas_needs_refocus = true;
        cx.notify();
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
        self.spawn_session_in_workspace_at(workspace_id, directory, agent, cx)
    }

    pub fn spawn_shell_split_in_workspace(
        &mut self,
        workspace_id: SharedString,
        anchor_session_id: SharedString,
        direction: AdjacentPanelDirection,
        placement: Rect,
        cx: &mut Context<Self>,
    ) -> Result<SharedString, String> {
        let Some(anchor_session) = self.sessions.get(anchor_session_id.as_ref()) else {
            events::record(
                "shell_split_failed",
                json!({
                    "workspace_id": workspace_id.as_ref(),
                    "anchor_session_id": anchor_session_id.as_ref(),
                    "reason": "unknown_anchor_session",
                }),
            );
            return Err(format!("unknown anchor session: {anchor_session_id}"));
        };
        let directory = anchor_session.directory.clone();
        events::record(
            "shell_split_submitted",
            json!({
                "workspace_id": workspace_id.as_ref(),
                "anchor_session_id": anchor_session_id.as_ref(),
                "direction": adjacent_direction_name(direction),
                "directory": directory.as_str(),
                "world_x": placement.x,
                "world_y": placement.y,
                "width": placement.width,
                "height": placement.height,
            }),
        );
        self.spawn_session_in_workspace_at_with_initial_placement(
            workspace_id,
            directory,
            SharedString::from("shell"),
            Some(placement),
            true,
            cx,
        )
    }

    pub fn spawn_session_in_workspace_at(
        &mut self,
        workspace_id: SharedString,
        directory: String,
        agent: SharedString,
        cx: &mut Context<Self>,
    ) -> Result<SharedString, String> {
        self.spawn_session_in_workspace_at_with_initial_placement(
            workspace_id,
            directory,
            agent,
            None,
            false,
            cx,
        )
    }

    fn spawn_session_in_workspace_at_with_initial_placement(
        &mut self,
        workspace_id: SharedString,
        directory: String,
        agent: SharedString,
        initial_placement: Option<Rect>,
        focus_after_spawn: bool,
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
        let _ = ws_entity;
        let session_id = automation::actions::generate_workspace_id();
        let (terminal_w, terminal_h) = initial_placement
            .map(|placement| (placement.width, placement.height))
            .unwrap_or((TERMINAL_W, TERMINAL_H));
        let (cols, rows) = panel_terminal_dims(terminal_w, terminal_h);
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
                initial_placement,
                focus_after_spawn,
            },
        );
        events::record(
            "session_spawn_submitted",
            json!({
                "session_id": session_id,
                "workspace_id": workspace_id.as_ref(),
                "agent": agent.as_ref(),
                "directory": directory,
                "initial_placement": initial_placement.map(|placement| json!({
                    "world_x": placement.x,
                    "world_y": placement.y,
                    "width": placement.width,
                    "height": placement.height,
                })),
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

    /// Commit a panel's final drag/resize geometry to the daemon. The
    /// canvas may already have applied this locally for pointer
    /// responsiveness; the daemon snapshot remains the durable owner and
    /// will reconcile the local panel on broadcast/reconnect.
    pub fn update_workspace_panel_geometry(
        &self,
        update: PanelGeometryUpdate,
        cx: &Context<Self>,
    ) -> Result<(), String> {
        let workspace_id_string = update.workspace_id.to_string();
        let panel_id_string = update.panel_id.to_string();
        let msg = UpdateWorkspacePanelGeometryMessage::new(
            workspace_id_string.clone(),
            panel_id_string.clone(),
            Some(update.world_x),
            Some(update.world_y),
            Some(update.width),
            Some(update.height),
        );
        match self.daemon.read(cx).send_cmd(&msg) {
            Ok(()) => {
                events::record(
                    "panel_geometry_update_submitted",
                    json!({
                        "workspace_id": workspace_id_string,
                        "panel_id": panel_id_string,
                        "world_x": update.world_x,
                        "world_y": update.world_y,
                        "width": update.width,
                        "height": update.height,
                    }),
                );
                Ok(())
            }
            Err(error) => {
                events::record(
                    "panel_geometry_update_send_failed",
                    json!({
                        "workspace_id": update.workspace_id.as_ref(),
                        "panel_id": update.panel_id.as_ref(),
                        "error": error,
                    }),
                );
                Err(error)
            }
        }
    }

    fn placement_for_new_panel(
        &self,
        ws_entity: &Entity<Workspace>,
        cx: &Context<Self>,
    ) -> Option<Rect> {
        let ws = ws_entity.read(cx);
        if self.registry.selected_id() != Some(&ws.id) {
            return None;
        }
        let frame = self.canvas.read(cx).placement_frame();
        let existing: Vec<PanelPlacementItem> = ws
            .panels
            .iter()
            .map(|panel| PanelPlacementItem {
                id: panel.id,
                rect: Rect {
                    x: panel.world_x,
                    y: panel.world_y,
                    width: panel.width,
                    height: panel.height,
                },
            })
            .collect();
        Some(place_panel(
            &existing,
            frame.selected_panel,
            frame.visible,
            PanelSize {
                width: TERMINAL_W,
                height: TERMINAL_H,
            },
        ))
    }

    fn persist_initial_panel_placement(
        &self,
        workspace_id: SharedString,
        panel_id: SharedString,
        placement: Rect,
        pending: &PendingSpawn,
        session_id: &str,
        cx: &Context<Self>,
    ) {
        let result = self
            .daemon
            .read(cx)
            .send_cmd(&UpdateWorkspacePanelGeometryMessage::new(
                workspace_id.to_string(),
                panel_id.to_string(),
                Some(placement.x),
                Some(placement.y),
                Some(placement.width),
                Some(placement.height),
            ));
        match result {
            Ok(()) => events::record(
                "panel_initial_placement_submitted",
                json!({
                    "workspace_id": workspace_id.as_ref(),
                    "panel_id": panel_id.as_ref(),
                    "session_id": session_id,
                    "agent": pending.agent.as_ref(),
                    "world_x": placement.x,
                    "world_y": placement.y,
                    "width": placement.width,
                    "height": placement.height,
                }),
            ),
            Err(error) => events::record(
                "panel_initial_placement_send_failed",
                json!({
                    "workspace_id": workspace_id.as_ref(),
                    "panel_id": panel_id.as_ref(),
                    "session_id": session_id,
                    "agent": pending.agent.as_ref(),
                    "error": error,
                }),
            ),
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
        let sidebar = self.sidebar.read(cx).automation_snapshot();

        json!({
            "selected_workspace_id": self.registry.selected_id().map(|s| s.to_string()),
            "workspaces": workspaces,
            "sessions": serde_json::to_value(self.sessions.snapshot()).unwrap_or(Value::Null),
            "canvas": canvas,
            "sidebar": sidebar,
            "settings_open": self.settings_page.is_some(),
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

    /// Reconcile each workspace's Terminal panels with the daemon-owned
    /// panel list. Sessions are only used to hydrate TerminalView state
    /// for panels the daemon explicitly includes in the workspace
    /// snapshot.
    fn sync_terminal_panels(&mut self, cx: &mut Context<Self>) {
        let sessions: Vec<Session> = self.sessions.snapshot();
        let sessions_by_id: std::collections::HashMap<String, Session> = sessions
            .iter()
            .map(|session| (session.id.clone(), session.clone()))
            .collect();

        events::record(
            "sync_terminal_panels_start",
            json!({
                "session_count": sessions.len(),
                "workspace_count": self.registry.len(),
            }),
        );

        for ws_entity in self.registry.workspaces().cloned().collect::<Vec<_>>() {
            let desired_panels = ws_entity.read(cx).daemon_panels.clone();
            let desired_ids: std::collections::HashSet<String> = desired_panels
                .iter()
                .filter(|panel| {
                    panel.kind == "terminal" && sessions_by_id.contains_key(&panel.session_id)
                })
                .map(|panel| panel.id.clone())
                .collect();

            ws_entity.update(cx, |ws, cx| {
                let workspace_id = ws.id.to_string();
                let before = ws.panels.len();
                ws.panels.retain(|panel| {
                    let alive = desired_ids.contains(panel.daemon_panel_id.as_ref());
                    if !alive {
                        events::record(
                            "panel_pruned",
                            json!({
                                "workspace_id": workspace_id.as_str(),
                                "panel_id": panel.id,
                                "daemon_panel_id": panel.daemon_panel_id.as_ref(),
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

            for daemon_panel in desired_panels {
                if daemon_panel.kind != "terminal" {
                    continue;
                }
                let Some(session) = sessions_by_id.get(&daemon_panel.session_id) else {
                    continue;
                };

                let already_present = ws_entity
                    .read(cx)
                    .panels
                    .iter()
                    .any(|p| p.daemon_panel_id.as_ref() == daemon_panel.id);
                if already_present {
                    ws_entity.update(cx, |ws, cx| {
                        if let Some(panel) = ws
                            .panels
                            .iter_mut()
                            .find(|p| p.daemon_panel_id.as_ref() == daemon_panel.id)
                        {
                            panel.title = SharedString::from(daemon_panel.title.clone());
                            panel.world_x = daemon_panel.world_x;
                            panel.world_y = daemon_panel.world_y;
                            panel.width = daemon_panel.width;
                            panel.height = daemon_panel.height;
                            panel.session_state = session.state;
                            panel.needs_review_after_long_run =
                                session.needs_review_after_long_run.unwrap_or(false);
                            cx.notify();
                        }
                    });
                    continue;
                }

                let session_id = session.id.clone();
                let label = if daemon_panel.title.is_empty() {
                    session.label.clone()
                } else {
                    daemon_panel.title.clone()
                };

                let session_key = SharedString::from(session_id.clone());
                let mut world_x = daemon_panel.world_x;
                let mut world_y = daemon_panel.world_y;
                let mut width = daemon_panel.width;
                let mut height = daemon_panel.height;
                let mut focus_after_spawn = false;
                if let Some(pending) = self
                    .registry
                    .pending_spawn_for_panel_placement(&session_key)
                    .filter(|pending| {
                        pending.workspace_id.as_ref() == ws_entity.read(cx).id.as_ref()
                    })
                {
                    if let Some(placement) = pending
                        .initial_placement
                        .or_else(|| self.placement_for_new_panel(&ws_entity, cx))
                    {
                        world_x = placement.x;
                        world_y = placement.y;
                        width = placement.width;
                        height = placement.height;
                        self.persist_initial_panel_placement(
                            ws_entity.read(cx).id.clone(),
                            SharedString::from(daemon_panel.id.clone()),
                            placement,
                            &pending,
                            &session_id,
                            cx,
                        );
                    }
                    focus_after_spawn = pending.focus_after_spawn;
                    self.registry.mark_panel_placed(session_key.clone());
                }

                let (cols, rows) = panel_terminal_dims(width, height);

                let daemon = self.daemon.clone();
                let model =
                    cx.new(|cx| TerminalModel::new(session_id.clone(), cols, rows, &daemon, cx));
                let view = cx.new(|cx| {
                    let mut tv = TerminalView::new(model, daemon.clone(), cx);
                    tv.set_content_size(width, (height - TITLE_HEIGHT).max(0.0));
                    tv
                });

                // Send attach. The TerminalView's render path will emit
                // the initial PtyResize once it sees its first content_size.
                let _ = self
                    .daemon
                    .read(cx)
                    .send_cmd(&AttachSessionMessage::new(session_id.clone()));

                let panel = Panel {
                    id: next_panel_id(),
                    daemon_panel_id: SharedString::from(daemon_panel.id.clone()),
                    title: SharedString::from(label),
                    world_x,
                    world_y,
                    width,
                    height,
                    session_id: SharedString::from(session_id.clone()),
                    session_state: session.state,
                    needs_review_after_long_run: session
                        .needs_review_after_long_run
                        .unwrap_or(false),
                    view,
                };

                let panel_id = panel.id;
                events::record(
                    "panel_added",
                    json!({
                        "workspace_id": ws_entity.read(cx).id.as_ref(),
                        "panel_id": panel_id,
                        "daemon_panel_id": daemon_panel.id.as_str(),
                        "session_id": session_id.as_str(),
                        "kind": "terminal",
                    }),
                );

                ws_entity.update(cx, |ws, cx| {
                    ws.panels.push(panel);
                    cx.notify();
                });
                if focus_after_spawn {
                    self.canvas.update(cx, |canvas, cx| {
                        canvas.focus_panel_by_session_on_next_render(
                            SharedString::from(session_id),
                            cx,
                        )
                    });
                }
            }
        }
    }
}

fn schedule_location_dialog_submit(
    app_handle: WeakEntity<NativeApp>,
    outcome: LocationDialogOutcome,
    cx: &mut App,
) -> Result<(), String> {
    if app_handle.upgrade().is_none() {
        return Err("NativeApp entity dropped".to_string());
    }

    cx.defer(move |cx| {
        let Some(app_handle) = app_handle.upgrade() else {
            return;
        };
        app_handle.update(cx, |app: &mut NativeApp, cx| {
            match app.handle_location_dialog_outcome(outcome, cx) {
                Ok(()) => app.close_location_dialog_after_submit(cx),
                Err(error) => {
                    if let Some(dialog) = app.location_dialog.clone() {
                        dialog.update(cx, |dialog, cx| dialog.set_error(error, cx));
                    }
                }
            }
        });
    });
    Ok(())
}

impl Render for NativeApp {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if self.canvas_needs_refocus && self.location_dialog.is_none() {
            self.canvas_needs_refocus = false;
            self.canvas.read(cx).focus_handle(cx).focus(window);
        }
        let mut root = div()
            .size_full()
            .flex()
            .flex_row()
            .bg(theme::ink::midnight());
        root = if self.canvas.read(cx).is_panel_fullscreen() {
            root.child(div().flex_1().overflow_hidden().child(self.canvas.clone()))
        } else {
            root.child(self.sidebar.clone())
                .child(div().flex_1().overflow_hidden().child(self.canvas.clone()))
        };
        if let Some(dialog) = self.location_dialog.clone() {
            root = root.child(dialog);
        }
        if let Some(settings_page) = self.settings_page.clone() {
            root = root.child(settings_page);
        }
        root
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

fn directory_title(directory: &str) -> String {
    std::path::Path::new(directory)
        .file_name()
        .map(|s| s.to_string_lossy().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| directory.to_string())
}

fn adjacent_direction_name(direction: AdjacentPanelDirection) -> &'static str {
    match direction {
        AdjacentPanelDirection::Right => "right",
        AdjacentPanelDirection::Bottom => "bottom",
    }
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
