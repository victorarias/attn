use std::{
    cell::{Cell, RefCell},
    rc::Rc,
};

use attn_protocol::{AttachSessionMessage, PtyInputMessage};
use gpui::{
    div, prelude::*, App, Bounds, Context, ElementId, Entity, EventEmitter, FocusHandle, Focusable,
    GlobalElementId, InspectorElementId, KeyDownEvent, Keystroke, LayoutId, MouseButton, Pixels,
    Render, Size, Style, Window,
};

use crate::{
    adapters::{
        daemon::DaemonClient,
        ghostty::{GhosttyInputMods, GhosttyKeyInput, GhosttyRuntime, GhosttySurface},
    },
    state::terminal_model::{TerminalChunk, TerminalEvent, TerminalModel},
    theme,
};

struct TerminalElement {
    model: Entity<TerminalModel>,
    daemon: Entity<DaemonClient>,
    runtime: Rc<GhosttyRuntime>,
    surface: Rc<RefCell<Option<GhosttySurface>>>,
    mount_failed: Rc<Cell<bool>>,
}

impl gpui::Element for TerminalElement {
    type RequestLayoutState = ();
    type PrepaintState = ();

    fn id(&self) -> Option<ElementId> {
        None
    }

    fn source_location(&self) -> Option<&'static std::panic::Location<'static>> {
        None
    }

    fn request_layout(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        window: &mut Window,
        cx: &mut App,
    ) -> (LayoutId, ()) {
        (
            window.request_layout(
                Style {
                    size: Size::full(),
                    ..Default::default()
                },
                [],
                cx,
            ),
            (),
        )
    }

    fn prepaint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        bounds: Bounds<Pixels>,
        _request_layout: &mut (),
        window: &mut Window,
        cx: &mut App,
    ) {
        if self.surface.borrow().is_none() && !self.mount_failed.get() {
            let runtime_id = self.model.read(cx).runtime_id.clone();
            match GhosttySurface::mount(self.runtime.clone(), runtime_id, bounds, window) {
                Ok(surface) => *self.surface.borrow_mut() = Some(surface),
                Err(error) => {
                    eprintln!("mount Ghostty surface: {error}");
                    self.mount_failed.set(true);
                }
            }
        }
        let mounted = self.surface.borrow();
        let Some(surface) = mounted.as_ref() else {
            return;
        };
        surface.update_sender(self.daemon.read(cx).command_sender());
        surface.update_frame(bounds, window);
        let output = self
            .model
            .update(cx, |model, _| model.take_pending_output());
        process_terminal_chunks(surface, output);
    }

    fn paint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        _bounds: Bounds<Pixels>,
        _request_layout: &mut (),
        _prepaint: &mut (),
        _window: &mut Window,
        _cx: &mut App,
    ) {
        if let Some(surface) = self.surface.borrow().as_ref() {
            surface.draw();
        }
    }
}

impl IntoElement for TerminalElement {
    type Element = Self;

    fn into_element(self) -> Self {
        self
    }
}

pub struct TerminalView {
    model: Entity<TerminalModel>,
    daemon: Entity<DaemonClient>,
    runtime: Rc<GhosttyRuntime>,
    surface: Rc<RefCell<Option<GhosttySurface>>>,
    mount_failed: Rc<Cell<bool>>,
    focus: FocusHandle,
}

#[derive(Clone)]
pub enum TerminalViewEvent {
    FocusRequested(String),
}

impl EventEmitter<TerminalViewEvent> for TerminalView {}

impl TerminalView {
    pub fn new(
        model: Entity<TerminalModel>,
        daemon: Entity<DaemonClient>,
        runtime: Rc<GhosttyRuntime>,
        cx: &mut Context<Self>,
    ) -> Self {
        cx.subscribe(&model, |this, _, event: &TerminalEvent, cx| {
            if matches!(event, TerminalEvent::Desync) {
                this.attach(cx);
            }
            this.flush_pending_output(cx);
            cx.notify();
        })
        .detach();
        Self {
            model,
            daemon,
            runtime,
            surface: Rc::new(RefCell::new(None)),
            mount_failed: Rc::new(Cell::new(false)),
            focus: cx.focus_handle(),
        }
    }

    pub fn attach(&mut self, cx: &mut Context<Self>) {
        let runtime_id = self.model.read(cx).runtime_id.clone();
        let _ = self
            .daemon
            .read(cx)
            .send(&AttachSessionMessage::new(runtime_id));
    }

    fn flush_pending_output(&mut self, cx: &mut Context<Self>) {
        let surface = self.surface.borrow();
        let Some(surface) = surface.as_ref() else {
            return;
        };
        let output = self
            .model
            .update(cx, |model, _| model.take_pending_output());
        process_terminal_chunks(surface, output);
    }

