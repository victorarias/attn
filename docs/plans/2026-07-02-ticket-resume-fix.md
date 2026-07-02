# Plan: Fix ticket Resume on a closed/unregistered session

## Goal

Pressing **Resume** on a ticket whose bound session is closed/unregistered rolls the
whole operation back with "Session spawn arguments were not prepared." (observed
empirically during e2e work circa 2026-06-30; pre-existing, still present in this
tree). Resume is a core affordance of the durable-ticket story — the vision doc
(docs/vision/chief-delegation-awareness.md) promises a board Victor can "return to —
to resume a conversation, track a project across days" — so it must work for tickets
whose sessions closed long ago and across app restarts. Fix the root cause (a racy
frontend orchestration), not the symptom.

## Root Cause

Grounded in code read in this tree; every step below was traced, not guessed.

**Where the error comes from.** The string exists in exactly one file:
`app/src/App.tsx`, thrown in `createWorkspaceSession` (and its sibling
`createSplitSession`) when `takeSessionSpawnArgs(sessionId, 80, 24)` returns `null`.
The catch runs `rollbackSessionCreation` (close pane → `closeSession` →
`sendUnregisterWorkspace`) and rethrows; `handleResumeTicket`'s spawn handler
surfaces it via `showError`. That is the observed "rolls back" behavior.

**Why `takeSessionSpawnArgs` returns null.** It is not a seeding gap:
`takeSessionSpawnArgs` (`app/src/store/sessions.ts`) derives the spawn args from the
session's row in the local zustand store and returns `null` **only** when
`sessions.find(id)` misses. `createSession` — called two lines earlier in
`createWorkspaceSession` — synchronously appends that row. The row is **removed
mid-flight**, between `createSession` and `takeSessionSpawnArgs`.

**The remover.** The only code that removes local sessions wholesale is
`syncFromDaemonSessions` (`app/src/store/sessions.ts`): it replaces the local list
with the daemon's, keeping local-only rows *only* when
`state === 'launching' && session.workspace.agents.some(pane => pane.sessionId === id && pane.status === 'spawning')`.
A freshly created resume session has `workspace: createDefaultWorkspaceState()` — no
panes — until the daemon's `workspace_layout_updated` broadcast round-trips into it
via `syncFromDaemonWorkspaces`. So the placeholder is unprotected for the whole
`await sendWorkspaceAddSessionPane(...)` window (a full ws round trip), plus the
React-effect lag after it.

**The trigger.** `App.tsx` has an effect (`syncFromDaemonSessions(daemonSessions)`,
deps `[daemonSessions, ...]`) that runs on **every** identity change of
`daemonSessions` — and `useDaemonSocket` produces a fresh array on every
`sessions_updated` **and every single-session `session_state_changed` /
`session_todos_updated` upsert**. Any such broadcast landing in the window prunes the
placeholder → `takeSessionSpawnArgs` → `null` → throw → rollback. Ticket resume hits
this near-deterministically because it happens in a busy daemon: the chief is live
and churning state, and a just-closed delegated session generates ticket traffic
(crash capture in `internal/daemon/ticket_crash.go`, nudge scheduling that mutates
session fields). Plain new-session creation shares the window but usually runs in a
quiet app — which is why this shipped. Note the effect ordering in `App.tsx` makes
the guard weaker still: the sessions-sync effect is declared *before* the
workspaces-sync effect, so a layout update arriving in the same render flush cannot
save the row.

**The daemon side is already resume-ready.** Resume needs nothing the frontend has:
`cwd` and `last_agent_id` live on the ticket (`store.SetTicketSession`, seeded by
`createDelegatedTicket`), and the agent-native resume id is mirrored onto the ticket
by `persistResumeSessionID` → `store.SetTicketResumeSessionID` *precisely because*
the session row is deleted on close. `handleSpawnSession` (`internal/daemon/ws_pty.go`)
already resolves it: the `resume_picker` branch falls back to
`store.GetTicketResumeSessionID(msg.ID)` for a precise resume. The rollback is pure
frontend orchestration failure — the daemon never even sees the spawn.

