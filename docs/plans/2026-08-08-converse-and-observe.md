# Plan: converse and observe — `attn agent peek` and `attn agent msg`

## Goal

First vertical through the **agents converse and observe** rock in
[docs/vision/friendly-home-for-agents.md](../vision/friendly-home-for-agents.md):
an attn-living agent inspects what another session is doing without
interrupting it, and sends it a directed, sender-attributed message. Sequenced
ahead of the garden build (Victor, 2026-08-06) because tickets retire only
when the garden is usable **and** directed messaging is live — this vertical
is what keeps ticket retirement from orphaning agent conversation.

Two primitives with one invariant each:

- **peek** is passive. Asking an agent what it is doing costs it a turn;
  watching it must cost nothing. Everything peek serves is daemon-held
  already — peek never generates PTY input and never wakes the observed
  agent.
- **msg** is delivery into the conversation. The message lands in the
  target's session view by construction (it is typed into the PTY like a
  doorbell), attributed to its sender, with the reply path in the payload.

Scope rulings carried from the vision (do not re-litigate here):
session-id addressing first, crew addressing later; seed comments skipped
("a comment is a message with no addressee"); `attn delegate` is the
human-side proof and the server-as-client future rides these same surfaces.

## Architecture Map

```text
Current (nothing agent-facing):
  observation: app WS only — session snapshots, ws_session_message.go (annotation UI)
  delivery:    typeDoorbell(sessionID, prompt)  [chief_of_staff.go:158]
                 callers: chief nudge, ticket nudge (content-free constant),
                          present notice, annotation submit
                 guard: isNudgeDeliveryAllowed — refuses pending_approval
                 mechanics: plugin-driver relay, else bracketed paste + fenced \r

Target:
  attn agent peek <session> [--json] [--scrollback N]
    -> client.send (unix socket, one JSON round trip)
      -> daemon handleAgentPeek                    [new, ScopeSession]
        -> store.Get(id)                            state, todos, stamps
        -> inspectableTranscriptPath + transcript.ExtractLastAssistantMessage
        -> ptybackend SnapshotProvider.Snapshot().Screen.Text
    <- one AgentPeekResult; the observed session is never touched

  attn agent msg <session> "text"
    -> client.send (source_session_id stamped by CLI from ATTN_SESSION_ID)
      -> daemon handleAgentMsg                     [new, ScopeSession]
        -> INSERT agent_messages row (sender, target, content)
        -> deliverable now?  -> composeAttributedPrompt -> typeDoorbell
           blocked (pending_approval / working guard)?
                             -> row stays queued; delivery retried on
                                state change (same trigger the ticket-nudge
                                countdown already watches)
    <- result says delivered | queued (never a silent drop)
```

CLI exemplars to mimic: `cmd/attn/state_explain.go` (read, session-scoped,
`--json`) and `cmd/attn/main.go` ticket comment (act, threads
`sourceSessionID`).

## Data Model / Interfaces

```ts
// AgentPeekResult — assembled per request, nothing new persisted
{
  session_id, name, workspace,
  state, state_reason?, state_since, last_seen,   // from protocol.Session
  turn_owed,
  todos: string[],            // flat pre-rendered strings, served as-is (see Decisions)
  last_assistant_message?: string,   // absent ≠ error, same contract as ws_session_message.go
  screen?: { text: string, cols, rows },  // viewport; --scrollback N appends history
}

// agent_messages — new table, daemon-owned; replaces dormant
// chief_of_staff_dispatch_messages (migration 46, zero readers/writers ever)
{
  id, sender_session_id, target_session_id,
  content,                    // the sender's words, uncomposed
  created_at, delivered_at?,  // delivered_at NULL = still queued
}

// composed prompt typed into the target (daemon-owned format):
// 📨 from session <short-id> (<workspace>): <content>
//    reply: attn agent msg <short-id> "..."
```

## Boundaries

- The **daemon** owns message persistence, attribution composition, and
  delivery timing. The sender's CLI never touches the target's PTY; the
  composed prompt format is the daemon's, so attribution cannot be forged by
  the sender beyond what it puts in `content`.
- **peek** reads only: `store.Get`, transcript file, worker snapshot. No
  `applyState`, no PTY writes, no doorbell. The snapshot RPC goes to the
  worker's parsed terminal — the agent process never observes it.
- **msg** reuses `typeDoorbell` and everything behind it (approval guard,
  plugin-driver relay, write fence, 150ms submit beat). No second delivery
  path: codex mid-turn queueing and claude mid-turn safety are already that
  primitive's contract.
- **Protocol**: both commands are new socket messages — same generated types
  and `Cmd*` switch as the WS path, so this bumps `ProtocolVersion`
  (216 → 217; lockstep: `constants.go`, regenerated `generated.go`/
  `generated.ts`, and `useDaemonSocket.ts` — the third spot rebases love to
  merge down). No frontend behavior change: the delivered message is visible
  in the session view because it goes through the PTY.
- **Linux**: everything here is `cmd/attn` + `internal/**`; nothing
  darwin-only. Cross-compile must stay green.

