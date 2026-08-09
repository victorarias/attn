# Plan: nisse, attn's headless conversation agent — vertical slices

## The name

The agent this plan builds is **nisse** (2026-08-09). It was called `pi-host`
while it was being built, which described the mechanism rather than the thing:
pi is the engine, and the host process, the envelopes, the delegation and the
pane are attn's. The rename landed before the epic merged to main, so `pi-host`
never reached anyone's machine and nothing carries it forward. Older sections
below are written in the vocabulary they were written in; where they say "the
host" they mean the process any conversation agent runs in, which is still what
that word means. `docs/glossary.md` holds both definitions.

## Alignment

Implements the rendered-conversation shape of
[docs/vision/pi-attn-plugins.md](../vision/pi-attn-plugins.md): attn renders
the conversation via pi's SDK through a headless host process. Slice-based:
every slice ships a usable behavior end to end (host -> daemon -> app); no
layer is built ahead of the slice that needs it. Chatbox first, then expand.

## Grounding (receipts)

Measured in the 2026-08-04 SDK spike / source read at pi 0.80.10, and
re-validated on 2026-08-05 at pi 0.83.0 (source diff of the 204 commits in
between plus a full re-run of the spike harness — every scenario passed with
unmodified scripts):

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
- Thinking-model streams measured ~480-550 `message_update` events / ~45-52 KB
  per ~5 s reply, bursty (observed peaks 250-1,970 events/s); WS clients
  buffer 256 messages -> host coalesces deltas (~30 ms flush) before the wire.
- Transcript corpus: p50 0.15 MB, p99 11.6 MB, max 128 MB with ~0.4% message
  text -> attach snapshot is windowed with collapsed tool cards from day one;
  pi's bash tool already truncates at 2000 lines/50 KB and writes full output
  to a temp file (`fullOutputPath`).
- pi releases ~3.6x/week with labeled breaking changes every 1-2 releases plus
  unlabeled type-level growth of the event union -> exact pin; a pin bump
  means re-running the spike scenarios; never exhaustively switch on pi's
  unions. The 0.80.10 -> 0.83.0 diff showed both failure modes live: the
  event union gained four types unannounced (`summarization_retry_*`,
  `bash_execution_update`) and agent-core took a labeled break (0.81.0,
  `streamFn` required) that the SDK path absorbs internally.
- `bindExtensions()` is required or `session_start` (and resource discovery)
  never fires.
- Auth is fully headless; a failed OAuth refresh throws with no env fallback
  (since 0.82.1 the error carries the provider's response; 0.83.0 adds
  `pi auth print-api-key`/`print-bearer-token` for credential export).
- New since the pin was first taken, useful to later slices:
  summarization/compaction retry lifecycle events reach SDK subscribers
  (slice 5), `bash_execution_update` streams bash output deltas (a slice 3
  render candidate), and a non-terminal `"pending"` stop reason exists on
  partial streaming messages.

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
- Exact pin `@earendil-works/pi-coding-agent` 0.83.0; the committed spike
  harness is the bump gate.

## Slices

### Slice 1 — live chatbox

Goal: type a prompt in the app, watch the reply stream, send the next prompt.

Ships:

- [x] Host entrypoint (`createAgentSession`, sessions under attn's data dir,
      explicit model, `bindExtensions` called, no suite yet).
- [x] Daemon spawn/kill of host as pgid leader.
- [x] Minimal envelope with semantic kinds `session_ready`/`run_started`/
      `run_settled` and render kind `message_delta` (coalesced ~30 ms).
- [x] Minimal React pane (message list + input, input disabled while running).
- [x] `prompt` verb.
- [x] Exact pin committed; spike harness committed beside the plugin as the
      pin-bump gate.

Acceptance:

- [x] Full round trip in a running dev app.
- [x] Killing the session leaves no orphan processes (verify with `ps`).
- [x] A second prompt works after settle.

### Slice 2 — a real attn citizen: state and nudges

Goal: the session behaves like every other attn session and nudges become
first-class.

Ships:

- [x] Host declares working/idle/waiting on run boundaries (states ride the
      existing `applyState` path). Every declaration carries the state it puts
      the session in; the daemon applies it as a `pluginReport` keyed on the
      envelope `seq`, so ordering and superseded runs need no new machinery.
      The host emits `working` and `idle`; `waiting_input` is on the wire and
      waits for slice 4 to have something to say with it — pi cannot ask a
      question outside a tool approval today, and `idle`/`waiting_input` are
      behaviorally identical to attn (both open a turn, both accept nudges), so
      classifying between them would buy nothing.
- [x] Turn integration (turn opens on settle-wanting-user, existing
      `turn_owed` derivation).
- [x] `steer` and `follow_up` verbs on the wire, host picks steer vs new
      prompt by run state.
