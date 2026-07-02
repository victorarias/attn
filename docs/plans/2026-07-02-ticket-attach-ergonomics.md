# Plan: Attach by ticket id + attach at delegate time

## Goal

Close the two attach gaps in the ticket loop. (1) `attn ticket attach` only works on the
calling session's **bound** ticket (`handleTicketAttach` resolves it via
`ActiveTicketForSession` and rejects everything else) — so the chief, which has no bound
ticket, cannot hand a file to a ticket it delegated, and no agent can drop an artifact on
a sibling's thread. Add a by-id form mirroring `attn ticket comment`. (2) Delegation
cannot carry artifacts: a brief that references a plan doc, a spec, or a screenshot mints
a ticket (`createDelegatedTicket`) with no way to bind those files to it, so the handover
context is not durable — it dies with the chief's context instead of riding the ticket
through resume and reassignment. Add `attn delegate --attach <file>` (repeatable). The
vision's "big outputs land as attachments" (docs/vision/chief-delegation-awareness.md)
currently runs in one direction only (agent → chief); this makes attachments a two-way
handover surface. One ProtocolVersion bump covers both.

## Architecture Map

```text
Current:
  attn ticket attach --file f          -> handleTicketAttach -> ActiveTicketForSession(source)   BOUND ONLY
                                            -> copyTicketAttachment -> AddTicketAttachment
                                            -> notifyTicketObservers + broadcastTicketsUpdated
  attn ticket comment <id> -m t        -> handleTicketComment -> AddTicketComment(id, author)     BY ID (the precedent)
  attn delegate --brief b              -> delegate() -> spawn -> createDelegatedTicket            NO ARTIFACTS
                                            -> broadcastTicketsUpdated

Target:
  attn ticket attach [<ticket-id>] --file f [--note t]
    -> runTicketAttach (interleave parse, like parseTicketCommentArgs)
      -> client.AttachTicket(source, ticketID, absPath, base, note)     ticketID "" = bound form
        -> handleTicketAttach:
             ticket_id given  -> GetTicket(id) exists-check (BEFORE copy — no orphan file)
             ticket_id empty  -> ActiveTicketForSession(source)          unchanged
           -> copyTicketAttachment -> AddTicketAttachment(author=source)
           -> notifyTicketObservers + broadcastTicketsUpdated            same fan-out both forms

  attn delegate --brief b --attach f1 --attach f2
    -> parseDelegateArgs: repeatable --attach, abs+stat+not-dir at parse (--brief-file precedent)
      -> DelegateMessage.attachments: string[]
        -> delegate():
             upfront (before ANY side effect): chief source required; stat each file; notebookRoot set
             ... existing worktree/workspace/pane/spawn flow ...
             createDelegatedTicket
             for each: copyTicketAttachment + store.AddDelegatedTicketAttachment(author=chief, assignee cursor advanced in-tx)
             on failure: unregisterSession + removeWorkspaceLayoutPaneForSession
                         + store.DeleteTicket + os.RemoveAll(attachments dir) + rollbackDelegation
             broadcastTicketsUpdated                                     attachments land before the board push

Tests (all in-process daemon, no socket):
  syncConn / net.Pipe helpers -> handleTicketAttach directly            (callTicketAttach, ticket_attach_test.go)
  delegateMany + fakeSpawnBackend -> d.delegate(...) directly           (delegate_test.go, ticket_notify_test.go)
  fireNudgeNow / wasNudged / callTicketInbox                            doorbell + inbox assertions
```

## Data Model / Interfaces

No migration — `ticket_attachments` and `ticket_events` already carry everything.

Protocol (internal/protocol/schema/main.tsp; then critical pattern #1: `rm -rf tsp-output`,
`make generate-types`, bump ProtocolVersion in constants.go — one bump for the whole plan):

```tsp
model TicketAttachMessage {
  // ... existing fields unchanged ...
  ticket_id?: string;      // attach BY ID (any ticket, mirrors ticket_comment authorization);
                           // empty = resolve the calling session's bound ticket (today's form)
}
model DelegateMessage {
  // ... existing fields unchanged ...
  attachments?: string[];  // absolute file paths copied onto the minted ticket at birth;
                           // requires a chief-tracked delegation (that's what mints the ticket)
}
```

No new commands, no new events, no frontend changes (TicketDetailPanel already renders
attachments; board rows stay bare).

Store (internal/store):

