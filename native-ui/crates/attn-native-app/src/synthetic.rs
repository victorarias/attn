//! Synthetic-load mode for the canvas perf spike.
//!
//! Spawns a workspace of N panels driven by a deterministic byte stream
//! pumped through alacritty's parser at a fixed cadence. The panels use
//! the same `TerminalModel` / `TerminalView` rendering path as live
//! panels — only the byte source is fake. That keeps measurements
//! honest: we're stress-testing the actual render pipeline, not a
//! parallel one.
//!
//! Triggered at startup via env vars:
//!
//!   ATTN_SPIKE5_SYNTHETIC_PANELS=N    # 1..256, default off
//!   ATTN_SPIKE5_SYNTHETIC_TICK_MS=K   # default 16 (≈60 ticks/sec); 0 = no ticker (static panels)
//!   ATTN_SPIKE5_SYNTHETIC_BYTES=B     # bytes per panel per tick, default 80
//!
//! When enabled, the spike skips waiting on the daemon for its initial
//! workspace and immediately drives a "synthetic" workspace into view.
//! Real daemon-backed workspaces still register normally if/when they
//! arrive — synthetic mode just guarantees there's something to paint.
//!
//! Static mode (`ATTN_SPIKE5_SYNTHETIC_TICK_MS=0`): panels are created
//! but no periodic ticker runs. The only thing causing renders is user
//! input (scroll, drag, etc). Useful for isolating event-handler cost
//! from byte-streaming cost when diagnosing scroll-wheel performance.

use std::time::Duration;

use gpui::{AppContext, Entity};

use crate::terminal_model::TerminalModel;

const DEFAULT_TICK_MS: u64 = 16;
const DEFAULT_BYTES_PER_TICK: usize = 80;
const MAX_PANELS: usize = 256;

#[derive(Clone, Copy, Debug)]
pub struct Config {
    pub panels: usize,
    /// `None` means no ticker — panels exist but nothing drives bytes
    /// through the parser. `Some(d)` means tick every `d`.
    pub tick: Option<Duration>,
    pub bytes_per_tick: usize,
}

/// Read env-driven config. Returns `None` when synthetic mode is off.
pub fn config_from_env() -> Option<Config> {
    let panels: usize = std::env::var("ATTN_SPIKE5_SYNTHETIC_PANELS")
        .ok()?
        .trim()
        .parse()
        .ok()?;
    if panels == 0 {
        return None;
    }
    let panels = panels.min(MAX_PANELS);

    let tick_ms = std::env::var("ATTN_SPIKE5_SYNTHETIC_TICK_MS")
        .ok()
        .and_then(|raw| raw.trim().parse::<u64>().ok())
        .unwrap_or(DEFAULT_TICK_MS);
    let tick = if tick_ms == 0 {
        None
    } else {
        Some(Duration::from_millis(tick_ms))
    };

    let bytes_per_tick = std::env::var("ATTN_SPIKE5_SYNTHETIC_BYTES")
        .ok()
        .and_then(|raw| raw.trim().parse::<usize>().ok())
        .unwrap_or(DEFAULT_BYTES_PER_TICK)
        .clamp(1, 64 * 1024);

    Some(Config {
        panels,
        tick,
        bytes_per_tick,
    })
}

/// Per-panel synthetic byte source. Owns a handle to the terminal
/// model whose parser it feeds.
pub struct SyntheticSource {
    model: Entity<TerminalModel>,
    panel_idx: usize,
    frame: u64,
    bytes_per_tick: usize,
}

impl SyntheticSource {
    pub fn new(model: Entity<TerminalModel>, panel_idx: usize, bytes_per_tick: usize) -> Self {
        Self { model, panel_idx, frame: 0, bytes_per_tick }
    }

    pub fn tick<C: AppContext>(&mut self, cx: &mut C) {
        let bytes = make_chunk(self.panel_idx, self.frame, self.bytes_per_tick);
        self.frame = self.frame.wrapping_add(1);
        self.model
            .update(cx, |m, inner_cx| m.feed_bytes(&bytes, inner_cx));
    }
}

/// Build one chunk of synthetic terminal output for the given (panel,
/// frame). Mixes ANSI color changes, scrolling output, and an occasional
/// cursor-positioning escape so the parser exercises a representative
/// slice of its work, not just plain ASCII appends.
fn make_chunk(panel_idx: usize, frame: u64, target_len: usize) -> Vec<u8> {
    let mut buf = Vec::with_capacity(target_len + 32);
    let color = 31 + ((frame as usize + panel_idx) % 6) as u8; // ANSI 31..36
    buf.extend_from_slice(format!("\x1b[{color}m").as_bytes());

    // Every 60th frame, jump the cursor home and clear-to-end so the
    // parser handles cursor-position + erase-in-display, which a
    // pure-append stream wouldn't trigger.
    if frame % 60 == 0 {
        buf.extend_from_slice(b"\x1b[H\x1b[J");
    }

    let header = format!("p{panel_idx:02} f{frame:>6} ");
    buf.extend_from_slice(header.as_bytes());

    // Filler: deterministic per-frame. Mix letters + digits to keep
    // glyph cache hot but not totally repetitive.
    static FILLER: &[u8] = b"abcdefghijklmnopqrstuvwxyz0123456789-_+#";
    let mut remaining = target_len.saturating_sub(buf.len());
    while remaining > 0 {
        let take = remaining.min(FILLER.len());
        buf.extend_from_slice(&FILLER[..take]);
        remaining -= take;
    }

    buf.extend_from_slice(b"\x1b[0m\r\n");
    buf
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn config_off_when_unset() {
        // Test isolation: only meaningful inside the per-test process,
        // so we just assert that an obviously-zero panels env returns
        // None.
        std::env::set_var("ATTN_SPIKE5_SYNTHETIC_PANELS", "0");
        let cfg = config_from_env();
        std::env::remove_var("ATTN_SPIKE5_SYNTHETIC_PANELS");
        assert!(cfg.is_none());
    }

    #[test]
    fn chunk_target_len_respected() {
        let bytes = make_chunk(0, 0, 80);
        // The chunk includes a small ANSI envelope on top of the filler;
        // sanity-check that it's roughly the right size and ends with a
        // newline so panels actually scroll.
        assert!(bytes.len() >= 80, "chunk smaller than target: {}", bytes.len());
        assert!(bytes.ends_with(b"\r\n"));
    }

    #[test]
    fn chunk_contents_change_per_frame() {
        let a = make_chunk(0, 0, 80);
        let b = make_chunk(0, 1, 80);
        assert_ne!(a, b, "frame counter should change chunk contents");
    }
}
