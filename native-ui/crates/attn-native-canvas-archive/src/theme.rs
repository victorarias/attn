//! Native app design system — **Observatory**.
//!
//! Single source of truth for every color, radius, spacing, hairline,
//! and motion duration the GPUI views use. Views never construct raw
//! `rgb(0xNNNNNN)` literals — they call into one of the submodules
//! below. Keeping color and geometry in one place is what lets the
//! design system stay coherent as the app grows; raw hex sprinkled
//! across views is what makes it drift.
//!
//! See `native-ui/AGENTS.md` for the design philosophy and the visual
//! plates at `docs/prototypes/native-design-system-observatory.html`.
//!
//! ## Adding a token
//!
//! 1. Add a `pub const FOO_HEX: u32 = 0x...;` to the right submodule.
//! 2. Add a `pub fn foo() -> Rgba { rgb(FOO_HEX) }` next to it.
//! 3. Document what it means and where it gets used.
//!
//! That's it. Mechanical. No coordinator file, no enum to extend, no
//! match arms to keep in sync.
//!
//! ## Token rules
//!
//! - **Color is meaning.** Hue carries either *identity* (which agent)
//!   or *status* (what state) — never decoration.
//! - **One accent.** Sodium-vapor orange. If sodium appears, it is the
//!   most important pixel on screen.
//! - **Three radii.** `r0` / `r1` / `r2`. Anything else is a bug.
//! - **Six surface inks.** Cool blue-black. Never pure black, never
//!   tinted toward purple.
//! - **One hairline scale.** Four steps in `theme::line` (`weak`, `mild`,
//!   `firm`, `bold`). A divider is one of these — never an ad-hoc rgba.

#![allow(dead_code)]

use gpui::{rgb, rgba, Rgba};

// ─────────────────────────────────────────────────────────────────────────
// INK — surface scale, deepest to lightest.
// ─────────────────────────────────────────────────────────────────────────

/// Surface inks, deepest (`void`) to lightest (`firm`). Used for
/// backgrounds, raised cards, panel chrome, and borders. Each step is
/// roughly 1.4× the luminance of the previous.
pub mod ink {
    use super::*;

    /// `#060810` — outer space. Used for the FPS overlay, deepest wells.
    pub const VOID_HEX: u32 = 0x060810;
    /// `#0a0e16` — primary canvas surface. The page lives on this.
    pub const MIDNIGHT_HEX: u32 = 0x0a0e16;
    /// `#10151f` — raised surface. Sidebar, panel body.
    pub const NOCTURNE_HEX: u32 = 0x10151f;
    /// `#161d2a` — hover / active row background, panel header bar.
    pub const SHADE_HEX: u32 = 0x161d2a;
    /// `#1f2837` — borders in the body of a card.
    pub const BORDER_HEX: u32 = 0x1f2837;
    /// `#2a3548` — firm structural border between major regions.
    pub const FIRM_HEX: u32 = 0x2a3548;
    /// `#060810 @ 0.72α` — modal scrim over the canvas.
    pub const VEIL_HEX: u32 = 0x060810b8;

    pub fn void() -> Rgba {
        rgb(VOID_HEX)
    }
    pub fn midnight() -> Rgba {
        rgb(MIDNIGHT_HEX)
    }
    pub fn nocturne() -> Rgba {
        rgb(NOCTURNE_HEX)
    }
    pub fn shade() -> Rgba {
        rgb(SHADE_HEX)
    }
    pub fn border() -> Rgba {
        rgb(BORDER_HEX)
    }
    pub fn firm() -> Rgba {
        rgb(FIRM_HEX)
    }
    pub fn veil() -> Rgba {
        rgba(VEIL_HEX)
    }
}

// ─────────────────────────────────────────────────────────────────────────
// MOON — text scale, primary to disabled.
// ─────────────────────────────────────────────────────────────────────────

/// Text values from primary (`moonstone`) to disabled (`cinder`). Warm
/// cream-white inspired by the surface of a quarter moon. Reading
/// `moonstone` on `ink::midnight` meets WCAG AAA at 14pt and above.
pub mod moon {
    use super::*;

