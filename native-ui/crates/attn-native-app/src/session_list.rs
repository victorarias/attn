use attn_protocol::{Session, SessionState};
use gpui::{div, prelude::*, px, rgb, Context, Entity, SharedString, Window};

use crate::daemon_client::{DaemonClient, DaemonEvent};

pub struct SessionListView {
    client: Entity<DaemonClient>,
}

impl SessionListView {
    pub fn new(client: Entity<DaemonClient>, cx: &mut Context<Self>) -> Self {
        cx.subscribe(&client, Self::on_daemon_event).detach();
        Self { client }
    }

    fn on_daemon_event(
        &mut self,
        _client: Entity<DaemonClient>,
        _event: &DaemonEvent,
        cx: &mut Context<Self>,
    ) {
        cx.notify();
    }
}

fn state_color(state: &SessionState) -> u32 {
    match state {
        SessionState::Working => 0x22c55e,
        SessionState::WaitingInput | SessionState::PendingApproval => 0xeab308,
        SessionState::Launching => 0x3b82f6,
        SessionState::Idle => 0x6b7280,
        SessionState::Unknown => 0x6b7280,
    }
}

fn session_row(session: &Session) -> impl IntoElement {
    let color = state_color(&session.state);
    let label: SharedString = session.label.clone().into();
    let state_text: SharedString = session.state.to_string().into();
    let agent_text: SharedString = session.agent.to_string().into();
    let dir_text: SharedString = session
        .directory
        .rsplit('/')
        .next()
        .unwrap_or(&session.directory)
        .to_string()
        .into();

    div()
        .flex()
        .items_center()
        .gap_3()
        .px_3()
        .py_2()
        .border_b_1()
        .border_color(rgb(0x333333))
        .child(div().w(px(10.)).h(px(10.)).rounded(px(5.)).bg(rgb(color)))
        .child(
            div()
                .flex()
                .flex_col()
                .flex_1()
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(div().text_sm().text_color(rgb(0xffffff)).child(label))
                        .child(div().text_xs().text_color(rgb(0x888888)).child(agent_text)),
                )
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(div().text_xs().text_color(rgb(color)).child(state_text))
                        .child(div().text_xs().text_color(rgb(0x666666)).child(dir_text)),
                ),
        )
}

impl Render for SessionListView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let client = self.client.read(cx);
        let connected = client.connected();
        let error = client.error().map(String::from);
        let sessions: Vec<Session> = client.sessions().to_vec();

        let header_color = if connected { 0x22c55e } else { 0xef4444 };
        let status_text: SharedString = if connected {
            format!("Connected — {} sessions", sessions.len()).into()
        } else if let Some(ref err) = error {
            format!("Disconnected: {err}").into()
        } else {
            "Connecting…".into()
        };

        let mut content = div().flex().flex_col().size_full().bg(rgb(0x1e1e1e));

        content = content.child(
            div()
                .flex()
                .items_center()
                .gap_2()
                .px_4()
                .py_3()
                .border_b_1()
                .border_color(rgb(0x333333))
                .child(
                    div()
                        .text_lg()
                        .text_color(rgb(0xffffff))
                        .child(SharedString::from("attn")),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(rgb(0x888888))
                        .child(SharedString::from("native")),
                )
                .child(div().flex_1())
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_1()
                        .child(
                            div()
                                .w(px(8.))
                                .h(px(8.))
                                .rounded(px(4.))
                                .bg(rgb(header_color)),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(rgb(header_color))
                                .child(status_text),
                        ),
                ),
        );

        if sessions.is_empty() && connected {
            content = content.child(
                div().flex().flex_1().items_center().justify_center().child(
                    div()
                        .text_color(rgb(0x666666))
                        .child(SharedString::from("No active sessions")),
                ),
            );
        } else {
            for session in &sessions {
                content = content.child(session_row(session));
            }
        }

        content
    }
}
