# attn-pi plugin guide

attn-side driver plugin for [pi](https://github.com/earendil-works/pi). Canonical
vision: `docs/vision/pi-attn-plugins.md`. Full grounding evidence with citations:
`docs/grounding/pi-plugins.md`. Reference implementation for the driver pattern:
`plugins/attn-opencode`.

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

- Follow `plugins/attn-opencode`: `attn-plugin.toml` (`attn_api_version`
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

## The headless host (`pi-host`)

This plugin registers two agents. `pi` launches the pi TUI in an attn PTY (the
driver pattern above). `pi-host` is a **conversation session**: pi runs headless
through its SDK in `host/index.ts`, a process attn's daemon spawns and owns.
See `docs/glossary.md` for the vocabulary and
`docs/plans/2026-08-05-pi-headless-host.md` for the slices.

- Three channels, and they must stay separate. **fd 3** is the envelope stream
  out (NDJSON), **stdin** is verbs in (`prompt`, `steer`, `follow_up`,
  `tool_detail`, `clear_queue`, `shutdown`), **stdout/stderr** are pi's and the
  host's own output, captured to `<data-dir>/hosts/log/<session>.log`. Envelopes
  never go on stdout: pi loads the user's own extensions and any one of them
  printing a line would corrupt a shared stream.
- Two envelope families. **Semantic** kinds (`session_ready`, `run_started`,
  `run_settled`, `tool_started`, `tool_finished`) are attn's vocabulary and the
  daemon may read them. **Renderings** (`message_start`, `message_delta`,
  `message_end`, `queue_update`, `tool_detail`) are drawn by the app and
  forwarded opaquely. Adding a rendering is a host + app change; a new semantic
  kind is a protocol conversation.
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
  user's login shell — because that is where an agent's credentials live.

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
