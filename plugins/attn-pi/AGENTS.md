# attn-pi plugin guide

**Read pi's own docs before changing this plugin.** The complete set for the
pinned version is installed at
`node_modules/@earendil-works/pi-coding-agent/docs/` (`bun install` here if it
is missing): `extensions.md` for the extension API, the event lifecycle and
session replacement; `sdk.md` for the headless runtime nisse's host drives;
`session-format.md` for the JSONL and `SessionManager`; then `rpc.md`,
`settings.md`, `compaction.md`, `tui.md`, `models.md`.

**This file carries only two things:** attn-specific invariants, and pi
behavior that is verified but undocumented — each stamped with the pi version
it was verified against. Do not add a fact pi documents; link it. An invariant
about one function belongs in a comment at that function. When an entry below
graduates into pi's docs, delete it here.

Canonical vision: `docs/vision/pi-attn-plugins.md`. Grounding with source
citations (stamped at pi 0.80.10): `docs/grounding/pi-plugins.md`. Vocabulary
— pi the engine vs nisse the agent vs a host, conversation session, auto mode:
`docs/glossary.md`. This is attn's only driver plugin, so the driver contract
itself lives in `internal/daemon/plugin_driver.go`.

## Pinning pi

pi is a 0.x rolling release (~1-2/week) whose breaking changes are documented
only as prose under "### Breaking Changes" in each package's `CHANGELOG.md`.

- **pi has no extension/version compat gate** (0.83.0: nothing in
  `core/extensions/loader.js` or the package manager reads a version). An
  extension built against old types loads silently and fails at the first
  missing call site. Pin the exact version, treat upgrades as deliberate
  changelog-gated events, and have the suite self-check pi's version at
  `session_start` rather than assume compatibility.
- `spike-harness/` drives pi's SDK without attn and is the gate a version bump
  has to pass; see its README.
- The compiled binary cannot read pi's `VERSION`: pi reads its own
  package.json off disk at runtime and a `bun build --compile` binary has no
  copy, so `VERSION` degrades to `"0.0.0"`. The pin is inlined from this
  plugin's exact dependency entry instead, and a disagreement with a
  runtime-readable VERSION is spawn-fatal.

## Verified pi behavior pi does not document

Re-verified against the pinned pi on 2026-08-18 unless an entry says
otherwise.

- **`session_shutdown` does not fire on an uncaught exception or on SIGKILL**
  (0.83.0). It fires on clean quit and SIGTERM/SIGHUP, which `extensions.md`
  does document; what it omits is that `uncaughtCrash` calls `process.exit(1)`
  with no emit. Never treat a missing goodbye as meaningful: the PTY child
  exit the driver observes is the authoritative liveness signal, and declared
  state rides on top.
- **Module scope survives a session transition, but not a reload** (0.83.0).
  pi caches the extension module per process, keyed on cwd and a generation
  counter. A session replacement builds a fresh `ResourceLoader`, so the cache
  is kept and module-scope state survives resume/fork/new — which is why the
  relay client and `AutoMode` live there. `ctx.reload()` and `/reload` reuse
  the loaded one and call `clearExtensionCache()`, so the module is
  re-imported and everything at module scope is rebuilt; a socket held there
  is orphaned, not closed. A cwd change clears it too. (That factories re-run
  and that captured contexts throw afterwards IS documented — `extensions.md`,
  "Session replacement lifecycle and footguns".)
- **`pi --session-id <id>` exists and is undocumented** (0.83.0). It creates
  the session when missing, and it is how the driver mints native identity at
  spawn and on resume. Never parse pi's session picker.
- **pi writes a `model_change` and a `thinking_level_change` into every
  session file before anything is said** (0.83.0). `session-format.md`
  documents the entry types, not that they always lead. Reconstruction must
  ignore a `model_change` that precedes the first message, or every new
  conversation claims to have switched models and (not being an assistant
  message) declares `waiting_input` on arrival.
