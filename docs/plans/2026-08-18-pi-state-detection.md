# pi state detection

A pi session that outlives a daemon restart stops reporting its state, for
good, and nothing says so. Observed 2026-08-18 on session `8360d126`
(`pi-a5-exit`): the pane showed `⠼ Working...` while attn had said `idle
since 17:02` for six hours.

pi declares its own state — there is no screen scraper for any agent any
more — so when the declaration channel breaks, the session's state simply
stops moving. `attn state explain` shows attn watching it happen:

```
22:43:39   worker_info   working   vetoed   resolver_owned   "watch subscribe replay"
```

This plan closes the break, then makes the remaining silence audible and
teaches pi the two states it can already see but never reports.

## How the channel breaks

The daemon restarted at 19:47 (and 22:22, and 22:43 — installs and app
restarts). Each restart starts a fresh `attn-pi` runtime. Five things then
line up:

1. **The relay socket path is pid-scoped.** `relaySocketPath()` returns
   `$TMPDIR/attn-pi-relay-<pid>.sock`. A pi process is spawned with that
   path in `ATTN_PI_SUITE_SOCKET`, which is immutable for the life of the
   process, so it can only ever dial the runtime that spawned it.
2. **The old runtime does not exit.** Its daemon connection closes and it
   keeps running: the relay server holds the event loop open. Measured on
   Victor's machine: 63 orphaned `attn-pi` processes at PPID 1, 711 MB RSS,
   oldest from Aug 5, and 102 stale entries in the procreap registry.
3. **So the suite stays connected to the orphan.** `lsof` receipt: pi's fd
   17 peers `0xe4a25ab2d3bac208`, which is the relay endpoint of runtime
   97088 — the generation from 16:48, orphaned since 19:47.
4. **The orphan cannot forward anything.** Every report reaches
   `AttnRPCClient.request` → `send` → `throw new Error("attn plugin socket
   is not connected")`. That error is in the plugin log four times today.
5. **Nobody hears it.** `RelaySuiteClient.send` swallows every failure by
   design, so pi never learns; the driver never learns either; and the
   daemon vetoes every PTY observation as `resolver_owned`, with no
   timeout behind the veto.

There is a second, independent break behind that one. `driver.register`
already returns the live runs (`active_runs`), and `PiDriver.initialize()`
throws them away — *"No recovery work: this driver keeps no cross-restart
run state."* Even a suite that reconnected to the new runtime would be
refused with `unknown pi suite token`.

Neither break has a witness. The whole chain is silent by construction.

## Slice 1 — a pi session survives a daemon restart

Five changes, one PR. Each is necessary; together they close the chain.

**The runtime exits when its daemon connection closes.** A plugin runtime
whose daemon is gone has nothing left to do, and staying alive is what
fools the suite into believing it is still reported. `AttnRPCClient`'s
`close` handler logs one line and exits 0. Live pi processes are unaffected
— pi is a child of the pty-worker, not of the runtime (receipt: pi 99750's
parent is worker 99745). This is also the fix for the orphan class, and the
reason the suite's socket closes at all, which is what makes the next
change reachable.

**The relay socket path is stable per profile.**
`$ATTN_PLUGIN_DATA_ROOT/relay.sock` — `<data-dir>/plugin-data/attn-pi/`.
The daemon sets `ATTN_PLUGIN_DATA_ROOT` for executable plugins only today;
it starts setting it for every kind, because a plugin's data directory has
nothing to do with how the plugin was packaged, and a path that exists in
the bundle and not in a checkout is a landmine for exactly the tests that
would catch this. 49 characters on Victor's machine against the 104-byte
`sun_path` limit, and shorter than the tmpdir path it replaces.

**Every connect re-says hello.** The suite says hello on `session_start`
and never again, so a reconnect leaves the driver with no
`run.connection` — `driver.deliver_message` answers `delivered: false`
until pi happens to report on its own. The hello moves into
`RelaySuiteClient`'s dial path, sent before the dial resolves, built from
the live pi context the factory rebound. A `close` also schedules a
reconnect with backoff capped at 30 s rather than waiting for the next
report, so a nudge to a quiet session lands. That loop runs only while
disconnected and stops on connect; a dial at a missing socket is one
ENOENT — and a dial that fails schedules the retry itself, because a
failed dial emits no `close` and nothing else would ever try again.

**A report the driver never took is retained.** Reconnecting is not enough
on its own: a run that settles during the outage has exactly one report to
make, and fire-and-forget throws it away. The client holds the newest
un-acknowledged report — only the newest, because a report says what the
session *is* — and the hello that follows the next connect flushes it.
Measured on a live session: prompt at 21:08:12 (`working`), daemon stopped
at 21:08:19, back at 21:09:19, and the settle made inside that minute
applied as `idle` at 21:09:27. Without it the session sat at `working`
until it was prompted again.

