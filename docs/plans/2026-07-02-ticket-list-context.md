# Plan: Expose branch/cwd on ticket list and board cards

## Goal

Collision awareness: expose WHERE each ticket's work lives. The vision
(docs/vision/chief-delegation-awareness.md) makes the chief the one that catches it
"when two agents' work collides" — and two working tickets on the same repo + branch
is exactly that signal. Today neither the chief (`attn ticket list`) nor Victor (the
board) can see it: the ticket row stores `cwd` but no branch, the wire shape carries
`cwd` but nothing renders it, and branch exists only on the *session*, whose row is
deleted on close. This plan adds a durable `branch` to the ticket (captured at
delegation mint), puts it on the wire, and surfaces branch + cwd in the `ticket list`
output and as a small secondary line on board cards. **Data exposure only** — no
automated collision detection; the chief's cognition and Victor's glance do the
detecting. Awareness, not autonomy: the board informs, never gates.

## Reality check (code vs brief)

Verified against the repo — the brief adapts as follows:

- `tickets.cwd` **already exists** (migration 55, `internal/store/sqlite.go`) and is
  **already on the wire**: `model Ticket { cwd: string }` in
  `internal/protocol/schema/main.tsp`, mapped by `ticketToProtocol`
  (`internal/daemon/ticket_board.go`), so `attn ticket list --json` already carries it.
  The cwd half of this plan is display-only (human line, board card).
- `branch` exists on **sessions** (`sessions.branch`, set synchronously in
  `handleSpawnSession` via `git.GetBranchInfo(cwd)` before `store.AddChecked`), but the
  session row dies on close — same reason `resume_session_id` was copied onto tickets
  in migration 57. So branch must be persisted on the ticket.
- The capture point already holds the value: `delegate()` (`internal/daemon/delegate.go`)
  calls `createDelegatedTicket(chiefSessionID, session, ...)` with the same
  `*protocol.Session` whose `session.Branch` it later copies into `result.Branch`. No
  new git call is needed — mint-time capture is a one-field pass-through.
- `store.SetTicketSession` (would update cwd) has **no production callers** — cwd is a
  mint-time snapshot today. Branch gets the same semantics: written once at mint,
  never refreshed. `attn ticket new` backlog tickets are unbound (no cwd, no branch).

## Architecture Map

```text
Current:
delegate() [internal/daemon/delegate.go]
  -> handleSpawnSession: session.Branch = git.GetBranchInfo(cwd)   (sync, pre-AddChecked)
  -> createDelegatedTicket(chief, session, brief, label, agent)    [delegate_ticket.go]
       -> store.CreateTicket{Cwd: session.Directory}               # branch DROPPED here
  -> result.Branch = session.Branch                                # wire-only, not durable

read side:
store.ListTickets -> ticketRows -> ticketToProtocol                [ticket_board.go]
  -> handleTicketList -> attn ticket list                          (--json has cwd; human: id/status/assignee/title)
  -> broadcastTicketsUpdated -> app BoardCard                      (title/id/when/assignee only)

Target:
delegate() -> createDelegatedTicket: Ticket.Branch = Deref(session.Branch)
tickets table + branch column (migration; survives session close)
ticketToProtocol maps Branch -> Ticket.branch on the wire          (generate-types + version bump)
  -> attn ticket list: human line gains a branch column; --json gains branch (cwd already there)
  -> BoardCard: secondary line "⎇ branch · dir(cwd)" under the title, omitted when empty

Tests:
store round-trip (in-memory sqlite) -> migration + scan
createDelegatedTicket unit (fake session w/ Branch) + delegate() e2e in a real git repo (runGitDaemon helper)
daemon handler test asserts branch on wire rows (this IS the --json guarantee: printJSON marshals protocol.Ticket verbatim)
vitest: BoardCard renders / omits the location line
```

## Data Model

```sql
-- next free migration version. Check MAX(version) in the REAL prod+dev DBs first
-- (versions have been burned before):
--   sqlite3 ~/.attn/attn.db     'SELECT MAX(version) FROM schema_migrations'
--   sqlite3 ~/.attn-dev/attn.db 'SELECT MAX(version) FROM schema_migrations'
-- In-repo tail is currently the review-loop drop; pick the first version above both.
ALTER TABLE tickets ADD COLUMN branch TEXT NOT NULL DEFAULT '';
```

```go
// internal/store/tickets.go
type Ticket struct {
    ...
    Cwd    string // last session's working dir (for resume)
    Branch string // git branch at delegation mint; durable after the session row dies. "" = unknown/unbound
    ...
}
```

```tsp
// internal/protocol/schema/main.tsp — model Ticket, next to cwd
branch: string;   // "" when unknown — mirrors cwd's required-with-empty convention
```