    /// `#f0ede0` — primary text. Titles, active rows, panel names.
    pub const MOONSTONE_HEX: u32 = 0xf0ede0;
    /// `#cdc8b6` — secondary text. Subtitles, terminal default fg, italics.
    pub const PARCHMENT_HEX: u32 = 0xcdc8b6;
    /// `#8a8678` — tertiary text. Labels, metadata.
    pub const BONE_HEX: u32 = 0x8a8678;
    /// `#545040` — quaternary text. Dim affordances, low-emphasis glyphs.
    pub const ASH_HEX: u32 = 0x545040;
    /// `#2f2c24` — disabled text. Almost the surface; reserved for "not for you".
    pub const CINDER_HEX: u32 = 0x2f2c24;

    pub fn moonstone() -> Rgba {
        rgb(MOONSTONE_HEX)
    }
    pub fn parchment() -> Rgba {
        rgb(PARCHMENT_HEX)
    }
    pub fn bone() -> Rgba {
        rgb(BONE_HEX)
    }
    pub fn ash() -> Rgba {
        rgb(ASH_HEX)
    }
    pub fn cinder() -> Rgba {
        rgb(CINDER_HEX)
    }
}

// ─────────────────────────────────────────────────────────────────────────
// SODIUM — the single accent.
// ─────────────────────────────────────────────────────────────────────────

/// The single accent. Sodium-streetlamp orange. Used for the cursor,
/// focus rings, and the primary affordance of any dialog — and nothing
/// else. If sodium appears, it is the most important pixel on screen.
pub mod sodium {
    use super::*;

    /// `#ff8a3d` — the canonical accent.
    pub const VAPOR_HEX: u32 = 0xff8a3d;
    /// `#d76d24` — sodium's hover/pressed and "selected, not focused".
    pub const DEEP_HEX: u32 = 0xd76d24;

    /// `#ff8a3d @ 0.14α` — wash for selected backgrounds, focus tints.
    pub const SOFT_HEX: u32 = 0xff8a3d24;
    /// `#ff8a3d @ 0.35α` — halo for caret glow / focused field rings.
    pub const GLOW_HEX: u32 = 0xff8a3d59;
    /// `#ff8a3d @ 0.06α` — barely-there pre-selection / hover hint.
    pub const HUSH_HEX: u32 = 0xff8a3d0f;

    pub fn vapor() -> Rgba {
        rgb(VAPOR_HEX)
    }
    pub fn deep() -> Rgba {
        rgb(DEEP_HEX)
    }
    pub fn soft() -> Rgba {
        rgba(SOFT_HEX)
    }
    pub fn glow() -> Rgba {
        rgba(GLOW_HEX)
    }
    pub fn hush() -> Rgba {
        rgba(HUSH_HEX)
    }
}

// ─────────────────────────────────────────────────────────────────────────
// STAR — agent identity. Hue means *who*, never *what*.
// ─────────────────────────────────────────────────────────────────────────

/// Agent stars. Each `SessionAgent` from the protocol gets exactly one
/// hue. Used for identity indicators (agent dot, panel-header star,
/// avatar) — never combined with a status meaning.
pub mod star {
    use super::*;

    /// `#ffb47a` — Claude. K-type, warm peach.
    pub const CLAUDE_HEX: u32 = 0xffb47a;
    /// `#6fb5ff` — Codex. B-type, Rigel blue.
    pub const CODEX_HEX: u32 = 0x6fb5ff;
    /// `#dde9ff` — Shell. A-type, Vega blue-white.
    pub const SHELL_HEX: u32 = 0xdde9ff;

    pub fn claude() -> Rgba {
        rgb(CLAUDE_HEX)
    }
    pub fn codex() -> Rgba {
        rgb(CODEX_HEX)
    }
    pub fn shell() -> Rgba {
        rgb(SHELL_HEX)
    }

    /// Resolve the star color for a wire-level agent identifier as it
    /// arrives from the daemon (`"claude"` / `"codex"` / `"shell"`).
    /// Unknown identifiers get `moon::bone()` so a future agent renders
    /// neutrally rather than mis-coloring an existing one.
    pub fn for_agent_id(agent_id: &str) -> Rgba {
        match agent_id {
            "claude" => claude(),
            "codex" => codex(),
            "shell" => shell(),
            _ => super::moon::bone(),
        }
    }
}

// ─────────────────────────────────────────────────────────────────────────
// STATE — observation classifications. Hue means *what*, never *who*.
// ─────────────────────────────────────────────────────────────────────────

