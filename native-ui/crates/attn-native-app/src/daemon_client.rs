use attn_protocol::{
    ClientHelloMessage, ServerEvent, Session, Workspace, CAPABILITY_SHELL_AS_SESSION,
    PROTOCOL_VERSION,
};
use futures_util::{SinkExt, StreamExt};
use gpui::{AsyncApp, Context, EventEmitter, WeakEntity};
use serde::Serialize;
use serde_json::json;

use crate::automation::events;

const DEFAULT_DAEMON_WS_URL: &str = "ws://localhost:9849/ws";

/// WebSocket URL the daemon client connects to. Defaults to the prod port
/// (9849); override with `ATTN_WS_URL=ws://localhost:29849/ws` to point at
/// the dev daemon (`make dev`) during attn-on-attn testing.
fn daemon_ws_url() -> String {
    std::env::var("ATTN_WS_URL").unwrap_or_else(|_| DEFAULT_DAEMON_WS_URL.to_string())
}

/// Events emitted by DaemonClient to subscribers.
#[derive(Debug, Clone)]
pub enum DaemonEvent {
    Connected,
    Disconnected,
    SessionsChanged,
    /// A workspace appeared (or the InitialState batch arrived). Carries the
    /// snapshot at registration time.
    WorkspaceRegistered { workspace: Workspace },
    /// A workspace was removed (cascade-closed by the daemon).
    WorkspaceUnregistered { workspace_id: String },
    /// A workspace's rolled-up status changed. Carries the fresh snapshot.
    WorkspaceStateChanged { workspace: Workspace },
    /// Raw PTY output for a specific session. Delivered directly so terminal
    /// models can subscribe without triggering full workspace re-renders.
    PtyOutput { session_id: String, data: String, seq: i32 },
    /// Attach result for a session — contains screen snapshot and replay data.
    AttachResult { session_id: String, msg: Box<attn_protocol::AttachResultMessage> },
    /// PTY desync: client should re-attach.
    PtyDesync { session_id: String },
    /// PTY was resized by another client.
    PtyResized { session_id: String, cols: u16, rows: u16 },
    /// Session process exited.
    SessionExited { session_id: String, #[allow(dead_code)] exit_code: i32 },
}

pub struct DaemonClient {
    sessions: Vec<Session>,
    workspaces: Vec<Workspace>,
    connected: bool,
    error: Option<String>,
    /// Channel sender for outbound commands. None when not connected.
    cmd_tx: Option<async_channel::Sender<String>>,
}

impl EventEmitter<DaemonEvent> for DaemonClient {}

impl DaemonClient {
    pub fn new(cx: &mut Context<Self>) -> Self {
        let url = daemon_ws_url();
        cx.spawn(async move |this: WeakEntity<DaemonClient>, cx: &mut AsyncApp| {
            loop {
                let connect_result =
                    async_tungstenite::async_std::connect_async(url.as_str()).await;

                match connect_result {
                    Ok((ws_stream, _)) => {
                        // Create a channel for outbound messages.
                        let (cmd_tx, cmd_rx) = async_channel::unbounded::<String>();

                        // Identify ourselves before any other command. Daemon
                        // gates per-client behavior on these capabilities;
                        // shell_as_session is what makes spawned shells appear
                        // as canvas panels (the Tauri app, which doesn't send
                        // hello, retains the legacy "shell = utility terminal"
                        // behavior).
                        let hello = ClientHelloMessage::new(
                            "native-canvas",
                            env!("CARGO_PKG_VERSION"),
                            vec![CAPABILITY_SHELL_AS_SESSION.to_string()],
                        );
                        if let Ok(text) = serde_json::to_string(&hello) {
                            let _ = cmd_tx.try_send(text);
                        }

                        let update_ok =
                            this.update(cx, |client: &mut DaemonClient, cx: &mut Context<DaemonClient>| {
                                client.connected = true;
                                client.error = None;
                                client.cmd_tx = Some(cmd_tx);
                                cx.emit(DaemonEvent::Connected);
                                cx.notify();
                            });
                        if update_ok.is_err() {
                            return;
                        }

                        let (mut write, mut read) = ws_stream.split();

                        // Writer task: drain cmd_rx → websocket write.
                        smol::spawn(async move {
                            while let Ok(msg) = cmd_rx.recv().await {
                                if write
                                    .send(async_tungstenite::tungstenite::Message::Text(
                                        msg.into(),
                                    ))
                                    .await
                                    .is_err()
                                {
                                    break;
                                }
                            }
                        })
                        .detach();

                        // Reader loop: incoming websocket messages → entity events.
                        while let Some(msg) = read.next().await {
                            match msg {
                                Ok(async_tungstenite::tungstenite::Message::Text(text)) => {
                                    let event = match ServerEvent::parse(&text) {
                                        Ok(e) => e,
                                        Err(_) => continue,
                                    };
                                    let should_break = this
                                        .update(cx, |client: &mut DaemonClient, cx: &mut Context<DaemonClient>| {
                                            client.handle_event(event, cx);
                                        })
                                        .is_err();
                                    if should_break {
                                        return;
                                    }
                                }
                                Err(_) => break,
                                _ => {}
                            }
                        }

                        let _ = this.update(cx, |client: &mut DaemonClient, cx: &mut Context<DaemonClient>| {
                            client.connected = false;
                            client.cmd_tx = None;
                            client.error = Some("Connection lost".into());
                            cx.emit(DaemonEvent::Disconnected);
                            cx.notify();
                        });
                    }
                    Err(e) => {
                        let _ = this.update(cx, |client: &mut DaemonClient, cx: &mut Context<DaemonClient>| {
                            client.connected = false;
                            client.cmd_tx = None;
                            client.error = Some(format!("Connect failed: {e}"));
                            cx.emit(DaemonEvent::Disconnected);
                            cx.notify();
                        });
                    }
                }

                smol::Timer::after(std::time::Duration::from_secs(2)).await;
            }
        })
        .detach();

        Self {
            sessions: Vec::new(),
            workspaces: Vec::new(),
            connected: false,
            error: None,
            cmd_tx: None,
        }
    }

