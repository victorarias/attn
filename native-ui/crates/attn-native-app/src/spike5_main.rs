/// Workspace canvas entry point — sidebar + pannable canvas with live
/// terminal panels driven by the daemon. Run with: `cargo run --bin attn-spike5`.
mod automation;
mod canvas_view;
mod daemon_client;
mod panel;
mod sidebar;
mod spike5_app;
mod spike5_canvas;
mod terminal_model;
mod terminal_view;
mod workspace;

use gpui::{actions, px, size, App, AppContext, Application, Bounds, KeyBinding, WindowBounds, WindowOptions};

use daemon_client::DaemonClient;
use spike5_app::Spike5App;

actions!(attn_spike5, [Quit]);

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
                cx.new(|cx| Spike5App::new(daemon, cx))
            },
        )
        .unwrap();
    });
}
