# attn agent guide

macOS only for the **app** (the Tauri UI). Do not add Linux or Windows app
compatibility unless requested. The **headless daemon**, however, is supported on
Linux (amd64/arm64) — the hub cross-compiles and runs it on Linux remotes — so
daemon-side code (`cmd/attn`, `internal/**`) must build and run on Linux too. See
"Native VT library" at the end.

## What makes attn special?

attn is Victor's most loved and most used piece of software. It is not widely
used, by design — Victor doesn't want to carry a large user base — but the few
people who run it matter to him. Maintain and iterate on it like something loved.

The things we can never compromise on: frictionless experience, keyboard
friendliness, and performance. They go hand in hand — a dropped frame or a
forced reach for the mouse is friction. And attn runs all day, every day:
memory that creeps or CPU burned while idle is a defect, not a footnote.

## Note from Victor

I love ambitious ideas and strive for simple, elegant systems. Refactoring
first so a new feature weaves in gracefully is the norm here, not an exception
to argue for. I work iteratively, crafting boutique software — not IKEA
software. Nothing wrong with IKEA; it just doesn't spark passion in me. attn is
my passion software.

## You are probably running inside attn

Most attn work is done from attn itself, often driven remotely. The session you
are working in is an attn session: its PTY lives in `internal/ptyworker`, the
daemon that owns it listens on `~/.attn/attn.sock`, and its state is in the
production `~/.attn` database. A careless command ends your own session, or one of
Victor's others.

- Never kill by pattern. No `pkill -f attn`, no `pgrep | kill`, no killing a PID
  you matched on a name, path, or worktree string — your own process and every
  other live session carry those strings in their argv. Kill only a PID you
  captured at spawn, or a port/socket owner you confirmed by its working
  directory.
- Production `~/.attn` is read-only to you. Copy out of it for realistic data,
  but never point a daemon at it, never open it read-write, never clean it up.
  See "Test safety".
- Restart the dev daemon, never the one hosting you. Non-production builds,
  installs, and restarts are pre-authorized precisely so you never need to touch
  production; confirm the `[attn profile=…]` banner before any lifecycle command.

## Language

`docs/glossary.md` is the source of truth for attn's domain vocabulary —
workspace context, the Notebook, tickets, delegations, turns, and friends. Read it
before naming anything new, and when an implementation has drifted from a
definition, fix one or the other in the same change.

Several terms collide in this repo. Keep them apart:

- **you** — the agent reading this file and changing attn.
- **we**, **Victor** — who you are talking to; attn's maintainer.
- **user** — the person using attn to direct agents. Usually Victor too, but the
  distinction decides product questions.
- **agent** — depending on context: you, the agent attn launches into a session
  (Claude Code, Codex), or a delegated agent on the board. Say which whenever the
  sentence does not make it obvious.
- **session** — one attn-managed agent process with a PTY. Not a delegation, not
  a workspace.

## Commands

```bash
# isolated non-production install (pre-authorized)
make dev                    # build/install/open attn-dev.app; ensure dev daemon
make install-daemon-dev     # replace/re-sign daemon sidecar only
make install PROFILE=<name> # build/install named isolated profile

# production (Victor's explicit approval required)
make                        # build/install/open ~/Applications/attn.app
make install
make install-daemon

# build and test
make build-app
make test
make test-frontend
make test-e2e
make test-harness           # Go + frontend + e2e
make test-all               # Go + frontend
go test ./internal/store -run TestList

# frontend-only loop
pnpm --dir app run dev
pnpm --dir app test
pnpm --dir app run e2e
```

Run full app builds/installs outside the sandbox: code signing needs the macOS
keychain; sandboxed identity lookup can cause ad-hoc signing and lose persistent
permissions.

## Profiles and live verification

- Non-production builds, installs, launches, and restarts are pre-authorized.
- Production `make`, `make install`, and `make install-daemon` require Victor's
  explicit approval.
- Install the cheapest tier that covers the change:
  - Go-only change (`cmd/attn`, `internal/**`) → `make install-daemon-dev`, or
    `make install-daemon PROFILE=<name>`. Replaces and re-signs the sidecar and
    restarts the daemon; no Tauri/Rust/frontend build.
  - Anything under `app/` (frontend, `src-tauri`, plugins), a protocol change
    (`generated.ts` moves with `generated.go`), or bundle metadata → `make dev`,
    or `make install PROFILE=<name>`.
  Escalate to the full build when unsure, or when a daemon-only install does not
  show the change.