- [x] `queue_update` surfaced so the UI shows queued -> seen.
- [x] Doorbell/ticket nudges routed through these verbs (no PTY typing, no
      monitors).

Acceptance:

- [x] Nudging a mid-run session lands at the next turn boundary and the UI
      shows the delivery.
- [x] Nudging an idle session starts a run.
- [x] Dashboard/turn behavior matches other harnesses.

All three are the packaged-app scenario `nisse-nudge`
(`app/scripts/real-app-harness/scenario-nisse-nudge.mjs`). It holds a real
agent inside a `sleep 25` so the steer is provably queued rather than racing the
reply, then asserts the strip, the delivered message, the reply that obeyed the
steer instead of the original instruction, and `working` → `idle` with a turn
owed read from the daemon.

### Slice 3 — tool visibility

Goal: see what the agent did.

Ships:

- [x] Semantic `tool_started`/`tool_finished` (name, status, files). They are
      semantic but **not** state declarations: `applyState` restamps
      `state_since` on every apply, so a session that ran twenty tools would
      report having been working only since the last one.
- [x] Render tool detail as collapsed cards, expand fetches detail on demand
      (`fullOutputPath` for oversized bash output). The `tool_detail` verb is
      addressed by `call_id` and its answer is broadcast, so two clients showing
      the same card cost one read and nothing can time out.
- [x] Edit-tool patches rendered with the existing diff viewer — `PatchDiff`
      from `@pierre/diffs`, fed pi's unified patch directly rather than
      reconstructing before/after text the patch does not carry.
- [x] pi's `toolResult` messages are dropped from the transcript. Found in live
      verification: pi hands each tool's whole output back to the model as a
      message of its own, and the pane was drawing it — 23,893 bytes for one
      `seq 1 5000` — which is exactly the ballooning the collapsed card exists
      to prevent. A message now appears only when it has text of its own, which
      also retires the empty bubble a tool-only assistant turn used to leave.
- [x] Cancelling a queued steer or follow-up (`clear_queue` → pi's
      `clearQueue()`), carried over from slice 2. All-or-nothing, because pi
      offers no per-entry removal; the strip empties on pi's answering
      `queue_update`, never on the click.

Acceptance:

- [x] A session that reads/edits/runs shows accurate cards.
- [x] Expanding a truncated bash output shows the full text.
- [x] A patch renders as a diff.
- [x] A queued steer can be cancelled and the queue strip reflects it.

All four are the packaged-app scenario `nisse-tools`
(`app/scripts/real-app-harness/scenario-nisse-tools.mjs`), run against a real
agent on a throwaway profile. It also asserts the negative that makes the cards
worth having: after 5,000 lines of tool output, no message in the transcript
carries it. The measured transcript for a session that read, printed 5,000
lines, edited and slept is 406 characters.

### Slice 4 — dying and coming back

Goal: crashes and restarts are survivable.

Ships:

- [x] Revive via reopening the session file through pi's `SessionManager`
      (migrations come free). `SessionManager.continueRecent(cwd, sessionDir)`
      is the whole of it: it continues the most recent session file under the
      dir and creates one only when there is none. The daemon side is the shape
      #651 already built for PTY sessions — a host exit applies `recoverable`,
      `reload_session` is the only entry, and the stored `LaunchIntent` is what
      the replacement is spawned from. No parallel mechanism.
- [x] The zero-file early-crash case falls back to a fresh session — the same
      `continueRecent` call, which is why it needed no code of its own.
      **Deviation, since closed:** the "+ LaunchIntent prompt" half was moot
      when this slice landed — the `nisse` driver registered no
      `initial_prompt` capability, so the spawn pipeline refused a nisse
      launch that carried one and there was no stored prompt to re-send. It was
      deferred to the work that needed it, delegation to a conversation agent,
      and shipped there: the driver declares the capability, the prompt travels
      to the host in `ATTN_NISSE_INITIAL_PROMPT`, and the host says it exactly
      when its reopened history is empty (`launchPromptIsUndelivered`). The
      daemon stores it in `LaunchIntent.InitialPrompt` for conversation agents
      only, so this fallback now relaunches the same task rather than an agent
      with nothing to do. A PTY agent still stores none: its relaunch resumes a
      transcript, so replaying the brief would re-run finished work.
- [x] Attach protocol: windowed conversation snapshot + live stream deduped by
      seq (mirrors the terminal restore contract). `agent_attach` asks;
      `conversation_snapshot` answers on the envelope stream, broadcast and
      replacing. The window is `SNAPSHOT_ITEM_LIMIT` (500 items) /
      `SNAPSHOT_BYTES_LIMIT` (1 MB), with the receipts in their comments.

Two decisions this slice made, both stated where the code is:

- **`waiting_input` is activated here.** A revived conversation whose reopened
  history does not end with an assistant message declares `waiting_input`;
  everything else declares `idle`. `idle` and `waiting_input` both open a turn
  and both accept a nudge, so this never changes what attn does — it changes
  what attn tells the user about why the session went quiet, and it is decidable
  from the file alone.
- **A new host resets the client's seq spine.** A replacement mints envelopes
  from 1 again, so `session_ready` is exempt from the app's dedup guard and
  resets `lastSeq`; the `conversation_snapshot` that follows refills the
  transcript. Without the exemption every envelope of a revived session reads as
  a duplicate of the dead host's and is dropped.

Acceptance:

- [x] `kill -9` the host mid-tool-run -> session goes recoverable -> reload
      resumes with history intact and no orphaned processes. **Partial on the
      last clause, by physics:** the dead host's process group is empty and the
      durable spawn record names the live replacement, but the tool subprocess
      the kill interrupted survives. SIGKILL skips the cooperative teardown pi
      needs to stop it, and pi detaches each tool child into its own process
      group, so the group kill cannot reach it either — the slice-1 receipt,
      neither improved nor worsened by revive, and unfixable from the daemon
      side because nothing records the child's pid. The scenario records what it
      found (`stranded-tool-children.json`) instead of asserting it away, and
      reaps it before exiting. Slice-5 input: recording tool-child pids as
      `tool_started` arrives would let procreap reach them.
- [x] Early-kill case relaunches cleanly.
- [x] Reattaching a second client shows identical state.

All three are the packaged-app scenario `nisse-revive`
(`app/scripts/real-app-harness/scenario-nisse-revive.mjs`), run against a real
agent on a throwaway profile. The load-bearing assertion is not that the pane
redraws — it is that the revived *agent* answers a question only the reopened
session file can answer: the pre-crash exchange plants a word, and the revived
session is asked for it. The second client is the app restarted mid-scenario,
which has never seen the host's stream, so everything it draws came from the
snapshot alone; it drew the same five messages and the same tool card, including
the bash call the kill interrupted, marked errored.

Left for slice 5, deliberately:

- Paging past the snapshot window. A conversation longer than the window loses
  its oldest items on a snapshot, and because the snapshot is a broadcast
  replace, one client attaching can shorten what every other client is showing.
  Accepted here; it is exactly what scroll-back paging retires.
- Resuming an arbitrary old session file. Revive reopens the most recent file
  under the session's own dir and nothing else.
- Reaching a tool subprocess a hard kill stranded. `tool_started` carries no pid,
  so procreap has nothing to record; giving it one is the fix, and it belongs
  with whichever slice wants tool cancellation anyway.

### Slice 4b — delegation to a conversation agent

Goal: `attn delegate --agent nisse` works, which is the first time a
conversation session is asked to do a job rather than hold a chat.

Ships:

- [x] The `initial_prompt` capability on the `nisse` driver — delegation
      refuses any agent that cannot be launched with a brief — and the closing
      of slice 4's deviation above. The brief travels in the environment, not
      argv: a brief is multi-line prose, and argv is world-readable text a
      sibling's `pkill -f` can match on.
- [x] The host decides delivery, because it is the only party that can. It
      reopens its own history at startup and says the brief exactly when that
      history is empty, so the same stored prompt handed to every replacement
      host is spoken once.

Two findings this slice made, both observed rather than reasoned:

- **A host was carrying no session identity, and worse, someone else's.** The
  environment chain daemon → host → pi → tool subprocess was assumed to carry
  it, since environment inherits by default. It does — but nothing was putting
  it there. `spawnHostSession` set the `ATTN_NISSE_*` block and stopped; the
  PTY path's identity block (`buildSpawnEnv`) had no counterpart. Live, the
  agent's bash tool printed an empty `ATTN_SESSION_ID`. Under test, where the
  daemon itself was launched from inside an attn session, the host inherited
  *that* session's id and agent — so a delegated agent's `attn ticket comment`
  would have reported as whichever session the daemon got its environment from.
  Fixed by giving the host the same identity block the PTY path builds.
- **`attn` on a host's PATH was the wrong install.** Bare `attn` resolved off
  the login shell's PATH to `~/Applications/attn.app` — production — from a
  session running on a throwaway profile. `launchenv.WithActiveAttnFirst`, which
  the PTY path already applied, is the fix; the two runtimes owe an agent the
  same environment.

Skills reach a delegated pi agent with no delivery mechanism of attn's: pi
inlines `AGENTS.md`/`CLAUDE.md` from the worktree and its ancestors into the
system prompt, and it scans `~/.agents/skills/` unconditionally — which is
exactly where attn already installs its own skill. Noted as a dependency rather
than a design: that install happens on the codex path, so the attn skill is
present for pi as a side effect of codex being configured.

Acceptance is the packaged-app scenario `nisse-delegate`
(`app/scripts/real-app-harness/scenario-nisse-delegate.mjs`).

