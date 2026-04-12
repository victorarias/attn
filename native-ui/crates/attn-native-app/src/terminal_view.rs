use alacritty_terminal::term::cell::Flags;
use alacritty_terminal::vte::ansi::{Color, NamedColor};
use gpui::{
    div, fill, point, prelude::*, px, rgb, size, App, Bounds, ElementId,
    Font, GlobalElementId, Hsla, InspectorElementId, LayoutId, Pixels, Size, Style, TextRun,
    Window, Context, Entity, FocusHandle, Focusable, KeyDownEvent,
};

use attn_protocol::{PtyInputMessage, PtyResizeMessage};

use crate::daemon_client::DaemonClient;
use crate::terminal_model::{RenderedCell, TerminalEvent, TerminalModel};

/// Approximate character cell dimensions for Source Code Pro at 13px.
const CHAR_WIDTH: f32 = 7.8;
const ROW_HEIGHT: f32 = 17.0;

/// Default terminal colors (dark theme).
const COLOR_BG: u32 = 0x1a1a1a;
const COLOR_FG: u32 = 0xd4d4d4;
const COLOR_CURSOR_BG: u32 = 0xd4d4d4;
const COLOR_CURSOR_FG: u32 = 0x1a1a1a;

/// 16 standard ANSI colors (normal then bright).
const ANSI_COLORS: [u32; 16] = [
    0x2e3436, // Black
    0xcc0000, // Red
    0x4e9a06, // Green
    0xc4a000, // Yellow/Brown
    0x3465a4, // Blue
    0x75507b, // Magenta
    0x06989a, // Cyan
    0xd3d7cf, // White
    0x555753, // Bright Black
    0xef2929, // Bright Red
    0x8ae234, // Bright Green
    0xfce94f, // Bright Yellow
    0x729fcf, // Bright Blue
    0xad7fa8, // Bright Magenta
    0x34e2e2, // Bright Cyan
    0xeeeeec, // Bright White
];

fn resolve_color(color: &Color, default_color: u32) -> u32 {
    match color {
        Color::Named(NamedColor::Foreground) => COLOR_FG,
        Color::Named(NamedColor::Background) => COLOR_BG,
        Color::Named(c) => {
            let idx = *c as usize;
            if idx < ANSI_COLORS.len() {
                ANSI_COLORS[idx]
            } else {
                default_color
            }
        }
        Color::Indexed(idx) => {
            let i = *idx as usize;
            if i < 16 {
                ANSI_COLORS[i]
            } else if (16..232).contains(&i) {
                // 6×6×6 color cube
                let i = i - 16;
                let b = (i % 6) as u32;
                let g = ((i / 6) % 6) as u32;
                let r = ((i / 36) % 6) as u32;
                let c = |v: u32| if v == 0 { 0 } else { 55 + v * 40 };
                (c(r) << 16) | (c(g) << 8) | c(b)
            } else if i < 256 {
                // Grayscale ramp
                let v = ((i - 232) as u32) * 10 + 8;
                (v << 16) | (v << 8) | v
            } else {
                default_color
            }
        }
        Color::Spec(rgb_val) => {
            ((rgb_val.r as u32) << 16) | ((rgb_val.g as u32) << 8) | (rgb_val.b as u32)
        }
    }
}

/// Canvas-based terminal element. Paints background quads and shaped text
/// directly onto the GPUI scene rather than relying on div layout.
pub struct TerminalElement {
    cells: Vec<Vec<RenderedCell>>,
    cols: usize,
}

pub struct TerminalPrepaint {
    cell_width: Pixels,
    line_height: Pixels,
    font: Font,
    font_size: Pixels,
}

