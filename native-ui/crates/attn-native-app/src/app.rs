/// Root view. Owns the live `Vec<Entity<Workspace>>` (the authoritative
/// list — sidebar and canvas just hold cloned handles), and subscribes to
/// `DaemonClient` to grow/shrink it as workspaces and sessions appear and
/// vanish on the wire.
///
/// Layout: sidebar pinned left at fixed width, canvas fills the rest.
use std::collections::HashMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use attn_protocol::{AttachSessionMessage, Session};
use gpui::{div, prelude::*, rgb, App, Context, Entity, ParentElement, Render, SharedString, Window};
use serde_json::{json, Value};

use crate::automation;
use crate::automation::events;
use crate::canvas::WorkspaceCanvas;
use crate::daemon_client::{DaemonClient, DaemonEvent};
use crate::panel::{Panel, PanelContent, TITLE_HEIGHT};
use crate::sidebar::Sidebar;
use crate::synthetic::{self, SyntheticSource};
use crate::terminal_model::TerminalModel;
use crate::terminal_view::TerminalView;
use crate::workspace::Workspace;

/// Initial terminal panel size in world-space units. ~380×240 gives
/// ~48 cols × ~12 rows once the title bar is subtracted.
const TERMINAL_W: f32 = 380.0;
const TERMINAL_H: f32 = 240.0;

pub struct NativeApp {
    daemon: Entity<DaemonClient>,
    workspaces_by_id: HashMap<SharedString, Entity<Workspace>>,
    sidebar: Entity<Sidebar>,
    canvas: Entity<WorkspaceCanvas>,
    selected_id: Option<SharedString>,
    /// Live automation server handle. Drop deletes the manifest. `None`
    /// when automation is disabled for this launch (default in prod) or
    /// when bind/start failed — in which case we still want the app to
    /// run, just without the test sidecar.
    _automation: Option<automation::server::Handle>,
    /// Synthetic-load sources, populated when
    /// `ATTN_NATIVE_SYNTHETIC_PANELS=N` is set at startup. Empty in
    /// regular use. Each source pumps deterministic bytes into one
    /// terminal model on a periodic tick (see `synthetic` module).
    synthetic: Vec<SyntheticSource>,
}

impl NativeApp {
    pub fn new(daemon: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        let canvas = cx.new(|cx| WorkspaceCanvas::new(cx));
        let app_handle = cx.entity().downgrade();
        let sidebar = cx.new(|cx| {
            Sidebar::new(
                Vec::new(),
                move |id, _window, cx| {
                    let app_handle = app_handle.clone();
                    let _ = app_handle.update(cx, |app: &mut NativeApp, cx| {
                        app.select_workspace(id, cx);
                    });
                },
                cx,
            )
        });

        cx.subscribe(&daemon, |this, _client, event: &DaemonEvent, cx| match event {
            DaemonEvent::WorkspaceRegistered { workspace } => {
                events::record(
                    "workspace_registered_observed",
                    json!({"workspace_id": workspace.id.as_str()}),
                );
                this.upsert_workspace(workspace.clone(), cx);
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
            }
            DaemonEvent::SessionsChanged => {
                events::record(
                    "sessions_changed_observed",
                    json!({"session_count": this.daemon.read(cx).sessions().len()}),
                );
                this.sync_terminal_panels(cx);
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
        })
        .detach();

        let automation_handle = if automation::automation_enabled() {
            start_automation(cx)
        } else {
            None
        };

        let mut app = Self {
            daemon,
            workspaces_by_id: HashMap::new(),
            sidebar,
            canvas,
            selected_id: None,
            _automation: automation_handle,
            synthetic: Vec::new(),
        };

        if let Some(cfg) = synthetic::config_from_env() {
            app.start_synthetic(cfg, cx);
        }

        app
    }

    /// Apply a zoom level to the canvas, centered on the canvas
    /// midpoint. Used by the automation `set_zoom` action so headless
    /// scripts can drive perf measurements at known zoom levels.
    pub fn set_canvas_zoom(&self, zoom: f32, reset_fps: bool, cx: &mut Context<Self>) {
        let canvas = self.canvas.clone();
        canvas.update(cx, |canvas, cx| canvas.set_zoom_centered(zoom, reset_fps, cx));
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
        self.workspaces_by_id
            .get(&SharedString::from(id.to_string()))
            .cloned()
    }

    /// Iterator over every live workspace handle. Used by automation
    /// actions that need to scan panels across workspaces (e.g.
    /// `read_pane_text` finding the terminal model for a given session).
    pub fn workspaces(&self) -> impl Iterator<Item = &Entity<Workspace>> {
        self.workspaces_by_id.values()
    }

    /// Switch the canvas + sidebar to the given workspace id. Shared by
    /// the sidebar's click callback and the automation `select_workspace`
    /// action so both paths produce identical state. No-op when `id`
    /// doesn't match a known workspace.
    pub fn select_workspace(&mut self, id: SharedString, cx: &mut Context<Self>) {
        let Some(ws) = self.workspaces_by_id.get(&id).cloned() else {
            events::record(
                "workspace_select_missed",
                json!({"id": id.as_ref(), "reason": "unknown_id"}),
            );
            return;
        };
        events::record("workspace_selected", json!({"id": id.as_ref()}));
        let canvas = self.canvas.clone();
        canvas.update(cx, |canvas, cx| canvas.set_selected(Some(ws), cx));
        self.selected_id = Some(id.clone());
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.set_selected(Some(id), cx));
    }

