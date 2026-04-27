use alacritty_terminal::{
    Term,
    event::VoidListener,
    grid::Dimensions,
    index::{Column, Line},
    term::{Config as TermConfig, cell::Cell},
    vte::ansi::Processor as AnsiProcessor,
};
use base64::{Engine, engine::general_purpose::STANDARD as BASE64};
use gpui::{Context, Entity, EventEmitter};
use serde_json::json;

use attn_protocol::AttachResultMessage;

use crate::automation::events;
use crate::daemon_client::{DaemonClient, DaemonEvent};

/// Emitted when the terminal screen content changes and needs to be repainted.
#[derive(Debug, Clone)]
pub enum TerminalEvent {
    DataReceived,
    Exited,
    Desync,
}

/// A simple dimensions struct for alacritty_terminal.
struct TermSize {
    cols: usize,
    lines: usize,
}

impl Dimensions for TermSize {
    fn total_lines(&self) -> usize {
        self.lines
    }
    fn screen_lines(&self) -> usize {
        self.lines
    }
    fn columns(&self) -> usize {
        self.cols
    }
}

/// Holds the terminal emulation state for a single session.
pub struct TerminalModel {
    pub session_id: String,
    term: Term<VoidListener>,
    parser: AnsiProcessor,
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
        let size = TermSize { cols: cols as usize, lines: rows as usize };
        let term = Term::new(TermConfig::default(), &size, VoidListener);
        let parser = AnsiProcessor::new();

        // Subscribe to daemon events so PTY output routes directly to this model.
        cx.subscribe(daemon, |this: &mut TerminalModel, _entity, event: &DaemonEvent, cx| {
            match event {
                DaemonEvent::AttachResult { session_id, msg } if *session_id == this.session_id => {
                    this.handle_attach_result(msg.as_ref(), cx);
                }
                DaemonEvent::PtyOutput { session_id, data, seq } if *session_id == this.session_id => {
                    this.handle_pty_output(data, *seq, cx);
                }
                DaemonEvent::PtyDesync { session_id } if *session_id == this.session_id => {
                    cx.emit(TerminalEvent::Desync);
                }
                DaemonEvent::PtyResized { session_id, cols, rows } if *session_id == this.session_id => {
                    this.resize(*cols, *rows);
                    cx.emit(TerminalEvent::DataReceived);
                }
                DaemonEvent::SessionExited { session_id, .. } if *session_id == this.session_id => {
                    cx.emit(TerminalEvent::Exited);
                }
                _ => {}
            }
        })
        .detach();

        Self { session_id, term, parser, last_seq: -1, cols, rows }
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
                self.resize(cols, rows);
            }
        }

        // Feed screen_snapshot (visible frame at attach time) through the parser.
        // This restores the visible terminal state.
        if let Some(snapshot) = &msg.screen_snapshot {
            if let Ok(bytes) = BASE64.decode(snapshot) {
                self.parser.advance(&mut self.term, &bytes);
            }
        }

        // Also feed any replay segments.
        if let Some(segments) = &msg.replay_segments {
            for seg in segments {
                // Resize if segment has different geometry than current.
                let seg_cols = seg.cols as u16;
                let seg_rows = seg.rows as u16;
                if seg_cols != self.cols || seg_rows != self.rows {
                    self.resize(seg_cols, seg_rows);
                }
                if let Ok(bytes) = BASE64.decode(&seg.data) {
                    self.parser.advance(&mut self.term, &bytes);
                }
            }
        }

        // Lock in last_seq from the attach response before accepting live output.
        if let Some(seq) = msg.last_seq {
            self.last_seq = seq;
        }

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
            self.parser.advance(&mut self.term, &bytes);
        }

        cx.emit(TerminalEvent::DataReceived);
        cx.notify();
    }

    /// Resize the terminal to new dimensions.
    pub fn resize(&mut self, cols: u16, rows: u16) {
        self.cols = cols;
        self.rows = rows;
        let size = TermSize { cols: cols as usize, lines: rows as usize };
        self.term.resize(size);
    }

    /// Access the underlying Term for rendering.
    #[allow(dead_code)]
    pub fn term(&self) -> &Term<VoidListener> {
        &self.term
    }

    /// Cursor position as (col, row), 0-based.
    #[allow(dead_code)]
    pub fn cursor(&self) -> (usize, usize) {
        let point = self.term.grid().cursor.point;
        (point.column.0, point.line.0 as usize)
    }

    /// Plain-text snapshot of the current screen, one row per Vec entry.
    /// Used by the automation `read_pane_text` action so scenarios can
    /// assert that typed input survived the PTY round-trip. Trailing
    /// space-only rows are kept (callers can trim) so callers see the
    /// real screen geometry.
    pub fn screen_text(&self) -> Vec<String> {
        let grid = self.term.grid();
        let lines = grid.screen_lines();
        let cols = grid.columns();
        let mut out = Vec::with_capacity(lines);
        for row in 0..lines {
            let line = Line(row as i32 - grid.display_offset() as i32);
            let mut s = String::with_capacity(cols);
            for col in 0..cols {
                let cell: &Cell = &grid[line][Column(col)];
                s.push(cell.c);
            }
            // Trailing spaces on a row are noise for substring matching;
            // trim them but keep the row entry so row indices line up
            // with what the user sees on screen.
            while s.ends_with(' ') {
                s.pop();
            }
            out.push(s);
        }
        out
    }

    /// Render a single row as a sequence of (character, fg_color, is_cursor) triples.
    /// Returns None if line index is out of range.
    pub fn render_row(&self, row: usize) -> Option<Vec<RenderedCell>> {
        let grid = self.term.grid();
        let lines = grid.screen_lines();
        if row >= lines {
            return None;
        }
        let cursor_point = grid.cursor.point;
        let cursor_row = (cursor_point.line.0 + grid.display_offset() as i32) as usize;
        let cursor_col = cursor_point.column.0;

        let line = Line(row as i32 - grid.display_offset() as i32);
        let cols = grid.columns();
        let mut cells = Vec::with_capacity(cols);
        for col in 0..cols {
            let cell: &Cell = &grid[line][Column(col)];
            let is_cursor = row == cursor_row && col == cursor_col;
            cells.push(RenderedCell {
                ch: cell.c,
                fg: cell.fg,
                bg: cell.bg,
                flags: cell.flags,
                is_cursor,
            });
        }
        Some(cells)
    }
}

/// A single rendered terminal cell.
pub struct RenderedCell {
    pub ch: char,
    pub fg: alacritty_terminal::vte::ansi::Color,
    pub bg: alacritty_terminal::vte::ansi::Color,
    pub flags: alacritty_terminal::term::cell::Flags,
    pub is_cursor: bool,
}
