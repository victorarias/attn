mod adapters;
mod app;
mod state;
mod theme;
mod views;

use gpui::{
    actions, px, size, App, AppContext, Application, Bounds, KeyBinding, WindowBounds,
    WindowOptions,
};
use gpui_component::Root;

use adapters::daemon::DaemonClient;
use app::NativeApp;

actions!(attn_native, [Quit]);

fn main() {
    Application::new().run(|cx: &mut App| {
        gpui_component::init(cx);
        app::bind_keys(cx);
        cx.bind_keys([KeyBinding::new("cmd-q", Quit, None)]);
        cx.on_action::<Quit>(|_, cx| cx.quit());
        let _ = cx.on_window_closed(|cx| cx.quit());

        let background_window = adapters::automation::background_window();
        let bounds = Bounds::centered(None, size(px(1440.), px(880.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                focus: !background_window,
                ..Default::default()
            },
            |window, cx| {
                let daemon = cx.new(DaemonClient::new);
                let view = cx.new(|cx| NativeApp::new(daemon, cx));
                cx.new(|cx| Root::new(view, window, cx))
            },
        )
        .expect("open attn native window");
        // A Ghostty AppKit surface must become active once in order to start
        // its terminal lifecycle. The background harness immediately returns
        // foreground ownership to the previously active process and asserts
        // that all subsequent actions remain non-frontmost.
        cx.activate(true);
    });
}
