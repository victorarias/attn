# Plan: `attn ticket show` — the non-consuming drill-in read

## Goal

Give agents a CLI read of ONE ticket's full record — id, title, status, assignee,
description, the complete activity thread (status changes + comments, each with author
and timestamp), attachments, cwd, created/updated timestamps — as human output and
`--json`. Today the agent socket has two reads and neither fits: `attn ticket inbox` is
a **consuming** read (`ticketnotify.Consume` advances the caller's per-ticket cursors),
and `attn ticket list` deliberately returns bare rows (no activity thread, matching the
board broadcast). So after a compaction or restart the chief literally cannot re-read a
thread from the CLI without eating its own unread queue. This is the drill-in read the
vision promises ("distilled by default, drill-in on demand" —
docs/vision/chief-delegation-awareness.md) and the durable-awareness gap: awareness must
survive compaction, and the app's `get_ticket` detail view already proves the read
exists — it just isn't reachable over the agent socket.

## Architecture Map

```text
Current (two agent reads, neither fits; the full read is app-only):
  attn ticket inbox -> handleTicketInbox (ticket_read.go) -> ticketnotify.Consume   CONSUMING (cursors advance)
  attn ticket list  -> handleTicketList  (ticket_board.go) -> store.ListTickets     bare rows (no activity)
  app detail view   -> ws get_ticket -> sendGetTicketWSResult -> store.GetTicket    full record, websocket only

Target (same full-record read, agent transport; cursors untouched):
  attn ticket show <ticket-id> [--session <id>] [--json]
    -> runTicketShow (cmd/attn/main.go, reuses parseTicketIDArgs)
      -> client.TicketShow (internal/client/client.go)
        -> unix socket {"cmd":"ticket_show","ticket_id":...}
          -> handleTicketShow (internal/daemon/ticket_board.go)
            -> store.GetTicket(id)        // row + activity + attachments — the SAME read get_ticket uses
            -> ticketToProtocol(ticket)   // Activity/Attachments mapped when present
            -> Response{ok, ticket_show_result:{ticket}}

Tests:
  internal/daemon/ticket_show_test.go
    NewForTesting daemon + syncConn -> handleTicketShow directly
    fixtures: delegateMany / boundTicketID / callSetTicketStatus / callTicketComment
    callTicketInbox AFTER show proves the cursor did not move
```

## Data Model / Interfaces

No store changes, no migration. `store.GetTicket` (internal/store/tickets.go) already
returns the full record: the row plus `ticketActivity(id)` and `ticketAttachments(id)`.
`ticketToProtocol` (internal/daemon/ticket_board.go) already maps activity and
attachments to the wire `Ticket` when present. This plan only adds a transport.

Protocol additions (internal/protocol/schema/main.tsp, then the CLAUDE.md critical
pattern #1 dance):

```tsp
// TicketShowMessage is the drill-in read of one ticket over the agent socket — the
// full record (row + activity thread + attachments), the agent-socket twin of the
// app's get_ticket. It is NON-consuming: unlike ticket_inbox it never advances any
// cursor, so an agent (a post-compaction chief especially) can re-read a thread
// without eating its unread queue. Like ticket_list it is a global by-id read, not
// identity-scoped: source_session_id is optional and unused, kept for uniformity.
model TicketShowMessage {
  cmd: "ticket_show";
  source_session_id?: string;
  ticket_id: string;
}

model TicketShowResult {
  ticket: Ticket;   // full record: activity + attachments populated
}

// Response gains:
//   ticket_show_result?: TicketShowResult;
```

Client method (internal/client/client.go, mirror `TicketList` for the optional
session and `CommentTicket` for the result-nil guard):

```go
func (c *Client) TicketShow(sourceSessionID, ticketID string) (*protocol.Ticket, error)
```

Human output (new `writeTicketDetail(w io.Writer, t protocol.Ticket)` next to
`printTicketBoard` in cmd/attn/main.go; `--json` is just `printJSON(ticket)`):

```text
store-migration  in_review  assignee: sess-abc
title:   Migrate the store to X
cwd:     /Users/victor/projects/x
created: 2026-07-02T10:00:00Z   updated: 2026-07-02T11:30:00Z

description:
  <indented, multi-line>

activity:
  2026-07-02T10:00:00Z  chief-1   created
  2026-07-02T11:00:00Z  sess-abc  working → in_review: "ready for eyes"
  2026-07-02T11:30:00Z  sess-x    comment: "looks good, ship it"

attachments:
  findings.md  /Users/victor/.attn/tickets/store-migration/findings.md  (the diagnosis)
```

## Boundaries

- **`handleTicketShow` is a pure read.** It must not touch cursors
  (`ticketnotify.Consume`, `store.SetTicketCursor`, `refreshTicketUnread`) and must not
  call `broadcastTicketsUpdated` / `notifyTicketObservers` — nothing mutated. `inbox`
  stays the only consuming read.
- **The store owns the full-record composition.** The handler calls `store.GetTicket`
  and maps with `ticketToProtocol`; it does not run its own activity/attachment queries.
  One read, two transports (ws `get_ticket` for the app, `ticket_show` for agents).
- **`hooks.go` is not touched.** `ChiefGuidance` / `TicketAwarenessGuidance` are owned
  by the chief-guidance-brief-craft plan landing in the same wave; teaching the chief
  *when* to drill in belongs there. This plan only documents the *how* in the skill
  reference (`references/tickets.md`) and `writeTicketHelp`.
- **`references/delegated-agent.md` untouched** — it covers the own-ticket surface
  (status/inbox); `show` is a cross-ticket read and lives with list/comment in
  `tickets.md`.
- attn authors nothing here (spine intact: the only attn-authored status stays
  `crashed`, internal/daemon/ticket_crash.go).

## Implementation Steps

- [ ] **Protocol.** Add `TicketShowMessage` + `TicketShowResult` models and the
      `ticket_show_result?` Response field to `internal/protocol/schema/main.tsp`
      (place next to `TicketListMessage`; comment per the sketch above). Then:
      `rm -rf tsp-output` (orphaned models glob back in), `make generate-types`,
      add `CmdTicketShow = "ticket_show"` and a `case CmdTicketShow:` decode arm to
      `ParseMessage` in `internal/protocol/constants.go` (copy the `CmdTicketList`
      arm), bump `ProtocolVersion` (CLAUDE.md critical pattern #1 — never pin the
      number). No new enums, but still tsc-check `app/src/types/generated.ts` after
      regen (quicktype merge gotcha).
- [ ] **Protocol decode test.** Add a `ticket show message` case to `TestParseCommand`
      in `internal/protocol/parse_test.go`:
      `{"cmd":"ticket_show","ticket_id":"tk"}` → `CmdTicketShow`.
- [ ] **Daemon handler.** `handleTicketShow(conn net.Conn, msg *protocol.TicketShowMessage)`
      in `internal/daemon/ticket_board.go`, next to `handleTicketList` /
      `sendGetTicketWSResult`: trim `msg.TicketID` (empty → `d.sendError`), call
      `d.store.GetTicket`; error or nil ticket → `d.sendError` (mirror
      `handleTicketComment`'s bad-id behavior — a clear error, not an empty success);
      else encode `protocol.Response{Ok: true, TicketShowResult:
      &protocol.TicketShowResult{Ticket: ticketToProtocol(ticket)}}`. Wire the
      dispatch: `case protocol.CmdTicketShow:` in the `handleMessage` switch in
      `internal/daemon/daemon.go`, beside `CmdTicketList`.
- [ ] **Daemon handler test.** New `internal/daemon/ticket_show_test.go` mirroring
      `ticket_list_test.go`: a `callTicketShow` helper (syncConn — show has no async
      side effects) and two tests.
      `TestHandleTicketShowReturnsFullThreadWithoutConsuming`: `delegateMany` a chief +
      worker, `boundTicketID`, then `callSetTicketStatus(worker → ready_for_review,
      "done")` and `callTicketComment` from a sibling; `callTicketShow(t, d, "", id)`
      with NO session must return Ok with description, assignee, and an Activity slice
      carrying the created + status_changed (with from/to) + commented entries, each
      with author and timestamp. THEN `callTicketInbox(t, d, chiefID)` must still
      deliver the status-change event — the linchpin assertion that show moved no
      cursor (behavioral, like `TestAgentCommentDoesNotSubscribeCommenter`).
      `TestHandleTicketShowUnknownID`: bad id → `Ok == false`.
- [ ] **Client.** `TicketShow` in `internal/client/client.go` per the signature above
      (mirror `TicketList`: optional source session set only when non-empty; nil
      `TicketShowResult` → error).
- [ ] **CLI.** In `cmd/attn/main.go`: add `case "show":` to `runTicket` (with the
      standard `hasHelpFlag` guard); `runTicketShow(args)` reuses
      `parseTicketIDArgs("ticket show", args)` — the shared one-id-positional parser
      subscribe/unsubscribe already use — but resolves the session best-effort like
      `runTicketList` (read `--session` then `ATTN_SESSION_ID`, no error when absent —
      do NOT use `resolveDispatchSession`, which errors). `--json` → `printJSON`;
      human → `writeTicketDetail(os.Stdout, t)` per the sketch. Add the `show` entry to
      `writeTicketHelp` between `list` and `attach`, noting "full activity thread;
      does not mark anything read; no session required".
- [ ] **CLI render test.** `TestWriteTicketDetail` in `cmd/attn/main_test.go`: build a
      `protocol.Ticket` with a status-change entry, a comment entry, and an attachment;
      assert the rendered string carries the thread in order with authors, the
      `from → to` arrow, the comment text, and the attachment line. (No new parse test:
      `parseTicketIDArgs` is shared and already covered by `TestParseTicketIDArgs` —
      re-running it under the "ticket show" name would restate an existing test.)
- [ ] **Docs.** `internal/agent/attn_skill/references/tickets.md`: add
      `attn ticket show <ticket-id> [--json]` to the "Reading the board and commenting
      on another ticket" section as the drill-in read — full thread + attachments, never
      marks anything read (inbox is the consuming read), the way to re-read a thread
      after a compaction or restart; `list` finds the id, `show` reads the ticket. The
      skill ships via daemon `//go:embed` (internal/agent/attn_skill.go), so nothing
      else to wire. Add a CHANGELOG.md entry (user-visible CLI addition, one bullet).

## Verification

```bash
go build ./...
go test ./internal/protocol
go test ./internal/daemon -run 'TestHandleTicketShow'   # scope: gitstatus scheduler test has a pre-existing race
go test ./cmd/attn
pnpm --dir app exec tsc --noEmit                        # generated.ts still typechecks after regen
```

Manual smoke (daemon/protocol change — use a throwaway profile, NOT dev: the dev app
auto-respawns an old daemon that silently drops new fields): rebuild `./attn`, start a
scratch `ATTN_PROFILE` daemon, delegate once, have the worker `attn ticket status
ready_for_review --comment "x"`, then from a bare shell (no `ATTN_SESSION_ID`) run
`attn ticket show <id>` and `attn ticket show <id> --json` — thread visible both times —
then `attn ticket inbox --session <chief>` still delivers the event. Clean up with
`attn profile clean`.

## Decisions

- **`show` never advances any cursor — no flag, no option.** `inbox` stays the only
  consuming read; the whole point of `show` is a re-read that costs nothing. (Settled
  with Victor.)
- **Global read, session optional.** Like `ticket list` (and unlike the identity-scoped
  write verbs), `show` needs no session; `--session` is accepted only for command-shape
  uniformity and the daemon ignores it. (Settled with Victor.)
- **Reuse the app's read, don't write a new one.** `store.GetTicket` +
  `ticketToProtocol` are exactly what ws `get_ticket` (`sendGetTicketWSResult`) serves
  the detail panel; `ticket_show` is the same full record over the agent socket. No new
  store queries.
- **Skill reference only; `hooks.go` untouched.** The `show` how-to goes in
  `references/tickets.md`; `ChiefGuidance` is owned by the chief-guidance-brief-craft
  plan in the same wave — touching it here would collide. (Settled with Victor.)
- **Reuse `parseTicketIDArgs`, not a new parser.** The brief anticipated mirroring
  `parseTicketCommentArgs`, but the generalized one-id-positional helper already exists
  (subscribe/unsubscribe use it) — reality adapted, and the CLI parse test collapses
  into the existing `TestParseTicketIDArgs` coverage.
- **Unknown id is an error, not an empty success** — mirrors `handleTicketComment`
  (`ErrTicketNotFound` path) rather than ws `get_ticket`'s failed-result-event shape,
  because agent-socket verbs report failure via `Response.Ok`.

## Open Questions / Follow-ups

- Chief guidance push ("after a compaction, `ticket list` then `ticket show` to
  rebuild the picture") — deferred to the chief-guidance-brief-craft plan, which owns
  that surface.
- Attachment `path` in the output is a daemon-local absolute path — fine for the
  same-machine CLI today; revisit if tickets ever cross machines (ticket export,
  work-tracker slice 8).
- No frontend work: the app already has the drill-in (`get_ticket` → TicketDetailPanel);
  `generated.ts` gains unused types, which is expected.