    pub(crate) fn screen_text(&self) -> Option<String> {
        self.surface
            .borrow()
            .as_ref()
            .and_then(GhosttySurface::viewport_text)
    }

    pub(crate) fn attached(&self, cx: &App) -> bool {
        self.model.read(cx).attached()
    }

    pub(crate) fn terminal_size(&self, cx: &App) -> (u16, u16) {
        self.surface.borrow().as_ref().map_or_else(
            || {
                let model = self.model.read(cx);
                (model.cols, model.rows)
            },
            GhosttySurface::size,
        )
    }

    pub(crate) fn focus_for_input(&self, window: &mut Window) {
        self.focus_terminal(window);
    }

    pub(crate) fn inject_keystroke(
        &mut self,
        keystroke: Keystroke,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        self.on_key_down(
            &KeyDownEvent {
                keystroke,
                is_held: false,
            },
            window,
            cx,
        )
    }

    fn on_key_down(
        &mut self,
        event: &KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        if !self.focus.is_focused(window) || event.keystroke.modifiers.platform {
            return false;
        }
        if let Some(surface) = self.surface.borrow().as_ref() {
            surface.set_focus(true);
            if !surface.send_key(&ghostty_key_input(event)) {
                return false;
            }
            cx.notify();
        } else {
            let input = fallback_key_bytes(event);
            if input.is_empty() {
                return false;
            }
            let runtime_id = self.model.read(cx).runtime_id.clone();
            let _ = self
                .daemon
                .read(cx)
                .send(&PtyInputMessage::new(runtime_id, input));
        }
        true
    }

    fn focus_terminal(&self, window: &mut Window) {
        self.focus.clone().focus(window);
        if let Some(surface) = self.surface.borrow().as_ref() {
            surface.set_focus(true);
        }
    }
}

impl Focusable for TerminalView {
    fn focus_handle(&self, _: &App) -> FocusHandle {
        self.focus.clone()
    }
}

impl Render for TerminalView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        div()
            .size_full()
            .overflow_hidden()
            .bg(theme::ink::midnight())
            .track_focus(&self.focus)
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| {
                    this.focus_terminal(window);
                    cx.emit(TerminalViewEvent::FocusRequested(
                        this.model.read(cx).runtime_id.clone(),
                    ));
                    cx.notify();
                }),
            )
            .on_key_down(cx.listener(|this, event, window, cx| {
                this.on_key_down(event, window, cx);
            }))
            .child(TerminalElement {
                model: self.model.clone(),
                daemon: self.daemon.clone(),
                runtime: self.runtime.clone(),
                surface: self.surface.clone(),
                mount_failed: self.mount_failed.clone(),
            })
    }
}

fn process_terminal_chunks(surface: &GhosttySurface, output: Vec<TerminalChunk>) {
    for chunk in output {
        match chunk {
            TerminalChunk::Replay(bytes) => surface.process_replay(&bytes),
            TerminalChunk::Live(bytes) => surface.process_output(&bytes),
        }
    }
}

fn ghostty_key_input(event: &KeyDownEvent) -> GhosttyKeyInput {
    let key = &event.keystroke;
    let text = if key.modifiers.platform || key.modifiers.control {
        None
    } else {
        key.key_char.clone()
    };
    GhosttyKeyInput {
        keycode: macos_keycode(&key.key),
        text,
        unshifted_codepoint: key.key.chars().next().map(u32::from).unwrap_or_default(),
        mods: GhosttyInputMods::from_flags(
            key.modifiers.control,
            key.modifiers.alt,
            key.modifiers.shift,
            key.modifiers.platform,
        ),
        repeat: event.is_held,
    }
}

fn fallback_key_bytes(event: &KeyDownEvent) -> String {
    let key = &event.keystroke;
    if let Some(text) = &key.key_char {
        if !key.modifiers.control && !key.modifiers.alt && !key.modifiers.platform {
            match key.key.as_str() {
                "enter" | "tab" | "backspace" | "escape" | "up" | "down" | "left" | "right"
                | "delete" | "home" | "end" | "pageup" | "pagedown" | "f1" | "f2" | "f3" | "f4"
                | "f5" | "f6" | "f7" | "f8" | "f9" | "f10" | "f11" | "f12" => {}
                _ => return text.clone(),
            }
        }
    }
    match key.key.as_str() {
        "enter" if key.modifiers.shift => "\x1b[13;2u".into(),
        "enter" => "\r".into(),
        "tab" if key.modifiers.shift => "\x1b[Z".into(),
        "tab" => "\t".into(),
        "backspace" => "\x7f".into(),
        "escape" => "\x1b".into(),
        "up" => "\x1b[A".into(),
        "down" => "\x1b[B".into(),
        "right" => "\x1b[C".into(),
        "left" => "\x1b[D".into(),
        "delete" => "\x1b[3~".into(),
        "home" => "\x1b[H".into(),
        "end" => "\x1b[F".into(),
        "pageup" => "\x1b[5~".into(),
        "pagedown" => "\x1b[6~".into(),
        "f1" => "\x1bOP".into(),
        "f2" => "\x1bOQ".into(),
        "f3" => "\x1bOR".into(),
        "f4" => "\x1bOS".into(),
        "f5" => "\x1b[15~".into(),
        "f6" => "\x1b[17~".into(),
        "f7" => "\x1b[18~".into(),
        "f8" => "\x1b[19~".into(),
        "f9" => "\x1b[20~".into(),
        "f10" => "\x1b[21~".into(),
        "f11" => "\x1b[23~".into(),
        "f12" => "\x1b[24~".into(),
        value if key.modifiers.control && value.len() == 1 => {
            let byte = value.as_bytes()[0].to_ascii_lowercase();
            if byte.is_ascii_lowercase() {
                char::from(byte - b'a' + 1).to_string()
            } else {
                String::new()
            }
        }
        _ => String::new(),
    }
}