**The driver adopts `active_runs`.** `initialize()` rebuilds
`runsByToken` / `runsBySessionID` from what the daemon hands back, so a
restarted driver knows the runs it inherited.

Two things have to be recoverable for that to work:

- *The token.* It stops being a fresh `randomUUID()` and becomes the
  `run_id`, which the daemon mints and returns in `active_runs`. Nothing is
  weakened: the run id is already a UUID already handed to the driver at
  spawn, the relay socket now lives in a 0700 profile directory, and
  reading another process's environment needs the same uid either way. The
  token's job here is naming which run is calling, and the run id names it.
- *The seq cursor.* `active_runs` gains `seq`, read from the run's
  persisted `agent_driver_report_seq` (the row survives the restart — the
  session carried `seq=5` throughout the incident). The driver continues
  from it. Without it a fresh driver starts at 1 and the daemon's
  strictly-increasing cursor discards every report it makes.

  `seq` is an additive optional field, so `pluginAPIVersion` does not move
  and an installed plugin at 5 keeps working. A new driver against a daemon
  too old to send it does not guess: it declines to adopt the run and logs
  the run id and the reason, because a driver reporting into a cursor it
  cannot see is the silent failure this slice exists to delete.

**Existing orphans.** `plugins.ReapRuntimeProcesses` exists and only
`attn profile clean` calls it, which never runs against production. The
daemon calls it at startup, before starting plugins, and logs what it
reaped. Without this the 63 processes already running outlive the fix and
it looks like the fix did not work.

### Verification

- Live: a pi session working through a daemon restart, state tracking it on
  both sides of the restart, read back from `attn state explain`.
- The orphan count and RSS before and after, as a receipt.
- `driver.test.ts` for adoption: register returning runs, a report landing
  at `seq+1`, a missing `seq` declining the run.
- `relay.test.ts` for hello-on-reconnect.
- Go: `active_runs` carries the cursor; startup reap.

## Slice 2 — say when the declaration is stale, and declare what pi can see

**A stale declaration becomes `unknown`.** The `resolver_owned` veto has no
timeout behind it, so a driver that goes quiet freezes the session forever.
The daemon compares the last plugin report against the last PTY output for
sessions whose state is driver-declared; when output has been flowing and
no report has arrived for longer than the tripwire, it applies `unknown`
with reason `plugin_driver_silent` and logs the limit, the gap and the
session.

`unknown` is the right answer and not a guess: `attention.OpensTurn`
already treats it as the daemon admitting it cannot tell, and
`BreaksSnooze` lets it through — which is exactly the six hours this bug
cost. The next plugin report wins over it, so recovery is automatic.

The tripwire is a tripwire: pi reports on every `agent_start`, so the real
gap between "output is flowing" and "a report arrived" is seconds. It gets
measured on a live session before the number is written down, and set far
past it.

**Relay failures get logged.** Fire-and-forget stays — nothing may turn a
failed report into a thrown exception inside pi — but "never throws" was
read as "tells no one". Every swallowed failure logs once per episode to
the plugin log, keyed so a disconnected driver does not write a line per
report.

**The startup race stops lying.** At 19:47 the veto said `resolver_owned`
when the run record was present and `seq=5`; the worker-info replay simply
beat `driver.register` by a second, so `pluginDriverReportsState` was still
false. The trace should name that window rather than blaming the resolver.

**pi reports `pending_approval`.** `parseRelayReportState` accepts only
`"working"`, while the daemon's `validatePluginReportedState` has always
accepted `working | waiting_input | pending_approval | idle | unknown`. The
protocol was ready; pi never used it. pi's extension API gives the suite a
blocking `tool_call` hook — auto mode already sits on it — and
`ctx.ui.confirm`, which the auto-mode breaker already uses. Both are
windows where pi is blocked on the user and attn says `working`, so no turn
opens and no nudge fires. The suite reports `pending_approval` entering
them and `working` on the way out.

**An interrupt is reported, not classified.** `_emitAgentSettled` runs in a
`finally`, so ESC does settle correctly. But the suite then feeds the
half-written paragraph to the stop classifier, which costs up to 30 s and
asks an LLM a question with an obvious answer: the user took the turn back.
pi knows — `stopReason === "aborted"` — so the suite carries it and the
driver reports `idle` without classifying.

### Verification

- Live: pi blocked on an auto-mode breaker confirm shows `pending_approval`
  and opens a turn; ESC settles to `idle` with no classifier call.
- Live: a driver killed mid-session degrades the state to `unknown` inside
  the tripwire and recovers on the next report.
- `synctest` for the staleness gate — it asserts elapsed time and that
  nothing fires while output is quiet.

## Out of scope

The 134 stale relay socket files in `$TMPDIR` stop accumulating once the
path is stable and the runtime exits cleanly; the existing ones are litter
in a directory the OS clears.