/// Observation classifications. Each `WorkspaceStatus` /
/// `SessionState` gets exactly one hue. Used for status pills, dots,
/// and the breath/pulse animations — never combined with an identity.
pub mod state {
    use super::*;

    /// `#5bd5e8` — `launching`. *oriens* (rising).
    pub const LAUNCHING_HEX: u32 = 0x5bd5e8;
    /// `#62e08e` — `working`. *transitus* (in transit).
    pub const WORKING_HEX: u32 = 0x62e08e;
    /// `#ffd75e` — `waiting_input`. *occultatum* (occulted).
    pub const WAITING_HEX: u32 = 0xffd75e;
    /// `#ff7a59` — `pending_approval`. *retentum* (halted).
    pub const APPROVAL_HEX: u32 = 0xff7a59;
    /// `#c49bff` — `needs_review_after_long_run`. *inscriptum* (logged).
    pub const REVIEW_HEX: u32 = 0xc49bff;
    /// `#545040` — `idle`. *quietum* (set). Same value as `moon::ash`,
    /// because an idle state should sit at the same visual weight as
    /// dim metadata.
    pub const IDLE_HEX: u32 = 0x545040;
    /// `#ff4d5e` — `error` / connection lost. *amissum* (lost).
    pub const ERROR_HEX: u32 = 0xff4d5e;

    pub fn launching() -> Rgba {
        rgb(LAUNCHING_HEX)
    }
    pub fn working() -> Rgba {
        rgb(WORKING_HEX)
    }
    pub fn waiting() -> Rgba {
        rgb(WAITING_HEX)
    }
    pub fn approval() -> Rgba {
        rgb(APPROVAL_HEX)
    }
    pub fn review() -> Rgba {
        rgb(REVIEW_HEX)
    }
    pub fn idle() -> Rgba {
        rgb(IDLE_HEX)
    }
    pub fn error() -> Rgba {
        rgb(ERROR_HEX)
    }
}

// ─────────────────────────────────────────────────────────────────────────
// SURFACE — row & button states composed from the primitives above.
// ─────────────────────────────────────────────────────────────────────────

/// Composite tokens for interactive surfaces (selected rows, pending
/// destroy, danger confirmation). These are *derived* from the
/// primitives above and named by behavior rather than hue, so call sites
/// stay readable.
pub mod surface {
    use super::*;

    /// Background of an active / selected list row.
    pub fn selected_row() -> Rgba {
        ink::shade()
    }

    /// Background of a row whose destructive action is in flight.
    /// Sits between `midnight` and `nocturne` so it reads as "removed
    /// from the active list, not yet gone".
    pub fn pending_row() -> Rgba {
        rgb(0x0d1119)
    }

    /// Background of a row whose destructive action awaits user
    /// confirmation. Tinted toward the error hue while staying dark
    /// enough to read text on top.
    pub fn danger_row() -> Rgba {
        rgb(0x2a1518)
    }

    /// Background of the "confirm destroy" affordance itself. A darker,
    /// muted error tone — emphatic but not screaming.
    pub fn danger_emphasis_bg() -> Rgba {
        rgb(0x6e2a30)
    }

    /// Foreground of text on `danger_emphasis_bg()`.
    pub fn danger_emphasis_fg() -> Rgba {
        rgb(0xffd3d6)
    }
}

// ─────────────────────────────────────────────────────────────────────────
// RADIUS — three values, no more.
// ─────────────────────────────────────────────────────────────────────────

/// Radii. Three values is enough to express tight (chips, badges),
/// medium (panels, dialogs), and large (canvas frames). Anything else
/// is a bug.
pub mod radius {
    /// `4px` — chips, badges, status pills, snap markers.
    pub const R0: f32 = 4.0;
    /// `7px` — cards, panel chrome, dialog body.
    pub const R1: f32 = 7.0;
    /// `12px` — outermost frames, large dialogs.
    pub const R2: f32 = 12.0;
}

// ─────────────────────────────────────────────────────────────────────────
// SPACE — the spacing ramp.
// ─────────────────────────────────────────────────────────────────────────

/// Spacing ramp, in screen pixels at zoom 1.0. Use these for paddings,
/// gaps, and inter-component gutters — not for world-space layout
/// (panel sizes, etc) which is its own domain.
pub mod space {
    pub const S0: f32 = 4.0;
    pub const S1: f32 = 8.0;
    pub const S2: f32 = 12.0;
    pub const S3: f32 = 16.0;
    pub const S4: f32 = 24.0;
    pub const S5: f32 = 36.0;
    pub const S6: f32 = 56.0;
}

