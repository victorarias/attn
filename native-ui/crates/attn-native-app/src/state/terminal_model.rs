use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use gpui::{Context, Entity, EventEmitter};
use libghostty_vt::{
    render::{CellIterator, CursorViewport, RenderState, RowIterator},
    screen::CellWide,
    style::RgbColor,
    Terminal, TerminalOptions,
};
use serde_json::json;

use attn_protocol::AttachResultMessage;

use crate::adapters::automation::events;
use crate::adapters::daemon::{DaemonClient, DaemonEvent};
use crate::theme;

/// Emitted when the terminal screen content changes and needs to be repainted.
#[derive(Debug, Clone)]
pub enum TerminalEvent {
    DataReceived,
    Exited,
    Desync,
}

const COLOR_BG: u32 = theme::ink::MIDNIGHT_HEX;
const COLOR_FG: u32 = theme::moon::PARCHMENT_HEX;
const DEFAULT_CELL_WIDTH_PX: u32 = 8;
const DEFAULT_CELL_HEIGHT_PX: u32 = 17;

/// Holds the terminal emulation state for a single session.
pub struct TerminalModel {
    pub session_id: String,
    terminal: Terminal<'static, 'static>,
    render_state: RenderState<'static>,
    row_iterator: RowIterator<'static>,
    cell_iterator: CellIterator<'static>,
    rendered_rows: Vec<Vec<RenderedCell>>,
    cursor: (usize, usize),
    cell_width_px: u32,
    cell_height_px: u32,
    last_seq: i32,
    pub cols: u16,
    pub rows: u16,
}

impl EventEmitter<TerminalEvent> for TerminalModel {}

impl TerminalModel {
    pub fn new(
        session_id: impl Into<String>,
        cols: u16,
        rows: u16,
        daemon: &Entity<DaemonClient>,
        cx: &mut Context<Self>,
    ) -> Self {
        let session_id = session_id.into();
        let terminal = Terminal::new(TerminalOptions {
            cols,
            rows,
            max_scrollback: 10_000,
        })
        .expect("create libghostty terminal");
        let render_state = RenderState::new().expect("create libghostty render state");
        let row_iterator = RowIterator::new().expect("create libghostty row iterator");
        let cell_iterator = CellIterator::new().expect("create libghostty cell iterator");

        // Subscribe to daemon events so PTY output routes directly to this model.
        cx.subscribe(
            daemon,
            |this: &mut TerminalModel, _entity, event: &DaemonEvent, cx| match event {
                DaemonEvent::AttachResult { session_id, msg } if *session_id == this.session_id => {
                    this.handle_attach_result(msg.as_ref(), cx);
                }
                DaemonEvent::PtyOutput {
                    session_id,
                    data,
                    seq,
                } if *session_id == this.session_id => {
                    this.handle_pty_output(data, *seq, cx);
                }
                DaemonEvent::PtyDesync { session_id } if *session_id == this.session_id => {
                    cx.emit(TerminalEvent::Desync);
                }
                DaemonEvent::PtyResized {
                    session_id,
                    cols,
                    rows,
                } if *session_id == this.session_id => {
                    this.resize(*cols, *rows, this.cell_width_px, this.cell_height_px);
                    cx.emit(TerminalEvent::DataReceived);
                }
                DaemonEvent::SessionExited { session_id, .. } if *session_id == this.session_id => {
                    cx.emit(TerminalEvent::Exited);
                }
                _ => {}
            },
        )
        .detach();

        let mut model = Self {
            session_id,
            terminal,
            render_state,
            row_iterator,
            cell_iterator,
            rendered_rows: Vec::new(),
            cursor: (0, 0),
            cell_width_px: DEFAULT_CELL_WIDTH_PX,
            cell_height_px: DEFAULT_CELL_HEIGHT_PX,
            last_seq: -1,
            cols,
            rows,
        };
        model.refresh_render_rows();
        model
    }

    fn handle_attach_result(&mut self, msg: &AttachResultMessage, cx: &mut Context<Self>) {
        events::record(
            "terminal_attach_processed",
            json!({
                "session_id": self.session_id.as_str(),
                "success": msg.success,
                "has_snapshot": msg.screen_snapshot.is_some(),
                "replay_segments": msg.replay_segments.as_ref().map(|s| s.len()).unwrap_or(0),
                "last_seq": msg.last_seq,
                "cols": msg.cols,
                "rows": msg.rows,
            }),
        );
        if !msg.success {
            return;
        }
        // Apply daemon-reported dimensions if provided.
        if let (Some(cols), Some(rows)) = (msg.cols, msg.rows) {
            if cols != self.cols || rows != self.rows {
                self.resize(cols, rows, self.cell_width_px, self.cell_height_px);
            }
        }

        // Feed screen_snapshot (visible frame at attach time) through the parser.
        // This restores the visible terminal state.
        if let Some(snapshot) = &msg.screen_snapshot {
            if let Ok(bytes) = BASE64.decode(snapshot) {
                self.terminal.vt_write(&bytes);
            }
        }

        // Also feed any replay segments.
        if let Some(segments) = &msg.replay_segments {
            for seg in segments {
                // Resize if segment has different geometry than current.
                let seg_cols = seg.cols as u16;
                let seg_rows = seg.rows as u16;
                if seg_cols != self.cols || seg_rows != self.rows {
                    self.resize(seg_cols, seg_rows, self.cell_width_px, self.cell_height_px);
                }
                if let Ok(bytes) = BASE64.decode(&seg.data) {
                    self.terminal.vt_write(&bytes);
                }
            }
        }

        // Lock in last_seq from the attach response before accepting live output.
        if let Some(seq) = msg.last_seq {
            self.last_seq = seq;
        }

        self.refresh_render_rows();
        cx.emit(TerminalEvent::DataReceived);
        cx.notify();
    }

