/// Native canvas client entry point. Sidebar + pannable canvas with live
/// terminal panels driven by the daemon.
///
/// Module layout follows MVVM with a hexagonal edge — see `AGENTS.md` at
/// the `native-ui/` root for the conventions.
mod adapters;
mod app;
mod domain;
mod state;
mod theme;
mod views;

use gpui::{
    actions, px, size, App, AppContext, Application, Bounds, KeyBinding, WindowBounds,
    WindowOptions,
};

use adapters::daemon::DaemonClient;
use app::NativeApp;

actions!(attn_native, [Quit]);

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
                let daemon = cx.new(DaemonClient::new);
                cx.new(|cx| NativeApp::new(daemon, cx))
            },
        )
        .unwrap();
        // When launched from a terminal (e.g. `cargo run`) macOS does not
        // automatically promote the process to the foreground, so the window
        // opens behind the terminal and looks like nothing happened.
        // Activating here brings the app forward; bundled `.app` launches
        // already do this through the OS, so the call is harmless there.
        cx.activate(true);
    });
}
