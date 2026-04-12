use attn_protocol::{ServerEvent, Session, PROTOCOL_VERSION};
use futures_util::{SinkExt, StreamExt};
use gpui::{AsyncApp, Context, EventEmitter, WeakEntity};
use serde::Serialize;

const DAEMON_WS_URL: &str = "ws://localhost:9849/ws";

/// Events emitted by DaemonClient to subscribers.
#[derive(Debug, Clone)]
pub enum DaemonEvent {
    Connected,
    Disconnected,
    SessionsChanged,
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
    connected: bool,
    error: Option<String>,
    /// Channel sender for outbound commands. None when not connected.
    cmd_tx: Option<async_channel::Sender<String>>,
}

impl EventEmitter<DaemonEvent> for DaemonClient {}

impl DaemonClient {
    pub fn new(cx: &mut Context<Self>) -> Self {
        cx.spawn(async |this: WeakEntity<DaemonClient>, cx: &mut AsyncApp| {
            loop {
                let connect_result =
                    async_tungstenite::async_std::connect_async(DAEMON_WS_URL).await;

                match connect_result {
                    Ok((ws_stream, _)) => {
                        // Create a channel for outbound messages.
                        let (cmd_tx, cmd_rx) = async_channel::unbounded::<String>();

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

        Self { sessions: Vec::new(), connected: false, error: None, cmd_tx: None }
    }

    /// Send a serializable command to the daemon. Silently drops if not connected.
    pub fn send_cmd<T: Serialize>(&self, msg: &T) {
        let Some(tx) = &self.cmd_tx else { return };
        let Ok(json) = serde_json::to_string(msg) else { return };
        let _ = tx.try_send(json);
    }

    fn handle_event(&mut self, event: ServerEvent, cx: &mut Context<Self>) {
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

    pub fn connected(&self) -> bool {
        self.connected
    }

    pub fn error(&self) -> Option<&str> {
        self.error.as_deref()
    }
}