### Slice 5 — history and depth

Goal: long sessions and old sessions are first-class.

Ships:

- [x] Scroll-back paging of older window ranges. The `history` verb names the
      oldest item a client holds; `conversation_page` answers with what precedes
      it. Behind the window the host keeps an archive bounded by
      `TRANSCRIPT_RETENTION_ITEMS` / `TRANSCRIPT_RETENTION_BYTES` — a separate
      budget from the window on purpose.
- [x] Resume of arbitrary existing session files. The picker lists the
      conversations under `<data-dir>/hosts/state` and a chosen one rides into
      `LaunchIntent.ResumeConversationFile`; the host forks it
      (`SessionManager.forkFrom`) into the new session's own dir.
- [x] Model switching mid-session (`set_model` / `model_changed`), with the
      daemon rewriting the launch intent so a revive keeps the choice.
- [x] Compaction/retry surfaced in the UI as `notice` rows, from the host's own
      events and from reconstruction.

Three decisions this slice made:

- **The epoch, and why the truncation edge needed one.** A snapshot is a
  broadcast replace, so on a conversation longer than the window, one client
  attaching used to shorten what every other client was showing. The snapshot
  now names the host process that built it: a same-epoch snapshot is spliced
  onto scroll-back the client already paged in, a different epoch replaces. It
  is the transcript's version of the seq-spine reset a new host already forced.
- **A page is addressed by item, not by offset.** `conversation_page` is
  broadcast like everything else, and a client takes one only when the epoch
  matches and the anchor is its own oldest item. An offset would have been
  correct only for the window that asked.
- **Resume forks, and only into an empty session dir.** The named conversation
  is copied into this session's own storage, so the session it came from is
  never written to and a revive of the resuming session never rewinds to the
  source. The order matters: revive (a non-empty dir) never consults the resume
  file at all.
- **`model_changed` is deliberately not a state declaration.** `applyState`
  restamps `state_since` on every apply, so routing a picker change through it
  would reset "working for 4m". The daemon reads the envelope directly and
  rewrites `LaunchIntent.Model`; a refusal comes back as the model still in
  force, and blanking the pin on that would launch the next revive on a default
  nobody chose.

Acceptance:

- [x] A 100+-turn session scrolls smoothly and pages history on demand. 1,200
      items (600 turns) resumed; the pane drew 500 and the page arrived in
      32 ms, taking it to 1,000 with no duplicates and no change to the newest
      item.
- [x] Resuming an old session works. A new session forked an existing
      conversation file, drew its six messages, answered a question only that
      conversation could answer, and wrote to a file of its own — the source was
      not touched.
- [x] Switching model mid-session takes effect next run.
      `openai/gpt-5.6-luna` -> `openai/gpt-4.1-mini` out of 62 offered, and the
      next prompt ran on it.

All three are the packaged-app scenario `nisse-history`
(`app/scripts/real-app-harness/scenario-nisse-history.mjs`), run against a real
agent on a throwaway profile. The long transcript is a synthesized pi session
file read through pi's own `SessionManager` rather than a thousand live turns —
proving the window needs more items than the window holds, and the cost of that
proof should be one resume. The scenario also holds the multi-client case
directly: a second WebSocket client attaches while the app pane is scrolled
back, and the transcript stays at 1,000 rather than snapping back to 500.

Two defects this run found, neither of which any unit test would have:

- **pi writes the model a session opened on into the file before anything is
  said.** Reconstruction drew that as "Model switched to X" on conversations
  that had never switched, and because the row is not an assistant message,
  every new session also came back declaring `waiting_input`.
- **A provider that refuses a turn reached nothing.** pi does not raise: it
  persists the assistant message with `stopReason: "error"` and the provider's
  own words. The run settled, the composer reopened, and the agent looked like
  it had chosen silence. It now draws an error row carrying the provider's
  sentence, live and after a reopen. The scenario proves it the honest way — a
  model pi's catalog offered had been retired upstream, and the row is what
  turned a 180-second timeout into a two-second diagnosis.

Deliberately left behind:

- **No CLI surface.** There is no CLI spawn path for conversation sessions at
  all today, so resume-from-file, paging and model switching have nothing to
  hang off. When one lands, `--resume` and `--model` are the shape.
- **Reaching a tool subprocess a hard kill stranded** (slice 4's handoff).
  `tool_started` still carries no pid.
- **A resumed session that also carries a launch brief and dies inside its
  first turn.** Delivery is decided from the reopened conversation, and a fork
  writes its file before the first turn runs, so the relaunch cannot tell
  history it was told from history it inherited and stays silent. The two
  halves — resume from the picker, briefs from delegation — do not meet on any
  product path today. Closing it means a delivered-marker in the session dir,
  which is the right shape whenever delegation grows a resume-from.
- **Remote/Linux host distribution**, per the epic's non-goals.

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
