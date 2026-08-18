# attn-pi plugin guide

attn-side driver plugin for [pi](https://github.com/earendil-works/pi). Canonical
vision: `docs/vision/pi-attn-plugins.md`. Full grounding evidence with citations:
`docs/grounding/pi-plugins.md`. This is attn's only driver plugin, so the
driver contract itself lives in `internal/daemon/plugin_driver.go`.

## Pinning pi

- pi is 0.x rolling-release (~1-2 releases/week), with real breaking changes
  every few releases, documented only as prose in each package's
  `CHANGELOG.md` under "### Breaking Changes".
- pi has NO extension/version compat gate: an extension built against old
  types loads silently and fails at the first missing call site. Pin the exact
  pi version; treat upgrades as deliberate, changelog-gated events; the
  pi-side suite must self-check the pi version at `session_start` rather than
  assume compatibility.

## pi lifecycle invariants (verified against pi source at v0.80.x)

- One pi process = one live session; resume/fork/new replace the session
  in-process, never re-exec.
- Extension factories re-run on EVERY session transition (resume/fork/new/
  reload); extension contexts from before a transition throw on any use. Only
  module-scope variables survive transitions (and only until cwd changes or
  process exit). A persistent socket must live at module scope; the factory
  re-links, it does not re-dial.
- `session_shutdown` fires on clean quit and SIGTERM/SIGHUP, but NOT on
  uncaught exceptions or SIGKILL. Never treat a missing goodbye as
  meaningful: the PTY child exit that the driver observes is the
  authoritative liveness signal; declared state rides on top.
- Session identity: resume keeps the same session id + JSONL file; fork mints
  a new id with a `parentSession` header link; a plain `/new` has NO lineage
  link. Correlation with the attn session must be re-declared on every
  `session_start`, not inferred from files.
- The driver mints native session identity at spawn with
  `pi --session-id <id>` (creates if missing); resume uses the same flag.
  Never parse pi's session picker.

## pi TUI under attn's PTY (verified against pi source)

- pi never uses the alternate screen, never sends CPR, and queries only OSC
  11 (background color, once, for light/dark detection) — the worker answers
  it from the daemon-pushed theme.
- pi negotiates the Kitty keyboard protocol with `ESC[>7u ESC[?u ESC[c`;
  attn's worker answers the trailing DA1, so pi deterministically falls back
  to modifyOtherKeys. Degraded-but-correct; do not "fix" this by answering
  kitty queries.
- pi full-redraws on any resize under DEC 2026 synchronized output and
  self-fires SIGWINCH at startup. Resize races are expected to self-heal;
  live-verify before assuming otherwise.
- Not yet live-verified: resize races under a real attn PTY, and Kitty
  graphics images surviving vt10x snapshot/replay.

## Driver pattern

- The shape the daemon expects: `attn-plugin.toml` (`attn_api_version`
  gate), `driver.register` with capability map, `driver.spawn`/`driver.resume`
  returning argv+env+cwd that the daemon runs in the attn-owned PTY,
  `session.report_metadata` as the resume token, `driver.session_closed`
  cleanup.
- The driver also owns pi citizenship (rock 2): it listens on a relay unix
  socket, injects `-e <bundled suite.js>` plus `ATTN_PI_SUITE_SOCKET` /
  `ATTN_PI_TOKEN` env at spawn/resume, registers `state_reporting` +
  `message_delivery`, relays suite reports to the daemon (the per-run seq
  cursor lives driver-side), and forwards `driver.deliver_message` to the
  suite. Stop verdicts come from daemon-side classification
  (`attn.classify_stop`); an empty settle (nothing said) reports `idle`
  without classifying. Never add screen-scraping state detectors for pi, and
  never fall back to PTY typing for message delivery.

## nisse, the headless agent

This plugin registers two agents. `pi` launches the pi TUI in an attn PTY (the
driver pattern above). `nisse` is a **conversation session**: pi runs headless
through its SDK in `host/index.ts`, a process attn's daemon spawns and owns.
See `docs/glossary.md` for the vocabulary and
`docs/plans/2026-08-05-pi-headless-host.md` for the slices.

The name is attn's, and the line it draws is worth keeping straight: **pi is the
engine, nisse is the agent, and a host is the process any conversation agent
runs in.** Everything under `internal/daemon` that says "host" is agent-agnostic
machinery — it would run a second conversation agent unchanged. Everything in
this plugin that says nisse is this agent in particular. Renaming one is not
renaming the other.

- Three channels, and they must stay separate. **fd 3** is the envelope stream
  out (NDJSON), **stdin** is verbs in (`prompt`, `steer`, `follow_up`,
  `tool_detail`, `clear_queue`, `snapshot`, `history`, `set_model`,
  `shutdown`), **stdout/stderr** are pi's and the
  host's own output, captured to `<data-dir>/hosts/log/<session>.log`. Envelopes
  never go on stdout: pi loads the user's own extensions and any one of them
  printing a line would corrupt a shared stream.
- Two envelope families. **Semantic** kinds (`session_ready`, `run_started`,
  `run_settled`, `tool_started`, `tool_finished`) are attn's vocabulary and the
  daemon may read them. **Renderings** (`message_start`, `message_delta`,
  `message_end`, `queue_update`, `tool_detail`, `conversation_snapshot`) are drawn
  by the app and forwarded opaquely — as are `conversation_page`, `notice` and
  `model_changed`. Adding a rendering is a host + app change; a new semantic kind
  is a protocol conversation. `model_changed` is the one rendering the daemon
  also reads, and only to rewrite the launch intent.
- **A state declaration is the subset of semantic kinds that carries a `state`**
  (`STATE_DECLARATION_KINDS`: `session_ready`, `run_started`, `run_settled`) —
  the attn state the session is in once that declaration is true (`idle`,
  `working`, and later `waiting_input`/`pending_approval`). The daemon applies it
  through the normal `applyState` path with the envelope's `seq` as the ordering
  cursor, so it cannot be invented daemon-side from the kind alone. One of those
  kinds arriving without a state is logged and dropped, never guessed at. Tool
  boundaries are deliberately NOT state declarations: `applyState` restamps
  `state_since` on every apply, so a session that ran twenty tools would show as
  having been working for however long the last one took.
- **Tool visibility is a declaration plus a fetch.** `tool_started` /
  `tool_finished` carry only what the transcript keeps forever — the tool's name,
  a one-line summary, the files it names, how it ended. What it read, wrote or
  printed stays in the host until a card is opened and the `tool_detail` verb
  asks for it. The receipt is the attach-snapshot corpus (claude JSONL, p50
  0.15 MB / p99 11.6 MB, ~0.4% message text): eagerly inlining tool output is how
  a transcript balloons. The held detail is bounded by `ToolDetailStore`, which
  names its own budget when it has evicted what you asked for.
- **A message appears when it has something to say.** Nothing is emitted on pi's
  `message_start`: the first text delta mints the message, and one that streamed
  nothing is decided at its end. pi opens a message per assistant turn whether or
  not that turn says anything (a tool-only turn ends empty), and it hands each
  tool's whole output back to the model as a `toolResult` message —
  `UNRENDERED_ROLES`, dropped, because the card is that call's record and drawing
  the message too inlines every byte the card exists to fetch on demand.
- `tool_detail` is addressed by `call_id`, not by a request id, and is broadcast
  to every client. Two windows showing the same card cost one read, a `full`
  answer upgrades both, and nothing can time out.
- `tool_execution_update` is dropped on purpose — a live-updating card would
  repaint continuously beside GPU terminals, and the delta stream is what the
  coalescer exists to bound. It is handled explicitly so it does not log as an
  unmapped pi event.
- Delivery is three verbs, and the host picks nothing: the daemon says which.
  `steer` drains at pi's next turn boundary, `follow_up` drains only when the
  run would otherwise settle, `prompt` opens a run and is refused mid-run. A
  steer or follow-up at an idle session is resolved into a run rather than
  dropped — a doorbell must never vanish for arriving at the wrong moment.
- `queue_update` is pi's own queue state, forwarded verbatim. pi emits it on
  enqueue and again immediately before the user message that delivers the entry,
  which is exactly "queued, then seen" — so the app never invents an optimistic
  entry of its own. `clear_queue` is the way out, and it is all-or-nothing
  because `session.clearQueue()` is: pi offers no per-entry removal, and clearing
  then re-queueing the survivors would race the drain. The strip empties on pi's
  answering `queue_update`, never on the click.
- **The host reopens, it never starts over.** `SessionManager.continueRecent`
  continues the most recent session file under the session dir and creates one
  only when there is none — which is both the revive path and the zero-file
  early-crash fallback, and it takes pi's session-format migrations for free.
  An **empty** session dir is the only thing that consults
  `ATTN_NISSE_RESUME_FILE`, and it FORKS (`SessionManager.forkFrom`) rather
  than appending: the named conversation is copied into this session's own dir,
  so the session it was picked up from is never written to and a revive of the
  resuming session never rewinds to the source. Two sessions on one file is the
  failure this ordering exists to make impossible.
  `buildContextEntries()` (compaction-aware) is what the transcript is rebuilt
  from. A relaunch is a NEW host, so its `seq` starts at 1 again: `session_ready`
  is the only envelope that resets a client's spine, and the app must exempt it
  from its own dedup or drop every envelope of the revived session.
- **The host holds a transcript, and it is fed the host's own envelopes.** Not
  pi's events, and not the session file: pi persists a message when it ends, so a
  snapshot rebuilt from disk stops one paragraph short of what a client watching
  the stream already has. `TranscriptStore` is the same reducer the app runs,
  consuming identical input, which is what keeps the two from drifting.
  `conversation_snapshot` is the rendering that carries it, in answer to the
  `snapshot` verb, and it is BROADCAST and REPLACES — the host is the authority,
  the same bargain the terminal's VT dump makes. It is windowed
  (`SNAPSHOT_ITEM_LIMIT`, `SNAPSHOT_BYTES_LIMIT`).
- **Two budgets, and confusing them is the bug.** `SNAPSHOT_*_LIMIT` bounds what
  one message CARRIES; `TRANSCRIPT_RETENTION_*` bounds what the host HOLDS. They
  are different numbers on purpose: a client asking for the page before the one
  it is showing must not be told the history is gone when the host is only
  refusing to send it all at once. Retention is a tripwire — a conversation that
  reaches it has been talking for weeks — and every settle logs what was held,
  which is where the next remeasurement comes from.
  `ATTN_NISSE_RETENTION_ITEMS` and `ATTN_NISSE_RETENTION_BYTES` lower them
  (read from the daemon's environment at spawn, inherited by the host): the
  tripwire is set past the longest conversation anyone here has ever had, so
  lowering it is the only way to watch a host actually drop history. A value
  that is not a positive whole number is logged and treated as absent — meaning
  the default, never zero, because a typo in a diagnostic variable must not
  quietly reduce a conversation to one item.
- **What retention drops, the snapshot admits to.** `dropped` counts the items
  gone for good, and it is a different question from `truncated` ("this message
  does not carry everything", which a client pages away) and from `has_more`
  ("ask me again"). Once nothing is left to page and `dropped` is non-zero, the
  app draws a row saying so: a transcript that silently begins mid-thought is
  the failure the compaction row already exists to prevent, and a budget nobody
  can see is worse than no budget. A rebuilt host reports its own count under a
  new epoch, so a revive that reads the whole session file back clears the row
  rather than inheriting a loss it does not have.
- **Retention never evicts a message that is still being written.** A streaming
  message is normally the newest item and safe, but it stops being newest the
  moment pi opens a tool beneath it. Evicting it there does not merely lose it:
  the next delta finds no open message, mints a fresh one from the tail alone,
  and the agent's paragraph reappears truncated and out of order, below the tool
  it was written above. Holding it costs one item over budget until it ends,
  which is the bargain the newest item already gets.
- **`epoch` is the transcript's seq spine.** A snapshot names the host process
  that built it. A replacement host rebuilds the transcript from pi's file and
  mints its own item ids, so nothing it sends can be spliced onto the dead
  host's items — same epoch merges, a new epoch replaces. Without it, the
  broadcast-replace bargain means one client attaching to a long conversation
  shortens what every other client is showing.
- **Scroll-back is asked for by item, not by offset.** The `history` verb names
  the oldest item a client is holding; `conversation_page` answers with what
  precedes it, and is BROADCAST like everything else. A client takes a page only
  when the epoch matches AND the anchor is its own oldest item — a window
  scrolled somewhere else would otherwise splice a page into the middle of its
  transcript and leave a hole.
- **A notice is a row that explains a silence.** Compaction and retry reach the
  app as `notice` (`id`, `level`, `text`, `done`), keyed so the row that says
  "compacting" becomes the row that says "compacted" in place rather than
  stacking. `done` is what separates one still happening from one that settled.
  Reconstruction mints them too: pi's compaction entry becomes the honest top of
  a revived transcript, because everything above it is a summary.
- **A refused turn is a notice, because pi does not raise.** A provider error
  arrives as an assistant message with `stopReason: "error"` and the provider's
  response in `errorMessage` — empty content, run settles, composer reopens.
  Without a row the agent looks like it chose not to answer. `messageFailure`
  digs the sentence out of the nested JSON envelopes providers wrap it in, and
  reconstruction mints the same row so a reopened conversation still explains
  itself. Measured 2026-08-09: a model pi's catalog offered had been retired by
  its provider, and the 404 reached nothing.
- **The model a session opened on is not a switch.** pi writes a `model_change`
  and a `thinking_level_change` into every session file before anything is said.
  Reconstruction ignores a `model_change` that precedes the first message —
  otherwise every new conversation claims to have switched models, and (since
  the row is not an assistant message) declares `waiting_input` on arrival.
  Notices are transparent to `conversationInterrupted` for the same reason: a
  switch is something that happened TO the conversation, not the agent losing
  its turn.
- **A model switch is not a state declaration.** `set_model` asks, `model_changed`
  answers with the model actually in force — a refusal comes back as the model
  that is still running, which is why the picker moves on the host's answer and
  never on the click. The daemon rewrites `LaunchIntent.Model` from it, so a
  revive relaunches on the model the user chose rather than the one the spawn
  pinned; it is deliberately kept out of `STATE_DECLARATION_KINDS` because
  `applyState` restamps `state_since` and a picker change must not reset "working
  for 4m".
- **`session_ready` says why the session went quiet.** A reopened conversation
  whose last item is not an assistant message declares `waiting_input`;
  everything else declares `idle`. Both open a turn and both take a nudge, so
  this is never a behavior choice — it is the honest answer to "what happened
  here", decidable from the file alone.
- `seq` is one monotonic spine across both families, minted only by
  `EnvelopeStream`.
- The mapper never claims exhaustiveness over pi's event union — pi added four
  event types between 0.80.10 and 0.83.0 with no changelog entry. Unknown types
  are logged once each and dropped.
- Deltas are coalesced on a 30 ms window (receipt in `DeltaCoalescer`'s comment:
  pi bursts to 1,970 events/s and attn's WebSocket clients buffer 256 messages).
  Flush before any `message_end` or declaration, so nothing overtakes the text
  it follows.
- **Every prompt settles.** `session.prompt` can reject before pi opens a run at
  all — an unauthenticated provider is the common one — so the catch emits
  `run_settled` with the reason. Without it the app's composer stays shut
  forever waiting for a reply nobody is writing. The app closes that composer at
  send time rather than on `run_started` — the acknowledgement is a round trip
  away and the host refuses a second prompt mid-run with only a log line — so
  the daemon settles a prompt that reached no host at all for the same reason.
- `bindExtensions` is required. Without it `session_start` never fires and
  resource discovery never runs, so extensions silently do nothing.
- The compiled binary cannot read pi's `VERSION`: pi reads its own package.json
  off disk at runtime and a `bun build --compile` binary has no copy, so
  `VERSION` degrades to `"0.0.0"`. The pin is inlined from this plugin's exact
  dependency entry instead, and a disagreement with a runtime-readable VERSION
  is spawn-fatal.
- Session storage is attn's (`<data-dir>/hosts/state/<session>`), never
  `~/.pi/agent/sessions`. Auth and resource discovery still resolve against the
  real `~/.pi/agent`, exactly as a bare `pi` invocation does.
- The daemon spawns the host as a **process-group leader** and gives it SIGTERM
  before killing the group. The SIGTERM is the load-bearing half: pi spawns each
  tool subprocess into its own process group (measured 2026-08-05), so only pi's
  own dispose — which the host runs on SIGTERM — tears the tools down. A hard
  kill orphans them (receipt: 3x reproduced, 2026-08-04 spike), and so would a
  host wedged past the grace window. Never "simplify" the teardown to a group
  kill.
- A host gets the same environment a PTY agent gets — the daemon's own plus the
  user's login shell — because that is where an agent's credentials live. It
  gets the PTY path's **identity block** too (`ATTN_SESSION_ID`, `ATTN_AGENT`,
  `ATTN_DAEMON_MANAGED`, `ATTN_INSIDE_APP`, and the active attn first on PATH),
  and that half is not cosmetic: the agent's tools shell out to `attn` to report,
  so without it a delegated agent's `attn ticket comment` is attributed to
  whichever session the daemon inherited its own environment from, and bare
  `attn` resolves to whichever install is on the login shell's PATH rather than
  to the profile that spawned the session. Both were observed, not theorized.
- **`ATTN_NISSE_INITIAL_PROMPT`** carries the launch's own first user message
  when it had one — today that is a delegation brief. It is
  in the environment rather than argv because a brief is multi-line prose and
  argv is world-readable text a sibling's `pkill -f` can match on. The daemon
  hands the same prompt to every replacement host, so delivery is the host's
  decision, not the daemon's: it says it exactly when nothing in the reopened
  conversation was SPOKEN (`launchPromptIsUndelivered`) — true for a first
  launch, and for a relaunch after a crash so early that pi wrote no session
  file. Only messages count. Notices do not, because pi writes one into every
  session file before the first word is said and a brief swallowed by a row
  nobody spoke is a delegation that sits there with nothing to do. A history
  that was FORKED in does not count either: it belongs to the conversation this
  session was picked up from, and this session has still never been told what it
  is for. The gap that remains is a session that forks AND carries a brief and
  then dies inside its first turn — the fork already wrote the file, so the
  relaunch cannot tell delivered from inherited and stays silent. Closing it
  needs a delivered-marker in the session dir; no product path reaches the
  combination today, because resume comes from the picker and briefs come from
  delegation.

`spike-harness/` drives pi's SDK without attn and is the gate a pi version bump
has to pass; see its README.

## Suite invariants

- The suite (`plugins/attn-pi/suite/`, staged as a single `suite.js` next to
  the driver executable) runs inside pi's runtime. It must never crash or
  block pi: relay sends are fire-and-forget, failures are swallowed, and
  missing `ATTN_PI_SUITE_SOCKET`/`ATTN_PI_TOKEN` env turns the whole suite
  into a no-op (bare pi outside attn).
- The relay client lives at module scope and survives session transitions;
  every factory re-run re-binds the current pi/ctx (stale ones throw on any
  use — `driver.deliver_message` answers `delivered: false` then).
- `agent_end` caches the last assistant message's text; `agent_settled` has
  no payload and ships the cache as `suite.report_stop`.
- `suite/index.ts` imports pi's `VERSION` from
  `@earendil-works/pi-coding-agent`; pi resolves it as a virtual module at
  load time, so the bundle step must keep that import `--external`.
- PTY child exit stays the authoritative liveness signal; suite silence is
  never meaningful.

## Auto mode

`automode/` is pi's permission system: a static safety envelope plus a
classifier for everything that reaches past it, denied conversationally
rather than through dialogs. Design and slices:
[docs/plans/2026-08-16-pi-auto-mode.md](../../docs/plans/2026-08-16-pi-auto-mode.md).

- The decision order in `policy.ts` IS the policy. Anything added to the
  envelope runs unjudged, so the read-only sets are conservative by
  construction: a command that can run another command, or reach the
  network, is not in them.
- Like `suite/`, the module is duck-typed against pi's shapes rather than
  importing pi, so `bun test` covers the whole extension including its
  `tool_call` wiring. `index.ts` is the only file that knows pi's event
  names.
- Fail-safe both ways: a handler that throws blocks the tool, and a call
  auto mode cannot judge is refused, never run. Model output that does not
  read as a verdict is one of those refusals.
- The seam for a nested completion is
  `registry.getProvider(id).streamSimple(model, context, options)`, with the
  request auth assembled the way `ModelRuntime.prepareRequest` assembles it —
  the runtime itself is not on the extension context, but every piece it uses
  is. It exists in 0.83.0, so this needed no pin bump.
  `ModelRegistry.complete()` (added 0.84.2) is the flatter call and the wrong
  one: it is the RAW api path, so it skips the thinking-level clamp and the
  per-API request options, and the model thinks unbounded — 354 output tokens
  and 5.7 s against 60 tokens and 2.9 s on glm-5.3 with the same prompt
  (2026-08-17).
- Each layer names an ordered list of models (`classifier_models`,
  `escalation_models`; the singular spellings load as a one-entry list). The
  list is walked ONLY when a model cannot be reached — a thrown request,
  `stopReason: "error"` — and each entry gets one immediate retry first. A
  model that answers ends the walk whatever it answered, including a deny and
  including output that does not parse: advancing on a verdict would be
  shopping for a different one. An abort ends the walk instead of advancing it.
  When a layer's list is exhausted the call is still blocked, but under the
  rule `classifier-unavailable` and with a reason naming the layer, the models
  tried and the last failure — nothing judged the call, and the user reading
  the block needs to know that. An unavailable deny is never cached: the cache
  holds verdicts, and there was none.
- Escalation is scoped to allow verdicts. A confident deny is final: the user
  overturns one by saying so, and a second opinion buys them only the wait.
  Letting denials escalate doubled the cost of the corpus before it was fixed.
- What a classification spends is held and folded into the next tool result's
  usage. A blocked call never reaches pi's result hook, so there is nothing to
  attach it to at the time — the session total is right, per-call attribution
  is not, and a session whose last act is a denial never reports that one.
- attn's config reaches a pi session through the launch: the daemon hands the
  promoted config to a driver that advertises the `auto_mode` capability, and
  the driver forwards it as `ATTN_PI_AUTOMODE_CONFIG` (JSON, the exact shape
  `automode/config.ts` parses). Environment rather than argv because prose
  entries are multi-line and argv is world-readable. A config change reaches
  the next session; a live one is not refreshed.
- Two entrypoints, one module. `suite/index.ts` composes auto mode in when
  `ATTN_PI_AUTOMODE_CONFIG` is set — attn's launch injection — and builds
  nothing at all when it is not, so a bare pi loading `suite.js` registers no
  command, no flag and no handler for it. `automode/standalone.ts` is the same
  extension for `pi -e automode.js`, reading the same JSON from
  `attn-automode.json` under pi's config dir (`PI_CODING_AGENT_DIR`, or
  `~/.pi/agent`). Both are staged by `build-bundled-plugins.sh`.
- The `AutoMode` in `mode.ts` lives at module scope and survives session
  transitions; the session state the factory owns does not. That is the line:
  the user's `/auto` choice is theirs until they change it, while the verdict
  cache and the breaker belong to one session.
- Precedence is `/auto` > `--auto`/`--no-auto` > `enabled_default`. The flags
  carry no default so an unset one reads as undefined, and the plan's
  "flag > /auto" holds at launch, when there is no `/auto` yet — after one, the
  command the user typed wins, because a command that loses to a flag is not a
  command.
- The classifier is built from `ctx.modelRegistry`, captured at
  `session_start`. A session with no catalog refuses classified calls with a
  named reason rather than running them.
- Every UI surface is set on a transition and never on a timer: the status when
  the mode changes, the working message for the length of one classification,
  the denial widget when a call is denied and cleared when the user speaks. A
  quiet session draws nothing.
- The breaker asks once per episode through `ctx.ui.confirm`; `hasUI === false`
  (`-p`, `--mode json`) is fail-closed, per `permission-gate.ts`'s precedent.
  Answering yes clears the counters and judges the call that tripped it — it
  approves nothing on its own. An outage counts toward the limits like any
  other block — grinding against an unreachable classifier is what the breaker
  exists to stop — but an episode where EVERY block was an outage says so
  (`BreakerState.outage`) and asks about the outage instead of claiming the
  session was refused N times. The breaker's own block inherits the episode's
  kind, so reporting the trip does not turn a pure-outage run into a mixed
  one.
- **A denial is written before it is reported, and the write is the durable
  half.** `ledger.ts` appends one JSON line per blocked call to
  `ATTN_PI_AUTOMODE_DENIAL_LOG` (the pi driver forwards what the daemon names,
  so an attn session's file sits in that profile's data dir) or, for a bare pi,
  to `attn-automode-denials.jsonl` under pi's config dir. Both entrypoints wire
  it, standalone included. The daemon reads it back on the denials read path
  (`internal/daemon/automode_ledger.go`) and imports what the log is missing.
  A failed write is said out loud as an error, because it is the only leg with
  nothing behind it. Design:
  [docs/plans/2026-08-18-automode-denial-ledger.md](../../docs/plans/2026-08-18-automode-denial-ledger.md).
- The ledger keeps an active file and one rotated generation, and every
  rotation past that writes a marker naming how many records were dropped, so
  the reader is told rather than shown a partial episode. `paths.ts` protects
  any last path segment beginning `attn-automode`: a session that can edit the
  record of what it was refused leaves no record.
- Denials are also reported through `AutoMode`'s `onDenial` seam, which is the
  live surface — a notification, a fact, a row that appears while the user is
  watching. `suite/index.ts` sets it to one `suite.report_denial` over the
  relay, which the driver forwards to the daemon as
  `session.report_automode_denial`; the standalone bundle leaves it unset, so a
  bare pi reports nowhere and relies on the ledger alone. It is fire-and-forget
  like every other suite report — a reporter that throws is caught, because
  nothing outside auto mode may turn a denial into something else. The report
  carries no seq: it is an append, not a claim about the session's state.
- A denial names who decided. Static rules keep their own names; a classified
  call reports the layer that answered (`classifier-2a`, `classifier-2b`), or
  `classifier-unavailable` when no model in the layer could be reached, which
  is why `ClassifierVerdict` carries a `layer` and an `unavailable` flag.
