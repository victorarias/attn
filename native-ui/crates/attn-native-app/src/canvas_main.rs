/// Spike 3 entry point — infinite canvas with dummy panels.
/// Run with: cargo run --bin attn-canvas
mod canvas_view;

use gpui::{actions, px, size, App, AppContext, Application, Bounds, Focusable, KeyBinding, WindowBounds, WindowOptions};

use canvas_view::WorkspaceCanvasView;

actions!(attn_canvas, [Quit]);

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
            |window, cx| {
                let view = cx.new(|cx| WorkspaceCanvasView::new(cx));
                view.focus_handle(cx).focus(window);
                view
            },
        )
        .unwrap();
    });
}