- Named profile: select it with `eval "$(./attn profile-env <name>)"`, then run
  `make install PROFILE=<name>`. The shell's `ATTN_PROFILE` must match.
- `profile-env` clears inherited routing overrides. Verify the emitted
  `[attn profile=…]` banner before acting.
- Inspect with `attn profile`, `attn profile list`, or
  `attn profile resolve --json`; remove with `attn profile clean <name>`.
- **Clean up the profile you created.** Nothing reaps it for you: its daemon
  (~40MB) and every pty-worker (~15MB) keep running until someone notices them
  days later. `attn profile clean <name>` reaps the workers, stops the daemon,
  quits the app, and removes the bundle and data dir. `make install
  PROFILE=<name>` records the worktree it ran from, so `attn profile list` tells
  you which profiles are yours, and a PostToolUse hook reminds you after you
  create or merge a PR.
- Full model and per-agent recipe: [docs/profiles.md](docs/profiles.md).

Every non-trivial PR needs live verification from the branch in a running
non-production app/daemon. Exempt only:

- trivial docs/comments/renames/log strings; or
- a pure isolated change fully covered by unit tests, with no daemon lifecycle,
  protocol, PTY, background-runner, timing, or UI surface. State the reason.

Match the verification tier to the behavior the change exposes, not to which
directory it lives in. The cheapest tier applies only when the change has no
app-observable behavior at all — a self-contained CLI command that never talks to
the app or daemon can be verified by running the built binary directly (plus its
unit tests). Example: `attn pr wait-ready` shells out to `gh` and touches no
daemon or app surface, so exercising the binary against a real PR is sufficient.

A daemon change is not automatically daemon-tier. Most daemon work reaches the app
— protocol, persisted state and its broadcasts, WebSocket events, PTY, PR/git
flows — and the app's reaction is part of the behavior, so it needs integrated
verification in the running app even though the code lives under `internal/`.
Reserve daemon-only verification for daemon internals with no path to the app.

Daemon lifecycle, protocol, PTY, background-runner, and UI changes always need
live verification. If the environment cannot run the tier the change requires,
stop and ask; do not merge on automated tests alone.

Before live verification, run the selected profile's bundled preflight:

```bash
profile_app="$(./attn profile resolve --field appPath)"
"$profile_app/Contents/MacOS/attn" preflight
# mirror pinned launch settings when applicable:
"$profile_app/Contents/MacOS/attn" preflight \
  --agent codex --model <model> --effort high --json
```

Use the bundled CLI, not an unrelated `attn` on `PATH`. Preflight is diagnostic;
fix reported tool/path/routing/daemon/protocol failures before treating scenario
output as product evidence.

### Packaged-app harness

- Single-tenant: never run packaged-app scenarios in parallel.
- Multiple scenarios: `pnpm --dir app run real-app:serial-matrix`.
- Rebuild before evidence-sensitive runs.
- Harness uses active `ATTN_PROFILE`, otherwise `dev`;
  `ATTN_HARNESS_PROFILE` overrides it.
- Production requires both `ATTN_HARNESS_PROFILE=` and `--run-against-prod`.
- On failure, inspect captured pane text and native screenshots before diagnosis.
- Remote scenarios target the local OrbStack VM (`attn-remote@orb`); provision with `pnpm --dir app run real-app:provision-remote`.

## Hit every surface

The most common defect is a change that works on the path you tested and is
missing everywhere else. The verification tier above decides how hard to check;
this decides what you forgot. Before calling work done, walk the list and say
which entries applied:

- **CLI.** `cmd/attn`. Behavior reachable from the app is usually also expected
  from the command line, and headless or remote users have only the CLI.
- **Daemon and app.** Most `internal/**` work reaches the app — protocol,
  persisted state and its broadcasts, WebSocket events, PTY, PR/git flows. The
  app's reaction is part of the behavior, not a follow-up.
- **Protocol.** `generated.ts` moves with `generated.go` and `ProtocolVersion`
  increments. Never hand-edit either.
- **Linux.** `cmd/attn` and `internal/**` cross-compile and run on Linux remotes.
  Darwin-only code needs a build constraint and a stub, not an assumption.
- **Plugins and SDK.** `plugins/` and `sdk/` consume the same surfaces.
- **Docs.** New vocabulary in `docs/glossary.md`, design and gate decisions in
  `docs/plans/`, user-visible behavior in a `changelog.d/` fragment.

