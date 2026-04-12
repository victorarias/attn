/// Spike 4 entry point — terminal panels on an infinite canvas.
/// Run with: cargo run --bin attn-spike4
mod canvas_view;
mod daemon_client;
mod spike4_canvas;
mod terminal_model;
mod terminal_view;

use gpui::{actions, px, size, App, AppContext, Application, Bounds, KeyBinding, WindowBounds, WindowOptions};

use daemon_client::DaemonClient;
use spike4_canvas::TerminalCanvasView;

actions!(attn_spike4, [Quit]);

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
                let daemon = cx.new(|cx| DaemonClient::new(cx));
                cx.new(|cx| TerminalCanvasView::new(daemon, cx))
            },
        )
        .unwrap();
    });
}