fn macos_keycode(key: &str) -> u32 {
    match key.to_ascii_lowercase().as_str() {
        "a" => 0x00,
        "s" => 0x01,
        "d" => 0x02,
        "f" => 0x03,
        "h" => 0x04,
        "g" => 0x05,
        "z" => 0x06,
        "x" => 0x07,
        "c" => 0x08,
        "v" => 0x09,
        "b" => 0x0b,
        "q" => 0x0c,
        "w" => 0x0d,
        "e" => 0x0e,
        "r" => 0x0f,
        "y" => 0x10,
        "t" => 0x11,
        "1" => 0x12,
        "2" => 0x13,
        "3" => 0x14,
        "4" => 0x15,
        "6" => 0x16,
        "5" => 0x17,
        "=" => 0x18,
        "9" => 0x19,
        "7" => 0x1a,
        "-" => 0x1b,
        "8" => 0x1c,
        "0" => 0x1d,
        "]" => 0x1e,
        "o" => 0x1f,
        "u" => 0x20,
        "[" => 0x21,
        "i" => 0x22,
        "p" => 0x23,
        "enter" => 0x24,
        "l" => 0x25,
        "j" => 0x26,
        "'" => 0x27,
        "k" => 0x28,
        ";" => 0x29,
        "\\" => 0x2a,
        "," => 0x2b,
        "/" => 0x2c,
        "n" => 0x2d,
        "m" => 0x2e,
        "." => 0x2f,
        "tab" => 0x30,
        "space" => 0x31,
        "`" => 0x32,
        "backspace" => 0x33,
        "escape" => 0x35,
        "home" => 0x73,
        "pageup" => 0x74,
        "delete" => 0x75,
        "end" => 0x77,
        "pagedown" => 0x79,
        "left" => 0x7b,
        "right" => 0x7c,
        "down" => 0x7d,
        "up" => 0x7e,
        _ => 0,
    }
}

#[cfg(test)]
mod tests {
    use super::{fallback_key_bytes, ghostty_key_input, macos_keycode};
    use gpui::{KeyDownEvent, Keystroke, Modifiers};

    fn key(key: &str, text: Option<&str>, modifiers: Modifiers) -> KeyDownEvent {
        KeyDownEvent {
            keystroke: Keystroke {
                key: key.into(),
                key_char: text.map(Into::into),
                modifiers,
            },
            is_held: false,
        }
    }

    #[test]
    fn ghostty_input_preserves_physical_and_typed_key_data() {
        let input = ghostty_key_input(&key("a", Some("a"), Modifiers::default()));

        assert_eq!(input.keycode, 0x00);
        assert_eq!(input.text.as_deref(), Some("a"));
        assert_eq!(input.unshifted_codepoint, u32::from('a'));
    }

    #[test]
    fn functional_key_uses_native_keycode_without_synthetic_text() {
        let input = ghostty_key_input(&key("enter", None, Modifiers::default()));

        assert_eq!(input.keycode, 0x24);
        assert_eq!(input.text, None);
        assert_eq!(
            fallback_key_bytes(&key("enter", None, Modifiers::default())),
            "\r"
        );
    }

    #[test]
    fn maps_navigation_keycodes_for_ghostty_terminal_modes() {
        assert_eq!(macos_keycode("left"), 0x7b);
        assert_eq!(macos_keycode("up"), 0x7e);
        assert_eq!(macos_keycode("delete"), 0x75);
    }

    #[test]
    fn control_keys_leave_encoding_to_ghostty() {
        let input = ghostty_key_input(&key(
            "c",
            Some("\u{3}"),
            Modifiers {
                control: true,
                ..Default::default()
            },
        ));

        assert_eq!(input.keycode, 0x08);
        assert_eq!(input.text, None);
    }
}
