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
- The driver also owns pi citizenship (rock 2): it listens on a relay unix
  socket, injects `-e <bundled suite.js>` plus `ATTN_PI_SUITE_SOCKET` /
  `ATTN_PI_TOKEN` env at spawn/resume, registers `state_reporting` +
  `message_delivery`, relays suite reports to the daemon (the per-run seq
  cursor lives driver-side), and forwards `driver.deliver_message` to the
  suite. Stop verdicts come from daemon-side classification
  (`attn.classify_stop`); an empty settle (nothing said) reports `idle`
  without classifying. Never add screen-scraping state detectors for pi, and
  never fall back to PTY typing for message delivery.

## Suite invariants

- The suite (`plugins/attn-pi/suite/`, staged as a single `suite.js` next to
  the driver executable) runs inside pi's runtime. It must never crash or
  block pi: relay sends are fire-and-forget, failures are swallowed, and
  missing `ATTN_PI_SUITE_SOCKET`/`ATTN_PI_TOKEN` env turns the whole suite
  into a no-op (bare pi outside attn).
- The relay client lives at module scope and survives session transitions;
  every factory re-run re-binds the current pi/ctx (stale ones throw on any
  use — `driver.deliver_message` answers `delivered: false` then).
- `agent_end` caches the last assistant message's text; `agent_settled` has
  no payload and ships the cache as `suite.report_stop`.
- `suite/index.ts` imports pi's `VERSION` from
  `@earendil-works/pi-coding-agent`; pi resolves it as a virtual module at
  load time, so the bundle step must keep that import `--external`.
- PTY child exit stays the authoritative liveness signal; suite silence is
  never meaningful.