impl Element for TerminalElement {
    type RequestLayoutState = ();
    type PrepaintState = TerminalPrepaint;

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
        let style = Style {
            size: Size::full(),
            ..Default::default()
        };
        let layout_id = window.request_layout(style, [], cx);
        (layout_id, ())
    }

    fn prepaint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        _bounds: Bounds<Pixels>,
        _request_layout: &mut (),
        window: &mut Window,
        _cx: &mut App,
    ) -> TerminalPrepaint {
        let text_style = window.text_style();
        let font = text_style.font();
        let font_size = text_style.font_size.to_pixels(window.rem_size());

        // Shape a single 'm' to measure the monospace cell width.
        let run = TextRun {
            len: 1,
            font: font.clone(),
            color: Hsla::from(rgb(COLOR_FG)),
            background_color: None,
            underline: None,
            strikethrough: None,
        };
        let shaped = window.text_system().shape_line("m".into(), font_size, &[run], None);
        let cell_width = shaped.width;
        let line_height = px(ROW_HEIGHT);

        TerminalPrepaint { cell_width, line_height, font, font_size }
    }

    fn paint(
        &mut self,
        _id: Option<&GlobalElementId>,
        _inspector_id: Option<&InspectorElementId>,
        bounds: Bounds<Pixels>,
        _request_layout: &mut (),
        prepaint: &mut TerminalPrepaint,
        window: &mut Window,
        cx: &mut App,
    ) {
        let TerminalPrepaint { cell_width, line_height, font, font_size } = prepaint;
        let (cell_width, line_height, font_size) = (*cell_width, *line_height, *font_size);
        let origin = bounds.origin;

        // Pass 1: paint non-default background quads.
        for (row_idx, row) in self.cells.iter().enumerate() {
            let y = origin.y + line_height * row_idx as f32;
            for (col_idx, cell) in row.iter().enumerate() {
                // SPACER cells are covered by the preceding WIDE_CHAR cell.
                if cell.flags.contains(Flags::WIDE_CHAR_SPACER) {
                    continue;
                }

                let bg = if cell.is_cursor {
                    COLOR_CURSOR_BG
                } else if cell.flags.contains(Flags::INVERSE) {
                    resolve_color(&cell.fg, COLOR_FG)
                } else {
                    resolve_color(&cell.bg, COLOR_BG)
                };

                if bg == COLOR_BG {
                    continue; // default background, no quad needed
                }

                // Wide characters occupy two cell columns.
                let bg_width = if cell.flags.contains(Flags::WIDE_CHAR) {
                    cell_width * 2.0
                } else {
                    cell_width
                };

                let x = origin.x + cell_width * col_idx as f32;
                window.paint_quad(fill(
                    Bounds::new(point(x, y), size(bg_width, line_height)),
                    rgb(bg),
                ));
            }
        }

        // Pass 2: shape and paint text for each row.
        for (row_idx, row) in self.cells.iter().enumerate() {
            let y = origin.y + line_height * row_idx as f32;

            let mut text = String::with_capacity(self.cols);
            let mut runs: Vec<TextRun> = Vec::new();
            let mut run_fg: u32 = COLOR_FG;
            let mut run_len: usize = 0;
            let mut first_cell = true;

            for cell in row.iter() {
                // SPACER cells have no character to render; skip them from the
                // text string. Because WIDE_CHAR naturally advances 2× cell_width,
                // the column positions remain correct without a placeholder.
                if cell.flags.contains(Flags::WIDE_CHAR_SPACER) {
                    continue;
                }

                let ch = if cell.ch == '\0' || cell.ch == '\u{FEFF}' { ' ' } else { cell.ch };
                let fg = if cell.is_cursor {
                    COLOR_CURSOR_FG
                } else if cell.flags.contains(Flags::INVERSE) {
                    resolve_color(&cell.bg, COLOR_BG)
                } else {
                    resolve_color(&cell.fg, COLOR_FG)
                };

                if first_cell {
                    run_fg = fg;
                    first_cell = false;
                } else if fg != run_fg {
                    if run_len > 0 {
                        runs.push(TextRun {
                            len: run_len,
                            font: font.clone(),
                            color: Hsla::from(rgb(run_fg)),
                            background_color: None,
                            underline: None,
                            strikethrough: None,
                        });
                    }
                    run_fg = fg;
                    run_len = 0;
                }

                text.push(ch);
                run_len += ch.len_utf8();
            }

            if run_len > 0 {
                runs.push(TextRun {
                    len: run_len,
                    font: font.clone(),
                    color: Hsla::from(rgb(run_fg)),
                    background_color: None,
                    underline: None,
                    strikethrough: None,
                });
            }

            if !text.is_empty() && !runs.is_empty() {
                let row_origin = point(origin.x, y);
                let shaped = window
                    .text_system()
                    .shape_line(text.into(), font_size, &runs, None);
                let _ = shaped.paint(row_origin, line_height, window, cx);
            }
        }
    }
}

impl IntoElement for TerminalElement {
    type Element = Self;
    fn into_element(self) -> Self {
        self
    }
}

/// GPUI view that renders a terminal and forwards keyboard input.
pub struct TerminalView {
    terminal: Entity<TerminalModel>,
    daemon: Entity<DaemonClient>,
    focus_handle: FocusHandle,
}

impl Focusable for TerminalView {
    fn focus_handle(&self, _cx: &gpui::App) -> FocusHandle {
        self.focus_handle.clone()
    }
}

impl TerminalView {
    pub fn new(
        terminal: Entity<TerminalModel>,
        daemon: Entity<DaemonClient>,
        cx: &mut Context<Self>,
    ) -> Self {
        let focus_handle = cx.focus_handle();

        // Re-render whenever terminal content changes.
        cx.subscribe(&terminal, |_this, _term, event: &TerminalEvent, cx| {
            match event {
                TerminalEvent::DataReceived | TerminalEvent::Exited => cx.notify(),
                TerminalEvent::Desync => cx.notify(),
            }
        })
        .detach();

        Self { terminal, daemon, focus_handle }
    }

    fn send_input(&self, data: &str, cx: &mut Context<Self>) {
        let session_id = self.terminal.read(cx).session_id.clone();
        let msg = PtyInputMessage::new(session_id, data);
        self.daemon.read(cx).send_cmd(&msg);
    }

    fn on_key_down(&mut self, event: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        if !self.focus_handle.is_focused(window) {
            return;
        }
        let seq = encode_key(event);
        if !seq.is_empty() {
            self.send_input(&seq, cx);
        }
    }
}

