# attn-pi plugin guide

attn-side driver plugin for [pi](https://github.com/earendil-works/pi). Canonical
vision: `docs/vision/pi-attn-plugins.md`. Full grounding evidence with citations:
`docs/grounding/pi-plugins.md`. Reference implementation for the driver pattern:
`plugins/attn-opencode`.

## Pinning pi

- pi is 0.x rolling-release (~1-2 releases/week), with real breaking changes
  every few releases, documented only as prose in each package's
  `CHANGELOG.md` under "### Breaking Changes".
- pi has NO extension/version compat gate: an extension built against old
  types loads silently and fails at the first missing call site. Pin the exact
  pi version; treat upgrades as deliberate, changelog-gated events; the
  pi-side suite must self-check the pi version at `session_start` rather than
  assume compatibility.

## pi lifecycle invariants (verified against pi source at v0.80.x)

- One pi process = one live session; resume/fork/new replace the session
  in-process, never re-exec.
- Extension factories re-run on EVERY session transition (resume/fork/new/
  reload); extension contexts from before a transition throw on any use. Only
  module-scope variables survive transitions (and only until cwd changes or
  process exit). A persistent socket must live at module scope; the factory
  re-links, it does not re-dial.
- `session_shutdown` fires on clean quit and SIGTERM/SIGHUP, but NOT on
  uncaught exceptions or SIGKILL. Never treat a missing goodbye as
  meaningful: the PTY child exit that the driver observes is the
  authoritative liveness signal; declared state rides on top.
- Session identity: resume keeps the same session id + JSONL file; fork mints
  a new id with a `parentSession` header link; a plain `/new` has NO lineage
  link. Correlation with the attn session must be re-declared on every
  `session_start`, not inferred from files.
- The driver mints native session identity at spawn with
  `pi --session-id <id>` (creates if missing); resume uses the same flag.
  Never parse pi's session picker.

## pi TUI under attn's PTY (verified against pi source)

- pi never uses the alternate screen, never sends CPR, and queries only OSC
  11 (background color, once, for light/dark detection) — the worker answers
  it from the daemon-pushed theme.
- pi negotiates the Kitty keyboard protocol with `ESC[>7u ESC[?u ESC[c`;
  attn's worker answers the trailing DA1, so pi deterministically falls back
  to modifyOtherKeys. Degraded-but-correct; do not "fix" this by answering
  kitty queries.
- pi full-redraws on any resize under DEC 2026 synchronized output and
  self-fires SIGWINCH at startup. Resize races are expected to self-heal;
  live-verify before assuming otherwise.
- Not yet live-verified: resize races under a real attn PTY, and Kitty
  graphics images surviving vt10x snapshot/replay.

## Driver pattern

- Follow `plugins/attn-opencode`: `attn-plugin.toml` (`attn_api_version`
  gate), `driver.register` with capability map, `driver.spawn`/`driver.resume`
  returning argv+env+cwd that the daemon runs in the attn-owned PTY,
  `session.report_metadata` as the resume token, `driver.session_closed`
  cleanup.
- Rock 1 is a pure driver with dumb state (no `state_reporting`); declared
  state arrives with the pi-side attn suite (rock 2). Do not add
  screen-scraping state detectors for pi.
- Staging the pi-side suite uses `pi install <local-dir> -a` (non-interactive;
  stores the absolute path, no copy).
