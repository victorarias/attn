# Plan: pi headless host and rendered conversation — vertical slices

## Alignment

Implements the rendered-conversation shape of
[docs/vision/pi-attn-plugins.md](../vision/pi-attn-plugins.md): attn renders
the conversation via pi's SDK through a headless host process. Slice-based:
every slice ships a usable behavior end to end (host -> daemon -> app); no
layer is built ahead of the slice that needs it. Chatbox first, then expand.

## Grounding (receipts)

Measured in the 2026-08-04 SDK spike / source read at pi 0.80.10:

- `agent_settled` fires last on every path (success, abort, error,
  retry-drain); attn's turn = pi run (`agent_start` -> `agent_settled`); pi's
  turn events are internal steps — one abort produces a phantom second turn
  pair.
- `stopReason` `"aborted"` on abort observed live on google and openai;
  source-verified for anthropic and bedrock. `errorMessage` wording differs
  per provider — never key on message text.
- `steer()` drains at turn boundaries; `followUp()` drains only when the whole
  run would settle.
- Hard-killing the host orphans running tool subprocesses (3x reproduced) —
  cooperative cleanup only; the daemon must own the host's process group.
- No session file exists until the first assistant message; crash before that
  leaves zero disk trace.
- Session files are versioned (v3) with automatic in-place migration on open;
  entries form a parent-linked tree.
- Thinking-model streams measured ~480 `message_update` events / ~45 KB per
  ~5 s reply, bursty (observed peaks 250-1,490 events/s); WS clients buffer
  256 messages -> host coalesces deltas (~30 ms flush) before the wire.
- Transcript corpus: p50 0.15 MB, p99 11.6 MB, max 128 MB with ~0.4% message
  text -> attach snapshot is windowed with collapsed tool cards from day one;
  pi's bash tool already truncates at 2000 lines/50 KB and writes full output
  to a temp file (`fullOutputPath`).
- pi releases ~3.6x/week with labeled breaking changes every 1-2 releases plus
  unlabeled type-level growth of the event union -> exact pin; a pin bump
  means re-running the spike scenarios; never exhaustively switch on pi's
  unions.
- `bindExtensions()` is required or `session_start` (and resource discovery)
  never fires.
- Auth is fully headless; a failed OAuth refresh throws with no env fallback.

## Design decisions (cross-slice)

- New session kind beside PTY sessions, not a replacement; the attn-pi plugin
  gains a host entrypoint; the daemon spawns the host per session as
  process-group leader and group-kills on teardown (receipt above).
- Wire: one envelope `{session_id, seq, kind, body}`. Semantic kinds are
  typed in the protocol (TypeSpec, ProtocolVersion-gated) in attn's
  vocabulary; render kinds carry opaque bodies typed in a TS package shared by
  host and app; the daemon routes render bodies without parsing them. Version
  the render stream in the host handshake; skew fails explicitly.
- The host maps pi events to attn semantics with a default arm for unknown pi
  event types (log + drop or forward-opaque); exhaustiveness only on attn's
  own types.
- State is declared by the host (it is attn code); the pi-side suite remains
  for in-loop powers only.
- Exact pin `@earendil-works/pi-coding-agent` 0.80.10; the committed spike
  harness is the bump gate.

## Slices

### Slice 1 — live chatbox

Goal: type a prompt in the app, watch the reply stream, send the next prompt.

Ships:

- [ ] Host entrypoint (`createAgentSession`, sessions under attn's data dir,
      explicit model, `bindExtensions` called, no suite yet).
- [ ] Daemon spawn/kill of host as pgid leader.
- [ ] Minimal envelope with semantic kinds `session_ready`/`run_started`/
      `run_settled` and render kind `message_delta` (coalesced ~30 ms).
- [ ] Minimal React pane (message list + input, input disabled while running).
- [ ] `prompt` verb.
- [ ] Exact pin committed; spike harness committed beside the plugin as the
      pin-bump gate.

Acceptance:

- [ ] Full round trip in a running dev app.
- [ ] Killing the session leaves no orphan processes (verify with `ps`).
- [ ] A second prompt works after settle.

### Slice 2 — a real attn citizen: state and nudges

Goal: the session behaves like every other attn session and nudges become
first-class.

Ships:

- [ ] Host declares working/idle/waiting on run boundaries (states ride the
      existing `applyState` path).
- [ ] Turn integration (turn opens on settle-wanting-user, existing
      `turn_owed` derivation).
- [ ] `steer` and `follow_up` verbs on the wire, host picks steer vs new
      prompt by run state.
- [ ] `queue_update` surfaced so the UI shows queued -> seen.
- [ ] Doorbell/ticket nudges routed through these verbs (no PTY typing, no
      monitors).

Acceptance:

- [ ] Nudging a mid-run session lands at the next turn boundary and the UI
      shows the delivery.
- [ ] Nudging an idle session starts a run.
- [ ] Dashboard/turn behavior matches other harnesses.

### Slice 3 — tool visibility

Goal: see what the agent did.

Ships:

- [ ] Semantic `tool_started`/`tool_finished` (name, status, files).
- [ ] Render tool detail as collapsed cards, expand fetches detail on demand
      (`fullOutputPath` for oversized bash output).
- [ ] Edit-tool patches rendered with the existing diff viewer.

Acceptance:

- [ ] A session that reads/edits/runs shows accurate cards.
- [ ] Expanding a truncated bash output shows the full text.
- [ ] A patch renders as a diff.

### Slice 4 — dying and coming back

Goal: crashes and restarts are survivable.

Ships:

- [ ] Revive via reopening the session file through pi's `SessionManager`
      (migrations come free).
- [ ] The zero-file early-crash case falls back to fresh session +
      LaunchIntent prompt.
- [ ] Attach protocol: windowed conversation snapshot + live stream deduped by
      seq (mirrors the terminal restore contract).

Acceptance:

- [ ] `kill -9` the host mid-tool-run -> session goes recoverable -> reload
      resumes with history intact and no orphaned processes.
- [ ] Early-kill case relaunches cleanly.
- [ ] Reattaching a second client shows identical state.

### Slice 5 — history and depth

Goal: long sessions and old sessions are first-class.

Ships:

- [ ] Scroll-back paging of older window ranges.
- [ ] Resume of arbitrary existing session files.
- [ ] Model switching mid-session.
- [ ] Compaction/retry surfaced in the UI from their events.

Acceptance:

- [ ] A 100+-turn session scrolls smoothly and pages history on demand.
- [ ] Resuming an old session works.
- [ ] Switching model mid-session takes effect next run.

### Later (named, unplanned)

Skills/resource loading through `bindExtensions`, the safety envelope,
subagents, background eyes — these stay rocks in the vision doc, not slices
here.

## Non-goals

Replacing the PTY pi driver (it stays until the host path proves out in daily
use); migrating claude/codex to this pattern; image-preserving restores; a
day-long memory soak (open item, 15-turn slope measured shallow).

## Open questions

- Remote (Linux) leg: host distribution and fingerprint coherence — same
  treatment the PTY worker got, deferred until the local path works.
- Whether the suite keeps any slice-2 role once the host declares state
  directly (leaning: suite dormant until the envelope/skills rocks need
  in-loop power).
