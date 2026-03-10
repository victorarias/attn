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