    /// Build a JSON snapshot of everything an external test script needs
    /// to reason about: live wire state, the canvas's local UI state, and
    /// which workspace is currently selected. Shape is the long-term
    /// contract; new fields are added as automation needs grow.
    pub fn automation_snapshot(&self, cx: &App) -> Value {
        let daemon = self.daemon.read(cx);
        let sessions = daemon.sessions();

        let workspaces: Vec<Value> = self
            .workspaces_by_id
            .values()
            .map(|ws| ws.read(cx).automation_snapshot())
            .collect();

        let canvas = self.canvas.read(cx).automation_snapshot();

        json!({
            "selected_workspace_id": self.selected_id.as_ref().map(|s| s.to_string()),
            "workspaces": workspaces,
            "sessions": serde_json::to_value(sessions).unwrap_or(Value::Null),
            "canvas": canvas,
            "daemon": {
                "connected": daemon.connected(),
                "error": daemon.error(),
            },
        })
    }

    fn upsert_workspace(&mut self, data: attn_protocol::Workspace, cx: &mut Context<Self>) {
        let id = SharedString::from(data.id.clone());
        if let Some(existing) = self.workspaces_by_id.get(&id) {
            existing.update(cx, |ws, cx| ws.apply_snapshot(data.clone(), cx));
            return;
        }
        let entity = cx.new(|_| Workspace::new(data, Vec::new()));
        self.workspaces_by_id.insert(id.clone(), entity.clone());
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.upsert_workspace(entity.clone(), cx));