- **`session.clearQueue()` is all-or-nothing** (0.83.0) — pi offers no
  per-entry removal, which is why attn's `clear_queue` is not either.
- **pi spawns each tool subprocess into its own process group** (measured
  2026-08-05), so a group kill misses them; only pi's own dispose, which the
  host runs on SIGTERM, tears them down.
- **pi's event union is neither stable nor changelogged**: four event types
  appeared between 0.80.10 and 0.83.0 with no entry. Never claim
  exhaustiveness over it — log an unknown type once and drop it.
- **`bindExtensions` is required per session.** `sdk.md` says to call it again
  after a replacement; the failure mode it omits is that without it
  `session_start` never fires and resource discovery never runs, so extensions
  silently do nothing.
- **pi's TUI under attn's PTY** (pi-tui 0.83.0): it never uses the alternate
  screen (no mode 1049), never sends CPR (no `ESC[6n`), queries only OSC 11
  once for light/dark detection, negotiates the Kitty keyboard protocol with
  `ESC[>7u ESC[?u ESC[c`, wraps every frame in DEC 2026 synchronized output,
  and self-fires SIGWINCH after enabling raw mode. attn's worker answers OSC
  11 from the daemon-pushed theme and answers the trailing DA1, so pi falls
  back to modifyOtherKeys deterministically — degraded but correct; do not
  "fix" this by answering the kitty query. Still not live-verified: resize
  races under a real attn PTY.

## Driver

- The shape the daemon expects: `attn-plugin.toml` (`attn_api_version` gate),
  `driver.register` with a capability map, `driver.spawn`/`driver.resume`
  returning argv+env+cwd that the daemon runs in the attn-owned PTY,
  `session.report_metadata` as the resume token, `driver.session_closed`
  cleanup.
- The driver also owns pi citizenship: it listens on a relay unix socket,
  injects `-e <bundled suite.js>` plus `ATTN_PI_SUITE_SOCKET` /
  `ATTN_PI_TOKEN` at spawn and resume, registers `state_reporting` +
  `message_delivery`, relays suite reports to the daemon (the per-run seq
  cursor is driver-side), and forwards `driver.deliver_message` to the suite.