If you added a way in, add the way out and the way to see it. Snooze needs
unsnooze, an opened turn needs a way to settle, `bus disable` needs `bus enable`,
a created profile needs `profile clean`. A one-way door is a bug.

## Test safety

Tests must never resolve `config.DataDir()` or derived paths to production
`~/.attn`.

- Scope with `ATTN_DATA_DIR`; never redirect `HOME`.
- Any package reaching config paths must define `TestMain`, create one temp dir,
  and call `config.ScopeTestEnvironment(dir)` before `m.Run()`.
- Do not replace that call with raw `os.Setenv`: the helper also clears inherited
  `ATTN_DB_PATH`, `ATTN_SOCKET_PATH`, `ATTN_CONFIG_PATH`, and `ATTN_PLUGIN_DIR`.
- Individual tests may add `t.Setenv("ATTN_DATA_DIR", t.TempDir())`.
- Under `go test`, missing `ATTN_DATA_DIR` intentionally panics.

See [docs/plans/2026-07-18-db-loss-mitigation.md](docs/plans/2026-07-18-db-loss-mitigation.md).

## Testing tools

Three adopted patterns; full receipts in
[docs/plans/2026-08-09-testing-spike-synctest-rapid-toxiproxy.md](docs/plans/2026-08-09-testing-spike-synctest-rapid-toxiproxy.md).

- A test that asserts elapsed time (backoff, debounce, recurrence) or that
  something **never** happens runs under `synctest.Test` — no sleeps, no poll
  loops. House rules, with receipts in `internal/daemon/synctest_test.go`:
  build long-lived resources (daemon, store, DB handles) *outside* the bubble
  and drive them *inside*; seed anything time-stamped inside the bubble (its
  clock starts in 2000, so a fixture stamped with real `time.Now` is decades
  in the future); tear down completely — any goroutine still alive at bubble
  exit is a panic even when every assertion passed. A child process or
  fsnotify watcher does not error — it silently pins the bubble clock to real
  time; those tests stay outside. `synctest.Wait()` is a happens-before edge
  for the race detector; `time.Sleep()` is not, so timer-written state still
  needs its own lock.
- A unit with a stated invariant and a large input space — especially when the
  interesting failures need a *sequence* of operations — gets a
  `pgregory.net/rapid` property beside its example tests. Rapid explores, it
  does not document. Commit the `testdata/rapid/` seeds it writes on failure.
- Behavior that *is* the network being bad (backpressure, eviction, reconnect)
  goes through the embedded Toxiproxy helper (`newToxiProxy(t, upstream)`,
  `internal/daemon`). Anything a fake or direct channel write can express
  does not — it costs real seconds.

## How changes ship

How much process a change gets is a judgment call, not a rulebook. When in
doubt, ask the maintainer.

- **Plan docs are the norm.** Non-trivial work gets a plan doc first — often
  written by the same agent that then implements it in the same session. Only a
  genuinely small change goes straight to a PR with no plan.
- **A plan becomes one or several PRs.** When the work changes existing
  behavior, the PRs target an `epic/*` branch and the epic branch gets a full
  review when it merges to main. When the work builds something new that
  cannot break what already exists, PRs may merge directly to main as they
  land. One question decides between the two — can this PR damage what already
  works? — not how big the plan is.
- **A spike answers a question.** It usually does not merge, but that is not a
  hard rule. What is constant: the maintainer decides what happens after a
  spike — merge it, discard it, or build on what it showed.

### Merging

Every PR merges once figgyster approved the current head, CI is green, and no
review threads are unresolved. No per-PR permission needed.

Wait for the maintainer's explicit OK only before:

- merging an epic branch to main;
- moving on from a spike;
- changes that irreversibly destroy or convert existing user state;
- one-way doors — a way in shipped without its way out;
- production installs and releases (already covered above).

### Experience testing

The maintainer tests how attn feels to use; the harness and figgyster cover
correctness. That testing happens at two moments: very early on spikes (is the
idea right?) and at the end of a substantial series of PRs (does the whole
thing feel right?). It is never per-PR QA — do not hold an approved PR
waiting for it.

When a series gets there, prepare the test: a running profile installed from
the branch, realistic data, and a short list of things to try, focused on what
changed and what you could not judge yourself — feel, latency, keyboard flow.
The maintainer should spend those minutes trying things, not setting up.

