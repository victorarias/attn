# Real App Harness

This harness now supports two packaged-app automation modes:

- native macOS input driving
- a dev-only in-app UI automation bridge

Current entrypoint:

```bash
pnpm run real-app:smoke
pnpm run real-app:repro-panes
pnpm run real-app:bridge-smoke
pnpm run real-app:bridge-remote-split-input
pnpm run real-app:bridge-remote-split-geometry
pnpm run real-app:bridge-remote-relaunch-splits
pnpm run real-app:bridge-repro-main
pnpm run real-app:bridge-diagnose-session -- --label blubs
pnpm run real-app:bridge-perf
pnpm run real-app:bridge-pty-bench
pnpm run real-app:bridge-cli -- --wait-ready get_state
pnpm run real-app:scenario-tr101
pnpm run real-app:scenario-tr102
pnpm run real-app:scenario-tr205
pnpm run real-app:scenario-tr402
pnpm run real-app:scenario-tr502
```

What the smoke flow does:

1. launch `/Applications/attn.app`
2. create a unique session through the real deep-link path
3. wait for the daemon to report the new session and workspace
4. send the real `Cmd+D` split shortcut to the packaged app
5. type into the new utility shell pane
6. verify the typed token appears in PTY scrollback

Artifacts are written to:

```text
/tmp/attn-real-app-harness/<run-id>/
```

Requirements:

- `/Applications/attn.app` installed and launchable
- daemon websocket reachable at `ws://127.0.0.1:9849/ws`

For the dev-only UI automation bridge, build the app with:

```bash
ATTN_UI_AUTOMATION=1 VITE_UI_AUTOMATION=1 make install-app
```

That writes a localhost bridge manifest to:

```text
~/Library/Application Support/com.attn.manager/debug/ui-automation.json
```

Useful bridge actions:

- `ping`
- `get_state`
- `get_workspace`
- `create_session`
- `select_session`
- `split_pane`
- `focus_pane`
- `write_pane`
- `read_pane_text`
- `set_pane_debug`
- `dump_pane_debug`
- `capture_structured_snapshot`
- `capture_perf_snapshot`
- `clear_perf_counters`
- `benchmark_pty_transport`
- `get_window_bounds`
- `capture_screenshot_data`

Notes:

- `capture_screenshot_data` is a DOM-rendered image from `html-to-image`. It is useful for layout structure, but it is not authoritative for WebGL-backed terminals.
- The real-app harness scripts now also capture native macOS window screenshots for diagnosis bundles when pixel-accurate terminal rendering matters.

Suggested diagnostic flow for terminal layout/rendering issues:

```bash
pnpm run real-app:bridge-diagnose-session -- --label blubs
```

Suggested relaunch/startup diagnostic flow that does not heal the session first:

```bash
pnpm run real-app:bridge-diagnose-session -- --fresh-launch --no-select-session --session-id <session-id>
```

Suggested remote split latency repro:

```bash
pnpm run real-app:bridge-remote-split-input
```

Suggested remote multi-split geometry repro:

```bash
pnpm run real-app:bridge-remote-split-geometry
```

Suggested remote relaunch split-persistence repro:

```bash
pnpm run real-app:bridge-remote-relaunch-splits
```

That flow:

- launches a fresh packaged app with an isolated remote attn daemon target on the SSH host
- creates a fresh remote endpoint on `ai-sandbox`
- spawns one remote session directly against the endpoint
- splits to a utility pane and measures focus, typed-echo, and output latency
- saves session-scoped render health, resize history, pane debug, runtime trace, PTY traffic attribution, local/remote binary provenance, and the matching local/remote daemon-worker log slices for the exercised runtime

That captures:

- session UI state
- structured model/view/DOM snapshot for the selected session
- native packaged-app window screenshot for the selected session
- terminal perf snapshot with per-pane render counters, startup lifecycle, and last resize info
- a condensed `summary.json` that compares workspace/container widths, pane bounds, and terminal metrics

This is intentionally a baseline harness. Once it is stable, extend it with:

- main-pane prompt/echo checks
- pane-switch regression reproduction
- screenshot/log capture on failure

Scenario foundation:

- `scenarioRunner.mjs`: shared step logging, assertions, and run summaries
- `scenarioAssertions.mjs`: pane visibility, content, coverage, and artifact helpers
- `scenarioAgents.mjs`: real-agent helpers such as Claude prompt readiness
- `scenarioRemote.mjs`: isolated remote daemon bootstrap helpers
- `paneNativeMetrics.mjs`: pane-cropped native screenshot analysis for painted-width and painted-height proof

