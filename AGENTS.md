# AGENTS.md

Repository guidance for coding agents working in this repo.

## Build And Test

```bash
# source/dev install
make install
make install-daemon

# app
make build-app

# tests
make test
make test-frontend
make test-e2e
make test-harness
make test-all
go test ./internal/store -run TestList
```

## Supported Platform

- attn supports macOS only. Do not add Windows or Linux compatibility code unless explicitly requested.

Use `make install` as the default source/dev install path; it rebuilds and installs the app bundle and ensures the bundled daemon is running. Use `make install-daemon` when only daemon/runtime code changed and you want the faster sidecar-only loop.

### Iterating On Attn Itself (Attn-On-Attn Testing)

**Standing authorization — non-prod installs are always allowed; prod needs approval.** You may build, code-sign, install, run, and restart any **non-prod** profile at will, without asking first — the `dev` sibling (`make dev`, `make install-daemon-dev`) or any named profile (`make install PROFILE=<name>`), including the full real code-signing flow. Treat this as pre-approved standing permission: when you need to see or test a change in the actual app, default to spinning up `attn-dev.app` rather than a mocked harness. Because signing needs the user's macOS keychain, run these dev/profile builds with the sandbox disabled (see the keychain note below) — that's expected and authorized for non-prod. The **only** thing that requires Victor's explicit approval is the **production** app: bare `make` / `make install` / `make install-daemon`, i.e. the live `~/Applications/attn.app` and its daemon. Never build, install, or restart prod without asking.

While iterating on attn code, prefer the dev sibling install so rebuilds do not repeatedly interrupt the live production app:

```bash
make dev              # builds + installs ~/Applications/attn-dev.app, starts dev daemon on port 29849
make install-daemon-dev  # faster: only rebuild the Go sidecar inside attn-dev.app
```

The dev install is fully isolated: its own bundle identifier (`com.attn.manager.dev`), its own data dir (`~/.attn-dev/`), its own socket, its own port. Once a fix is verified, the production app (`make`) ships it to the user — but per the authorization note above, only do that prod install with Victor's approval. The production install closes and reopens the app and restarts its daemon; attn is designed to recover persisted session state gracefully. Bare `make install` / `make install-daemon` build the **prod** bundle, so they refuse at parse time if `ATTN_PROFILE` is set in your shell (that would clobber prod). To build a profile's own isolated app instead, use `make install PROFILE=<name>` — it must match your shell's `ATTN_PROFILE`. `make dev` (and `install-daemon-dev`) is the shorthand for the dev sibling and works from any shell, including one scoped to another profile. `attn profile clean <name>` tears a profile down (stop daemon, quit app, remove its bundle + data dir).

Run full app builds and installs via `make` or `make dev` outside the sandbox. These commands need access to the user's macOS keychain so `security find-identity` can select the stable code-signing identity. Inside the sandbox, identity discovery can incorrectly return nothing, causing certificate creation or ad-hoc signing and breaking the app's persistent macOS permissions.

To make CLI commands (`attn`, `attn list`, etc.) target the dev daemon in your shell, run `eval "$(./attn profile-env dev)"` (bash/zsh) or `./attn profile-env --fish dev | source` (fish). Any `attn` subcommand then prints a one-line `[attn profile=dev ...]` banner so you can always see which daemon you're talking to. Unset with `eval "$(attn profile-env --unset)"`.

`dev` is just one named profile. `ATTN_PROFILE` selects an isolated world (data dir, socket, port, app bundle) for **every** entrypoint, so multiple agents can run side by side. Run `attn profile` to see where you are, `attn profile list` for all profiles, and `attn profile resolve --json` for the machine-readable resolution. See **[docs/profiles.md](docs/profiles.md)** for the full model, the per-agent test recipe, and the safety rules.

Frontend-only shortcuts:

```bash
cd app
pnpm run dev
pnpm test
pnpm run e2e
```

## Core Rules

