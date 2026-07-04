# Real App Harness Policy

This policy applies to packaged-app scenarios in this directory.

## Profiles (which world a run targets)

The harness honors the **one knob**, `ATTN_PROFILE`, like every other entrypoint
(see [docs/profiles.md](../../../docs/profiles.md)). Resolution order:

1. `ATTN_HARNESS_PROFILE` — explicit override when the harness must target a
   *different* world than the surrounding shell. Empty (or `default`) is the
   production escape hatch and still requires `--run-against-prod`.
2. otherwise `ATTN_PROFILE` — a shell that already selected `agent7` drives
   `attn-agent7.app` / its daemon with no extra flags.
3. otherwise the safe `dev` sibling. An unset/empty/`default` `ATTN_PROFILE`
   **never** targets production by omission.

All resources (bundle id, app path, ports, socket, deep-link scheme) come from
the single authority `attn profile resolve`; `harnessProfile.mjs` does not
re-derive them. dev/prod are fast-path literals that a drift guard in
`harnessProfile.test.mjs` asserts equal the authority. Named-profile resolution
needs `./attn` built (`make dev` / `go build -o ./attn ./cmd/attn`); override the
binary with `ATTN_HARNESS_BIN`.

## Real-App Parity

- Scenarios must match real app usage. Do not invent command sequences that the app cannot perform.
- If workspace/session product behavior changes, update these scenarios in the same PR.
- If these scenarios pass while users can reproduce workspace/session errors in the packaged app, treat that as a test design bug.
- Real-app commands target the dev sibling (or the active `ATTN_PROFILE`) by default. Production runs must pass `--run-against-prod`; never bypass the shared production-target guard.

## Screenshot Crop / Scale

`captureFrontWindowScreenshot` (and `capture-app-screenshot.mjs`'s `--crop`/`--max-dim`
flags) can crop to a window-relative region and downscale the PNG at capture time via
`sips -Z`, so an agent that actually looks at the image pays far fewer tokens. `--crop`
accepts `x,y,WxH` (e.g. `0,0,800x600`) or the all-comma `x,y,w,h` form; a crop is clamped
to the window's bounds and only throws if it does not overlap the window at all. Prefer
these over capturing full-resolution, full-window screenshots when only a sub-region
matters for the assertion.

## Workspace Sessions

- A visible pane is a session pane. Do not model durable non-session terminals.
- Resolve pane IDs from daemon/app state. Do not hardcode legacy pane IDs such as `main` for new scenarios.
- Empty workspaces are invalid user-visible state. Tests that create or observe one should assert it is removed or hidden.
- Shortcut scenarios should exercise the documented app shortcuts or the same shortcut registry IDs used by the app.