**Residual ambiguity:** *which* broadcast landed in the observed e2e repro is
unproven (any of the above suffices). Hence implementation step 1 is a concrete
reproduction before the fix.

## Behaviors That Must Survive (AGENTS.md rule)

1. Resume reopens an agent bound to the **same ticket identity**: session id =
   `ticket.assignee`, so the resumed agent's `attn ticket status` lands on the same
   ticket and participation/nudge routing keep working.
2. Opens in the ticket `cwd`.
3. **Precise resume** of the `last_agent_id` transcript when the ticket's mirrored
   resume id is resumable; the cwd-scoped resume picker as fallback (comment in
   `createWorkspaceSession` documents this contract today).
4. Bound session still tracked → **focus it, never spawn a duplicate** local session
   row (the poison documented in `app/src/utils/ticketResume.ts`); a dead-but-
   recoverable pane keeps surfacing its own close-and-restart prompt via the attach
   path.
5. No usable bound id (unassigned or the human `you`) → resume still works by
   minting a fresh session id (today's `planTicketResume` behavior).
6. Ticket without a bound agent session (no cwd / last_agent_id) → clear error, no
   side effects; the panel already hides the button (`canResume` in
   `TicketDetailPanel.tsx`).
7. Rename authority: a stored workspace title / session label still beats what the
   resume sends (`handleRegisterWorkspace` title guard, `handleSpawnSession` label
   guard).
8. Detail panel closes on success; the resumed session takes focus.
9. Resume authors **no** ticket status or event — attn authors only `crashed`
   (spine). A crashed ticket stays crashed until the resumed agent reports.

## Architecture Map

```text
Current (frontend-seeded, racy):
TicketDetailPanel "Resume" ─▶ App.handleResumeTicket
  ─▶ planTicketResume (app/src/utils/ticketResume.ts)
       focus ─▶ handleSelectSession                       (assignee still tracked)
       spawn ─▶ createWorkspaceSession(assignee id, resumePicker)
                 await sendRegisterWorkspace              ws round trip
                 sessionStore.createSession               seeds LOCAL placeholder
                 await sendWorkspaceAddSessionPane        ws round trip   ◀── window
                 takeSessionSpawnArgs ── null? ──▶ THROW + rollbackSessionCreation
                 ptySpawn ─▶ spawn_session ─▶ daemon resolves ticket resume id

  pruning path (fires any time during the window):
  any sessions_updated / session_state_changed broadcast
    ─▶ useDaemonSocket.onSessionsUpdate ─▶ useDaemonStore.setDaemonSessions
      ─▶ App effect syncFromDaemonSessions ─▶ drops the placeholder (no spawning pane yet)

Target (daemon-owned, one command — the delegate() precedent):
TicketDetailPanel "Resume" ─▶ App.handleResumeTicket
  ─▶ sendTicketResume(ticketId)                            useDaemonSocket, pattern #2
    ─▶ ws ticket_resume ─▶ handleTicketResume ─▶ resumeTicket   internal/daemon/ticket_resume.go
         store.GetTicket → validate cwd + last_agent_id
         assignee still registered? ─▶ result{already_running, session_id}
         handleRegisterWorkspace(nil, "workspace-"+sessionID)    (as delegate() does)
         handleWorkspaceLayoutAddSessionPane(internal ws client)
         handleSpawnSession(internal ws client, ResumePicker: true)
           └─ existing ticket-resume branch resolves GetTicketResumeSessionID
              + NEW ResumeAvailable downgrade (reload.go precedent)
         failure ─▶ rollback (mirror rollbackDelegation, minus worktree)
    ◀─ ticket_resume_result{session_id, workspace_id, already_running}
  ─▶ handleSelectSession(session_id); handleCloseTicketDetail()
  (session/pane reach the UI via the normal broadcasts — exactly how a delegated
   session appears today; the frontend seeds nothing)

Tests:
internal/daemon/ticket_resume_test.go ─▶ NewForTesting + fakeSpawnBackend (delegate_test.go style)
app/src/hooks/useDaemonSocket.test.tsx ─▶ mock ws.emit (existing "ticket request/result" describe)
```

## Data Model / Interfaces

No store schema change, no migration — resume reads existing ticket columns
(`cwd`, `last_agent_id`, the assignee-keyed resume mirror).

Protocol (TypeSpec, `internal/protocol/schema/main.tsp`, next to
`TicketChangeStatusMessage` / `TicketActionResultMessage`):

```tsp
// ticket_resume reopens the agent session bound to a ticket: same session id
// (the assignee), the ticket's cwd, resuming the mirrored conversation when
// resumable. The daemon owns the whole composite (workspace + pane + spawn).
model TicketResumeMessage {
  cmd: "ticket_resume";
  request_id?: string;
  ticket_id: string;
}

model TicketResumeResultMessage {
  event: "ticket_resume_result";
  request_id: string;
  success: boolean;
  error?: string;
  session_id?: string;
  workspace_id?: string;
  already_running?: boolean;   // assignee was still registered; frontend just focuses
}
```

Both models join the message union(s) alongside their ticket siblings. Go constants
(`CmdTicketResume`, `EventTicketResumeResult`) and a decode case mirroring
`CmdTicketChangeStatus` go in `internal/protocol/constants.go`; bump
ProtocolVersion (CLAUDE.md critical pattern #1).

## Boundaries

- **The daemon owns the resume composite** — validate, register workspace, add
  pane, spawn, rollback. The frontend sends one command and focuses the result; it
  never seeds local session state for a session it did not create.
- **`handleSpawnSession` stays the single resume-id resolver.** `resumeTicket` does
  not pass a `ResumeSessionID`; it sets `ResumePicker: true` and lets the existing
  ticket-resume branch resolve the mirrored id. The `ResumeAvailable` downgrade
  lands inside that branch so both callers (and any future one) get it.
- **Resume writes nothing to the ticket** — no status, no event, no cursor. The
  board informs; it never gates.
- `internal/ticketnotify`, nudges, and cursors are untouched.

## Implementation Steps

- [ ] **1. Reproduce (pre-fix), pinning the trigger.** On the dev profile
      (`make dev`): create a chief, have it delegate a trivial brief, wait for the
      bound ticket, close the delegated pane, keep the chief busy (state churn =
      broadcasts), then board → ticket → Resume. Expect the "Session spawn arguments
      were not prepared." toast and the workspace flash+rollback. If it does not
      fire, add a temporary `console.warn` in `syncFromDaemonSessions`'s prune path
      (remove after) to confirm the placeholder drop lands in the window. This
      validates the mechanism before code changes.
- [ ] **2. Protocol.** Add the two models to `main.tsp` (+ unions); `rm -rf
      tsp-output && make generate-types` (orphan-glob gotcha); add `CmdTicketResume`
      / `EventTicketResumeResult` + decode case to `constants.go` (copy the
      `CmdTicketChangeStatus` case); bump ProtocolVersion; `tsc`-check generated.ts
      (quicktype merges value-identical enums silently).
- [ ] **3. Daemon: `internal/daemon/ticket_resume.go`.** `resumeTicket(ticketID)`:
      `store.GetTicket` → error if nil; error "ticket has no agent session to
      resume" when `Cwd`/`LastAgentID` empty (parity with `planTicketResume`);
      `sessionID := ticket.Assignee`, mint `uuid.NewString()` when empty or `"you"`;
      if `d.store.Get(ticket.Assignee) != nil` return `{already_running, session_id,
      workspace_id}`; validate the directory with `validateDelegationDirectory`
      (worktree may be gone — clear error, no side effects); then mirror
      `delegate()`: `handleRegisterWorkspace(nil, …)` on `"workspace-"+sessionID`
      (title = ticket.Title; idempotent re-register preserves a stored rename),
      `handleWorkspaceLayoutAddSessionPane` via `newInternalWSClient()` +
      `readInternalActionResult`, `handleSpawnSession` with `{ID: sessionID, Cwd,
      WorkspaceID, Agent: ticket.LastAgentID, Cols: 80, Rows: 24, Label:
      ticket.Title, ResumePicker: protocol.Ptr(true)}`. On pane/spawn failure roll
      back like `delegate()` does (`removeWorkspaceLayoutPaneForSession`; unregister
      the workspace only if this call created it — check `store.GetWorkspace` before
      registering). No initial prompt, no yolo, no chief flag, no ticket writes.
      `handleTicketResume(client, msg)` wraps it and replies with
      `TicketResumeResultMessage` (mirror `sendTicketActionResult`'s shape);
      dispatch in `websocket.go` next to `protocol.CmdTicketChangeStatus`
      (`go d.handleTicketResume(...)`, same style as the other ticket actions).
- [ ] **4. Daemon: `ResumeAvailable` downgrade.** In `handleSpawnSession`'s
      ticket-resume branch (`ws_pty.go`, the `GetTicketResumeSessionID` lookup):
      only adopt the mirrored id when `agentdriver.ResumeAvailable(driver, id)`;
      otherwise leave `resumeSessionID` empty so `ResumePicker` falls back to the
      picker instead of `claude -r <dead-id>` exiting non-zero. Mirrors the
      fresh-spawn downgrade in `buildReloadSpawnOptions` (`reload.go`).
- [ ] **5. Frontend socket.** `sendTicketResume(ticketId): Promise<{sessionId,
      workspaceId?, alreadyRunning?}>` in `useDaemonSocket.ts` — copy
      `sendTicketAction`'s pending-key + timeout shape but resolve with the payload
      (the `get_ticket` result handler is the payload-carrying precedent); add the
      `ticket_resume_result` case next to `ticket_action_result`. Wire through the
      App two-component pattern: destructure in `App`, prop to `<AppContent>`,
      `AppContentProps`, destructure in `AppContent` (app/CLAUDE.md gotcha #1).
- [ ] **6. Frontend App.** Rewrite `handleResumeTicket` to
      `sendTicketResume(ticketId).then(r => { handleSelectSession(r.sessionId);
      handleCloseTicketDetail(); }).catch(showError)`. Delete
      `app/src/utils/ticketResume.ts` + `ticketResume.test.ts` (the focus/spawn/
      error branching moves daemon-side; the daemon result arrives after
      `session_registered` broadcast, so the session is already in
      `daemonSessions` when we focus). Remove the now-dead `resumePicker` option
      from `createWorkspaceSession` and `useUiAutomationBridge`'s type (keep
      `chiefOfStaff`).
- [ ] **7. Daemon tests** (`ticket_resume_test.go`, `NewForTesting` +
      `fakeSpawnBackend`, reuse `delegateForNotify`/`boundTicketID` from
      ticket_actions_test.go and `setupDelegationSource` from delegate_test.go):
      - `TestTicketResumeRespawnsClosedBoundSession`: delegate → close the leaf
        (`unregisterSession`) → seed a resumable id (`persistResumeSessionID`) →
        `handleTicketResume` → success result; session re-registered under the same
        id in the ticket cwd; workspace + spawning→ready pane exist; backend spawn
        opts carry the mirrored `ResumeSessionID`; **no new ticket events** (spine).
      - `TestTicketResumeAlreadyRunningFocusesInsteadOfSpawning`: assignee still
        registered → `already_running`, backend records no spawn.
      - `TestTicketResumeFallsBackToPickerWhenTranscriptGone`: mirror
        `TestReloadSessionAgentFreshSpawnsWhenNotResumable` (`t.Setenv("HOME",
        t.TempDir())` empties claude's transcript lookup) → spawn opts have empty
        `ResumeSessionID` and `ResumePicker: true`.
      - `TestTicketResumeValidation`: unknown ticket / no bound agent session /
        missing directory → error result, no workspace or pane side effects.
      - `TestTicketResumeRollsBackPaneWhenSpawnFails`: mirror
        `TestDelegateRollsBackPaneWhenSpawnFails`.
      - `TestTicketResumeMintsFreshSessionWhenAssigneeIsYou`.
- [ ] **8. Frontend test** — extend the `useDaemonSocket ticket request/result`
      describe in `useDaemonSocket.test.tsx` (same `ws.emit` harness as the
      `get_ticket` tests): sends `cmd: 'ticket_resume'` with `ticket_id` +
      `request_id`; resolves with `session_id` on a success event; rejects with the
      error string on failure.
- [ ] **9. CHANGELOG** — one user-facing entry: resuming a ticket whose session was
      closed (or after an app restart) now reliably reopens the agent instead of
      failing with "Session spawn arguments were not prepared."

## Verification

```bash
go build ./...
go test ./internal/daemon -run 'TestTicketResume'   # scope: TestGitStatusScheduler has a pre-existing race
go test ./internal/protocol
rm -rf tsp-output && make generate-types && pnpm --dir app exec tsc --noEmit
pnpm --dir app test useDaemonSocket
pnpm --dir app test
```

Manual (dev profile — pre-approved; run `make dev` outside the sandbox for keychain):

1. `make dev`; in attn-dev create a chief session and prompt it to delegate a
   trivial brief; wait for the bound ticket on the board.
2. Close the delegated session's pane. Confirm the sidebar row is gone and the
   ticket detail still shows **Resume** (row keeps cwd + last_agent_id).
3. Quit and reopen attn-dev.app (the across-restart leg).
4. Board → ticket → **Resume**: the agent reopens in the ticket cwd with the prior
   conversation resumed (transcript visible, not a fresh chat), the detail panel
   closes, no error toast. From that pane, `attn ticket status` shows the same
   ticket (same identity binding).
5. Click Resume again from the detail panel while the session is open → focuses the
   existing pane; no duplicate sidebar row.
6. Negative: delegate into a worktree, close the session, remove the worktree
   directory, Resume → clear error toast, no phantom workspace left behind.

## Decisions

- **Fix the root cause, not the symptom** (settled with Victor). No retry loop or
  re-seed band-aid around `takeSessionSpawnArgs`.
- **Resume is a core affordance of the durable-ticket story** (settled): it must
  work for sessions closed long ago and across app restarts — which rules out any
  fix that depends on frontend state accumulated while the session was alive.
- **Daemon-owned resume over hardening the frontend seeding.** The daemon already
  owns every input (ticket cwd, agent, mirrored resume id) and `delegate()` proves
  the register→pane→spawn composite with rollback; the frontend placeholder guard
  is inherently racy (any broadcast in a two-round-trip window kills it). One
  command, request/result (critical pattern #2), no local seeding.
- **Same session id (`ticket.assignee`) is reused** so assignee == session stays
  the binding; fresh id only when there is no usable bound id (parity with today's
  `planTicketResume`).
- **Dead resume ids downgrade, never crash the spawn**: `ResumeAvailable` check in
  the spawn ticket-resume branch (reload.go precedent), falling back to the
  cwd-scoped picker.
- **Resume authors nothing on the ticket** — attn's only self-authored status stays
  `crashed`; doorbells/nudges are untouched.

## Open Questions / Follow-ups

- The same prune window still makes *normal* `createWorkspaceSession` /
  `createSplitSession` creations flaky under broadcast churn. Follow-up (separate
  PR): protect in-flight creations explicitly (e.g. an in-flight flag consumed on
  `ptySpawn` settle) instead of inferring from layout state.
- `attn ticket resume <id>` CLI over the unix socket can reuse `resumeTicket`
  verbatim — deferred until an agent-side need shows up.
- The packaged scenario (`real-app:scenario-ticket-lifecycle`) only asserts
  `canResume` visibility; extending it to click Resume against a closed session
  would lock this end-to-end — follow-up hardening.
- Resume does not unmute a muted workspace (delegate unmutes for chief-tracked
  work). Left as-is; revisit if a resumed session hides.