### Protocol bumps and migrations

Protocol version bumps and DB migrations are day-to-day work; their checklists
are in this file. Do them, verify them, and do not present them as risks,
blockers, or reasons to pause. They need the maintainer's attention only when
they hit the wait list above — destroying user state, closing a door, touching
production — never just because they are a migration or a protocol change.

## Pull requests

Ready-for-review (not draft) and rebase onto main first, per the global guide;
when to merge is in "How changes ship" above. What is attn-specific is how you
wait on one.

To wait on a GitHub PR, run `attn pr wait-ready <pr> --repo <owner/repo>
--reviewer <login>` once; do not poll checks, reviews, and comments separately.
It returns on the first poll with an actionable update and reports it by exit
code: `0` approved, `1` checks failed, `3` changes requested, `4` human comment,
`5` error, `6` bot comment, `124` timeout. One poll can see several of those at
once; the exit code names the highest ranked (closed, checks failed, changes
requested, human comment, approved, bot comment) and the rest are still reported
— `also <event>:` on stdout, `events` in `--json`. Do not treat the exit code as
the whole answer when you need to know everything that happened. The reviewer's
own verdict is one event, not a verdict plus a comment. Comments already present
when the wait starts are the baseline; `--ignore-author` drops an author of
either kind. Your own comments are never events: the account `gh` is
authenticated as is resolved once per run and its remarks are dropped, so posting
a reply and then waiting does not wake you with your own text. Pass
`--include-self` to watch a PR you also comment on.

The output carries what was said — comment bodies with `file:line` when inline,
the verdict's text, failing check names with URLs, the PR URL — so act on it
instead of querying GitHub again. Successive waits on the same PR resume from
what the previous one reported (recorded under the data dir), so a comment that
lands while you are answering the last one is still reported, and a failing check
you were already told about does not return instantly a second time. `--reset`
forgets that position and `--since <RFC3339>` replays from an instant.

## Change discipline

- Diagnose root cause. Do not remove requested behavior without explicit
  approval. For refactors, list and verify behaviors that must survive.
- Do not copy production code into tests or test compile-time guarantees.
- Every PR adds a changelog fragment under `changelog.d/` — CI enforces it.
  Do not edit `CHANGELOG.md` directly; it is compiled from fragments at
  release time. Format and release process:
  [docs/making-a-release.md](docs/making-a-release.md).
- Complexity belongs at the boundary. The daemon owns orchestration and stays
  authoritative (`applyState`, `wireProjections`); the frontend renders what it
  is told.
- No continuously repainting animations. attn renders GPU terminals, often on
  high-refresh displays, beside agents that run all day — a permanent repaint
  loop is a battery and thermal bug no test will catch.
- Comments state what the code cannot show — a constraint, an invariant, a
  measured receipt — in one or two lines, and move when the code moves. Godoc
  on an exported symbol is one line. A package header is a few lines plus a
  link to the design doc, never a retelling of it. Never narrate the next line
  or argue that the change is correct: that talk belongs in the PR, and a
  comment addressed to the reviewer is a defect.
- Conventional commit titles with a scope, in plain language:
  `fix(queue): hand over the next agent however a turn closes`.

## Architecture

- `cmd/attn`: CLI, agent launch, session registration, hooks/settings
- `internal/hooks`: Claude hooks and state/todo reporting
- `internal/daemon`: lifecycle, PTY orchestration, git/GitHub, WebSocket
- `internal/pty`: PTY, read loop, replay, terminal-query responses
- `internal/ptybackend`: `worker` (default) / `embedded` selector
- `internal/ptyworker`: per-session process; production PTYs run here through
  `internal/pty`, not inside the daemon
- `internal/store`: SQLite plus in-memory cache
- `internal/bus`: durable event bus (domain facts, per-consumer cursors)
- `internal/docstore`: document-store query semantics, SQL compilation, and the
  physical naming (no DB handle; `internal/store/documents.go` executes what it
  compiles). A collection is its own table `doc_<id>`, minted from its registry
  row; a declared field is an indexed generated column over the body. Every
  identifier the store executes is derived here from an integer or a validated
  field name — never from caller text
- `internal/jobs`: durable job queue (retry/backoff, coalescing, commit fence,
  cron entries) — every background duty and every periodic tick runs on it
- `internal/classifier`: stop-time state classification
- `internal/transcript`: assistant-message extraction from JSONL
- `app`: Tauri frontend; WebSocket `ws://localhost:9849`

