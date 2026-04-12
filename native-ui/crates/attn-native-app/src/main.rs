mod daemon_client;
mod session_list;

use gpui::{
    prelude::*, px, size, App, Application, Bounds, WindowBounds, WindowOptions,
};

use daemon_client::DaemonClient;
use session_list::SessionListView;

fn main() {
    Application::new().run(|cx: &mut App| {
        let bounds = Bounds::centered(None, size(px(800.), px(600.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                ..Default::default()
            },
            |_window, cx| {
                let client = cx.new(|cx| DaemonClient::new(cx));
                cx.new(|cx| SessionListView::new(client, cx))
            },
        )
        .unwrap();
    });
}