    fn handle_pty_output(&mut self, data: &str, seq: i32, cx: &mut Context<Self>) {
        // Drop out-of-order or duplicate output.
        if seq <= self.last_seq {
            return;
        }
        self.last_seq = seq;

        if let Ok(bytes) = BASE64.decode(data) {
            self.terminal.vt_write(&bytes);
        }
        self.refresh_render_rows();

        cx.emit(TerminalEvent::DataReceived);
        cx.notify();
    }

    /// Resize the terminal to new dimensions.
    pub fn resize(&mut self, cols: u16, rows: u16, cell_width_px: u32, cell_height_px: u32) {
        let cell_width_px = cell_width_px.max(1);
        let cell_height_px = cell_height_px.max(1);
        if self.cols == cols
            && self.rows == rows
            && self.cell_width_px == cell_width_px
            && self.cell_height_px == cell_height_px
        {
            return;
        }

        self.cols = cols;
        self.rows = rows;
        self.cell_width_px = cell_width_px;
        self.cell_height_px = cell_height_px;
        let _ = self
            .terminal
            .resize(cols, rows, self.cell_width_px, self.cell_height_px);
        self.refresh_render_rows();
    }

    /// Cursor position as (col, row), 0-based.
    #[allow(dead_code)]
    pub fn cursor(&self) -> (usize, usize) {
        self.cursor
    }

    /// Plain-text snapshot of the current screen, one row per Vec entry.
    /// Used by the automation `read_pane_text` action so scenarios can
    /// assert that typed input survived the PTY round-trip. Trailing
    /// space-only rows are kept (callers can trim) so callers see the
    /// real screen geometry.
    pub fn screen_text(&self) -> Vec<String> {
        self.rendered_rows
            .iter()
            .map(|row| {
                let mut s = String::with_capacity(row.len());
                for cell in row {
                    if cell.is_spacer {
                        continue;
                    }
                    if cell.text.is_empty() {
                        s.push(' ');
                    } else {
                        s.push_str(&cell.text);
                    }
                }
                while s.ends_with(' ') {
                    s.pop();
                }
                s
            })
            .collect()
    }

    /// Render a single row as the normalized cell data consumed by the GPUI painter.
    /// Returns None if line index is out of range.
    pub fn render_row(&self, row: usize) -> Option<Vec<RenderedCell>> {
        self.rendered_rows.get(row).cloned()
    }

    fn refresh_render_rows(&mut self) {
        let Ok(snapshot) = self.render_state.update(&self.terminal) else {
            return;
        };
        let default_fg = COLOR_FG;
        let default_bg = COLOR_BG;
        let cursor = snapshot
            .cursor_visible()
            .ok()
            .filter(|visible| *visible)
            .and_then(|_| snapshot.cursor_viewport().ok().flatten());
        self.cursor = cursor
            .map(|CursorViewport { x, y, .. }| (x as usize, y as usize))
            .unwrap_or((0, 0));

        let mut rows = Vec::with_capacity(self.rows as usize);
        let Ok(mut row_iter) = self.row_iterator.update(&snapshot) else {
            return;
        };
        let mut row_idx = 0usize;
        while let Some(row) = row_iter.next() {
            let Ok(mut cell_iter) = self.cell_iterator.update(row) else {
                row_idx += 1;
                continue;
            };
            let mut cells = Vec::with_capacity(self.cols as usize);
            let mut col_idx = 0usize;
            while let Some(cell) = cell_iter.next() {
                let style = cell.style().unwrap_or_default();
                let raw_cell = cell.raw_cell().ok();
                let wide = raw_cell
                    .and_then(|raw| raw.wide().ok())
                    .unwrap_or(CellWide::Narrow);
                let is_spacer = matches!(wide, CellWide::SpacerTail | CellWide::SpacerHead);
                let mut fg = cell
                    .fg_color()
                    .ok()
                    .flatten()
                    .map(color_to_u32)
                    .unwrap_or(default_fg);
                let mut bg = cell
                    .bg_color()
                    .ok()
                    .flatten()
                    .map(color_to_u32)
                    .unwrap_or(default_bg);

                if style.inverse {
                    std::mem::swap(&mut fg, &mut bg);
                }
                if style.invisible {
                    fg = bg;
                }

                let text = if is_spacer {
                    String::new()
                } else {
                    let chars = cell.graphemes().unwrap_or_default();
                    if chars.is_empty() {
                        String::from(" ")
                    } else {
                        chars.into_iter().collect()
                    }
                };
                let is_cursor = cursor
                    .map(|cursor| cursor.x as usize == col_idx && cursor.y as usize == row_idx)
                    .unwrap_or(false);

                cells.push(RenderedCell {
                    text,
                    fg,
                    bg,
                    is_wide: matches!(wide, CellWide::Wide),
                    is_spacer,
                    is_cursor,
                });
                col_idx += 1;
            }
            rows.push(cells);
            row_idx += 1;
        }
        self.rendered_rows = rows;
    }
}

/// A single rendered terminal cell.
#[derive(Clone)]
pub struct RenderedCell {
    pub text: String,
    pub fg: u32,
    pub bg: u32,
    pub is_wide: bool,
    pub is_spacer: bool,
    pub is_cursor: bool,
}

fn color_to_u32(color: RgbColor) -> u32 {
    ((color.r as u32) << 16) | ((color.g as u32) << 8) | color.b as u32
}
