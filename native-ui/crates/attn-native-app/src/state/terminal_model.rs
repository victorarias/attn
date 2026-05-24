use attn_protocol::AttachResultMessage;
use base64::{engine::general_purpose::STANDARD as BASE64, Engine};
use gpui::{Context, Entity, EventEmitter};
use serde_json::json;

use crate::adapters::{
    automation::events,
    daemon::{DaemonClient, DaemonEvent},
};

#[derive(Debug, Clone)]
pub enum TerminalEvent {
    Changed,
    Desync,
}

pub enum TerminalChunk {
    Replay(Vec<u8>),
    Live(Vec<u8>),
}

pub struct TerminalModel {
    pub runtime_id: String,
    pending_output: Vec<TerminalChunk>,
    last_seq: i32,
    attached: bool,
    pub cols: u16,
    pub rows: u16,
}

impl EventEmitter<TerminalEvent> for TerminalModel {}

impl TerminalModel {
    pub fn new(
        runtime_id: impl Into<String>,
        cols: u16,
        rows: u16,
        daemon: &Entity<DaemonClient>,
        cx: &mut Context<Self>,
    ) -> Self {
        let runtime_id = runtime_id.into();
        cx.subscribe(daemon, |this, _, event: &DaemonEvent, cx| match event {
            DaemonEvent::Disconnected(_) if this.attached => {
                this.attached = false;
                cx.emit(TerminalEvent::Changed);
                cx.notify();
            }
            DaemonEvent::Message(message) => match message {
                attn_protocol::ServerEvent::AttachResult(result)
                    if result.id == this.runtime_id =>
                {
                    this.apply_attach(result, cx);
                }
                attn_protocol::ServerEvent::PtyOutput(output) if output.id == this.runtime_id => {
                    this.apply_output(&output.data, output.seq, cx);
                }
                attn_protocol::ServerEvent::PtyResized(resized)
                    if resized.id == this.runtime_id =>
                {
                    this.resize(resized.cols, resized.rows);
                    cx.emit(TerminalEvent::Changed);
                    cx.notify();
                }
                attn_protocol::ServerEvent::PtyDesync(id) if id == &this.runtime_id => {
                    this.attached = false;
                    cx.emit(TerminalEvent::Changed);
                    cx.emit(TerminalEvent::Desync);
                    cx.notify();
                }
                _ => {}
            },
            _ => {}
        })
        .detach();

        Self {
            runtime_id,
            pending_output: Vec::new(),
            last_seq: -1,
            attached: false,
            cols,
            rows,
        }
    }

    pub fn take_pending_output(&mut self) -> Vec<TerminalChunk> {
        std::mem::take(&mut self.pending_output)
    }

    pub fn attached(&self) -> bool {
        self.attached
    }

    pub fn resize(&mut self, cols: u16, rows: u16) {
        self.cols = cols;
        self.rows = rows;
    }

    fn apply_attach(&mut self, result: &AttachResultMessage, cx: &mut Context<Self>) {
        events::record(
            "terminal_attach_processed",
            json!({
                "runtime_id": self.runtime_id.as_str(),
                "success": result.success,
                "has_snapshot": result.screen_snapshot.is_some(),
                "replay_segments": result.replay_segments.as_ref().map(Vec::len).unwrap_or(0),
                "last_seq": result.last_seq,
                "cols": result.cols,
                "rows": result.rows,
            }),
        );
        if !result.success {
            self.attached = false;
            cx.emit(TerminalEvent::Changed);
            cx.notify();
            return;
        }
        self.attached = true;
        self.pending_output.clear();
        if let (Some(cols), Some(rows)) = (result.cols, result.rows) {
            self.resize(cols, rows);
        }
        if let Some(snapshot) = &result.screen_snapshot {
            if let Ok(bytes) = BASE64.decode(snapshot) {
                self.pending_output.push(TerminalChunk::Replay(bytes));
            }
        }
        if let Some(segments) = &result.replay_segments {
            for segment in segments {
                self.resize(segment.cols as u16, segment.rows as u16);
                if let Ok(bytes) = BASE64.decode(&segment.data) {
                    self.pending_output.push(TerminalChunk::Replay(bytes));
                }
            }
        }
        self.last_seq = result.last_seq.unwrap_or(self.last_seq);
        cx.emit(TerminalEvent::Changed);
        cx.notify();
    }

    fn apply_output(&mut self, encoded: &str, seq: i32, cx: &mut Context<Self>) {
        if seq <= self.last_seq {
            return;
        }
        self.last_seq = seq;
        if let Ok(bytes) = BASE64.decode(encoded) {
            self.pending_output.push(TerminalChunk::Live(bytes));
            cx.emit(TerminalEvent::Changed);
            cx.notify();
        }
    }
}