- Do not add tests that only restate compile-time guarantees.
- Do not copy production code into tests.
- Maintain `CHANGELOG.md` for user-visible changes and include it in the same commit when appropriate.
- Diagnose root cause before fixing symptoms.
- Do not remove requested functionality without explicit approval.

## Live-App Testing Is Required

Automated tests are necessary but not sufficient. Every PR must be exercised in the **live app** (a running daemon/app built from the branch) before it is called done or merged, with exactly two exemptions:

- the change is **trivial** (e.g. a comment, a doc, a rename, a log-string tweak), or
- the change is one you have **extremely high confidence** in without a live-app test, and you can say concretely why (for example: a pure, well-isolated function fully covered by unit tests, with no daemon-process, protocol, timing, or UI surface).

A large automated suite — even a race-clean concurrency suite — is not by itself one of these exemptions. If the change touches the daemon process lifecycle, protocol, PTY, background runners, or any UI, it needs live-app verification.

Use a **non-prod** profile for this (the `dev` sibling or a throwaway `ATTN_PROFILE`) — that is pre-authorized; never smoke on prod. See [Iterating On Attn Itself](#iterating-on-attn-itself-attn-on-attn-testing) and [docs/profiles.md](docs/profiles.md).

If a PR needs live-app verification and you **cannot** run it (no ability to build/install/run a non-prod profile in this environment), **stop and ask the user for guidance** — do not merge on automated tests alone. State plainly in the PR and to the user what was and was not verified live.

## Packaged-App Harness

- Real packaged-app scenarios are single-tenant. Never run them in parallel.
- Treat failures from parallel packaged-app runs as invalid harness usage, not product evidence.
- Prefer `pnpm --dir app run real-app:serial-matrix` for multiple packaged-app scenarios.
- If you need one scenario, run exactly one packaged-app scenario at a time and wait for it to finish.
- Rebuild first when packaged-app evidence matters, or you may be testing an older installed app.
- Real-app harness commands honor the active `ATTN_PROFILE` (the one knob — see [docs/profiles.md](docs/profiles.md)) and otherwise default to the **dev** install (`~/Applications/attn-dev.app`, port 29849) so they never take over the live prod app. Run `make dev` first if there's no dev install yet. `ATTN_HARNESS_PROFILE` overrides the shell's profile for the harness only. To target prod explicitly, set `ATTN_HARNESS_PROFILE=` (empty) and pass `--run-against-prod`; the harness refuses production lifecycle operations without that flag.
- When a packaged-app scenario fails, always inspect the captured pane text and any native screenshot artifacts before diagnosing the cause. Startup prompts, permission dialogs, and agent-owned redraws are part of the evidence.

## Debugging And Logging

Verbose daemon logging:

```bash
DEBUG=debug attn -s test
```

- daemon log: `~/.attn/daemon.log`
- worker PTY logs (production backend): `~/.attn/workers/<daemon-instance>/log/<session>.log` — `pty.Session` `logf` lines land HERE, not in `daemon.log` (which only shows `pty_output forward`/`pty_resize`). Swap the data dir for dev (`~/.attn-dev/...`).
- the app auto-respawns the daemon WITHOUT `DEBUG` when its socket drops; to capture debug logs, quit the app first, then start the daemon manually (`DEBUG=debug ... attn daemon ensure`)
- use `d.logf(...)` in daemon code and pass `LogFunc` into subpackages when needed
- do not use `log.Printf()` for daemon logging because stderr is discarded in background mode
- use prefixed `console.log/warn/error` in frontend code and inspect via Tauri DevTools

### Frontend Instrumentation (disk-based)

For hard-to-reproduce UI bugs, prefer disk-based JSONL logs over `console.log` — agents can read files but not DevTools. Write to `$APPLOCALDATA/debug/<name>.jsonl` using Tauri's `writeTextFile`. See `app/src/utils/terminalDiagnosticsLog.ts` and `app/src/utils/terminalLinkHitTestLog.ts` for the pattern. Remove temporary instrumentation once the bug is resolved.

## Architecture Snapshot

- `cmd/attn`: CLI wrapper that launches agents, registers sessions, and wires hooks/settings
- `internal/hooks`: Claude hook generation and state/todo reporting
- `internal/daemon`: session lifecycle, PTY management, git/github operations, websocket updates
- `internal/pty`: PTY session, read loop, scrollback/replay, terminal-query (CPR/DA1/OSC) responses
- `internal/ptybackend`: PTY backend selector — `embedded` (in-daemon) vs `worker` (default, set by `ATTN_PTY_BACKEND`)
- `internal/ptyworker`: per-session worker process. Production runs the **worker** backend: a separate `attn` process per session. Both backends spawn PTYs through `internal/pty`, so read-loop changes live in one place but RUN in the worker process, not the daemon.
- `internal/store`: SQLite-backed state with in-memory cache
- `internal/classifier`: stop-time WAITING/DONE classification
- `internal/transcript`: last-assistant-message extraction from JSONL transcripts
- app: Tauri frontend over `ws://localhost:9849`

Session states:

- `launching`: session opened, waiting for first authoritative runtime signal
- `working`: actively running
- `pending_approval`: blocked on approval
- `waiting_input`: stopped and needs user direction
- `idle`: done
- `unknown`: state could not be determined reliably

`needs_review_after_long_run` is not a state — it is a separate boolean flag on the session, set when a 5+ minute run finishes; final classification is deferred until the user views the session, which clears the flag.

## Critical Patterns

### 1. Protocol Versioning

When changing commands, events, or protocol message shapes:

1. update `internal/protocol/schema/main.tsp` if needed
2. run `make generate-types`
3. update `internal/protocol/constants.go`
4. increment `ProtocolVersion` in `internal/protocol/constants.go` for protocol changes
5. run `make install`

Why: the daemon survives app rebuilds. Old daemon plus new app creates silent breakage unless protocol versioning forces a reconnect failure.

Generated files you do not hand-edit:

- `internal/protocol/generated.go`
- `app/src/types/generated.ts`

### 2. Async WebSocket Actions

For operations that can fail, do not use optimistic fire-and-forget UI.

Use the request/result pattern:

- daemon handles the command and emits a `*_result` event
- protocol defines success and error fields
- frontend returns a `Promise`, tracks pending actions, and resolves or rejects on the result event

Canonical example: `sendPRAction` in `app/src/hooks/useDaemonSocket.ts`.

### 3. Classifier Timestamp Protection

Every persisted daemon session-state transition must go through `applyState` in
`internal/daemon/session_state.go`; direct store state writes bypass required
effects and are structurally forbidden. Classifier work is asynchronous and can
be slow, so capture its observation timestamp before classification starts and
pass it through `classifierObservation` so the door rejects stale results.

### 4. Review Comment Fan-Out

When changing `ReviewComment`, update all consumers:

- TypeSpec schema
- store (`internal/store/review.go`)
- daemon websocket handlers (`internal/daemon/ws_review.go`)
- frontend hooks and UI

### 5. Pointer/Value Conversions

Store APIs often use pointer slices while protocol responses use value slices. Prefer helpers in `internal/protocol/helpers.go` such as `Ptr`, `Deref`, `SessionsToValues`, and `PRsToValues`.

### 6. Terminal Focus Ownership

When switching sessions, do not blindly refocus the main terminal. If the selected session has an active utility terminal, keep focus there.

Why:

- `Cmd+T` and utility workflows depend on keyboard input reaching the utility PTY
- delayed `mainTerminal.focus()` can steal focus after utility mount
- failure mode: cursor looks active but typing goes to the wrong PTY or disappears

Implementation rule:

- in `app/src/App.tsx`, selection should `fit()` main terminal but only `focus()` it when utility is not open or active
- in `app/src/components/SessionTerminalWorkspace/index.tsx`, prefer the active `GhosttyTerminal` handle's `focus()` before any fallback retry

Verification (manual — the dedicated realpty spec was removed together with the
legacy non-session workspace panes, and no automated focus-ownership spec
replaced it):

- confirm `Cmd+T` typing works without extra click
- confirm switch-away and switch-back still types into utility without extra click

### 7. PTY Geometry And Replay

Before changing PTY attach/replay, terminal resize, mobile keyboard viewport handling, or daemon-served terminal rendering, preserve these rules:

- PTY geometry has one authority at a time: the most recently active interactive client
- `pty_resize` is authoritative; `attach_session` replay is provisional context only
- do not use replay as a generic redraw repair tool
- do not treat local `fit()` or viewport churn as proof the PTY is correct
- do not let replayed historical terminal queries produce fresh live PTY input
- the daemon (inside the worker) is the sole responder for CPR, DA1, and OSC 10/11/12 color queries — answered every time from the read loop. The frontend answers none of these: it strips CPR, DA1, and OSC color responses from its terminal model's output and only pushes theme changes down via `set_terminal_theme` (the daemon stores the theme globally, fans it out to live sessions, and seeds new spawns). Breaking this split reintroduces the ~10–30s reattach prompt-hang, and late or duplicate color replies surface as stray `^[]11;rgb:...` input in shell panes and crash interactive prompts like `gh pr create`.
- for mobile or viewport bugs, keep structured instrumentation and prefer transition evidence over screenshots alone

### 8. macOS Menu Intercepts Cmd+C

In the packaged app, plain Cmd+C never reaches DOM keydown: the native Edit > Copy menu
claims the key equivalent and WebKit fires a DOM `copy` clipboard event instead. Handle
copy behavior in an `onCopy` handler (see `GhosttyTerminal`), not only keydown. Browser
e2e cannot catch this — Playwright delivers Cmd+C as a normal keydown — so copy-shortcut
changes need packaged-app verification (`real-app:scenario-terminal-block-copy`).

The same trap applies to ANY web shortcut whose combo matches a `Menu::default` accelerator
(the macOS Edit/File/View/Window standard items). Example: `terminal.toggleZoom` is ⇧⌘Z,
which is Edit > Redo — the native Redo item swallowed it, so zoom was dead in the installed
app while e2e/unit tests passed (Playwright/jsdom never hit the native menu). Fix in
`app/src-tauri/src/lib.rs` `app_menu()`: remove the predefined item that owns the combo so the
key reaches the WebView, where the DOM shortcut resolver in `useShortcut.ts` dispatches it —
which keeps the shortcut respecting user rebindings, unlike a hardcoded native menu item. (Use
the forward-via-`dispatch_native_shortcut` bridge — as Close Window → Close Pane does — only
when you also need a visible/relabeled menu item; it hardcodes the action and bypasses the
resolver.) When adding a new shortcut, check it against the default-menu accelerators first.

## Communication

- unix socket IPC: `~/.attn/attn.sock`
- websocket: `ws://localhost:9849`
- websocket clients have a 256-message buffer; sustained frontend over-send can drop messages or disconnect slow clients

## Changelog

`CHANGELOG.md` uses dated sections, not versions.

When updating it:

- write for users, not maintainers
- summarize behavior changes, not implementation details
- scope entries to the PR as a whole: describe the net user-visible outcome the PR delivers, not each step, iteration, or dead end taken along the way. A PR usually warrants a single focused entry even if it spanned many commits.
- group related work into a small number of bullets
- update it for meaningful user-visible features, fixes, or removals; internal-only changes (refactors, instrumentation, tests) do not need an entry

## When Something Is Broken

1. Diagnose why before proposing a fix.
2. Fix the root cause instead of removing behavior to hide the symptom.
3. If refactoring, list the behaviors that must survive and verify them.
4. Do not remove user-requested functionality without approval.

Ask: am I fixing the problem or avoiding it?
