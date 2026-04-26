/// Panels are the canvas's spatial objects. Each carries world-space
/// position + size and a typed `PanelContent` that decides how it renders
/// and how interactions (resize → PTY reflow, focus → keyboard routing)
/// are dispatched.
///
/// Adding a new panel type is: define the view struct + `Render`, add one
/// `PanelContent` arm, handle it in the canvas's render-panel match. No
/// trait objects — the enum keeps type-specific behaviour explicit and
/// avoids GPUI's `AnyView` erasure when we need to call type-specific
/// methods (resize → `PtyResize` only for terminals, focus handle only on
/// terminals).
use gpui::{div, prelude::*, px, rgb, Context, Entity, ParentElement, Render, SharedString, Window};

#[derive(Clone, Debug)]
pub struct Panel {
    pub id: usize,
    pub title: SharedString,
    pub world_x: f32,
    pub world_y: f32,
    pub width: f32,
    pub height: f32,
    pub content: PanelContent,
}

#[derive(Clone)]
pub enum PanelContent {
    /// Stand-in for any non-terminal panel type — todo lists, browsers,
    /// drawing canvases. Renders static text with a colour-coded body.
    /// The variant exists in the spike to prove the enum dispatches over
    /// at least two distinct render paths.
    Placeholder(Entity<PlaceholderView>),
    /// Terminal panel — for the canvas-UI spike this renders the session
    /// id and a stub label. Wiring real PTY rendering (TerminalView from
    /// spike 4) into the workspace context is a follow-up; the architecture
    /// already accommodates it.
    Terminal { session_id: SharedString },
}

impl std::fmt::Debug for PanelContent {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Placeholder(_) => write!(f, "Placeholder(..)"),
            Self::Terminal { session_id } => write!(f, "Terminal({session_id})"),
        }
    }
}

/// Static-text panel body. Created cheaply; one entity per panel so each
/// can be addressed independently in the future (e.g. for a todo list
/// that needs to subscribe to its own data source).
pub struct PlaceholderView {
    pub label: SharedString,
}

impl PlaceholderView {
    pub fn new(label: impl Into<SharedString>) -> Self {
        Self { label: label.into() }
    }
}

impl Render for PlaceholderView {
    fn render(&mut self, _window: &mut Window, _cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .size_full()
            .flex()
            .items_center()
            .justify_center()
            .text_color(rgb(0xa0a0b0))
            .text_size(px(13.))
            .child(self.label.clone())
    }
}