    /// Send a serializable command to the daemon. Silently drops if not connected.
    pub fn send_cmd<T: Serialize>(&self, msg: &T) {
        let Some(tx) = &self.cmd_tx else { return };
        let Ok(json) = serde_json::to_string(msg) else { return };
        let _ = tx.try_send(json);
    }

    fn handle_event(&mut self, event: ServerEvent, cx: &mut Context<Self>) {
        record_inbound_event(&event);
        match event {
            ServerEvent::InitialState(msg) => {
                if let Some(ref v) = msg.protocol_version {
                    if v != PROTOCOL_VERSION {
                        self.error = Some(format!(
                            "Protocol mismatch: daemon={v}, client={PROTOCOL_VERSION}"
                        ));
                        cx.notify();
                        return;
                    }
                }
                self.sessions = msg.sessions;
                // Workspaces are event-driven on the consumer side (add/remove
                // events update sidebar + canvas state), so a fresh InitialState
                // must emit removals for any workspace that disappeared during
                // a disconnect — otherwise stale rows linger after the daemon
                // restarts with fewer workspaces.
                let new_ids: std::collections::HashSet<&str> =
                    msg.workspaces.iter().map(|w| w.id.as_str()).collect();
                for old in &self.workspaces {
                    if !new_ids.contains(old.id.as_str()) {
                        cx.emit(DaemonEvent::WorkspaceUnregistered {
                            workspace_id: old.id.clone(),
                        });
                    }
                }
                self.workspaces = msg.workspaces;
                // Replay every persisted workspace as a Registered event so
                // sidebar/canvas subscribers can hydrate without special-casing
                // the InitialState path. WorkspaceRegistered is idempotent on
                // the consumer side (dedup'd by id).
                for ws in self.workspaces.clone() {
                    cx.emit(DaemonEvent::WorkspaceRegistered { workspace: ws });
                }
                cx.emit(DaemonEvent::SessionsChanged);
                cx.notify();
            }
            ServerEvent::SessionRegistered(msg) => {
                if !self.sessions.iter().any(|s| s.id == msg.session.id) {
                    self.sessions.push(msg.session);
                }
                cx.emit(DaemonEvent::SessionsChanged);
                cx.notify();
            }
            ServerEvent::SessionUnregistered(msg) => {
                self.sessions.retain(|s| s.id != msg.session.id);
                cx.emit(DaemonEvent::SessionsChanged);
                cx.notify();
            }
            ServerEvent::SessionStateChanged(msg) => {
                if let Some(s) = self.sessions.iter_mut().find(|s| s.id == msg.session.id) {
                    *s = msg.session;
                }
                cx.emit(DaemonEvent::SessionsChanged);
                cx.notify();
            }
            ServerEvent::SessionsUpdated(msg) => {
                self.sessions = msg.sessions;
                cx.emit(DaemonEvent::SessionsChanged);
                cx.notify();
            }
            ServerEvent::WorkspaceRegistered(msg) => {
                let ws = msg.workspace;
                if let Some(existing) = self.workspaces.iter_mut().find(|w| w.id == ws.id) {
                    *existing = ws.clone();
                } else {
                    self.workspaces.push(ws.clone());
                }
                cx.emit(DaemonEvent::WorkspaceRegistered { workspace: ws });
                cx.notify();
            }
            ServerEvent::WorkspaceUnregistered(msg) => {
                let id = msg.workspace.id;
                self.workspaces.retain(|w| w.id != id);
                cx.emit(DaemonEvent::WorkspaceUnregistered { workspace_id: id });
                cx.notify();
            }
            ServerEvent::WorkspaceStateChanged(msg) => {
                let ws = msg.workspace;
                if let Some(existing) = self.workspaces.iter_mut().find(|w| w.id == ws.id) {
                    *existing = ws.clone();
                }
                cx.emit(DaemonEvent::WorkspaceStateChanged { workspace: ws });
                cx.notify();
            }
            ServerEvent::AttachResult(msg) => {
                let session_id = msg.id.clone();
                cx.emit(DaemonEvent::AttachResult {
                    session_id,
                    msg: Box::new(msg),
                });
            }
            ServerEvent::PtyOutput(msg) => {
                cx.emit(DaemonEvent::PtyOutput {
                    session_id: msg.id,
                    data: msg.data,
                    seq: msg.seq,
                });
            }
            ServerEvent::PtyDesync(msg) => {
                cx.emit(DaemonEvent::PtyDesync { session_id: msg.id });
            }
            ServerEvent::PtyResized(msg) => {
                cx.emit(DaemonEvent::PtyResized {
                    session_id: msg.id,
                    cols: msg.cols,
                    rows: msg.rows,
                });
            }
            ServerEvent::SessionExited(msg) => {
                cx.emit(DaemonEvent::SessionExited {
                    session_id: msg.id,
                    exit_code: msg.exit_code,
                });
            }
            ServerEvent::Unknown(_) => {}
        }
    }

