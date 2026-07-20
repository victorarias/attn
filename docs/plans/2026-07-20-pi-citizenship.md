# Plan: pi citizenship (rock 2)

## Why / Alignment

Rock 2 of `docs/vision/pi-attn-plugins.md`: pi becomes a native attn citizen —
the session links itself on birth, declares its own state (no scraping, no
classifier fallback), takes ticket nudges and doorbell wakes as in-band
steering, and enriches its stops (done vs waiting on a reply) via an attn-side
classification service.

Victor's calls (2026-07-20):

- **Whole rock in one PR** — linking + declared state + steering +
  stop-enrichment together (accepted that this lands past the usual 1k-line
  comfort zone).
- **Transport: relay via driver.** The pi-side suite talks only to the attn-pi
  driver over a driver-owned unix socket; the driver relays through its
  already-authorized daemon connection. No new daemon client role.
- **Staging: driver injects `--extension`.** The suite is bundled with the
  attn app next to the driver executable; `driver.spawn`/`resume` append
  `-e <suite>` to argv. No user-level `pi install`; version always matches the
  driver.
- **Nudges use pi's steering API, not PTY typing.** Daemon fire policy is
  unchanged — the countdown fires even into `working` sessions; only
  `pending_approval` blocks; selection pause, the 3s splice guard, and the
  watch lease all stay as-is. Only the delivery mechanism changes for pi.
- Stop classification is **attn's job** (vision: "a service, not a guess") —
  the suite ships the final assistant text up; attn classifies; the verdict
  comes back as the stop report. opencode's classify-it-yourself pattern is
  not copied.

Done looks like: a pi session under attn shows declared `working`/`idle`/
`waiting_input`; a ticket nudge or chief doorbell arrives in-band (steered
into a live turn, or starting a turn when idle); a stop that ends in a
question shows `waiting_input`; `/new` or a fork inside pi keeps the attn
resume token fresh.

## Architecture Map

```text
Current (rock 1):
daemon --driver.spawn--> attn-pi driver --> argv [pi --session-id <id>] --> attn PTY
  state: dumb (working while alive, idle on PTY exit)
  nudges: typeDoorbell --> bracketed paste + \r into PTY

Target:
daemon
  typeDoorbell (single delivery seam, unchanged policy)
    -> capability check: driver has in-band delivery?
       yes -> plugin RPC driver.deliver_message --> attn-pi driver
                --> relay socket --> suite --> pi.sendUserMessage (steer / new turn)
       no  -> bracketed paste into PTY (claude/codex/copilot unchanged)

attn-pi driver (bun executable, long-lived)
  - listens on relay socket (one per driver process)
  - driver.spawn/resume: argv += [-e, <bundled suite.js>]
                         env  += {ATTN_PI_SUITE_SOCKET, ATTN_PI_TOKEN}
  - relays suite reports -> daemon (session.report_state/stop/metadata,
    run_id + seq cursor owned here)
  - asks daemon attn.classify_stop for stop verdicts

pi suite (single bundled .js, loaded per-session via -e)
  - module-scope socket client (survives session transitions; factory re-links)
  - session_start (every reason: startup/reload/resume/fork/new):
      hello {token, pi_session_id, pi_version, reason} -> driver refreshes
      PiMetadata (report_metadata, seq++)  [fixes stale resume token on fork/new]
  - agent_start -> report_state working
  - agent_end   -> cache last assistant text from event messages
  - agent_settled -> cached text -> driver -> attn.classify_stop
                   -> report_stop {verdict: idle | waiting_input | unknown}
  - deliver_message (driver->suite) -> sendUserMessage; steer if turn live

PTY child exit stays the authoritative liveness signal (daemon forces idle;
driver.session_closed) — declared state rides on top, suite silence is never
meaningful.
```

## Data Model / Interfaces

```ts
// relay protocol: ndjson JSON-RPC over the driver-owned unix socket
// suite -> driver
type Hello       = { token: string; pi_session_id: string; pi_version: string;
                     reason: "startup" | "reload" | "resume" | "fork" | "new" };
type ReportState = { state: "working" };            // idle arrives via ReportStop
type ReportStop  = { assistant_text: string };      // driver classifies via daemon
// driver -> suite
type DeliverMessage = { text: string };             // -> { delivered: boolean }
```

```go
// daemon <-> plugin (attn_api_version 4 -> 5, exact-match gate)
// new daemon -> plugin request
driver.deliver_message {session_id, run_id, text} -> {ok bool}
// new plugin -> daemon request
attn.classify_stop {session_id, run_id, assistant_text} -> {verdict}  // idle | waiting_input | unknown
// existing, now used by pi: session.report_state / report_stop / report_metadata
// driver.register capabilities += {state_reporting, message_delivery}
```

`PiMetadata` stays schema 1; `pi_session_id` (and `pi_version`) are refreshed
via `session.report_metadata` on every suite hello, so fork/`new` inside pi
updates the resume token instead of stranding it.

