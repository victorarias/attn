# Real App Harness

This harness now supports two packaged-app automation modes:

- native macOS input driving
- a dev-only in-app UI automation bridge

Current entrypoint:

```bash
pnpm run real-app:smoke
pnpm run real-app:repro-panes
pnpm run real-app:bridge-smoke
pnpm run real-app:bridge-repro-main
pnpm run real-app:bridge-perf
pnpm run real-app:bridge-pty-bench
pnpm run real-app:bridge-cli -- --wait-ready get_state
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
- `capture_screenshot`

This is intentionally a baseline harness. Once it is stable, extend it with:

- main-pane prompt/echo checks
- pane-switch regression reproduction
- screenshot/log capture on failure

Current scenarios:

- `real-app:smoke`
  Uses native app input to create one utility pane and verify typed shell output in the packaged app.
- `real-app:repro-panes`
  Uses native app input to create two utility panes, return to the older pane, type again, and verify the older pane still receives shell input.
- `real-app:bridge-smoke`
  Uses the dev-only UI automation bridge instead of macOS input injection to create a session, split a pane, focus it, type into it, and verify shell output.
- `real-app:bridge-repro-main`
  Uses the bridge to split to a utility pane, verify utility output, return to `main`, type a unique token without submitting, and assert that the token appears in both main-runtime scrollback and visible main-pane text.
- `real-app:bridge-perf`
  Uses the bridge to create a feature-branch session in a synthetic git repo, sample the packaged app, daemon, and child WebKit processes for CPU/RSS, then capture frontend terminal, PTY transport, and diff/review perf snapshots across app-ready, session-open, split-pane, terminal-output, and diff-detail checkpoints. The terminal stage records command-completion time plus delivered scrollback bytes/lines.
- `real-app:bridge-pty-bench`
  Creates a throwaway session and utility pane, then benchmarks three frontend terminal delivery modes on the same payload: `json_base64` (current JSON parse + base64 decode + write path), `base64` (skip JSON parse), and `bytes` (skip JSON parse and base64). The script also samples app, daemon, and WebKit process CPU/RSS while each mode is running.