## Implementation Steps

### Slice 1 — peek

- [ ] Protocol: `AgentPeekMessage` / `AgentPeekResult` in `main.tsp`,
      `make generate-types`, version bump, `CommandMeta` (`ScopeSession`),
      decoder case.
- [ ] Daemon: `handleAgentPeek` assembling the three reads;
      transcript via `inspectableTranscriptPath` (the inspection ladder,
      no cwd guess); screen via `SnapshotProvider` type-assert with a
      clean "screen unavailable" degrade (old worker, no snapshot).
- [ ] CLI: `attn agent peek <session>` with human output and `--json`;
      session-id prefix match like other session-scoped commands; errors
      name the session asked for.
- [ ] Tests: store-backed daemon test (state+todos), transcript fixture,
      snapshot-less degrade; CLI output golden.

Acceptance: from session A, `attn agent peek B` shows B's state, todos,
last assistant message, and screen while B is mid-turn — and B's transcript
shows no reaction of any kind.

### Slice 2 — msg

- [ ] Migration: create `agent_messages`, drop
      `chief_of_staff_dispatch_messages` (never written; check
      `MAX(version)` in real DBs before numbering).
- [ ] Protocol: `AgentMsgMessage` / `AgentMsgResult` (`delivered | queued`),
      version bump.
- [ ] Daemon: `handleAgentMsg` — persist row, compose attributed prompt,
      deliver via `typeDoorbell`; on guard refusal leave queued and hook
      redelivery into the state-change path the ticket-nudge countdown
      already rides; stamp `delivered_at` on success.
- [ ] CLI: `attn agent msg <session> "text"`; sender from `ATTN_SESSION_ID`
      (refuse with a clear error when unset — a message needs a sender);
      print `delivered` or `queued (target pending approval)`.
- [ ] Content cap with a named limit: measure a sane doorbell paste size
      first (bracketed paste of multi-KB text through the fence), set the
      tripwire past it, and make the error name the limit, its value, and
      the ask.
- [ ] Tests: delivery mid-idle, queue-then-deliver across a state change,
      attribution format, self-msg, cap error.

Acceptance: A sends B a message while B is `pending_approval`; the result
says `queued`; when B's approval clears, the message lands in B's pane,
attributed to A, with the reply command; B replies with the printed command
and it lands in A's pane the same way. Ticket comments were never involved.

### Verification

Daemon + PTY + protocol change: live verification on a throwaway profile
(never `dev`-as-shared, never production), two real sessions, both
acceptance walks above, plus codex-target delivery mid-turn (doorbell
queueing) and `make build-linux-amd64`. Preflight before evidence.

## Decisions

- **Todos ship as the flat strings the hook already stores.** Structured
  todos (status, ids) require changing `_hook-todo`'s shape — a different
  vertical. Peek's `--json` emits the strings; consumers parse the
  `[✓]/[→]/[ ]` prefix if they care. Revisit only if a real consumer needs
  more.
- **Queued, not skipped, on `pending_approval`.** The annotation path
  returns `skipped`+retry because a human client is watching; an agent
  sender would have to poll, which converts msg into ticket-comment-shaped
  indirection — the thing this vertical retires. The daemon owns retry.
- **New `agent_messages` table; the dormant dispatch table goes.** Migration
  46's `chief_of_staff_dispatch_messages` has never had a writer, so no user
  state exists and drop-and-replace is safe. Its shape (dispatch-scoped,
  ack columns) doesn't fit; a fresh minimal table beats adopting columns
  we'd ignore.
- **Reply is just msg.** No reply/thread machinery in v1: the composed
  prompt carries the exact command to answer with. Threading, read receipts,
  and crew addressing wait for evidence.
- **One delivery primitive.** msg composes a prompt and calls
  `typeDoorbell`; it does not grow a parallel injection path. If doorbell
  mechanics need to change (they shouldn't), that change belongs to the
  doorbell, benefiting all six callers.

## Open Questions

- **Ping-pong risk**: two agents auto-replying to each other could loop.
  V1 carries no rate machinery (single-user garden, messages are visible in
  both panes, the human is watching); named here so silence isn't mistaken
  for a decision.
- **`working`-state delivery for claude targets**: mid-turn injection is
  memory-safe for claude and queued-by-doorbell for codex, but whether msg
  should *prefer* waiting for idle (politeness) over immediate delivery is
  a product feel question — v1 delivers whenever the guard allows, matching
  ticket nudges.
- Whether peek's screen text should ever travel to remote/hub sessions
  (relay is a text pipe, so mechanically fine) — out of v1 scope, noted for
  the server-as-client arc.

## Follow-ups

- `attn agent` group joins `writeHelp` once both subcommands exist
  (`ticket` precedent: wired before advertised).
- Glossary entries for *peek* and *message* when the vocabulary survives
  first contact.
- Crew addressing (`attn agent msg trellis`) after the daemon crew
  primitive lands.
- `queueBands.ts:168` (+ test twin) still calls the chief "the seat you
  always want to reach" — rename leftover flagged by review on #787, fix
  next time in `app/` code.