// ─────────────────────────────────────────────────────────────────────────
// LINE — hairline scale. Four steps over a moonstone-tinted alpha.
// ─────────────────────────────────────────────────────────────────────────

/// Hairlines for dividers, faint borders, and "barely-there" structure.
/// Always a moonstone-tinted alpha so it sits inside the warm palette
/// instead of reading as a cold gray over the cool inks.
///
/// Pick by emphasis, not by color: `weak` for the quietest separators
/// inside a card, `mild` for the body of a card border, `firm` for the
/// border between major regions, `bold` for an emphatic structural
/// divider that should still feel like a hairline.
pub mod line {
    use super::*;

    /// `moonstone @ 0.05α` — quietest divider; in-card row separators.
    pub const WEAK_HEX: u32 = 0xf0ede00d;
    /// `moonstone @ 0.10α` — default card border, faint dividers.
    pub const MILD_HEX: u32 = 0xf0ede01a;
    /// `moonstone @ 0.18α` — region borders, panel chrome edges.
    pub const FIRM_HEX: u32 = 0xf0ede02e;
    /// `moonstone @ 0.28α` — emphatic structural rule.
    pub const BOLD_HEX: u32 = 0xf0ede047;

    pub fn weak() -> Rgba {
        rgba(WEAK_HEX)
    }
    pub fn mild() -> Rgba {
        rgba(MILD_HEX)
    }
    pub fn firm() -> Rgba {
        rgba(FIRM_HEX)
    }
    pub fn bold() -> Rgba {
        rgba(BOLD_HEX)
    }
}

// ─────────────────────────────────────────────────────────────────────────
// MOTION — duration tokens, in seconds. Names describe the *register*.
// ─────────────────────────────────────────────────────────────────────────

/// Motion durations, in seconds. GPUI 0.2 has no implicit transitions,
/// but views still drive timed effects (debounces, breath cycles, snap
/// guide flashes); putting their durations here keeps the temporal
/// vocabulary as disciplined as the spatial one.
///
/// Three registers live in the design plates:
/// - **TIGHT / NORMAL / RELAXED** — UI state changes the user requested
///   (hover, click, focus). Snappy.
/// - **BREATH_LIVE / BREATH_HALTED** — ambient pulse on live state dots.
///   Slower for working, faster for halted; never blink.
/// - **DRIFT_FAR / DRIFT_NEAR** — parallax-slow background drift.
pub mod motion {
    /// `0.08s` — micro-interaction (cursor in/out, dot scale).
    pub const TIGHT: f32 = 0.08;
    /// `0.14s` — default UI transition (hover, focus ring, swap).
    pub const NORMAL: f32 = 0.14;
    /// `0.24s` — relaxed transition (panel entrance, dialog open).
    pub const RELAXED: f32 = 0.24;
    /// `3.6s` — working / waiting breath. Slow, present.
    pub const BREATH_LIVE: f32 = 3.6;
    /// `2.4s` — halted / approval breath. Faster — more urgent voice.
    pub const BREATH_HALTED: f32 = 2.4;
    /// `2.4s` — aurora caret pulse cycle.
    pub const AURORA: f32 = 2.4;
}

// ─────────────────────────────────────────────────────────────────────────
// MAPPING — protocol enums to the design system.
// ─────────────────────────────────────────────────────────────────────────

/// Resolve the classification hue for a `WorkspaceStatus`. Putting this
/// here keeps the mapping next to the tokens it produces; views never
/// hand-roll a `match` over status values.
pub fn workspace_status_color(status: attn_protocol::WorkspaceStatus) -> Rgba {
    use attn_protocol::WorkspaceStatus;
    match status {
        WorkspaceStatus::Launching => state::launching(),
        WorkspaceStatus::Working => state::working(),
        WorkspaceStatus::WaitingInput => state::waiting(),
        WorkspaceStatus::PendingApproval => state::approval(),
        WorkspaceStatus::Idle => state::idle(),
    }
}

