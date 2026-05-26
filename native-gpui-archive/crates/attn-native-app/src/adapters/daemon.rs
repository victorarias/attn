use attn_protocol::{ClientHelloMessage, ServerEvent, PROTOCOL_VERSION};
use futures_util::StreamExt;
use gpui::{AsyncApp, Context, EventEmitter, WeakEntity};
use serde::Serialize;

const DEFAULT_DAEMON_WS_URL: &str = "ws://localhost:9849/ws";
const BUILD_DAEMON_WS_URL: Option<&str> = option_env!("ATTN_NATIVE_BUILD_WS_URL");

fn daemon_ws_url() -> String {
    std::env::var("ATTN_WS_URL")
        .ok()
        .filter(|url| !url.trim().is_empty())
        .or_else(|| BUILD_DAEMON_WS_URL.map(ToOwned::to_owned))
        .unwrap_or_else(|| DEFAULT_DAEMON_WS_URL.to_string())
}

#[derive(Debug, Clone)]
pub enum DaemonEvent {
    Connected,
    Disconnected(String),
    Message(ServerEvent),
}

pub struct DaemonClient {
    connected: bool,
    error: Option<String>,
    cmd_tx: Option<async_channel::Sender<String>>,
}

impl EventEmitter<DaemonEvent> for DaemonClient {}

impl DaemonClient {
    pub fn new(cx: &mut Context<Self>) -> Self {
        let url = daemon_ws_url();
        cx.spawn(
            async move |this: WeakEntity<DaemonClient>, cx: &mut AsyncApp| loop {
                match async_tungstenite::smol::connect_async(url.as_str()).await {
                    Ok((socket, _)) => {
                        let (cmd_tx, cmd_rx) = async_channel::unbounded::<String>();
                        if let Ok(hello) = serde_json::to_string(&ClientHelloMessage::native(env!(
                            "CARGO_PKG_VERSION"
                        ))) {
                            let _ = cmd_tx.try_send(hello);
                        }
                        if this
                            .update(cx, |client, cx| {
                                client.connected = true;
                                client.error = None;
                                client.cmd_tx = Some(cmd_tx);
                                cx.emit(DaemonEvent::Connected);
                                cx.notify();
                            })
                            .is_err()
                        {
                            return;
                        }

                        let (mut writer, mut reader) = socket.split();
                        smol::spawn(async move {
                            while let Ok(message) = cmd_rx.recv().await {
                                if writer
                                    .send(async_tungstenite::tungstenite::Message::Text(
                                        message.into(),
                                    ))
                                    .await
                                    .is_err()
                                {
                                    break;
                                }
                            }
                        })
                        .detach();

                        while let Some(incoming) = reader.next().await {
                            match incoming {
                                Ok(async_tungstenite::tungstenite::Message::Text(text)) => {
                                    let Ok(event) = ServerEvent::parse(&text) else {
                                        continue;
                                    };
                                    if this
                                        .update(cx, |_, cx| cx.emit(DaemonEvent::Message(event)))
                                        .is_err()
                                    {
                                        return;
                                    }
                                }
                                Ok(async_tungstenite::tungstenite::Message::Ping(_))
                                | Ok(async_tungstenite::tungstenite::Message::Pong(_))
                                | Ok(async_tungstenite::tungstenite::Message::Binary(_))
                                | Ok(async_tungstenite::tungstenite::Message::Frame(_)) => {}
                                Ok(async_tungstenite::tungstenite::Message::Close(_)) | Err(_) => {
                                    break;
                                }
                            }
                        }
                        let _ = this.update(cx, |client, cx| {
                            client.connected = false;
                            client.cmd_tx = None;
                            client.error = Some("Connection lost".into());
                            cx.emit(DaemonEvent::Disconnected("Connection lost".into()));
                            cx.notify();
                        });
                    }
                    Err(error) => {
                        let message = format!("Connect failed: {error}");
                        let _ = this.update(cx, |client, cx| {
                            client.connected = false;
                            client.cmd_tx = None;
                            client.error = Some(message.clone());
                            cx.emit(DaemonEvent::Disconnected(message.clone()));
                            cx.notify();
                        });
                    }
                }
                smol::Timer::after(std::time::Duration::from_secs(2)).await;
            },
        )
        .detach();

        Self {
            connected: false,
            error: None,
            cmd_tx: None,
        }
    }

    pub fn send<T: Serialize>(&self, message: &T) -> Result<(), String> {
        let tx = self
            .cmd_tx
            .as_ref()
            .ok_or_else(|| self.error.clone().unwrap_or_else(|| "Not connected".into()))?;
        let text = serde_json::to_string(message).map_err(|error| format!("serialize: {error}"))?;
        tx.try_send(text)
            .map_err(|error| format!("queue daemon command: {error}"))
    }

    pub fn command_sender(&self) -> Option<async_channel::Sender<String>> {
        self.cmd_tx.clone()
    }

    pub fn connected(&self) -> bool {
        self.connected
    }

    pub fn error(&self) -> Option<&str> {
        self.error.as_deref()
    }

    pub fn protocol_version() -> &'static str {
        PROTOCOL_VERSION
    }

    #[cfg(test)]
    pub(crate) fn connected_for_test(cmd_tx: async_channel::Sender<String>) -> Self {
        Self {
            connected: true,
            error: None,
            cmd_tx: Some(cmd_tx),
        }
    }
}
