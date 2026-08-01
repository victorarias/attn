# Plan: agent discoverability of the attn codebase

## Goal

Make attn cheaper and more accurate for coding agents to navigate. Agents locate
code by ripgrep over names, not by semantic analysis, so the cost of a change is
driven by how many tokens an agent burns before it finds the right definition —
and by whether it finds the wrong one confidently.

Source: [How Coding Agents Read Your Code](https://modem.dev/blog/how-coding-agents-read-your-code).
Its two measured results that apply here:

- splitting a 4,943-line monolith into concept-named modules cut token spend
  ~31–34% for weaker models;
- on the same four planted bugs with an eight-turn budget, Haiku found 4/8 and
  Sonnet 3/8 in the monolith, and both found 8/8 after the split.

This is not a naming-convention sweep. attn already scores well on most of the
article's checklist (see Non-goals). Four specific gaps are worth closing.

## Current state, re-measured 2026-08-01 (on `origin/main` @ b4efe628)

| Surface | Measurement |
| --- | --- |
| `app/src/hooks/useDaemonSocket.ts` | 5,869 lines, 134 event cases, 52 exports, 20 `any` |
| `app/src/App.tsx` | 4,231 lines |
| Wire strings | 88 of 170 `protocol.Cmd*` wire strings appear nowhere in `internal/daemon` |
| `AGENTS.md` `app/` coverage | one bullet for ~99k lines of frontend |
| `internal/daemon/daemon.go` | 3,684 lines, in a package of 108 non-test files |

Already good, and deliberately left alone: concept-named Go packages
(`internal/pty`, `internal/ptyworker`, `internal/classifier`, `internal/transcript`),
`internal/daemon` already split by concept (`branch.go`, `browser.go`,
`delegate.go`, `ws_pty.go`), 177 distinctive `handleXWS` methods, near-zero `any`
in TS, and topic-suffixed test files (`App.worktreeCleanup.test.tsx`) that name
the concern rather than just the source file.

## Step 1 — wire-string back-references — DONE 2026-08-01

An agent starting from a `daemon.log` line, a frontend `cmd: "delete_worktree"`,
or a user report greps the wire string and lands in `constants.go`, `main.tsp`,
and generated code — never the handler. It must infer `CmdDeleteWorktree` and
grep again. Two hops on the single most-walked path in the repo.

```text
Current:
  rg delete_worktree
    -> internal/protocol/constants.go        (CmdDeleteWorktree = "delete_worktree")
    -> internal/protocol/generated.go        (noise)
    -> internal/protocol/schema/main.tsp     (noise)
    -> app/src/hooks/useDaemonSocket.ts
    (handler not in results; infer symbol, grep again)

Now:
  rg -A1 delete_worktree
    internal/daemon/websocket.go: case protocol.CmdDeleteWorktree: // wire: delete_worktree
    internal/daemon/websocket.go-   d.handleDeleteWorktreeWS(client, ...)
    internal/daemon/daemon.go:    case protocol.CmdDeleteWorktree: // wire: delete_worktree
    internal/daemon/daemon.go-      d.handleDeleteWorktree(conn, ...)
```

The annotation went on the **dispatch case**, not on each handler. The switches
are the routing tables, and the case line sits one line above the handler's name,
so a single grep result window carries both. It is also uniform: several commands
dispatch from more than one switch (WebSocket and unix socket), and some cases
inline their work instead of calling a `handleXWS`, so "annotate the handler" had
no single meaning.

- [x] `// wire: <cmd>` on all 244 `case protocol.Cmd*:` lines across
      `websocket.go`, `daemon.go`, and `automations.go`. All 170 wire strings are
      now greppable in `internal/daemon`; 88 were not.
- [x] `internal/daemon/wire_dispatch_test.go` enforces it. This is the value of
      the step — a one-time comment pass decays; an enforced invariant does not.
  - `TestWireCommandsAreGreppable` — every dispatch case carries its constants'
    wire names, and every `Cmd*` constant has a dispatch case.
  - `TestWireCommentsMatchTheirConstants` — a comment naming a *different*
    command's wire string fails. Drift is worse than absence: it sends the next
    reader to the wrong handler with full confidence.
- Each assertion was mutation-checked separately (drop a comment, swap it for
  another valid wire name, add an undispatched constant); each failed on its own
  line with the message that names the fix.
- Not done: the same treatment for `Event*` constants. Events have no dispatch
  table on the daemon side — they are emitted from wherever the state changes —
  so there is no equivalent single line to anchor to. Left for a later pass.

## Step 2 — split `useDaemonSocket.ts` — STARTED 2026-08-01, 3 of ~13 domains

Every frontend agent touching anything daemon-adjacent loads 5,897 lines. The
134 cases already group cleanly by wire-name prefix (measured): workspace 18,
fs 11, automations 9, notebook 6, session 5, tickets 4, markdown 4, pty 3,
git 3, worktree 2, workflow 2, presentation 2, endpoints 2, plugins, tasks,
notifications.

```text
Current:
  App.tsx
    -> useDaemonSocket.ts        (5,897 lines: transport + 134 cases + 53 exports)

Target:
  App.tsx
    -> useDaemonSocket.ts        (transport, reconnect, request/result plumbing)
      -> daemonWorkspaceEvents.ts
      -> daemonFsEvents.ts
      -> daemonNotebookEvents.ts
      -> daemonPtyEvents.ts
      -> daemonGitPrEvents.ts
      -> daemonTicketEvents.ts
      -> daemonAutomationEvents.ts
```

The plan assumed pure reducers over a `DaemonState`. There is no such state: the
whole hook is one ~5,000-line function and the case bodies close over 28 refs and
setters. What the cases actually share is not state — it is a **correlation
protocol**. 115 of the 134 cases are the same five lines: look up
`<kind>:<requestId>` in `pendingActionsRef`, delete it, resolve or reject. That
protocol had no name anywhere in the codebase, which is why the file reads as
undifferentiated bulk.

So the seam is: name the protocol, then move each domain's event bodies to a
module that takes only the collaborators it needs.

```text
useDaemonSocket.ts          transport, reconnect, the switch, every send*
  daemonPendingRequests.ts  `<kind>:<requestId>` + settlePendingRequest
  daemonFsEvents.ts         fs_changed + 10 fs_*_result
  daemonNotebookEvents.ts   notebook_changed + 5 notebook_*_result
  daemonMarkdownAnnotationEvents.ts
                            a SECOND correlation scheme: `<op>:<wsId>:<path>`,
                            last-writer-wins, request_id-checked
```

Extracted domains are reached from the switch's `default`, so the hot cases
(`pty_output`) never pay for the chain and an unclaimed event stays ignored,
exactly as before.

- [x] `daemonPendingRequests.ts` — `pendingRequestKey` + `settlePendingRequest`.
      Adopted at 16 call sites so far.
- [x] `daemonFsEvents.ts`, `daemonNotebookEvents.ts`,
      `daemonMarkdownAnnotationEvents.ts`. 5,869 → 5,536 lines.
- [x] Public surface unchanged; no consumer touched.
- [x] Tests for the notebook and annotation paths. They had **none** — the first
      extraction passed the whole suite vacuously, and only fs failed under
      mutation. 8 new tests; every extracted case now fails when its wire name is
      mutated.
- [ ] Remaining domains: workspace 10, automations 9, session 5, tickets 4,
      git 3, pty 3, worktrees 2, workflow 2, presentations 2, endpoints 2.
      Workspace/session/pty are the stateful ones — they belong with the
      transport unless a clean collaborator set appears.
- [ ] The other 17 `any`, and the generic `*Result` interface names.
- [ ] Split `useDaemonSocket.test.tsx` (now 3,190 lines) along the same seams.

## Step 3 — split `App.tsx` (deferred; premise was wrong)

The original argument was that six topic-scoped test files named seams `App.tsx`
had not yet been split along. Re-measured 2026-08-01, that is no longer true:

```text
app/src/utils/fsChangeSignals.ts            <- extracted (also owns
app/src/utils/fsChangeSignals.test.ts          fsIndexToNotebookEntries)
app/src/utils/fsIndexToNotebookEntries.test.ts
app/src/utils/presentationNotices.ts        <- extracted
app/src/components/WorktreeCleanupPrompt.tsx

app/src/App.worktreeCleanup.test.tsx        \  all three render(<App/>) —
app/src/App.sessionlessWorkspace.test.tsx    > integration tests by design,
app/src/App.chiefOfStaffClose.test.tsx      /  not orphaned unit tests
```

Three of the six concerns already have named modules. The three remaining test
files import `App` and render it whole, so their existence is not evidence of a
missing module — they are testing composed behavior on purpose.

`App.tsx` is still 4,231 lines and probably still worth splitting, but the seams
have to be found by reading it, not inferred from test names. Defer until steps
1 and 2 land and we have a measured token delta to justify the cost.

Same caveat applies to `GhosttyTerminal.tsx` (3,396 lines).

## Step 4 — document the frontend in AGENTS.md — DONE 2026-08-01

- [x] Frontend map in the Architecture section, sized like the Go one: the socket
      hook and what its return value feeds, the two correlation schemes and which
      one an annotation command must not use, the `daemon<Domain>Events.ts`
      convention and how to add one, and the `Source.concern.test.tsx` naming.
- Written after step 2's first domains landed, so it describes real files. It
  will need one more pass when the remaining domains move.

## Decisions

- Not renaming domain identifiers. `Session` (709 hits), `State` (707), `Status`
  (664) look like the article's `create()` problem but are attn's actual domain
  vocabulary; Go package qualification and the frontend's `Daemon*` prefixes
  already disambiguate. Renaming is churn with no retrieval win.
- Step 1 ships an enforcing test, not just comments. A comment pass alone rots
  within a few PRs and leaves a false signal behind.
- Step 2's real seam is the request/result correlation protocol, not per-domain
  state. Naming `<kind>:<requestId>` collapsed most of the 134 cases to one line
  each; the plan's original "pure reducer over DaemonState" shape does not exist
  in this hook and would have meant inventing state to refactor toward.
- A green suite is not evidence that a move was safe. The notebook and annotation
  events had zero tests, so the first extraction "passed" without executing any
  of the moved code. Mutating each wire name is what turned that into a signal.
- Step 2 keeps the `useDaemonSocket` public surface frozen. Splitting the file and
  changing the API at once makes it impossible to tell a regression from a
  refactor, and this hook is on the critical path for every session.
- Step 3 was demoted, not dropped. Its stated evidence (orphan test files) had
  already been acted on by the time we re-measured; splitting a 4,231-line file
  on a premise that no longer holds is how a refactor ends up unjustified.
- `daemon.go` (3,684 lines) is listed but not scheduled. Its siblings were already
  concept-split; what remains is lifecycle and registration, which is a coherent
  concern even if large. Revisit only if it keeps accreting.

## Verification, 2026-08-01

- `go test ./internal/daemon/... ./internal/protocol/...` — pass. Each of the
  three wire-comment assertions mutation-checked separately.
- `pnpm --dir app test` — 2,134 pass. Each extracted event mutation-checked; five
  that previously passed vacuously now fail when mutated.
- `pnpm --dir app run e2e` — 190 pass, 2 fail (`settings.spec.ts:204`,
  `workspace-sessions.spec.ts:138`). Both pass when re-run on their own, so they
  are load flakes in a 16-minute suite, not regressions. Neither touches an
  extracted domain.
- `make dev` install, dev-profile preflight clean (protocol 200 end to end).
- Live scenarios on the branch: `markdown-opener`, `notebook-tile-finder`,
  `notebook-link-nav` (exercises `fs_read_asset_result` via a `..`-relative
  image), `notebook-tile-close`, `editor-workspace-root`, `present-flow` — all
  pass.
- `notebook-editor-undo` fails at `focus_editor_with_native_click`. **Pre-existing**:
  it fails identically on an `origin/main` worktree built and run the same way.
  The note's content renders in the tile, so the extracted read path is fine; the
  CodeMirror pane never takes focus. Not investigated further — out of scope here.
- Not covered live: markdown annotations. No harness scenario exercises them, and
  none did before. Covered only by the new unit tests.

## Follow-ups

- Consider re-measuring after step 2: run the same agent task (locate and fix a
  planted frontend bug) before and after, and record the token delta. Would tell
  us whether to keep going into `GhosttyTerminal.tsx` and `SettingsModal.tsx`.
- `internal/store/store.go` (2,228) and `internal/hub/manager.go` (1,806) are the
  next Go monoliths if the frontend work pays off.