```go
// tickets.go — mint-time handover: same body as AddTicketAttachment plus
// setTicketCursorTx(tx, assignee, ticketID, eventSeq, now) in the same tx,
// mirroring CreateTicket's birth-cursor block ("brief handed out of band").
AddDelegatedTicketAttachment(att TicketAttachment, author, assignee string, now time.Time) (*TicketAttachment, error)

// tickets.go — rollback-only hard delete of ONE ticket, cascading the same five
// tables SweepExpiredTickets does (activity, attachments, events, cursors,
// subscriptions). ErrTicketNotFound on a missing id. Not exposed over any
// protocol: it exists solely so a failed delegate --attach can unmint the
// ticket the SAME call just created and never broadcast.
DeleteTicket(id string) error

// ticket_events.go — both participation UNIONs change:
//   kind != 'commented'  ->  kind NOT IN ('commented', 'attachment_added')
// in UnreadTicketEvents and TicketParticipants (update both doc comments).
```

Client (internal/client/client.go): `AttachTicket` gains a `ticketID string` param
(empty = bound form; one call site, `runTicketAttach`); `DelegateOptions` gains
`Attach []string`, mapped to `msg.Attachments` in `Delegate` (omitted when empty).

Daemon prompt: `delegatedTicketPrompt(brief string, attachments []string)` appends, when
non-empty, a short block: handover files are attached to your ticket; list each as
`<basename> (source: <abs path>)` so the agent can read the original immediately (the
prompt is composed before mint, so ticket-store destination paths don't exist yet).

## Boundaries

- **The store owns participation.** Handlers call `notifyTicketObservers` and never
  recompute or special-case who gets reached; making attach one-shot is a change to the
  participation queries, not handler logic (same boundary the comment plan enforced).
- **attn still authors nothing but `crashed`.** Every attachment event is authored by the
  calling session (by-id form) or the chief (delegate form). The daemon copies bytes; it
  never speaks on the ticket.
- **Doorbells stay content-free.** The by-id attach fan-out is the existing
  `notifyTicketObservers` → nudge-countdown path; the event's `Detail` is just the
  filename, and nothing is typed into a PTY beyond the fixed `ticketNudgePrompt`.
- **The board informs, never gates.** Attaching never changes status, blocks a
  transition, or requires one; mint attachments ride the existing
  `broadcastTicketsUpdated` that delegation already fires.
- **delegate() owns atomicity.** All fallible-by-user-error checks (chief source, file
  exists, not a dir, notebook root configured) run before the first side effect; the
  store's `DeleteTicket` is a dumb cascade and knows nothing about delegation.
- **Sequencing: lands after chief-guidance-brief-craft**
  ([2026-07-02-chief-guidance-brief-craft.md](2026-07-02-chief-guidance-brief-craft.md)),
  which edits the same `delegatedTicketPrompt` (inserting the three-field
  terminal-report contract between the status-command list and the closing paragraph,
  asserted by new substrings in `TestChiefOfStaffDelegateBindsTicketAndPrompt`) and the
  same `references/delegation.md` (checklist tighten + fork line). The edits are
  compatible — different insertion points — but this plan rebases onto the landed text:
  the `(brief string, attachments []string)` signature change and appended
  handover-files block must keep that plan's substring assertions green.

## Implementation Steps

- [ ] **Protocol:** add `ticket_id?` to `TicketAttachMessage` and `attachments?: string[]`
      to `DelegateMessage` in `internal/protocol/schema/main.tsp`; `rm -rf tsp-output`;
      `make generate-types`; bump ProtocolVersion once (CLAUDE.md critical pattern #1;
      no constants.go command changes — no new cmds). Extend
      `TestParseDelegatePlacementAndWorktree`-style decode coverage in
      `internal/protocol/parse_test.go` for both new fields.
- [ ] **Store — attach is one-shot:** change both UNIONs in
      `UnreadTicketEvents`/`TicketParticipants` (internal/store/ticket_events.go) to
      `kind NOT IN ('commented','attachment_added')`; update the two doc comments. Tests
      (internal/store/ticket_events_test.go): extend `TestTicketParticipants` with an
      attachment-only author (excluded), and add
      `TestUnreadTicketEventsExcludesAttachmentOnlyAuthor` mirroring
      `TestUnreadTicketEventsExcludesCommentOnlyAuthor` (attach via `AddTicketAttachment`
      instead of comment; assignee sanity check included).
- [ ] **Daemon + CLI — attach by id:** in `handleTicketAttach`
      (internal/daemon/ticket_attach.go), resolve `msg.TicketID` when set: `GetTicket`
      exists-check **before** `copyTicketAttachment` (a bad id must not leave an orphan
      under `.attn/tickets/<bad-id>/`), clear "ticket not found" error mirroring
      `handleTicketComment`; empty keeps the `ActiveTicketForSession` path byte-for-byte.
      Rework `parseTicketAttachArgs` (cmd/attn/main.go) to the interleave peel-loop from
      `parseTicketCommentArgs`, accepting 0 or 1 positionals (`TicketID` field); thread
      through `runTicketAttach` → `client.AttachTicket`. Tests: daemon
      `TestTicketAttachByIDFromNonAssignee` (X attaches to Z's ticket → recorded+copied;
      unknown id → error AND no file/dir written) and the linchpin
      `TestAgentAttachDoesNotSubscribeAttacher` mirroring
      `TestAgentCommentDoesNotSubscribeCommenter` (internal/daemon/ticket_comment_test.go):
      X attaches by id, assignee Z is nudged (`fireNudgeNow`/`wasNudged`), X is not; a
      LATER event on the ticket reaches neither X's PTY nor its `callTicketInbox`. CLI:
      extend the `parseTicketAttachArgs` table in cmd/attn/main_test.go (by-id form,
      flags on both sides of the id, >1 positionals rejected, bound form unchanged).
- [ ] **Store — delegate-attach helpers:** `AddDelegatedTicketAttachment` and
      `DeleteTicket` per Data Model above (internal/store/tickets.go; `setTicketCursorTx`
      already accepts a tx). Tests (internal/store/tickets_test.go):
      `TestAddDelegatedTicketAttachmentMarksConsumedForAssignee` (event appended; assignee
      cursor == the attachment event's seq so `UnreadTicketEvents(assignee)` is empty;
      author cursor untouched) and `TestDeleteTicketCascades` (row + activity + attachment
      rows + events + cursors + subscriptions gone; other tickets untouched; missing id →
      `ErrTicketNotFound`).
- [ ] **Daemon + CLI — delegate --attach:** in `delegate()` (internal/daemon/delegate.go):
      upfront block right after `trackedByChief` is computed — non-chief source with
      attachments → error; per-file `os.Stat` + not-a-dir; `d.notebookRoot()` non-empty —
      all before the placement switch so failure has zero side effects. After
      `createDelegatedTicket` (inside the `trackedByChief` block, before
      `broadcastTicketsUpdated`): loop `copyTicketAttachment(ticketID, path, filepath.Base(path))`
      + `AddDelegatedTicketAttachment(..., chiefSessionID, session.ID, ...)`; on any error
      run the existing mint-failure teardown (`unregisterSession` +
      `removeWorkspaceLayoutPaneForSession` + `rollbackDelegation`) plus
      `store.DeleteTicket(ticketID)` and best-effort `os.RemoveAll` of the ticket's
      attachments dir. No `notifyTicketObservers` at mint (chief self-authored; assignee
      consumed — it would be a no-op). Extend `delegatedTicketPrompt` with the
      handover-files block. CLI: `--attach` via a small `flag.Value` slice type in
      `parseDelegateArgs` (abs+stat+not-dir at parse, the `--brief-file` precedent);
      `DelegateOptions.Attach`; `writeDelegateHelp`. Tests (internal/daemon/delegate_test.go,
      mirroring `TestDelegateCreatesAndBindsTicket` + `TestTicketAttachCopiesAndRecords`'s
      `SetSetting(SettingNotebookRoot, ...)` setup): `TestDelegateAttachLandsOnMintedTicket`
      (delegate() returns → `GetTicket` shows both attachments with copied bytes — "on the
      ticket before the agent's first read" — AND `callTicketInbox(agent)` carries no
      attachment events: birth-handover consumed); `TestDelegateAttachMissingFileFails`
      (error, no workspace/pane/session/ticket minted);
      `TestDelegateAttachRequiresChiefSource`; `TestDelegateAttachCopyFailureUnmintsTicket`
      (pre-create `<root>/.attn/tickets` as a regular FILE so the post-mint `MkdirAll`
      fails → session gone, ticket deleted, pane rolled back — mirror the assertions in
      `TestDelegateRollsBackPaneWhenSpawnFails`). CLI parse test:
      `TestParseDelegateArgsAttachRepeatable` (two `--attach` → both absolute; missing
      file → error), following `TestParseDelegateArgsModelAndEffort`.
- [ ] **Guidance + changelog:** `writeTicketHelp` attach line gains the `[<ticket-id>]`
      form ("any ticket by id; does not subscribe you"); tickets.md's "Reading the board
      and commenting on another ticket" section (internal/agent/attn_skill/references/)
      gains the by-id attach bullet next to comment; delegation.md gains a short
      "Handover files" subsection (`--attach`, repeatable, chief-tracked only, files ride
      the ticket durably). No hooks.go changes — always-on guidance stays thin (the
      collaboration-plan precedent). One CHANGELOG entry for the whole capability.

## Verification

- `go test ./internal/store ./internal/daemon ./internal/protocol ./internal/ticketnotify ./cmd/attn`
  (if using `-race` on internal/daemon, scope with `-run` — the pre-existing
  `TestGitStatusScheduler` race aborts the package run).
- After `make generate-types`: `git status` shows only generated.go/generated.ts +
  schema; `pnpm --dir app test` (tsc catches the quicktype enum-merge class of breakage;
  no enums change here, the check is cheap).
- Dev-profile smoke (standing authorization, sandbox off for signing): `make dev`, make a
  chief, then from it `attn delegate --brief "read the attached plan" --attach docs/plans/<any>.md`;
  confirm the attachment in the ticket detail panel, `attn ticket inbox` on the delegate
  shows no attachment event, and `attn ticket attach <that-id> --file <other> --session <chief>`
  lands a second attachment authored by the chief. Note: the skill references ship via
  daemon `//go:embed` — rebuild before checking guidance text.

## Decisions

- **By-id attach mirrors comment in shape AND authorization** (settled): positional id +
  interleaved flags (`parseTicketCommentArgs` precedent); any agent holding the id may
  attach; existence is the only check (terminal/archived tickets accept attachments, as
  they accept comments); one-shot — participants notified via `notifyTicketObservers`,
  attacher not enrolled. Bound form (no positional) unchanged — backward compatible.
- **`attachment_added` joins `commented` in the participation exclusions** (forced by the
  settled "no participation conferred": it is a non-comment event kind, so without the
  query change a by-id attacher would silently become a participant). Accepted edge,
  same as the comment plan's: a displaced previous assignee whose only authored events
  were attachments drops out of participation on reassignment.
- **One ProtocolVersion bump covers both features** (settled).
- **`delegate --attach` failure is atomic** (settled): a half-delegated ticket missing
  its promised artifact is worse than a clean error. Verified reality: today's
  `rollbackDelegation` + mint-failure path tear down worktree/workspace/pane/session but
  nothing can delete a ticket — hence `store.DeleteTicket`, rollback-only, safe because
  the failed call minted the ticket and never broadcast it.
- **`--attach` requires the chief as source** (derived): only a chief-tracked delegation
  mints a ticket (`trackedByChief` gates `createDelegatedTicket`), so a non-chief
  `--attach` has nothing to attach to → clean upfront error, consistent with atomicity.
- **Mint attachments join the birth-handover cursor exception** (derived from
  `CreateTicket`'s in-tx assignee-cursor advance): the files are part of the same act as
  the brief, so the assignee's cursor advances past their events in the same tx
  (`AddDelegatedTicketAttachment`) — otherwise the agent gets doorbelled about files it
  was told about at spawn, the exact self-nudge `CreateTicket`'s comment warns about.
  Delegation birth remains the single cursor-advance exception; by-id attach advances
  nothing.
- **The delegated prompt lists source paths, not ticket-store paths**: the prompt is
  composed before mint (spawn precedes `createDelegatedTicket`), so destination paths
  don't exist yet; the source absolute path is same-machine and readable immediately.
  The durable copy serves later readers (resume, reassignment, the detail panel).
- **CLI validates `--attach` files at parse time** (mirrors `--brief-file`'s
  read-at-parse), so most failures never reach the daemon.

## Open Questions / Follow-ups

- **Agent read-path to attachment copies:** events carry only filenames, and there is no
  CLI that returns an attachment's stored path yet. The sibling plan
  `docs/plans/2026-07-02-ticket-show.md` (`attn ticket show --json`) exposes attachment
  paths and closes this for resumed/reassigned agents; until then the prompt's source
  paths cover birth, and the detail panel covers the human.
- **Per-file `--note` on delegate --attach** is deferred — a single `--note` is ambiguous
  across repeated `--attach` flags; add it if a real need shows up.
- **Chief system-prompt (hooks.go) stays untouched**; if chiefs don't discover by-id
  attach from the skill reference in practice, add a clause to the chief block later.
- Orphaned copied files when `AddTicketAttachment` fails after a successful copy exist in
  the bound path today too (accepted); the delegate path's `os.RemoveAll` teardown covers
  its own case.