Frontend map (`app/src`) — `app/CLAUDE.md` covers components and test patterns;
this is where daemon traffic lands:

- `hooks/useDaemonSocket.ts`: the socket. Connection, reconnect/circuit breaker,
  the event switch, and every `send*` command. Its return value is the frontend's
  entire daemon API. `App` is its only caller and publishes it through
  `contexts/DaemonApiContext.tsx`; everything below reads it with `useDaemonApi()`
  rather than receiving it as props.
- `hooks/daemonPendingRequests.ts`: request/result correlation. A fallible
  command parks its promise under a key until the matching `*_result` event
  lands. `settlePendingRequest` is the typed way in. `sendRequest` (fresh request
  id per call) and `sendKeyedRequest` (caller's key, for the deliberately
  last-writer-wins commands) are the two ways out; both reject when the socket is
  down and time out on a daemon that never answers.
- `hooks/daemon<Domain>Events.ts` (`Fs`, `Notebook`, `MarkdownAnnotation`):
  per-domain event bodies lifted out of the switch, reached from its `default`.
  Grep a wire name (`fs_write_result`) to find the module that owns it. Adding a
  domain means a new module plus one line in that `default` chain.
- `hooks/daemonMarkdownAnnotationEvents.ts` uses a *second* correlation scheme —
  keyed `<op>:<workspaceId>:<path>`, last-writer-wins, `request_id`-checked — so
  annotation drafts supersede rather than queue. Do not route it through
  `daemonPendingRequests`.
- `store/daemonSessions.ts`: Zustand store for session/PR state.
- `pty/`: transport, attach planning, binary frame decode, runtime lifecycle.
- Tests are topic-suffixed: `Source.concern.test.tsx`. Keep that — the suffix
  names the behavior, and the set of suffixes maps a large file's seams.

States: `launching`, `working`, `pending_approval`, `waiting_input`, `idle`,
`unknown`, `scheduled`, `recoverable`. A turn opens when a session reaches a
state that wants the user (`internal/attention`) and closes only when the user
settles it; `turn_owed` is derived at broadcast from the persisted
`turn_opened_at`/`turn_settled_at` stamps, never stored.

IPC: `~/.attn/attn.sock`. WebSocket clients buffer 256 messages; sustained
over-send may drop messages or disconnect slow clients.

## Cross-cutting contracts

### Protocol

For command/event/message-shape changes:

1. edit `internal/protocol/schema/main.tsp`;
2. run `make generate-types`;
3. update `internal/protocol/constants.go` and increment `ProtocolVersion`;
4. verify with a non-production install.

Never hand-edit generated `internal/protocol/generated.go` or
`app/src/types/generated.ts`. The daemon survives app rebuilds; version skew
must fail explicitly.

### WebSocket and state

- Fallible async UI actions use request/result: daemon emits `*_result`; frontend
  returns a `Promise` and resolves/rejects it. See `sendPRAction`.
- Persisted daemon state transitions go through `applyState` in
  `internal/daemon/session_state.go`; never write state directly to the store.
- Capture classifier observation time before async classification and pass it via
  `classifierObservation`; reject stale results.
- Prefer `internal/protocol/helpers.go` pointer/value helpers (`Ptr`, `Deref`,
  `SessionsToValues`, `PRsToValues`).

### Event bus

`internal/bus` is the durable spine. State-change broadcasts do not touch the
hub directly: a producer publishes a **domain fact** (dotted `domain.verb`, an
indexed subject naming the entity, a small payload), and the hub — an ephemeral
consumer — runs the matching entry in `wireProjections`
(`internal/daemon/bus.go`) to produce the wire traffic, often a snapshot
re-push. Every state-change broadcast goes through it;
`TestWireTrafficComesFromProjections` fails on a new one that does not, and
carries the enumerated exception list.

- A fact without a subject is a snapshot invalidation, not a fact. If the
  producer does not know the entity id, that is the bug to fix — change the
  signature, or diff around the mutation to recover what moved.
- A projection writes to the wire and does nothing else. It must not mutate
  state and it must not publish: the bus holds its publish lock across the
  inline fan-out, so a nested publish deadlocks. Anything the old broadcaster
  did beyond pushing bytes belongs on the producer side.
- A bulk operation publishes one fact per entity and wraps them in
  `coalesceSnapshots`, which collapses the resulting whole-list pushes into one
  wire message per snapshot.