/// Resolve the classification hue for a `SessionState`. `unknown` is
/// deliberately error-coloured because it means the daemon/client cannot
/// classify where keyboard attention belongs.
pub fn session_state_color(status: attn_protocol::SessionState) -> Rgba {
    use attn_protocol::SessionState;
    match status {
        SessionState::Launching => state::launching(),
        SessionState::Working => state::working(),
        SessionState::WaitingInput => state::waiting(),
        SessionState::PendingApproval => state::approval(),
        SessionState::Idle => state::idle(),
        SessionState::Unknown => state::error(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Guards the design rule: idle status sits at the same visual
    /// weight as dim metadata. If you change one, you should change the
    /// other (or break this test on purpose).
    #[test]
    fn idle_state_matches_ash_text() {
        assert_eq!(state::IDLE_HEX, moon::ASH_HEX);
    }

    /// Sodium is the single accent — there is no second one. If a
    /// future change introduces another saturated warm hue at this
    /// chroma, the design rule wants you to revisit the choice.
    #[test]
    fn sodium_vapor_is_the_canonical_accent() {
        assert_eq!(sodium::VAPOR_HEX, 0xff_8a_3d);
    }

    /// Sodium tints share the vapor hue and only vary alpha — so the
    /// glow / soft / hush always read as "the same orange, more
    /// transparent" rather than as a second accent that drifted on
    /// hue. Compare the high three RGB bytes only.
    #[test]
    fn sodium_tints_share_the_vapor_hue() {
        let vapor = sodium::VAPOR_HEX;
        let hue_only = |hex_with_alpha: u32| (hex_with_alpha >> 8) & 0xff_ff_ff;
        assert_eq!(hue_only(sodium::SOFT_HEX), vapor);
        assert_eq!(hue_only(sodium::GLOW_HEX), vapor);
        assert_eq!(hue_only(sodium::HUSH_HEX), vapor);
    }

    /// Hairline scale is monotone. `weak < mild < firm < bold` in
    /// alpha (the low byte of the 0xRRGGBBAA representation). If a
    /// future tweak swaps two values you'll catch it here before the
    /// dividers stop reading as a ramp.
    #[test]
    fn line_scale_is_monotone() {
        let alpha = |hex: u32| hex & 0xff;
        assert!(alpha(line::WEAK_HEX) < alpha(line::MILD_HEX));
        assert!(alpha(line::MILD_HEX) < alpha(line::FIRM_HEX));
        assert!(alpha(line::FIRM_HEX) < alpha(line::BOLD_HEX));
    }

    /// Motion register: live breath is slower than halted breath. Halted
    /// is the more urgent voice, so its pulse cycles faster. If those
    /// invert the design rule "halted is the only urgent voice" gets
    /// quietly broken.
    #[test]
    fn halted_breath_is_faster_than_live() {
        const { assert!(motion::BREATH_HALTED < motion::BREATH_LIVE) };
    }

    /// Star colors are unique. A collision would mean two agents read
    /// identically in the canvas — a bug.
    #[test]
    fn agent_stars_are_distinct() {
        assert_ne!(star::CLAUDE_HEX, star::CODEX_HEX);
        assert_ne!(star::CODEX_HEX, star::SHELL_HEX);
        assert_ne!(star::CLAUDE_HEX, star::SHELL_HEX);
    }

    /// Same for state hues.
    #[test]
    fn state_hues_are_distinct() {
        let hues = [
            state::LAUNCHING_HEX,
            state::WORKING_HEX,
            state::WAITING_HEX,
            state::APPROVAL_HEX,
            state::REVIEW_HEX,
            state::ERROR_HEX,
        ];
        for (i, a) in hues.iter().enumerate() {
            for b in hues.iter().skip(i + 1) {
                assert_ne!(a, b, "state hue collision: {:#08x} vs {:#08x}", a, b);
            }
        }
    }

    #[test]
    fn session_state_mapping_covers_every_wire_state() {
        use attn_protocol::SessionState;

        assert_eq!(
            session_state_color(SessionState::Launching),
            state::launching()
        );
        assert_eq!(session_state_color(SessionState::Working), state::working());
        assert_eq!(
            session_state_color(SessionState::WaitingInput),
            state::waiting()
        );
        assert_eq!(
            session_state_color(SessionState::PendingApproval),
            state::approval()
        );
        assert_eq!(session_state_color(SessionState::Idle), state::idle());
        assert_eq!(session_state_color(SessionState::Unknown), state::error());
    }
}
