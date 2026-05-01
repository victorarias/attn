//! Opt-in FPS / frame-time overlay. Set `ATTN_NATIVE_FPS=1` to enable.
//!
//! Records an `Instant` at the start of every render and reports:
//!   - `fps`     — number of recorded frames within the last 1 second.
//!   - `avg_ms`  — mean inter-frame interval over the recent window.
//!   - `last_ms` — most recent inter-frame interval.
//!
//! GPUI 0.2 only paints when something calls `cx.notify()`, so these
//! numbers reflect actual repaint activity, not a hypothetical refresh
//! rate. Built for the 2026-04-28 canvas perf spike; kept as the
//! long-term render-perf affordance.

use std::collections::VecDeque;
use std::time::{Duration, Instant};

use gpui::{div, prelude::*, px, IntoElement, ParentElement, SharedString};

use crate::theme;

const WINDOW: Duration = Duration::from_secs(1);
const MAX_SAMPLES: usize = 240;

#[derive(Default)]
pub struct FpsCounter {
    samples: VecDeque<Instant>,
    last: Readout,
}

#[derive(Clone, Copy, Debug, Default)]
pub struct Readout {
    pub fps: f32,
    pub avg_ms: f32,
    pub last_ms: f32,
}

impl FpsCounter {
    pub fn new() -> Self {
        Self::default()
    }

    /// Record that a frame is being rendered right now and return the
    /// latest readout. Must be called once per `Render::render` call.
    pub fn record_frame(&mut self) -> Readout {
        self.record_sample_at(Instant::now())
    }

    fn record_sample_at(&mut self, now: Instant) -> Readout {
        self.samples.push_back(now);

        let cutoff = now - WINDOW;
        while self.samples.front().map(|t| *t < cutoff).unwrap_or(false) {
            self.samples.pop_front();
        }
        while self.samples.len() > MAX_SAMPLES {
            self.samples.pop_front();
        }

        let fps = self.samples.len() as f32;

        let last_ms = match self.samples.len() {
            0 | 1 => 0.0,
            n => {
                let prev = self.samples[n - 2];
                (now - prev).as_secs_f32() * 1000.0
            }
        };

        let avg_ms = if self.samples.len() >= 2 {
            let span = now - *self.samples.front().unwrap();
            let intervals = (self.samples.len() - 1) as f32;
            (span.as_secs_f32() * 1000.0) / intervals
        } else {
            0.0
        };

        let readout = Readout {
            fps,
            avg_ms,
            last_ms,
        };
        self.last = readout;
        readout
    }

    /// Last readout computed by `record_frame`. Returns the default
    /// (zeroed) readout if no frames have been recorded yet. Used by the
    /// automation snapshot so external scripts can read perf without
    /// inducing a render.
    pub fn last_readout(&self) -> Readout {
        self.last
    }

    /// Drop all recorded samples. Useful when a context change (e.g.
    /// zoom) makes prior samples non-representative of the new
    /// steady-state.
    pub fn reset(&mut self) {
        self.samples.clear();
        self.last = Readout::default();
    }
}

/// Top-right overlay element. Cheap to render: one absolutely positioned
/// card with a small heading, three timing rows, and a context footer.
/// Caller adds it as a child after the panels so it always paints on top.
///
/// Color discipline: only the live `fps` numeral takes the working-state
/// hue (the overlay's whole reason for being is to report liveness).
/// The rest of the rows — labels, timing values, context — sit in the
/// muted moonstone scale so the eye lands on the one number that
/// actually matters at a glance.
pub fn overlay(readout: Readout, panel_count: usize, zoom: f32) -> impl IntoElement {
    let fps_value = format!("{:>5.1}", readout.fps);
    let avg_value = format!("{:>5.2} ms", readout.avg_ms);
    let last_value = format!("{:>5.2} ms", readout.last_ms);
    let context_line = format!("n={} · z={:.2}", panel_count, zoom);

    div()
        .absolute()
        .top(px(theme::space::S1))
        .right(px(theme::space::S1))
        .px(px(theme::space::S2))
        .py(px(theme::space::S1))
        .bg(theme::ink::void())
        .border_1()
        .border_color(theme::line::firm())
        .rounded(px(theme::radius::R0))
        .text_xs()
        .text_color(theme::moon::parchment())
        .child(metric_header())
        .child(metric_row(SharedString::from("fps"), fps_value, true))
        .child(metric_row(SharedString::from("avg"), avg_value, false))
        .child(metric_row(SharedString::from("last"), last_value, false))
        .child(metric_footer(context_line))
}

/// Section heading — a quiet "FPS" label so the card identifies itself
/// without taking visual weight from the live number below.
fn metric_header() -> impl IntoElement {
    div()
        .pb(px(theme::space::S0))
        .text_color(theme::moon::ash())
        .child(SharedString::from("FPS"))
}

/// One label/value pair. The value is right-aligned via the format
/// width; only the canonical `fps` row paints in the working hue so the
/// rest of the card recedes.
fn metric_row(label: SharedString, value: String, live: bool) -> impl IntoElement {
    let value_color = if live {
        theme::state::working()
    } else {
        theme::moon::parchment()
    };
    div()
        .flex()
        .gap(px(theme::space::S2))
        .child(
            div()
                .min_w(px(28.0))
                .text_color(theme::moon::bone())
                .child(label),
        )
        .child(div().text_color(value_color).child(value))
}

/// Trailing context line — panel count and zoom in the quietest tone.
fn metric_footer(line: String) -> impl IntoElement {
    div()
        .pt(px(theme::space::S0))
        .text_color(theme::moon::ash())
        .child(line)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn empty_counter_reports_zero() {
        let mut c = FpsCounter::new();
        let r = c.record_sample_at(Instant::now());
        assert_eq!(r.fps, 1.0);
        assert_eq!(r.avg_ms, 0.0);
        assert_eq!(r.last_ms, 0.0);
    }

    #[test]
    fn two_samples_produce_inter_frame_interval() {
        let mut c = FpsCounter::new();
        let first = Instant::now();
        c.record_sample_at(first);
        let r = c.record_sample_at(first + Duration::from_millis(10));
        assert_eq!(r.fps, 2.0);
        assert_eq!(r.last_ms, 10.0);
        assert_eq!(r.avg_ms, 10.0);
    }

    #[test]
    fn samples_outside_window_are_dropped() {
        let mut c = FpsCounter::new();
        let now = Instant::now();
        c.samples.push_back(now - Duration::from_secs(5));
        c.samples.push_back(now - Duration::from_secs(2));
        let r = c.record_sample_at(now);
        assert_eq!(r.fps, 1.0, "old samples should be evicted");
    }
}