- Publish subject-only when the entity is store-backed (the projection re-reads
  it, and the synchronous fan-out means it sees the new value); carry a payload
  when the entity is gone, transient, or a list the caller computed.
- Byte streams stay off the bus by design: PTY output, PTY desync, attach
  results, workspace tile content, and fs bursts keep their direct paths, as
  does the remote relay (`broadcastRawWSMessage`), whose events were already
  published as facts on the remote daemon. Attach traffic routes by a
  per-client predicate, which pub/sub cannot express.
- Durable consumers get ordered, at-least-once delivery from a persisted cursor,
  so handlers must tolerate redelivery. A failing handler stalls its own
  consumer rather than skipping the event.
- Retention trims past the age window but never past an **enabled** consumer's
  cursor. Disabled consumers do not pin the log; they resume at head with a
  logged gap.
- Operator surface: `attn bus status`, `attn bus disable|enable <consumer>`.
  The enabled bit is database-only on purpose — the kill switch must not depend
  on the daemon it kills.

Design and gate decisions:
[docs/plans/2026-08-01-ext-a1-event-bus.md](docs/plans/2026-08-01-ext-a1-event-bus.md).

### Terminal

- The latest active interactive client owns PTY geometry.
- `pty_resize` is authoritative; attach restore is provisional context.
- The daemon worker's single parsed terminal (libghostty-vt) backs approval
  classification, CPR replies, grid/automation snapshots, and attach restore.
  Ghostty construction failure is spawn-fatal on supported platforms.
- Restore is server-authoritative: the daemon worker serializes that terminal
  and the attach serves its VT dump as the sole restore payload
  (`attach_result.snapshot`). The frontend resets a fresh Ghostty model, resizes
  to the snapshot grid, and writes the dump with responses suppressed. There is
  no raw-scrollback/screen-snapshot/segment fallback — a snapshot-less attach
  keeps whatever the client has and dedups the live stream against `last_seq`.
  See
  [docs/plans/2026-07-22-server-authoritative-terminal.md](docs/plans/2026-07-22-server-authoritative-terminal.md).
- OSC 133 command blocks are worker-owned state carried beside the dump as
  structured `attach_result.snapshot.blocks` (the VT dump rebuilds none); the
  frontend seeds `TerminalBlockStore` from them after the dump write
  (Phase 3a).
- Do not use restore as redraw repair or infer PTY correctness from local `fit()`.
- Restored terminal queries must not generate fresh PTY input; the worker
  already answered CPR/DA1/OSC and forwarded the query gap over the wire, so the
  client always writes the dump suppressed.
- The daemon/worker alone answers CPR, DA1, and OSC 10/11/12; frontend strips
  model replies and sends theme changes via `set_terminal_theme`.
- Kitty images are worker-authoritative and **on by default**. The worker is the
  only kitty parser in the system — ghostty hard-disables kitty on wasm, so the
  client model never sees an APC and the worker's grid stays authoritative. The
  worker describes what it stored: `kitty_placements` carries the active screen's
  whole placement set, the app pulls pixels it lacks with `get_kitty_image`, and
  the attach snapshot carries placements beside the VT dump and the OSC 133
  blocks. `KittyImageStorageLimit` is 320MB at construction (ghostty's own app
  default; receipt in the plan); `ATTN_KITTY_STORAGE_LIMIT` (bytes, read from the
  daemon's environment at session spawn, inherited by the worker, forwarded to
  remote daemons) tunes it, and **0 disables the protocol** — the escape hatch,
  where a session stores no image and observes no placement. An image larger than
  the whole limit is refused outright and silently by ghostty; the worker logs
  that with the limit and the ask. Design:
  [docs/plans/2026-08-02-terminal-kitty-images.md](docs/plans/2026-08-02-terminal-kitty-images.md).
  Sixel does not exist in ghostty at all.
- Two capabilities, not one. `kitty_images` means "describe images to me" and
  gates the placement events; `binary_pty_output` decides only how a blob
  TRAVELS (binary frame `0x02` vs base64 JSON `kitty_image_result`). The hub
  relay advertises the first and never the second, because it is a text pipe —
  collapsing them would either starve the relay of placements or corrupt it.
- Session switching must retain utility-terminal focus. `App.tsx` may fit the
  main terminal but focuses it only when utility is inactive;
  `SessionTerminalWorkspace` prefers the active `GhosttyTerminal` handle.