    pub fn sessions(&self) -> &[Session] {
        &self.sessions
    }

    #[allow(dead_code)]
    pub fn workspaces(&self) -> &[Workspace] {
        &self.workspaces
    }

    #[allow(dead_code)]
    pub fn connected(&self) -> bool {
        self.connected
    }

    #[allow(dead_code)]
    pub fn error(&self) -> Option<&str> {
        self.error.as_deref()
    }
}

/// Summarize an inbound `ServerEvent` and record it in the global event
/// log. PtyOutput is excluded — at typical streaming rates it would
/// dominate the ring buffer and crowd out lower-frequency events that are
/// far more useful for diagnosing UI-level problems. Specific
/// PTY-byte-count diagnosis can be added later as a separate higher-level
/// event (e.g. "first output for session X received") if we need it.
fn record_inbound_event(event: &ServerEvent) {
    let payload = match event {
        ServerEvent::InitialState(m) => json!({
            "kind": "initial_state",
            "session_count": m.sessions.len(),
            "workspace_count": m.workspaces.len(),
        }),
        ServerEvent::SessionRegistered(m) => json!({
            "kind": "session_registered",
            "session_id": m.session.id.as_str(),
            "workspace_id": m.session.workspace_id.as_deref(),
            "agent": format!("{:?}", m.session.agent),
        }),
        ServerEvent::SessionUnregistered(m) => json!({
            "kind": "session_unregistered",
            "session_id": m.session.id.as_str(),
        }),
        ServerEvent::SessionStateChanged(m) => json!({
            "kind": "session_state_changed",
            "session_id": m.session.id.as_str(),
            "state": format!("{:?}", m.session.state),
        }),
        ServerEvent::SessionsUpdated(m) => json!({
            "kind": "sessions_updated",
            "session_count": m.sessions.len(),
        }),
        ServerEvent::WorkspaceRegistered(m) => json!({
            "kind": "workspace_registered",
            "workspace_id": m.workspace.id.as_str(),
            "title": m.workspace.title.as_str(),
        }),
        ServerEvent::WorkspaceUnregistered(m) => json!({
            "kind": "workspace_unregistered",
            "workspace_id": m.workspace.id.as_str(),
        }),
        ServerEvent::WorkspaceStateChanged(m) => json!({
            "kind": "workspace_state_changed",
            "workspace_id": m.workspace.id.as_str(),
        }),
        ServerEvent::AttachResult(m) => json!({
            "kind": "attach_result",
            "session_id": m.id.as_str(),
            "success": m.success,
            "has_snapshot": m.screen_snapshot.is_some(),
            "replay_segments": m.replay_segments.as_ref().map(|s| s.len()).unwrap_or(0),
            "last_seq": m.last_seq,
        }),
        ServerEvent::PtyDesync(m) => json!({
            "kind": "pty_desync",
            "session_id": m.id.as_str(),
        }),
        ServerEvent::PtyResized(m) => json!({
            "kind": "pty_resized",
            "session_id": m.id.as_str(),
            "cols": m.cols,
            "rows": m.rows,
        }),
        ServerEvent::SessionExited(m) => json!({
            "kind": "session_exited",
            "session_id": m.id.as_str(),
            "exit_code": m.exit_code,
        }),
        ServerEvent::PtyOutput(_) => return,
        ServerEvent::Unknown(name) => json!({"kind": "unknown", "event": name.as_str()}),
    };
    events::record("daemon_event", payload);
}