Verified against pi v0.80.10 (f1c587dd): `session_start.reason` includes
`"reload"`; `agent_end` carries `messages`; `agent_settled` fires only once a
run is fully settled — no retry, compaction, or queued continuation follows —
and has no payload, so the suite caches the last assistant text at
`agent_end` and classifies at `agent_settled`. `sendUserMessage(content,
{deliverAs})` always triggers a turn (`"steer" | "followUp"` applies only
while streaming; pick `steer` when `ctx.isIdle()` is false). Identity comes
from `ctx.sessionManager.getSessionId()`, pi version from the package
`VERSION` export.

## Boundaries

- **daemon** owns nudge fire policy (countdown, selection pause, splice guard,
  watch lease, pending_approval block) — untouched. `typeDoorbell` is the one
  seam that dispatches on the `message_delivery` capability.
- **driver** owns the relay socket, the per-run token, and the run_id/seq
  report cursors. It is the only process that talks to attn.sock.
- **suite** owns pi-event mapping and steering delivery. It never dials
  attn.sock. Socket client lives at module scope; the extension factory
  re-links on every session transition (contexts from before a transition
  throw — see plugins/attn-pi/AGENTS.md).
- Liveness is the PTY's: a missing suite goodbye means nothing (uncaught
  crash / SIGKILL fire no session_shutdown).

## Implementation Steps

- [x] Daemon: add `message_delivery` to the capability vocabulary; add
      `driver.deliver_message` (daemon->plugin request) and route
      `typeDoorbell` through it when the session's driver run has the
      capability; same error semantics as today (log, keep unread).
- [x] Daemon: add `attn.classify_stop` plugin RPC backed by a text-input
      entrypoint on the existing stop classifier (tool-less small-model call);
      verify prompt/config reuse against internal/classifier.
- [x] Protocol: bump `internal/plugins` APIVersion 4->5; bump both plugin
      tomls (attn-pi, attn-opencode); verify skew failure is explicit.
- [x] Driver: relay socket server (token check, one client per run), env
      injection, `-e <suite>` argv injection, report relaying with seq
      discipline, metadata refresh on hello, deliver_message forwarding,
      register `state_reporting` + `message_delivery`.
- [x] Suite: new `plugins/attn-pi/suite/` TypeScript source; module-scope
      client; session_start hello (all reasons); agent_start/agent_end
      mapping; deliver_message -> sendUserMessage (steer when turn live, plain
      when idle — verify exact deliverAs semantics against pi 0.80.10 types).
- [x] Bundling: stage suite as a single bundled .js next to the driver
      executable (scripts/build-bundled-plugins.sh); driver resolves it
      relative to process.execPath with an env override for tests; extend
      scripts/source-fingerprint.sh includes.
- [x] Tests: driver relay + report-cursor unit tests (bun); suite mapping
      tests against a fake pi ExtensionAPI (bun); daemon deliver_message +
      classify_stop tests (Go, plugin fixture e2e).
- [x] Live verification (throwaway profile, never dev): declared state flips
      working->idle on a real turn; question-shaped stop shows waiting_input;
      ticket nudge delivered in-band mid-turn; chief doorbell wakes an idle pi
      session; fork inside pi then resume from attn restores the forked
      session.
- [x] CHANGELOG + plugins/attn-pi/AGENTS.md updates (suite invariants).

## Decisions

- **attn_api_version 4->5** even though the change is additive: the gate is
  exact-match, and "version the seam" beats silently registering new
  capability names against an old daemon (which would fail as an opaque
  `unsupported driver capability` error).
- **Splice guard stays for pi** even though in-band delivery cannot splice:
  the fire policy stays uniform across agents; pi-specific policy bypasses
  wait for the Background Eyes rock.
- **No PTY fallback when in-band delivery fails**: log and keep the unread
  indicator, same as today's doorbell-error path. A typed fallback would
  reintroduce the splice risk the steering path removes.
- **`waiting_input` only via stop verdicts** for now: pi has no approval UI
  (safety envelope is a later rock), so `pending_approval` is not reported.
- **Stop classification daemon-side, text-in/verdict-out**: the suite never
  runs its own LLM call (opencode's pattern is the rejected alternative).
- **Empty assistant text at settle → deterministic `idle`**: an aborted/empty turn has nothing to await; the classifier only ever sees real text.
- **Missing bundled suite fails the spawn** (no silent no-suite fallback): the suite ships next to the driver by our own build, so absence is a packaging bug; degrading silently would break nudges with no visible cause.

## Open Questions

- (none right now)

## Follow-ups

- Mid-turn steer policy refinements (immediate wakes for background eyes).
- `pi install`-able suite distribution for bare pi sessions outside attn.
- Kitty graphics through vt10x snapshot/replay (still unverified).
- Safety envelope + auto mode (next rock).
- `attn preflight --agent pi` fails with "agent \"pi\" is not registered": the
  check reads the CLI's in-process built-in agent registry, which cannot see
  daemon-side plugin agents. Pre-existing (rock 1 had it too); preflight needs
  a daemon-backed agent lookup for plugin drivers.