- Manually verify `Cmd+T` typing and switch-away/back utility focus.

### macOS shortcuts

Packaged-app default menu accelerators can consume shortcuts before DOM keydown.

- Cmd+C: handle the DOM `copy` event (`GhosttyTerminal`), not keydown alone;
  verify with `real-app:scenario-terminal-block-copy`.
- Check every new shortcut against `Menu::default` accelerators.
- In `app/src-tauri/src/lib.rs`, remove a conflicting predefined menu item so
  the WebView resolver handles rebindings.
- Use `dispatch_native_shortcut` only when a visible/relabeled native menu item
  is required; it hardcodes the action.

## Diagnostics

- Daemon: `~/.attn/daemon.log` (profile data-dir equivalent for non-prod).
- Worker PTY: `<data-dir>/workers/<daemon-instance>/log/<session>.log`;
  `pty.Session` logs are here, not in `daemon.log`.
- Debug daemon: quit the app first (it respawns without `DEBUG`), then run
  `DEBUG=debug attn daemon ensure` for the selected profile.
- Daemon code: use `d.logf(...)` / injected `LogFunc`; background stderr drops
  `log.Printf()`.
- Frontend: use prefixed console logging and Tauri DevTools.
- Hard-to-reproduce UI bugs: prefer disk JSONL under
  `$APPLOCALDATA/debug/<name>.jsonl`; follow `terminalDiagnosticsLog.ts` or
  `terminalLinkHitTestLog.ts`; remove temporary instrumentation after the fix.

## Native VT library

`internal/ghosttyvt` links `libghostty-vt` (Ghostty's VT core) via cgo on
darwin/arm64 **and** linux/amd64+arm64 (the daemon's restore path serializes on
Linux too); every other target compiles a pure-Go stub. The `//go:build`
constraint and the per-tuple `#cgo` directives in `ghosttyvt.go` must stay in
lockstep with the supported-platform list in `scripts/lib/libghostty-vt.sh` and
the Makefile. The static archive is **per platform**, living under gitignored
`third_party/ghostty-vt/<goos>_<goarch>/`. On a fresh checkout it is absent, so
the `build` target depends on the archive for the platform it targets and
`scripts/build-libghostty-vt.sh` runs automatically on the first `make build`/
`make dev`/`make install*` (and cross builds via `make build-linux-{amd64,arm64}`
or `GOOS=… GOARCH=… make build`).

**Download-first (no zig for most contributors, and none in CI/release).** The
script fetches the prebuilt archive **for the target platform** — assets are
named `libghostty-vt-<key>-<goos>_<goarch>.tar.gz`, keyed by the ghostty pin
(`ghostty-vt.pin`) plus the carried `ghostty-vt-native.patch` — from the
rolling `native-vt-prebuilts` GitHub release and verifies it against the
matching `sha256_<goos>_<goarch>` in `ghostty-vt-native.lock` (fail-closed). The
key is shared across platforms (same source); the lock carries one sha per
platform. The repo is public, so this needs only network access. A **source
build (zig 0.16.x)** happens only when you have edited the pin/patch (no
published asset for the new key yet), when the download/verify fails, or when
`ATTN_VT_FROM_SOURCE=1` forces it. `GHOSTTY_VT_GOOS`/`GHOSTTY_VT_GOARCH` scope the
script to a target when cross-building (the Makefile sets them).

**Changing the VT source.** After editing the shared `ghostty-vt.pin`, rebuild
the vendored browser core with `app/scripts/build-ghostty-vt-wasm.sh`; it also
rewrites `app/vendor/ghostty-vt/ghostty-vt.lock`, which normal builds and tests
verify against the pin, adapter, patch, recipe, and binary. Then run
`make publish-native-vt` (`scripts/publish-libghostty-vt.sh`) after editing the
shared pin or `ghostty-vt-native.patch`: it cross-builds **every** supported
native target from one host (needs zig 0.16.x and an authenticated `gh`), uploads
all the keyed assets, and rewrites `ghostty-vt-native.lock` with the shared key +
per-platform shas. **Commit both regenerated locks when the shared pin changes**
— the build depends on them, so committing them is what makes every checkout
reject stale artifacts. Shared native logic lives in
`scripts/lib/libghostty-vt.sh`. See
[docs/plans/2026-07-22-server-authoritative-terminal.md](docs/plans/2026-07-22-server-authoritative-terminal.md).