impl Render for TerminalView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        // Sync terminal size to the current viewport.
        let viewport = window.viewport_size();
        let new_cols = ((viewport.width / px(CHAR_WIDTH)) as u16).max(1);
        let new_rows = ((viewport.height / px(ROW_HEIGHT)) as u16).max(1);
        let (cur_cols, cur_rows, session_id) = {
            let t = self.terminal.read(cx);
            (t.cols, t.rows, t.session_id.clone())
        };
        if new_cols != cur_cols || new_rows != cur_rows {
            self.terminal.update(cx, |t, _| t.resize(new_cols, new_rows));
            self.daemon.read(cx).send_cmd(&PtyResizeMessage::new(session_id, new_cols, new_rows));
        }

        let terminal = self.terminal.read(cx);
        let rows = terminal.rows as usize;
        let cols = terminal.cols as usize;

        // Collect all cell data for the canvas element.
        let mut all_cells: Vec<Vec<RenderedCell>> = Vec::with_capacity(rows);
        for row_idx in 0..rows {
            let cells = terminal.render_row(row_idx).unwrap_or_default();
            all_cells.push(cells);
        }

        let focus_handle = self.focus_handle.clone();

        div()
            .size_full()
            .bg(rgb(COLOR_BG))
            .font_family("Source Code Pro for Powerline")
            .text_size(px(13.))
            .track_focus(&focus_handle)
            .on_key_down(cx.listener(Self::on_key_down))
            .child(TerminalElement { cells: all_cells, cols })
    }
}

/// Encode a GPUI keystroke into a terminal byte sequence.
fn encode_key(event: &KeyDownEvent) -> String {
    let k = &event.keystroke;
    let ctrl = k.modifiers.control;
    let alt = k.modifiers.alt;
    let shift = k.modifiers.shift;

    // Printable character input via IME key_char takes priority.
    // But don't send key_char for Ctrl/Alt combos or named special keys —
    // those need dedicated escape encoding below.
    if let Some(key_char) = &k.key_char {
        if !ctrl && !alt && !key_char.is_empty() {
            match k.key.as_str() {
                // These keys always use dedicated escape sequences regardless
                // of what key_char says (e.g. macOS may return "\n" for Enter).
                "enter" | "escape" | "tab" | "backspace" | "delete"
                | "up" | "down" | "left" | "right"
                | "home" | "end" | "pageup" | "pagedown"
                | "f1" | "f2" | "f3" | "f4" | "f5" | "f6"
                | "f7" | "f8" | "f9" | "f10" | "f11" | "f12" => {}
                _ => return key_char.clone(),
            }
        }
    }

    // Special keys.
    let seq: Option<&str> = match k.key.as_str() {
        "enter" => {
            // Shift+Enter inserts a newline in apps using kitty keyboard
            // protocol (e.g. Claude Code). Plain Enter always submits (\r).
            if shift { Some("\x1b[13;2u") } else { Some("\r") }
        }
        "escape" => Some("\x1b"),
        "tab" => {
            if shift { Some("\x1b[Z") } else { Some("\t") }
        }
        "backspace" => Some("\x7f"),
        "delete" => Some("\x1b[3~"),
        "up" => Some("\x1b[A"),
        "down" => Some("\x1b[B"),
        "right" => Some("\x1b[C"),
        "left" => Some("\x1b[D"),
        "home" => Some("\x1b[H"),
        "end" => Some("\x1b[F"),
        "pageup" => Some("\x1b[5~"),
        "pagedown" => Some("\x1b[6~"),
        "f1" => Some("\x1bOP"),
        "f2" => Some("\x1bOQ"),
        "f3" => Some("\x1bOR"),
        "f4" => Some("\x1bOS"),
        "f5" => Some("\x1b[15~"),
        "f6" => Some("\x1b[17~"),
        "f7" => Some("\x1b[18~"),
        "f8" => Some("\x1b[19~"),
        "f9" => Some("\x1b[20~"),
        "f10" => Some("\x1b[21~"),
        "f11" => Some("\x1b[23~"),
        "f12" => Some("\x1b[24~"),
        _ => None,
    };

    if let Some(s) = seq {
        return s.to_string();
    }

    // Ctrl+letter: encode as control character.
    if ctrl && !alt {
        let key = k.key.as_str();
        if key.len() == 1 {
            let c = key.chars().next().unwrap();
            if ('a'..='z').contains(&c) {
                // Ctrl+a = \x01, Ctrl+b = \x02, ..., Ctrl+z = \x1a
                return String::from(char::from(c as u8 - b'a' + 1));
            }
            match c {
                '@' => return "\x00".to_string(),
                '[' => return "\x1b".to_string(),
                '\\' => return "\x1c".to_string(),
                ']' => return "\x1d".to_string(),
                '^' => return "\x1e".to_string(),
                '_' => return "\x1f".to_string(),
                _ => {}
            }
        }
    }

    // Alt+key: prefix with ESC.
    if alt {
        if let Some(key_char) = &k.key_char {
            if !key_char.is_empty() {
                return format!("\x1b{key_char}");
            }
        }
        if k.key.len() == 1 {
            return format!("\x1b{}", k.key);
        }
    }

    String::new()
}