- Stop verdicts come from daemon-side classification (`attn.classify_stop`);
  an empty settle and one the user interrupted (pi's `stopReason: "aborted"`)
  both report `idle` without classifying — a classification costs seconds and
  money to answer a question the session already knows the answer to. **Never
  add screen-scraping state detectors for pi, and never fall back to PTY
  typing for message delivery.**
- **A declared state is only as current as the channel that declares it**, so
  both ends of the relay watch it: a run whose relay connection is gone is
  reported `unknown` once `unbackedGraceMs` passes, and the daemon runs the
  mirror of that alarm (`pluginDriverSilenceWatch`) for a driver that stops
  speaking. Coming back is the other half — every `suite.hello` carries
  `pi_state`, forwarded with `only_if_unknown` so attn takes it only when it
  has nothing; applied unconditionally it would restamp `state_since` and
  re-open a settled turn on every reconnect.
- Design: `docs/plans/2026-07-20-pi-citizenship.md`,
  `docs/plans/2026-08-18-pi-state-detection.md`.

## Suite

`plugins/attn-pi/suite/` runs inside pi's runtime, staged as a single
`suite.js` next to the driver executable.

- It must never crash or block pi: relay sends are fire-and-forget, failures
  are swallowed, and missing `ATTN_PI_SUITE_SOCKET`/`ATTN_PI_TOKEN` turns the
  whole suite into a no-op so a bare pi outside attn is unaffected.
- The relay client lives at module scope (see the module-scope entry above);
  every factory re-run re-binds the current pi/ctx rather than re-dialing, and
  a stale one makes `driver.deliver_message` answer `delivered: false`.
- **Identity is re-announced on every `session_start`, never inferred from
  files** — a transition mints a new pi session id on a connection that is
  already open (`suite/core.ts` carries the same note at the handler). pi's
  own lineage rules are in `session-format.md`: resume keeps the id and the
  file, fork records a `parentSession`, a plain `/new` records nothing.
- `agent_end` caches the last assistant message's text and whether pi stopped
  because the user took the turn back; `agent_settled` has no payload and
  ships that cache as `suite.report_stop`.
- **The suite cannot log** — a stray write corrupts pi's TUI. What it could
  not hand over is counted and the count rides the next hello as
  `dropped_reports`, for the driver to write the line.
- `reportApprovalWindow` is the one place pi is blocked on the user: auto
  mode's breaker asking through `ctx.ui.confirm`. Classification inside
  `tool_call` blocks on a model, not on the user, and stays `working`.
- `suite/index.ts` imports pi's `VERSION` from
  `@earendil-works/pi-coding-agent` and pi resolves it as a virtual module at
  load time, so the bundle step must keep that import `--external`.
- Suite silence is never meaningful; PTY child exit is the liveness signal.

## nisse, the headless agent

This plugin registers two agents: `pi` launches the pi TUI in an attn PTY (the
driver above), and `nisse` is a conversation session — pi running headless
through its SDK in `host/index.ts`, a process the daemon spawns and owns.
**Every decision below has its reasoning and its receipt in
[docs/plans/2026-08-05-pi-headless-host.md](../../docs/plans/2026-08-05-pi-headless-host.md);
read it before changing the host.** Everything under `internal/daemon` that
says "host" is agent-agnostic machinery that would run a second conversation
agent unchanged; everything here that says nisse is this agent in particular.

- **Three channels, and they must stay separate.** fd 3 is the envelope stream
  out (NDJSON), stdin is verbs in (`prompt`, `steer`, `follow_up`,
  `tool_detail`, `clear_queue`, `snapshot`, `history`, `set_model`,
  `shutdown`), stdout/stderr are pi's and the host's own output captured to
  `<data-dir>/hosts/log/<session>.log`. Envelopes never go on stdout: pi loads
  the user's own extensions and any one of them printing a line would corrupt
  a shared stream.
- **Two envelope families.** Semantic kinds (`session_ready`, `run_started`,
  `run_settled`, `tool_started`, `tool_finished`) are attn's vocabulary and the
  daemon may read them; renderings (`message_start`, `message_delta`,
  `message_end`, `queue_update`, `tool_detail`, `conversation_snapshot`,
  `conversation_page`, `notice`, `model_changed`) are drawn by the app and
  forwarded opaquely. Adding a rendering is a host + app change; a new semantic
  kind is a protocol conversation. `model_changed` is the one rendering the
  daemon also reads, and only to rewrite the launch intent.
- `seq` is one monotonic spine across both families, minted only by
  `EnvelopeStream`. A relaunch is a NEW host whose `seq` restarts at 1:
  `session_ready` is the only envelope that resets a client's spine, and the
  app must exempt it from its own dedup or drop every envelope of the revived
  session.
- **A state declaration is the subset of semantic kinds carrying a `state`**
  (`STATE_DECLARATION_KINDS`), applied through `applyState` with the
  envelope's `seq` as the ordering cursor and never invented daemon-side from
  the kind alone; one of those kinds arriving without a state is logged and
  dropped. Tool boundaries and model switches are deliberately NOT
  declarations — `applyState` restamps `state_since` on every apply, so a
  session that ran twenty tools would report working for however long the last
  one took.
- **Tool visibility is a declaration plus a fetch.** `tool_started` /
  `tool_finished` carry only what the transcript keeps forever; what a tool
  read, wrote or printed stays in the host until a card is opened and the
  `tool_detail` verb asks for it; what is held is bounded by
  `ToolDetailStore`, which names its own budget when it has evicted what you
  asked for. `tool_detail` is addressed by `call_id` and broadcast, so two
  windows on one card cost one read and nothing can time out.
  `tool_execution_update` is dropped on purpose — a live-updating card would
  repaint continuously beside GPU terminals — and handled explicitly so it
  does not log as an unmapped pi event.
- **A message appears when it has something to say.** Nothing is emitted on
  pi's `message_start`: the first text delta mints the message and one that
  streamed nothing is decided at its end. pi's `toolResult` messages are
  dropped (`UNRENDERED_ROLES`) — the card is that call's record. Deltas
  coalesce on a 30 ms window (receipt in `DeltaCoalescer`'s comment), flushed
  before any `message_end` or declaration so nothing overtakes the text it
  follows.
- **Delivery is three verbs and the host picks nothing; the daemon says
  which.** `steer` drains at pi's next turn boundary, `follow_up` only when
  the run would otherwise settle, `prompt` opens a run and is refused mid-run.
  A steer or follow-up at an idle session resolves into a run rather than
  being dropped — a doorbell must never vanish for arriving at the wrong
  moment. `queue_update` is pi's own queue state forwarded verbatim, so the
  app never invents an optimistic entry and the strip empties on pi's answer,
  not on the click.
- **Every prompt settles.** `session.prompt` can reject before pi opens a run
  at all, so the catch emits `run_settled` with the reason, and the daemon
  settles a prompt that reached no host for the same reason. Otherwise the
  app's composer waits forever for a reply nobody is writing.
- **The host reopens, it never starts over.** `SessionManager.continueRecent`
  continues the most recent session file and creates one only when there is
  none. An **empty** session dir is the only thing that consults
  `ATTN_NISSE_RESUME_FILE`, and it FORKS (`SessionManager.forkFrom`) rather
  than appending, so the conversation it was picked up from is never written
  to; `buildContextEntries()` is what the transcript is rebuilt from. Two
  sessions on one file is the failure this ordering exists to make impossible.
- **The transcript is fed the host's own envelopes**, not pi's events and not
  the session file: pi persists a message when it ends, so a snapshot rebuilt
  from disk stops one paragraph short of what a watching client already has.
  `TranscriptStore` is the same reducer the app runs, which is what keeps the
  two from drifting. `conversation_snapshot` is BROADCAST and REPLACES — the
  host is the authority, the same bargain the terminal's VT dump makes.
- **`epoch` is the transcript's seq spine**: same epoch merges, a new epoch
  replaces. Scroll-back is asked for by item, not offset — `history` names the
  oldest item a client holds, and a client takes the answering
  `conversation_page` only when the epoch matches AND the anchor is its own
  oldest item.
- **Two budgets, and confusing them is the bug.** `SNAPSHOT_*_LIMIT` bounds
  what one message CARRIES; `TRANSCRIPT_RETENTION_*` bounds what the host
  HOLDS. `ATTN_NISSE_RETENTION_ITEMS` / `ATTN_NISSE_RETENTION_BYTES` lower the
  latter (read from the daemon's environment at spawn) and are the only way to
  watch a host actually drop history; a value that is not a positive whole
  number is logged and treated as absent, never as zero.
- **What retention drops, the snapshot admits to** — `dropped` (gone for
  good), `truncated` (this message does not carry everything) and `has_more`
  (ask again) are three different questions, and the app draws a row once
  nothing is left to page and `dropped` is non-zero. **Retention never evicts
  a message still being written**: a streaming message stops being newest the
  moment pi opens a tool beneath it, and evicting it there makes the next
  delta mint a fresh message from the tail alone.
- **A notice is a row that explains a silence** (`id`, `level`, `text`,
  `done`), keyed so one row updates in place rather than stacking;
  reconstruction mints them too. A refused turn is a notice because pi does
  not raise — a provider error arrives as an assistant message with
  `stopReason: "error"` and the sentence buried in `errorMessage`, which
  `messageFailure` digs out. Notices are transparent to
  `conversationInterrupted`.
- **`session_ready` says why the session went quiet**: a reopened conversation
  whose last item is not an assistant message declares `waiting_input`,
  everything else `idle`. Both open a turn and both take a nudge.
- **A model switch is not a state declaration.** `set_model` asks,
  `model_changed` answers with the model actually in force, so the picker
  moves on the host's answer and never on the click; the daemon rewrites
  `LaunchIntent.Model` from it so a revive relaunches on the model the user
  chose.
- **Teardown is SIGTERM first, then the group.** The SIGTERM is the
  load-bearing half — pi's own dispose is what tears its tool subprocesses
  down (see the process-group entry above) and a hard kill orphans them (3x
  reproduced, 2026-08-04). Never "simplify" this to a group kill.
- Session storage is attn's (`<data-dir>/hosts/state/<session>`), never
  `~/.pi/agent/sessions`. Auth and resource discovery still resolve against
  the real `~/.pi/agent`, exactly as a bare `pi` does.
- **A host gets the PTY path's identity block** (`ATTN_SESSION_ID`,
  `ATTN_AGENT`, `ATTN_DAEMON_MANAGED`, `ATTN_INSIDE_APP`, and the active attn
  first on PATH) on top of the daemon's environment and the user's login
  shell. Without it a delegated agent's `attn ticket comment` is attributed to
  whichever session the daemon inherited its environment from, and bare `attn`
  resolves to whichever install is on the login shell's PATH. Both were
  observed, not theorized.
- **`ATTN_NISSE_INITIAL_PROMPT`** carries the launch's first user message when
  it had one (today, a delegation brief) — in the environment rather than argv
  because argv is world-readable text a sibling's `pkill -f` can match on. The
  daemon hands the same prompt to every replacement host, so delivery is the
  host's decision: it says it exactly when nothing in the reopened
  conversation was SPOKEN (`launchPromptIsUndelivered`). Only messages count —
  not notices, and not a FORKED-in history. Known gap: a session that forks
  AND carries a brief and then dies inside its first turn stays silent,
  because the fork already wrote the file; closing it needs a delivered-marker
  in the session dir, and no product path reaches that combination today.

## Auto mode

`automode/` is pi's permission system: a static safety envelope plus a
classifier for everything reaching past it, denied conversationally rather
than through dialogs. Design, receipts and slices:
[docs/plans/2026-08-16-pi-auto-mode.md](../../docs/plans/2026-08-16-pi-auto-mode.md).

- **The decision order in `policy.ts` IS the policy.** Anything added to the
  envelope runs unjudged, so the read-only sets are conservative by
  construction: a command that can run another command, or reach the network,
  is not in them.
- **Fail-safe both ways:** a handler that throws blocks the tool, and a call
  auto mode cannot judge is refused, never run. Model output that does not
  read as a verdict is one of those refusals.
- Like `suite/`, the module is duck-typed against pi's shapes rather than
  importing pi, so `bun test` covers the whole extension including its
  `tool_call` wiring. `index.ts` is the only file that knows pi's event names.
- The seam for a nested completion is
  `registry.getProvider(id).streamSimple(model, context, options)`, with the
  request auth assembled the way `ModelRuntime.prepareRequest` assembles it.
  `ModelRegistry.complete()` (added 0.84.2) is the flatter, wrong call: it is
  the RAW api path, so it skips the thinking-level clamp and the per-API
  request options and the model thinks unbounded — 354 output tokens and 5.7 s
  against 60 tokens and 2.9 s on glm-5.3 with the same prompt (2026-08-17).
- **Each layer names an ordered model list** (`classifier_models`,
  `escalation_models`; the singular spellings load as a one-entry list), and
  it **is walked only when a model cannot be reached** (a thrown request,
  `stopReason: "error"`), each entry getting one immediate retry first. A
  model that answers ends the walk whatever it answered, including a deny and
  including unparseable output — advancing on a verdict would be shopping for
  a different one. An exhausted list still blocks, under the rule
  `classifier-unavailable` and with a reason naming the layer, the models
  tried and the last failure. An unavailable deny is never cached: the cache
  holds verdicts, and there was none.
- **Escalation is scoped to allow verdicts.** A confident deny is final — the
  user overturns one by saying so, and a second opinion buys them only the
  wait. Letting denials escalate doubled the cost of the corpus.
- What a classification spends is held and folded into the next tool result's
  usage. A blocked call never reaches pi's result hook, so the session total
  is right and per-call attribution is not.
- **Two entrypoints, one module.** `suite/index.ts` composes auto mode in when
  `ATTN_PI_AUTOMODE_CONFIG` is set (attn's launch injection, JSON in the exact
  shape `automode/config.ts` parses) and builds nothing at all when it is not,
  so a bare pi loading `suite.js` registers no command, flag or handler for
  it. `automode/standalone.ts` is the same extension for `pi -e automode.js`,
  reading the same JSON from `attn-automode.json` under pi's config dir. Both
  are staged by `build-bundled-plugins.sh`. attn's half of that injection is
  the daemon handing the promoted config to a driver that advertises the
  `auto_mode` capability; a config change reaches the next session, a live one
  is not refreshed.
- The `AutoMode` in `mode.ts` lives at module scope and the session state the
  factory owns does not. That is the line: the user's `/auto` choice is theirs
  until they change it, while the verdict cache and the breaker belong to one
  session.
- Precedence is `/auto` > `--auto`/`--no-auto` > `enabled_default`. The flags
  carry no default so an unset one reads as undefined, and a command that
  loses to a flag is not a command.
- The classifier is built from `ctx.modelRegistry`, captured at
  `session_start`. A session with no catalog refuses classified calls with a
  named reason rather than running them.
- **Every UI surface is set on a transition and never on a timer.** A quiet
  session draws nothing.
- The breaker asks once per episode through `ctx.ui.confirm`;
  `hasUI === false` is fail-closed, per `permission-gate.ts`'s precedent.
  Answering yes clears the counters and judges the call that tripped it — it
  approves nothing on its own. An outage counts toward the limits, but an
  episode where EVERY block was an outage says so (`BreakerState.outage`) and
  asks about the outage instead of claiming the session was refused N times.
- **A denial names who decided.** Static rules keep their own names; a
  classified call reports the layer that answered (`classifier-2a`,
  `classifier-2b`), or `classifier-unavailable` when no model in the layer
  could be reached, which is why `ClassifierVerdict` carries a `layer` and an
  `unavailable` flag. Denials reach attn through `AutoMode`'s `onDenial` seam,
  which `suite/index.ts` sets to one fire-and-forget `suite.report_denial`
  over the relay, forwarded to the daemon as `session.report_automode_denial`;
  the standalone bundle leaves it unset, so a bare pi reports nowhere.
- **A denial is written before it is reported, and the write is the durable
  half.** `ledger.ts` appends one JSON line per blocked call to
  `ATTN_PI_AUTOMODE_DENIAL_LOG` (or, for a bare pi,
  `attn-automode-denials.jsonl` under pi's config dir); both entrypoints wire
  it, and the daemon imports what its own record is missing
  (`internal/daemon/automode_ledger.go`). A failed write is said out loud,
  because it is the only leg with nothing behind it. Design:
  [docs/plans/2026-08-18-automode-denial-ledger.md](../../docs/plans/2026-08-18-automode-denial-ledger.md).
- The ledger keeps an active file and one rotated generation, and a rotation
  counts the destroyed generation's records AND its markers into one marker
  opening the new active file. The marker in the file being renamed survives
  the rename and is counted where it lands, never here too: counting one twice
  doubles every earlier rotation and compounds. `paths.ts` protects any last
  path segment beginning `attn-automode` — a session that can edit the record
  of what it was refused leaves no record.