        // First workspace to appear becomes the canvas's initial selection.
        if self.selected_id.is_none() {
            self.selected_id = Some(id.clone());
            self.canvas
                .update(cx, |canvas, cx| canvas.set_selected(Some(entity), cx));
            self.sidebar
                .update(cx, |sidebar, cx| sidebar.set_selected(Some(id), cx));
        }
    }

    fn apply_workspace_snapshot(
        &mut self,
        data: attn_protocol::Workspace,
        cx: &mut Context<Self>,
    ) {
        let id = SharedString::from(data.id.clone());
        if let Some(existing) = self.workspaces_by_id.get(&id) {
            existing.update(cx, |ws, cx| ws.apply_snapshot(data, cx));
        } else {
            // State change for a workspace we haven't seen — treat as a
            // late registration. Daemon ordering says this shouldn't
            // happen, but the cost of being defensive is one line.
            self.upsert_workspace(data, cx);
        }
    }

    fn remove_workspace(&mut self, id: String, cx: &mut Context<Self>) {
        let id = SharedString::from(id);
        if self.workspaces_by_id.remove(&id).is_none() {
            return;
        }
        let id_str = id.clone();
        self.sidebar
            .update(cx, |sidebar, cx| sidebar.remove_workspace(&id_str, cx));
        if self.selected_id.as_ref() == Some(&id) {
            self.selected_id = None;
            self.canvas
                .update(cx, |canvas, cx| canvas.set_selected(None, cx));
        }
    }

    /// Walk every workspace's Terminal panels and re-issue
    /// `AttachSession` for each. Called on `Connected` so existing
    /// terminals resume receiving PtyOutput after a daemon restart or
    /// any websocket reconnect.
    fn reattach_existing_terminals(&self, cx: &mut Context<Self>) {
        let daemon = self.daemon.read(cx);
        for ws_entity in self.workspaces_by_id.values() {
            for panel in ws_entity.read(cx).panels.iter() {
                if let PanelContent::Terminal { session_id, .. } = &panel.content {
                    daemon.send_cmd(&AttachSessionMessage::new(session_id.to_string()));
                }
            }
        }
    }

    /// Reconcile each workspace's Terminal panels with the daemon's
    /// current session list: spawn panels for new sessions, drop panels
    /// whose session has gone away. Idempotent.
    fn sync_terminal_panels(&mut self, cx: &mut Context<Self>) {
        // Snapshot sessions out of the daemon read borrow before we
        // start mutating workspaces.
        let sessions: Vec<Session> = self.daemon.read(cx).sessions().to_vec();
        let live_session_ids: std::collections::HashSet<&str> =
            sessions.iter().map(|s| s.id.as_str()).collect();

        events::record(
            "sync_terminal_panels_start",
            json!({
                "session_count": sessions.len(),
                "workspace_count": self.workspaces_by_id.len(),
            }),
        );

        // Prune Terminal panels whose session is no longer alive on the
        // daemon. Dropping the panel drops the last handle to its
        // TerminalView, which detaches subscriptions for free.
        //
        // Synthetic-mode panels (session_id starts with "synthetic-")
        // are never tracked by the daemon, so they would always look
        // "dead" here — protect them explicitly so the synthetic
        // workspace survives daemon events.
        for ws_entity in self.workspaces_by_id.values().cloned().collect::<Vec<_>>() {
            ws_entity.update(cx, |ws, cx| {
                let workspace_id = ws.id.to_string();
                let before = ws.panels.len();
                ws.panels.retain(|p| match &p.content {
                    PanelContent::Terminal { session_id, .. } => {
                        if session_id.starts_with("synthetic-") {
                            return true;
                        }
                        let alive = live_session_ids.contains(session_id.as_ref());
                        if !alive {
                            events::record(
                                "panel_pruned",
                                json!({
                                    "workspace_id": workspace_id.as_str(),
                                    "panel_id": p.id,
                                    "session_id": session_id.as_ref(),
                                }),
                            );
                        }
                        alive
                    }
                    _ => true,
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
            let key = SharedString::from(ws_id.to_string());
            let Some(ws_entity) = self.workspaces_by_id.get(&key).cloned() else {
                continue;
            };

            let already_present = ws_entity.read(cx).panels.iter().any(|p| matches!(
                &p.content,
                PanelContent::Terminal { session_id, .. } if session_id.as_ref() == session.id
            ));
            if already_present {
                continue;
            }

            // Find a non-overlapping x position by counting existing
            // terminal panels in this workspace.
            let existing = ws_entity
                .read(cx)
                .panels
                .iter()
                .filter(|p| matches!(p.content, PanelContent::Terminal { .. }))
                .count();
            let world_x = 30.0 + existing as f32 * (TERMINAL_W + 30.0);
            let world_y = 50.0;

            let session_id = session.id.clone();
            let label = session.label.clone();

            // Default cols/rows derived from world-space size; the
            // canvas re-pushes content_size each frame so these will
            // be corrected on first render if needed.
            let (cols, rows) = panel_terminal_dims(TERMINAL_W, TERMINAL_H);

            let daemon = self.daemon.clone();
            let model = cx.new(|cx| TerminalModel::new(session_id.clone(), cols, rows, &daemon, cx));
            let view = cx.new(|cx| {
                let mut tv = TerminalView::new(model, daemon.clone(), cx);
                tv.set_content_size(TERMINAL_W, (TERMINAL_H - TITLE_HEIGHT).max(0.0));
                tv
            });

            // Send attach. The TerminalView's render path will emit the
            // initial PtyResize once it sees its first content_size.
            self.daemon
                .read(cx)
                .send_cmd(&AttachSessionMessage::new(session_id.clone()));

            let panel = Panel {
                id: next_panel_id(),
                title: SharedString::from(label),
                world_x,
                world_y,
                width: TERMINAL_W,
                height: TERMINAL_H,
                content: PanelContent::Terminal {
                    session_id: SharedString::from(session_id.clone()),
                    view,
                },
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

    /// Bring up a synthetic workspace + N panels driven by an internal
    /// ticker. Used by the canvas perf harness to characterize rendering
    /// scaling without depending on the daemon. Real daemon-backed
    /// workspaces still register normally and appear alongside.
    fn start_synthetic(&mut self, cfg: synthetic::Config, cx: &mut Context<Self>) {
        let ws_id = "synthetic-load";
        let ws_data = attn_protocol::Workspace {
            id: ws_id.to_string(),
            title: format!("Synthetic Load ({} panels)", cfg.panels),
            directory: "/synthetic".to_string(),
            status: attn_protocol::WorkspaceStatus::Working,
        };
        self.upsert_workspace(ws_data, cx);

        let Some(ws_entity) = self
            .workspaces_by_id
            .get(&SharedString::from(ws_id.to_string()))
            .cloned()
        else {
            return;
        };

        // Lay panels out in a square-ish grid with a fixed gap so the
        // canvas isn't a stack of overlapping rects.
        let cols = (cfg.panels as f32).sqrt().ceil() as usize;
        let gap = 30.0_f32;

        for i in 0..cfg.panels {
            let row = i / cols;
            let col = i % cols;
            let world_x = 30.0 + col as f32 * (TERMINAL_W + gap);
            let world_y = 50.0 + row as f32 * (TERMINAL_H + gap);

            let session_id = format!("synthetic-{i:02}");
            let label = format!("synthetic-{i:02}");

            let (cterm, rterm) = panel_terminal_dims(TERMINAL_W, TERMINAL_H);
            let daemon = self.daemon.clone();
            let model = cx.new(|cx| TerminalModel::new(session_id.clone(), cterm, rterm, &daemon, cx));
            let view = cx.new(|cx| {
                let mut tv = TerminalView::new(model.clone(), daemon.clone(), cx);
                tv.set_content_size(TERMINAL_W, (TERMINAL_H - TITLE_HEIGHT).max(0.0));
                tv
            });

            let panel = Panel {
                id: next_panel_id(),
                title: SharedString::from(label),
                world_x,
                world_y,
                width: TERMINAL_W,
                height: TERMINAL_H,
                content: PanelContent::Terminal {
                    session_id: SharedString::from(session_id),
                    view,
                },
            };

            ws_entity.update(cx, |ws, cx| {
                ws.panels.push(panel);
                cx.notify();
            });

            self.synthetic
                .push(SyntheticSource::new(model, i, cfg.bytes_per_tick));
        }

        // Ticker: pump every source on a fixed cadence. Detached — runs
        // until app shutdown drops the WeakEntity. `None` means static
        // mode: panels are created but never re-rendered until user
        // input (used to isolate scroll-handler cost from byte-stream
        // cost during perf diagnosis).
        if let Some(tick) = cfg.tick {
            let app_handle = cx.entity().downgrade();
            cx.spawn(async move |_, cx| {
                loop {
                    smol::Timer::after(tick).await;
                    let updated = app_handle.update(
                        cx,
                        |app: &mut NativeApp, app_cx: &mut Context<NativeApp>| {
                            for src in &mut app.synthetic {
                                src.tick(app_cx);
                            }
                        },
                    );
                    if updated.is_err() {
                        // App was dropped — bail out so we don't spin.
                        break;
                    }
                }
            })
            .detach();
        }

        eprintln!(
            "[synthetic] {} panels, tick={}, bytes/tick={}",
            cfg.panels,
            cfg.tick
                .map(|t| format!("{:?}", t))
                .unwrap_or_else(|| "off (static)".into()),
            cfg.bytes_per_tick
        );
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
    use crate::terminal_view::{CHAR_WIDTH, ROW_HEIGHT};
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
