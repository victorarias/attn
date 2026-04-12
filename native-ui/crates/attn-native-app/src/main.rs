mod daemon_client;
mod terminal_model;
mod terminal_view;

use gpui::{
    prelude::*, actions, px, size, App, Application, Bounds, Focusable, KeyBinding,
    WindowBounds, WindowOptions,
};

actions!(attn_native, [Quit]);

use daemon_client::DaemonClient;
use terminal_model::TerminalModel;
use terminal_view::TerminalView;

/// Root view that waits for a connected daemon, picks the first session,
/// and renders a fullscreen terminal.
struct RootView {
    client: gpui::Entity<DaemonClient>,
    terminal_view: Option<gpui::Entity<TerminalView>>,
    attached_session: Option<String>,
    needs_focus: bool,
}

impl RootView {
    fn new(client: gpui::Entity<DaemonClient>, cx: &mut gpui::Context<Self>) -> Self {
        cx.subscribe(&client, Self::on_daemon_event).detach();
        Self { client, terminal_view: None, attached_session: None, needs_focus: false }
    }

    fn on_daemon_event(
        &mut self,
        _client: gpui::Entity<DaemonClient>,
        event: &daemon_client::DaemonEvent,
        cx: &mut gpui::Context<Self>,
    ) {
        match event {
            daemon_client::DaemonEvent::Connected
            | daemon_client::DaemonEvent::SessionsChanged => {
                self.maybe_attach_first_session(cx);
            }
            _ => {}
        }
    }

    fn maybe_attach_first_session(&mut self, cx: &mut gpui::Context<Self>) {
        if self.attached_session.is_some() {
            return;
        }
        let session_id = {
            let client = self.client.read(cx);
            if !client.connected() {
                return;
            }
            let Some(session) = client.sessions().first() else { return };
            session.id.clone()
        };

        self.attached_session = Some(session_id.clone());

        // Default terminal dimensions — will be resized once the window is known.
        let cols: u16 = 120;
        let rows: u16 = 40;

        let terminal = cx.new(|cx| {
            TerminalModel::new(session_id.clone(), cols, rows, &self.client, cx)
        });

        let daemon = self.client.clone();
        let terminal_view = cx.new(|cx| TerminalView::new(terminal, daemon, cx));

        // Send attach_session command to the daemon.
        let attach = attn_protocol::AttachSessionMessage::new(session_id);
        self.client.read(cx).send_cmd(&attach);

        self.terminal_view = Some(terminal_view);
        self.needs_focus = true;
        cx.notify();
    }
}

impl gpui::Render for RootView {
    fn render(
        &mut self,
        window: &mut gpui::Window,
        cx: &mut gpui::Context<Self>,
    ) -> impl gpui::IntoElement {
        if let Some(ref tv) = self.terminal_view {
            if self.needs_focus {
                self.needs_focus = false;
                tv.focus_handle(cx).focus(window);
            }
            gpui::div().size_full().child(tv.clone())
        } else {
            let client = self.client.read(cx);
            let msg: gpui::SharedString = if client.connected() {
                "Connected — waiting for sessions…".into()
            } else if let Some(err) = client.error() {
                format!("Connecting… {err}").into()
            } else {
                "Connecting to daemon…".into()
            };
            gpui::div()
                .size_full()
                .flex()
                .items_center()
                .justify_center()
                .bg(gpui::rgb(0x1a1a1a))
                .text_color(gpui::rgb(0x888888))
                .child(msg)
        }
    }
}

fn main() {
    Application::new().run(|cx: &mut App| {
        cx.bind_keys([KeyBinding::new("cmd-q", Quit, None)]);
        cx.on_action::<Quit>(|_, cx| cx.quit());
        let _ = cx.on_window_closed(|cx| cx.quit());

        let bounds = Bounds::centered(None, size(px(1280.), px(800.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                ..Default::default()
            },
            |_window, cx| {
                let client = cx.new(|cx| DaemonClient::new(cx));
                cx.new(|cx| RootView::new(client, cx))
            },
        )
        .unwrap();
    });
}