Native pane assertions use two modes:

- absolute health thresholds for one pane crop
- optional before/after tolerances for acceptable metric drift

They do not rely on exact pixel equality between screenshots.

The first scenario scripts using that foundation are:

- `real-app:scenario-tr101`
  Local Claude session. Prompts the main agent pane for structured output, splits from `main`, and checks that both the source pane and the new utility pane retain meaningful visible content. The main pane is also validated with a pane-cropped native screenshot so the scenario can fail if content collapses into a narrow strip or footer band.
- `real-app:scenario-tr102`
  Local Claude session. Creates a utility pane, splits from that utility pane, and checks that both the original and the new utility pane remain visible and writable with preserved content.
- `real-app:scenario-tr205`
  Remote real-agent session on `ai-sandbox`. Creates one split, relaunches the packaged app, adds two more splits, then closes utility panes one by one and checks that the surviving main Codex pane regains width and repaints into the reclaimed space after each close.
- `real-app:scenario-tr402`
  Remote real-agent session on `ai-sandbox`. Splits the main Codex pane, closes the new utility pane, and checks that the surviving main pane regains width, keeps meaningful visible content, and repaints enough of its pane body to match the pre-split baseline instead of staying visually narrow.
- `real-app:scenario-tr502`
  Remote real-agent session on `ai-sandbox`. Creates a split session, relaunches the packaged app, then splits again from both the main pane and an existing utility pane. The restored main and original shell panes are checked for absolute native paint health and acceptable pre/post relaunch drift.

Current scenarios:

- `real-app:smoke`
  Uses native app input to create one utility pane and verify typed shell output in the packaged app.
- `real-app:repro-panes`
  Uses native app input to create two utility panes, return to the older pane, type again, and verify the older pane still receives shell input.
- `real-app:bridge-smoke`
  Uses the dev-only UI automation bridge instead of macOS input injection to create a session, split a pane, focus it, type into it, and verify shell output.
- `real-app:bridge-repro-main`
  Uses the bridge to split to a utility pane, verify utility output, return to `main`, type a unique token without submitting, and assert that the token appears in both main-runtime scrollback and visible main-pane text.
- `real-app:bridge-diagnose-session`
  Captures a packaged-app diagnostic bundle for one existing session, including workspace model/view/DOM layout state, a native window screenshot, and per-terminal render metrics. Use `--no-select-session` for first-paint relaunch bugs where selecting the session might trigger a refit or redraw.
- `real-app:bridge-remote-split-input`
  Creates one remote session on `ai-sandbox`, splits to a utility pane, types a command, and fails if typed echo or command output exceed the configured thresholds. The artifacts include session-scoped resize history and PTY traffic summaries so remote input lag can be attributed to focus, layout, or websocket delivery.
- `real-app:bridge-remote-split-geometry`
  Creates one remote Codex-style session on `ai-sandbox`, splits the main pane twice, and fails if rendered pane widths drift too far from the workspace model. The artifacts include render health, structured DOM/model snapshots, and per-runtime PTY resize bursts so split-layout regressions can be separated from PTY redraw issues.
- `real-app:bridge-remote-relaunch-splits`
  Creates one remote split session on `ai-sandbox`, relaunches the packaged app, returns to the same session, then splits again from both the main pane and the original utility pane. The run fails if pre-existing panes do not restore visibly or if post-relaunch splits do not appear and accept typing.
- `real-app:scenario-tr502`
  The tiered remote relaunch scenario for `TR-502`. It uses real macOS typing into post-split shell panes, fails if typed echo exceeds the configured threshold (`ATTN_REMOTE_RELAUNCH_SPLITS_ECHO_THRESHOLD_MS`, default `2500`), and artifacts pane-debug plus terminal-runtime traces so input lag can be attributed to focus, attach/replay, or PTY delivery.
- `real-app:bridge-perf`
  Uses the bridge to create a feature-branch session in a synthetic git repo, sample the packaged app, daemon, and child WebKit processes for CPU/RSS, then capture frontend terminal, PTY transport, and diff/review perf snapshots across app-ready, session-open, split-pane, terminal-output, and diff-detail checkpoints. The terminal stage records command-completion time plus delivered scrollback bytes/lines.
- `real-app:bridge-pty-bench`
  Creates a throwaway session and utility pane, then benchmarks three frontend terminal delivery modes on the same payload: `json_base64` (current JSON parse + base64 decode + write path), `base64` (skip JSON parse), and `bytes` (skip JSON parse and base64). The script also samples app, daemon, and WebKit process CPU/RSS while each mode is running.
