use attn_protocol::{ServerEvent, Session, PROTOCOL_VERSION};
use futures_util::StreamExt;
use gpui::{AsyncApp, Context, EventEmitter, WeakEntity};

const DAEMON_WS_URL: &str = "ws://localhost:9849";

#[derive(Debug, Clone)]
pub enum DaemonEvent {
    Connected,
    Disconnected(String),
    SessionsChanged(Vec<Session>),
}

pub struct DaemonClient {
    sessions: Vec<Session>,
    connected: bool,
    error: Option<String>,
}

impl EventEmitter<DaemonEvent> for DaemonClient {}

impl DaemonClient {
    pub fn new(cx: &mut Context<Self>) -> Self {
        cx.spawn(|this: WeakEntity<DaemonClient>, cx: &mut AsyncApp| async move {
            loop {
                let connect_result =
                    async_tungstenite::async_std::connect_async(DAEMON_WS_URL).await;

                match connect_result {
                    Ok((ws_stream, _)) => {
                        let _ = this.update(cx, |client: &mut DaemonClient, cx: &mut Context<DaemonClient>| {
                            client.connected = true;
                            client.error = None;
                            cx.emit(DaemonEvent::Connected);
                            cx.notify();
                        });

                        let (_, mut read) = ws_stream.split();

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
                            client.error = Some("Connection lost".into());
                            cx.emit(DaemonEvent::Disconnected("Connection lost".into()));
                            cx.notify();
                        });
                    }
                    Err(e) => {
                        let _ = this.update(cx, |client: &mut DaemonClient, cx: &mut Context<DaemonClient>| {
                            client.connected = false;
                            client.error = Some(format!("Connect failed: {e}"));
                            cx.emit(DaemonEvent::Disconnected(format!("{e}")));
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
            connected: false,
            error: None,
        }
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
                cx.emit(DaemonEvent::SessionsChanged(self.sessions.clone()));
                cx.notify();
            }
            ServerEvent::SessionRegistered(msg) => {
                if !self.sessions.iter().any(|s| s.id == msg.session.id) {
                    self.sessions.push(msg.session);
                }
                cx.emit(DaemonEvent::SessionsChanged(self.sessions.clone()));
                cx.notify();
            }
            ServerEvent::SessionUnregistered(msg) => {
                self.sessions.retain(|s| s.id != msg.session.id);
                cx.emit(DaemonEvent::SessionsChanged(self.sessions.clone()));
                cx.notify();
            }
            ServerEvent::SessionStateChanged(msg) => {
                if let Some(s) = self.sessions.iter_mut().find(|s| s.id == msg.session.id) {
                    *s = msg.session;
                }
                cx.emit(DaemonEvent::SessionsChanged(self.sessions.clone()));
                cx.notify();
            }
            ServerEvent::SessionsUpdated(msg) => {
                self.sessions = msg.sessions;
                cx.emit(DaemonEvent::SessionsChanged(self.sessions.clone()));
                cx.notify();
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