Ownership: the store owns the durable snapshot; **delegation mint is the only writer**
(`createDelegatedTicket`). Wire + CLI + board are read-only projections.

## Boundaries

- `internal/store` owns the `branch` column; nothing updates it post-create (same as
  cwd today — `SetTicketSession` stays orphaned).
- `createDelegatedTicket` copies `session.Branch`; it must not call git itself
  (`handleSpawnSession` already resolved it, and `result.Branch` proves it's populated).
- `ticketToProtocol` is a dumb map; no derivation, no fallback to live session state.
- CLI and board **render** branch/cwd; neither computes collisions, sorts by them, or
  gates anything. attn still authors only the `crashed` status.
- Notifications/doorbells untouched.

## Implementation Steps

- [ ] **Store: migration + field.** In `internal/store/sqlite.go` append a migrations-slice
      entry (version per the Data Model note — do NOT pin it here) with an
      `applyMigration<N>` guarded by `columnExists(tx, "tickets", "branch")`, dispatched
      from `migrateDB` — mirror migration 57 (`applyMigration57`, the
      `m.version == 57` arm) exactly. In `internal/store/tickets.go`: add `Branch` to the
      `Ticket` struct, the `CreateTicket` INSERT column/value lists, the `ticketSelect`
      const, and `scanTicket`. Tests: extend `TestTicketCRUDRoundTrip`
      (`tickets_test.go`) to set + round-trip `Branch`; add `{"tickets", "branch"}` to
      `TestMigrations_MigratedColumnsExist` (`sqlite_test.go`); add an idempotency test
      mirroring `TestMigration53AddsClosedStateColumnIdempotently` (pre-create the
      column, re-run, no error).
- [ ] **Capture at mint.** `createDelegatedTicket` (`internal/daemon/delegate_ticket.go`)
      sets `Branch: strings.TrimSpace(protocol.Deref(session.Branch))` in the
      `store.Ticket` template. Tests: new `TestCreateDelegatedTicketCapturesBranch` in
      `delegate_ticket_test.go` mirroring `TestCreateDelegatedTicketCollisionSuffix`
      (session with `Branch: protocol.Ptr("feat/x")` → stored ticket carries it); extend
      `TestDelegateCreatesAndBindsTicket` (`delegate_test.go`) to run the source in a
      real git repo — `setupDelegationSourceAt` with a dir initialized via the existing
      `runGitDaemon` helper (`worktree_naming_test.go`: init, commit, checkout -b) — and
      assert the bound ticket's `Branch` equals the repo branch, proving the
      spawn→mint hand-off end to end.
- [ ] **Protocol.** Add `branch: string;` to `model Ticket` in
      `internal/protocol/schema/main.tsp`. `rm -rf tsp-output` (orphaned-model glob-back
      trap), `make generate-types`, then tsc-check the app (quicktype enum-merge trap —
      no enums change here, but verify the build). Bump `ProtocolVersion` in
      `internal/protocol/constants.go` (CLAUDE.md critical pattern #1; no new commands,
      so constants are otherwise untouched). Map it in `ticketToProtocol`
      (`internal/daemon/ticket_board.go`): `Branch: t.Branch`. Never hand-edit
      `generated.go` / `generated.ts`. Test: extend
      `TestGetTicketWSResultReturnsFullRecord` (`ticket_board_test.go`) — seed the ticket
      with `Branch` and assert it on the wire row. That daemon-side assert is also the
      `--json` guarantee: `runTicketList` prints the `[]protocol.Ticket` verbatim via
      `printJSON`.
- [ ] **CLI.** `printTicketBoard` (`cmd/attn/main.go`): human line becomes
      `id\tstatus\tassignee\tbranch\ttitle` with `-` for an empty branch (same
      convention as the assignee column). Refactor it to `printTicketBoard(w io.Writer,
      tickets)` so it's testable (call sites pass `os.Stdout`). Update the `list` entry
      in `writeTicketHelp` — "(id, column, assignee, branch, title)". Test: new
      `TestPrintTicketBoardIncludesBranch` in `cmd/attn/main_test.go` (branch shown;
      `-` when empty).
- [ ] **Board card.** `BoardCard` in `app/src/components/TicketBoardPanel.tsx`: a
      secondary line directly under `tb-card-title` — render `⎇ {branch}` plus the last
      path segment of `cwd` (add a small exported `cwdBasename(cwd: string)` helper next
      to `relativeTime` for testability), `title={ticket.cwd}` for the full path on
      hover, and **omit the whole line when both are empty** (unbound `ticket new`
      backlog tickets). CSS: a `.tb-card-loc` rule in `TicketBoardPanel.css` mirroring
      `.tb-card-meta`'s size/color tokens — reuse the tokens that rule already uses; do
      not introduce new bare `var()` tokens (the undefined-token silent no-op trap).
      Tests (`TicketBoardPanel.test.tsx`): add `branch: ''` to `makeTicket`; new cases
      "renders branch and cwd basename on the card" and "omits the location line when
      branch and cwd are empty". Update `TicketDetailPanel.test.tsx` fixtures for the
      new required field (mechanical).
- [ ] **Docs/guidance.** `internal/agent/attn_skill/references/tickets.md`: the
      `ticket list` bullet enumerates the human columns — add branch, and one sentence
      that the branch/cwd fields are the collision signal (two working tickets on the
      same repo + branch deserve a flag). One coordinated clause to the same effect in
      the chief's `ticket list` sentence in `hooks.ChiefGuidance`
      (`internal/hooks/hooks.go`) — **sequencing:** that sentence lives inside the
      "Delegation hands work off" bullet that
      [2026-07-02-chief-guidance-brief-craft.md](2026-07-02-chief-guidance-brief-craft.md)
      rewrites and owns, so this plan lands AFTER it: rebase onto the landed bullet,
      keep every substring its `TestChiefGuidance` asserts (e.g. "arm a harness
      Monitor", "a default, not a hard rule") byte-identical, and keep the addition to
      one clause (it sits outside that plan's line budget, acknowledged there).
      CHANGELOG.md entry (user-visible: board cards + list show where work lives).

## Verification

```bash
go build ./...
go test ./internal/store -run 'Ticket|Migration'
go test ./internal/daemon -run 'Ticket|Delegate'   # keep -run scoping; bare -race trips the pre-existing gitstatus scheduler race
go test ./cmd/attn
rm -rf tsp-output && make generate-types && git diff --stat internal/protocol/generated.go app/src/types/generated.ts
pnpm --dir app exec tsc --noEmit
pnpm --dir app test TicketBoardPanel
pnpm --dir app test TicketDetailPanel
```

Manual smoke (protocol change — use a throwaway profile, NOT dev: the dev app
auto-respawns an old daemon that silently drops new JSON fields): fresh
`ATTN_PROFILE`, rebuilt `./attn`, `attn daemon ensure`, delegate from a chief-marked
session with `--worktree`, then `attn ticket list` (branch column) and
`attn ticket list --json | jq '.[] | {id, branch, cwd}'`. For the card line:
`make dev` and eyeball the board (⌘K → board) with one worktree delegation and one
`attn ticket new` backlog ticket (no line).

## Decisions

- **Data exposure only — no automated collision detection** (settled with Victor).
  attn never diffs branches or warns; the chief's cognition and Victor's glance do the
  detecting. Keeps the spine: the board informs, never gates.
- **Branch is captured at delegation mint and persisted on the ticket** (settled with
  Victor), not derived live from the bound session — the session row is deleted on
  close, so live derivation goes blank exactly when the board still matters. Same
  durability rationale as `resume_session_id` (migration 57). Concretely: copy
  `session.Branch` in `createDelegatedTicket`; it is already resolved synchronously at
  spawn (`handleSpawnSession` → `git.GetBranchInfo`) and already feeds
  `DelegateResult.Branch`.
- **Mint-time snapshot, never refreshed.** Matches cwd's existing semantics (no
  production writer after create). It records where the work was *placed*; live drift
  (agent switches branches mid-task) is out of scope.
- **`branch: string` required-with-empty on the wire**, mirroring `cwd` — not optional.
  One convention for ticket location fields; fixture updates are mechanical.
- **Human `list` line gains only the branch column; full cwd stays `--json`-only.**
  The tab-separated line must stay scannable; cwd was already in `--json` (recorded
  reality — the brief's "add cwd to --json" was already true). The board card shows
  the cwd basename with the full path on hover.
- **No migration number pinned in this doc.** Check `MAX(version)` in the real prod
  and dev DBs before numbering — versions have been burned before (see the migration
  49-52 comment block in sqlite.go).

## Open Questions / Follow-ups

- `TicketDetailPanel` shows no location at all (cwd is only used for the Resume
  gate). A branch/cwd row in the detail header is a cheap follow-up once the field is
  on the wire.
- `attn ticket take` reassigns without touching cwd/branch, and ticket Resume reuses
  the stored cwd — if reassignment/resume placement ever diverges from mint placement,
  wire the orphaned `store.SetTicketSession` (extended with branch) into those paths.
- Unbound backlog tickets (`attn ticket new`) carry no location by design. If backlog
  capture later wants a target repo, that is a `ticket new --cwd` feature, not this
  plan.
