# Dispatch → Notebook handoff

## Why / Alignment

**Why.** A delegated agent (a chief-of-staff *dispatch*) can only hand the chief a
*small* payload: its `dispatch report` summary is the signal the chief sees, and it
is clamped to 1500 runes in the deterministic raw-tier capture. When the agent
builds a *large* artifact with the user (a report, a design doc, findings), it has
no way to forward it — and it is not even aware the Notebook exists (only
chief-of-staff launches get Notebook guidance). "Done" for this chunk: a dispatched
agent can persist that artifact into the Notebook and report back a **reference**,
and the chief can leave it, move it, or promote it.

**Aligned on (the product/scope calls):**

- **No hardcoded artifact location.** The dispatched agent is told the Notebook
  *exists*, but *where to write comes from the chief (or the user)*, per-task, in
  the brief. The chief is the Notebook expert and designates placement; the agent
  does not invent a home. (Victor's steer on Q1.)
- **The reference travels in the report message**, not a new structured
  `DispatchReport` field — so it flows through `dispatch list`, the dashboard, and
  the raw-tier capture with zero protocol fan-out beyond the new command. (Q2.)
- **Mechanism is a new `attn dispatch handoff` CLI**, not prompt-only: it copies
  the artifact into the Notebook and emits the referencing report atomically, so
  the agent can't half-do it. (Q3.)
- **The chief stays in control.** It "may or may not choose to move" the artifact —
  unchanged from today; the Notebook is its home.

**In scope (this chunk):**

1. `attn dispatch handoff --file <artifact> --to <notebook-path> [--message <text>]
   [--coordination-file <json>] [--session <id>]` — new CLI subcommand.
2. Daemon command `handoff_dispatch`: resolve the dispatch by source session,
   `CleanPath` the destination, write the artifact via `notebook.Store`
   (create-or-overwrite so a refined re-handoff updates the same note), compose the
   report message with a reference line, record it through the existing report
   envelope path (so terminal capture/journaling still fire), broadcast
   `notebook_changed` + `dispatches_updated`.
3. Make the dispatched agent **Notebook-aware**: extend `chiefOfStaffDispatchPrompt`
   to mention the Notebook and the handoff command, and that placement comes from
   the chief/user.
4. **Chief-side guidance** (`references/chief-of-staff.md`): when delegating work
   that will yield a durable artifact, designate the destination; expect a
   reference back; curate/move it.
5. Protocol bump + generated types; CHANGELOG; tests.

**Deferred (explicitly not now):**

- A structured `notebook_artifact` field on `DispatchReport` + a dashboard artifact
  chip. (Revisit if the in-message reference proves too weak.)
- Any new Notebook folder convention / staging area (placement is chief-designated).
- Non-markdown artifacts; multi-file artifacts.

**Vision.** Advances [The output-aware chief](../vision/chief-delegation-awareness.md)
— specifically the *Output-into-context* and *Durable capture* big rocks. This is the
**large-output branch** of those rocks: when a delegated output is too big for the
distilled report, the agent stashes it in the Notebook and the report carries the
reference, so the chief's drill-in target is a durable note, not only the transcript.
It does not touch the vision's awareness/monitoring core (`dispatch watch`, Monitor,
poll fallback) — those remain future big rocks.

## Implementation map

Protocol fan-out (Critical Pattern #1): TypeSpec → `make generate-types` →
`constants.go` → bump `ProtocolVersion` (117 → 118) → `make install`.

| Layer | File | Change |
|---|---|---|
| TypeSpec | `internal/protocol/schema/main.tsp` | add `HandoffDispatchMessage` (cmd, source_session_id, to, content, report?, structured_report?); reuses `Response.chief_of_staff_dispatch` |
| Generated | `internal/protocol/generated.go`, `app/src/types/generated.ts` | regenerated |
| Constants | `internal/protocol/constants.go` | `CmdHandoffDispatch = "handoff_dispatch"`; bump `ProtocolVersion` |
| Client | `internal/client/client.go` | `HandoffDispatch(sourceSessionID, to, content, report, structured)` |
| CLI | `cmd/attn/main.go` | `case "handoff"` in `runDispatch`; `parseDispatchHandoffArgs`; help text |
| Daemon route | `internal/daemon/daemon.go` | `case protocol.CmdHandoffDispatch` |
| Daemon meta | `internal/daemon/command_meta.go` | `CmdHandoffDispatch: ScopeSession` |
| Daemon handler | `internal/daemon/chief_of_staff_dispatch.go` | `handleHandoffDispatch`: validate → CleanPath → Store.Write → compose ref → record report |
| Prompt | `internal/daemon/chief_of_staff_dispatch.go` | extend `chiefOfStaffDispatchPrompt` (Notebook-aware) |
| Guidance | `internal/agent/attn_skill/references/chief-of-staff.md` | chief designates placement; dispatched-agent handoff pattern |
| Changelog | `CHANGELOG.md` | one user-facing bullet |
| Tests | `internal/daemon/*_test.go`, `cmd/attn/main_test.go` | handler happy-path + bad-path; CLI arg parse |

### Open detail (decided)

- **Overwrite policy:** create-or-overwrite at `--to` (a refined re-handoff updates
  the same note). The destination is chief-designated, so clobbering is the chief's
  call, not a surprise.
- **`--to` required.** No default home (per the no-hardcoded-location steer). If the
  chief designated none, the agent asks (via a normal report) or reports without a
  handoff.
- **Reference line** appended server-side so it always points at the real written
  path: `Artifact written to the notebook: /<to>`.

## Progress

- [x] Plan written
- [x] Protocol + generated types (`HandoffDispatchMessage`, `CmdHandoffDispatch`, ProtocolVersion 117→118, frontend `PROTOCOL_VERSION`)
- [x] Daemon handler + routing + meta (`handleHandoffDispatch`, `composeHandoffReport`, `writeNotebookOverwrite`)
- [x] Client + CLI (`HandoffDispatch`, `dispatch handoff` subcommand + `parseDispatchHandoffArgs` + help)
- [x] Prompt + guidance + CHANGELOG (Notebook-aware dispatch prompt; chief + dispatched-agent sections)
- [x] Tests (daemon handler happy/error/overwrite/terminal/full-socket-path; CLI parse) + `make generate-types` + build + gofmt/vet + full daemon suite + frontend `tsc`
- [x] Manual verify: live round-trip on an isolated profile daemon (CLI↔daemon routing + validations confirmed)

Not yet done (awaiting Victor): commit/PR; workspace-context update at publish.
