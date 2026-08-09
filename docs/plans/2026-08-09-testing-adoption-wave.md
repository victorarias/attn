# Plan: testing adoption wave — apply the spike's tools

## Goal

The spike (#799, `docs/plans/2026-08-09-testing-spike-synctest-rapid-toxiproxy.md`)
adopted synctest, rapid, and embedded Toxiproxy. This wave turns the adoption
into practice: fix the product issue the spike found, apply each tool where it
pays today, and write the guidance that makes the patterns the default reflex
for future agents. Four tracks, independently executable; A, B1, B2 can run as
parallel delegations, C is small enough to do inline.

## Track A — fix the silent eviction (product bug)

The spike's finding: when the hub evicts a slow WebSocket client
(`internal/daemon/websocket.go`, `maxSlowCount = 3`), the
`StatusPolicyViolation "client too slow"` close frame queues behind the very
backlog that caused the eviction. On a degraded link the client sees ~45s of
silence while the daemon hung up after 1s — eviction is indistinguishable from
a dead network, at the exact moment it matters.

Two independent fixes, both in scope:

1. **Die promptly.** The close frame cannot outrun the backlog on the same TCP
   stream — physics, not a bug. So stop pretending: on eviction, write the
   close frame with a short deadline, then hard-close the connection
   (`CloseNow`). The client's read fails in ~1s instead of its own timeout.
   The frontend's reconnect logic (`useDaemonSocket`) already treats abrupt
   close correctly; verify, don't assume.
2. **Explain on return.** The daemon remembers the eviction and the next
   `client_hello` response carries it (e.g. `evicted_at` + reason), so the app
   can log it or surface "connection dropped: too slow" instead of a generic
   reconnect. Needs a protocol change (main.tsp, `make generate-types`,
   `ProtocolVersion` bump, plus the third lockstep spot in
   `useDaemonSocket.ts`). If client identity across reconnects turns out not
   to exist cheaply, ship fix 1 alone and record why.

Verification: extend the spike's own
`internal/daemon/toxiproxy_slow_client_test.go` — the throttled client must
observe connection death promptly *while still degraded* (that is the whole
point; the current test heals the link first). Protocol change ⇒ live
verification in a non-production app per AGENTS.md.

## Track B1 — rapid: three new property targets

Chosen for invariant density; each has a stated invariant the current tests
don't explore. Keep example tests; rapid explores, it does not document.

1. **`internal/pty` OSC/kitty scanners — chunk-boundary invariance.** The
   invariant behind bug #737 (UTF-8 abort leaking through the OSC 133 strip):
   output must not depend on where the byte stream was split. Property: take
   corpus inputs (real ones exist in `internal/pty/testdata`), let rapid choose
   arbitrary split points, feed the chunks through the scanner, assert output
   is byte-identical to the unsplit run. This directly targets the defect class
   we have already shipped once.
2. **`internal/docstore` — identifier safety.** Stated invariant (AGENTS.md):
   every identifier the store executes is derived from an integer or a
   validated field name, never from caller text. Property: arbitrary caller
   strings (field names, collection names, query values) never appear verbatim
   in compiled SQL identifiers; hostile names are rejected at validation, not
   quoted around.
3. **`internal/workspacelayout` — tree invariants under random ops.** Random
   sequences of tile/moveleaf/remove operations; after every op the layout is
   still a well-formed tree (no orphans, no duplicate leaves, ranks strictly
   ordered). Same shape as the rankkey stateful property, one level up.

Each target: one property file, default 100 checks, commit any
`testdata/rapid/` regression seeds rapid produces.

## Track B2 — synctest: triage-then-convert sweep

Raw counts of `time.Sleep`/`time.After` in tests: daemon 194, ptybackend 22,
pty 21, ptyworker 12, jobs 10, daemonctl 7, workflow 6. **The counts
over-state the sweep**: the bubble boundary rule from the spike (no real
sockets, no real PTYs, no DB opened inside) excludes most daemon harness tests
and everything touching a real PTY. Real processes and fds cannot be bubbled.

So the work is triage first, convert second:

- [x] Triage: for each package above, classify each sleep/poll-paced test as
      *bubble-able* (pure logic + store + fake time) or *boundary-bound*
      (real socket/PTY/process). Record the split in this plan — that map is
      a deliverable, not overhead. See "Track B2 triage map" below.
- [x] Convert the bubble-able ones: remaining `internal/jobs` tests,
      `internal/workflow` (loop/cancel/engine pacing), and the daemon tests
      that are store+logic only (candidates: dwell, countdown, retention,
      auto-settle pacing — triage decides).
- [x] Open long-lived resources (stores, DBs) outside the bubble — the spike's
      documented deadlock. `waitFor`/`fakeClock` stay for boundary-bound tests.

Success measure: converted tests keep their assertions, lose their sleeps, and
the flake classes they carried (load-sensitive poll loops like
`TestStartJobQueueArmsThePeriodicTicks`) are retired by construction.

## Track C — guidance for future agents (inline, small)

Add a short **Testing tools** note to AGENTS.md (near "Test safety") — the
rules exist and are validated; this makes them load-bearing (plan docs rot,
AGENTS.md is read every session):

- elapsed-time assertion or "this never happens" ⇒ `synctest.Test`; open
  DBs/stores outside the bubble; never bubble real sockets, PTYs, processes.
- stated invariant + large input space (especially sequence-dependent) ⇒
  `pgregory.net/rapid`; keep example tests; commit rapid's regression seeds.
- behavior that *is* the network being bad ⇒ embedded Toxiproxy helper
  (`newToxiProxy(t, upstream)`); never for what a fake can express.
- pointer to the spike plan's Findings for the full receipts.

~12 lines, no ceremony. Ship with whichever track lands first.

## Track D — insane options (priced, opt-in, not started)

Named per the boil-the-ocean rule; each needs an explicit go.

1. **Daemon-in-a-bubble.** Abstract the daemon's transport seam so harness
   tests run entirely in-memory, then run whole-daemon logic tests (state
   machine, turn lifecycle, bus projections) under synctest at production tick
   rates — deterministic integration tests, minutes of fake time in
   milliseconds. Unknown: whether `net.Pipe`-style in-memory conns count as
   durably blocked (they are channel-backed; a cheap probe answers it). Cost:
   a real seam through daemon startup — days, touches wiring. Payoff:
   the daemon's 194 sleep sites stop being the untestable majority.
2. **rr chaos mode on the orb VM for the open plugin-driver flake.** The
   close-notification drop (instrumented in #784, still unexplained) is
   exactly rr's shape: `rr record -h` randomizes scheduling to provoke the
   race, then replays it deterministically under a debugger. Cost: a
   half-day probe (does the VM expose perf counters?), then either gold or a
   documented dead end.
3. **Sometimes-assertions catalog.** Tiny `internal/testinv` helper: tests
   register "interesting state reached" marks; the suite reports marks never
   hit across a full run — vacuous-green detection as infrastructure. Cheap
   to build; the cost is the discipline of placing marks. Parked unless
   wanted.

## Boundaries

- Tracks are independent; no track blocks another. A/B1/B2 are delegation-
  sized; C is inline.
- Track A is the only one that changes production code; it follows full
  protocol/live-verification discipline. B1/B2 are test-only.
- No new dependencies anywhere (rapid and toxiproxy are already in).
- Test safety as always: `config.ScopeTestEnvironment`, never production
  `~/.attn`.

## Implementation Steps

- [ ] Track A: prompt-death fix + eviction-reason-on-reconnect (or documented
      fallback to fix 1 alone); extended toxiproxy test; live verification
- [ ] Track B1: three rapid property files (pty scanners, docstore,
      workspacelayout)
- [x] Track B2: triage map, then bubble-able conversions (two passes; candidate
      column at zero)
- [ ] Track C: AGENTS.md testing-tools note
- [ ] Track D: none until explicitly picked
- [ ] Changelog fragments per PR

## Decisions

- Toxiproxy's next target is Track A's verification, not the remote leg — the
  remote leg needs a second daemon to proxy to and stays a follow-up until we
  next touch hub/remote code.
- The synctest sweep is triage-first because raw sleep counts over-state it;
  the daemon's sleeps mostly sit behind real sockets where the bubble cannot
  go (that ocean is Track D option 1, priced separately).
- Frontend property testing (fast-check on `useDaemonSocket` correlation or
  reconnect logic) deferred — nothing there is currently flaking, and the Go
  side has higher invariant density per test written.

## Follow-ups

- Toxiproxy on the hub-transport/remote leg when that code is next touched
  (zero CI coverage today; the helper already takes any upstream).
- Frontend fast-check, if a frontend invariant bug ever surfaces.

## Track B2 triage map (2026-08-09)

A test counts as **paced** when its body sleeps, waits on `time.After`, or spins
a deadline-bounded poll loop.

The sweep ran in two passes. The first pass converted what was already known to
be safe and left a remainder of unverified candidates; the second pass took that
remainder to zero, either by converting a candidate or by giving it a reason.

**First pass** (the triage itself), measured on `test/synctest-sweep` against
its merge base. Tags are resolved through the fixtures a test calls, two levels
deep, so a test inherits the boundary its helper crosses.

| package | paced | converted | boundary-bound | candidate, not converted |
|---|---|---|---|---|
| `internal/daemon` | 199 | 20 | 59 | 120 |
| `internal/jobs` | 21 | 17 | 0 | 2 |
| `internal/pty` | 14 | 0 | 9 | 5 |
| `internal/ptybackend` | 11 | 0 | 8 | 3 |
| `internal/ptyworker` | 9 | 7 | 2 | 0 |
| `internal/daemonctl` | 6 | 0 | 6 | 0 |
| `internal/workflow` | 4 | 3 | 1 | 0 |
| **total** | **264** | **47** | **85** | **130** |

**Closing state**, counted on the tree itself rather than against a merge base,
so it can be re-derived at any time: a test is *bubbled* if its body contains a
`synctest.Test` call, and *still paced* if it does not and its body sleeps,
waits on `time.After`, or spins a poll loop.

| package | bubbled | still paced | of those, with a recorded reason | candidate |
|---|---|---|---|---|
| `internal/daemon` | 106 | 98 | 98 | 0 |
| `internal/jobs` | 21 | 0 | — | 0 |
| `internal/pty` | 0 | 14 | 14 | 0 |
| `internal/ptybackend` | 1 | 10 | 10 | 0 |
| `internal/ptyworker` | 7 | 2 | 2 | 0 |
| `internal/daemonctl` | 0 | 6 | 6 | 0 |
| `internal/workflow` | 3 | 1 | 1 | 0 |
| **total** | **138** | **131** | **131** | **0** |

The two scans differ by a handful per package — the first pass resolved a test's
tags through the helpers it calls, two levels deep, and this one reads bodies
only. Neither is wrong; they answer slightly different questions, and the
column that matters is the last one either way.

The population is the tree at `39c3c8c4`. Paced tests that landed on main after
that — `activity_test.go` (7, from the activity line) and `ws_eviction_test.go`
(4, Track A's own) — are not in it. A scan run later will find them; that is new
work arriving, not the sweep leaving something behind.

(The brief's raw `grep` counts — daemon 194, ptybackend 22, pty 21, ptyworker
12, jobs 10, daemonctl 7, workflow 6 — count *lines*, including sleeps inside
shared helpers and inside tests that are not otherwise paced. The table counts
tests.)

### The boundary rule, corrected

The plan assumed "most daemon sleeps sit behind real sockets/PTYs where the
bubble cannot go". That is not what the code says. Of the 48 daemon test
files containing paced tests, 34 carry no socket, PTY, or child-process signal
at all, and the ones that do are concentrated in `daemon_test.go`,
`testharness_test.go` and `plugin_worktree_test.go`.
The parallel D1 investigation (`docs/plans/2026-08-09-daemon-in-a-bubble.md`)
measured the same thing independently.

What actually pins a bubble to real time:

1. **A child process.** An outstanding `exec.Command` is a real fd nobody is
   durably blocked on; the fake clock stops advancing and the test silently
   runs at wall-clock speed (D1 measured 30.01s against 0.00s).
2. **fsnotify.** The watcher goroutine parks in `epoll`/`kqueue`, which is not
   durable blocking. Same failure.
3. **A real socket or PTY.** Same reason, and the case the plan named.
4. **Filesystem mtime.** A test that writes a file and needs the *real* clock
   to move for the mtime to differ (`tilecontent_test.go`) cannot use a fake
   one — the sleep is buying a wall-clock nanosecond, not a schedule.
5. **A CPU-bound goroutine.** `internal/workflow`'s `while(true){}` watchdog
   test spins in goja; a spinning goroutine is never durably blocked.

The second pass added three more, all found by converting a test and watching it
hang:

6. **A claim about a goroutine parked on a lock.** `go doc testing/synctest` is
   explicit that locking a `sync.Mutex` is *not* durable blocking. A test whose
   subject is "this writer is stuck behind that lock" — `doorbellMu`,
   `autoSettleFireMu`, the spawn lock, the evidence and trace tables — has no
   state a bubble can settle into, so `synctest.Wait()` hangs instead of
   returning. This is the largest single class in the remainder.
7. **An assertion on real elapsed wall-clock.** `delegation_operation_test.go`
   asserts a slow preparation took *more* than 100ms of real time. Under a fake
   clock that reads zero and the assertion is vacuous — converting it would
   quietly delete the test's only claim.
8. **A goroutine with no exit path.** `go d.wsHub.run()` loops forever over its
   channels. A bubble ends only when its last goroutine exits, so a test that
   starts the hub inside one never finishes (measured: hangs to the binary's own
   timeout). Giving the hub a quit channel is a production change, and this
   sweep is test-only.

Four house rules make a daemon fit inside a bubble; all four are written up
where they are used, in `internal/daemon/synctest_test.go`:

1. Build the daemon **outside** the bubble (`database/sql`'s `connectionOpener`
   goroutine never exits — the spike's documented deadlock).
2. Stop its background subsystems **inside** it, via a cleanup registered on the
   bubble's `T`.
3. Seed anything time-stamped **inside** it. The bubble clock starts at
   2000-01-01, so a fixture row stamped with real `time.Now` is dated decades
   ahead: a turn opened outside and settled inside settles *before* it opened
   and still reads as owed. This cost an hour of debugging on
   `TestAutoSettle_RealTimersSettleTheTurn`.
4. Let every goroutine the body started **finish** before the body returns —
   including one the code under test spawned and left sleeping. `handleStop`
   starts a classifier that retries the transcript read for 2s; a test that only
   cares about `handleStop`'s synchronous effects still has to run that window
   out (`settleStopClassification`) or the bubble reports blocked goroutines.
   The same shape appears wherever production polls: `handleDelegateWS` checks
   its operation every 100ms, and the spawn probe waits out its own 250ms read
   deadline. On a fake clock those windows are free — sleep past them.

One more, from D1 and worth repeating: `synctest.Wait()` is a happens-before
edge for the race detector, `time.Sleep` is **not**. State a timer writes still
needs its own lock.

### Converted in the second pass

87 more: 86 in `internal/daemon`, 1 in `internal/ptybackend`.

- `notebook_narration_test.go` (18), `notebook_daily_narrate_test.go` (10),
  `notebook_tasks_enabled_test.go` (6) — the cluster the first pass named as the
  biggest remaining win, and it was: the executor stub never execs, so every
  poll over job-queue state collapses to a `synctest.Wait()`.
- `plugin_rpc_test.go` (9), `plugin_supervisor_test.go` (7) — the open question
  was whether the plugin runtime spawns a real interpreter. It does not: these
  drive the RPC over `net.Pipe`, which is channel-backed and bubble-safe, read
  deadlines included. The supervisor tests now run the shipped restart backoff
  rather than a turned-down copy of it.
- `transcript_watcher_abort_test.go` (5) — the first pass filed these under
  fsnotify. They do not use fsnotify; the watcher polls the transcript on a
  ticker, which is exactly what a bubble is for.
- `workspace_keeper_test.go` (5) — context-compaction debounce and timeouts.
- `browser_test.go` (3), `reload_test.go` (3), `daemon_test.go` (3),
  `spawn_characterization_test.go` (2), `stop_terminality_test.go` (2),
  `ticket_buffer_test.go` (2), `tilecontent_test.go` (2), `ws_tasks_test.go` (2),
  and one each in `automations_test.go`, `bus_test.go`, `delegate_test.go`,
  `plugin_driver_test.go`, `session_state_characterization_test.go`,
  `workspace_test.go`, `ws_plugins_test.go`.
- `internal/ptybackend` — `TestWorkerStream_PublishOverflowWaitsForBufferSpace`,
  pure channels and a timer.

Two corrections to the first pass's classification came out of this:
`reload_test.go` drives a `fakeReloadBackend`, not `ptybackend` worker
processes — 3 of its 6 convert cleanly, and the other 3 are blocked by an
fsnotify watcher the *notebook root setting* starts, not by workers.
`tilecontent_test.go` is not wholly mtime-bound either; only one of its tests
needs the real clock to move.

New shared helpers, in `internal/daemon/synctest_test.go`: `requireOutbound` and
`requireNoOutbound`, which replace `select { case <-client.send: ... case
<-time.After(2s): }` with a settled read. The negative is the one that changes
meaning — outside a bubble "nothing was sent" is only ever "nothing yet".

`waitForNudgeDeadline`, `waitForNudgeDeadlineBefore` and `waitForBus` are gone.
`bus_test.go` in particular no longer turns `PollInterval` down to 5ms: it runs
the shipped `bus.DefaultPollInterval` and sleeps two periods of fake time.

### Converted in the first pass

- `internal/jobs` — all remaining paced tests (`runner_test.go` ×14 beyond the
  spike's two pre-existing call sites, `cron_test.go` ×3). `waitFor` deleted.
- `internal/workflow` — `cancel_test.go`, `concurrency_test.go` (the cap
  saturation probe, previously a 5s deadline poll over an atomic),
  `ordinal_test.go`. `waitForInFlight` deleted.
- `internal/ptyworker` — the seven `Runtime` self-stop tests. These now run the
  shipped TTLs: `exitedSessionCleanupTTL` 45s and `orphanedWorkerTTL` **12
  hours**, where the tests previously substituted 15–50ms and then waited a
  tolerance window on top.
- `internal/daemon` — 20 tests: `snooze_test.go` ×4, `nudge_countdown_test.go`
  ×4, `auto_settle_test.go` ×1, `gitstatus_test.go` ×6 (the whole scheduler
  family, now on the production 1s debounce / 30s safety / 2min slow-safety /
  5s slow threshold), `cancel_countdown_test.go` ×2, `ticket_notify_test.go`
  ×2, `periodic_cron_test.go` ×1.

`overrideGitStatusSchedulerForTesting`, `waitForGitStatusTestCondition`,
`waitForNudge`, `waitFor` and `waitForInFlight` are gone: the production
intervals no longer need turning down, so nothing needs to outrun them.

### Flake classes retired

- **`TestStartJobQueueArmsThePeriodicTicks`** ("`notebook_cron` is not armed",
  full-suite load only). `startJobQueue` hands the cron entries to the runner,
  whose dispatch goroutine writes them; the test read them straight afterwards
  and won that race only on an idle machine. `synctest.Wait()` makes the read
  happen after the write by construction. 20/20 clean.
- **Tolerance-window negatives.** Every "X must not happen" that was asserted
  by waiting 20–60ms — git-status refresh sharing, orphan-watch suppression,
  auto-settle arm phase, workflow cap saturation — now asserts against a
  settled system. These were not flaking loudly, but they were passing for the
  wrong reason on a fast machine and were the load-sensitive class.

### Boundary-bound, with reasons

Every paced test that is not converted is here. Where the reason is not obvious
from the file's imports it is also written above the test itself.

Child processes and real fds:

- `internal/daemonctl` (6/6) — every `stop_test.go` case forks a helper
  process to hold or release the pid lock.
- `internal/pty` (14/14) — real PTYs and read loops over real fds. The first
  pass listed 5 of these as candidates; all 5 go through `manager.Spawn` or an
  `os/exec` helper, so the correct count is 14.
- `internal/ptybackend` (10/11) — worker child processes and the control
  socket. `embedded_test.go`'s concurrent-close probe belongs here for a
  different reason: it needs 16 goroutines *actually running* while `Close`
  lands, and a bubble only advances once they are all parked.
- `internal/daemon/plugin_worktree_test.go` (14) — `exec.Command` per case.
- `internal/daemon/plugin_runtime_test.go` (3),
  `plugin_driver_e2e_test.go` (2), `codex_resume_e2e_test.go` (1),
  `real_agent_harness_test.go` (1) — real plugin and agent processes.
- `internal/ptyworker` (2/9) — `reap_test.go` waits on a real child dying,
  `runtime_test.go`'s theme RPC spawns a real PTY.

A started daemon (listener, WebSocket hub, or both):

- `internal/daemon/daemon_test.go` (31) and `testharness_test.go` (5).
- `internal/daemon/documents_socket_test.go` (1),
  `toxiproxy_slow_client_test.go` (1) — a real socket is the subject.
- `internal/daemon/stop_terminality_test.go` (2), `ws_tasks_test.go` (1),
  `workspace_context_test.go` (1), `fs_test.go` (7) — `go d.wsHub.run()`
  (rule 8). `fs_test.go` also carries fsnotify.

fsnotify:

- `internal/daemon/fs_watch_test.go` (5), `notebook_test.go` (6).
- `internal/daemon/reload_test.go` (3) — not worker processes, as the first pass
  had it: setting the notebook root starts a watcher.

Lock-parking claims (rule 6):

- `internal/daemon/spawn_lock_test.go` (2), `ws_automations_test.go` (2),
  `auto_settle_test.go` (1), `session_evidence_test.go` (1),
  `state_trace_test.go` (1), `ticket_buffer_test.go` (1),
  `nudge_countdown_test.go` (1).

One-offs:

- `internal/daemon/tilecontent_test.go` (1) — real mtime granularity (rule 4).
- `internal/daemon/automations_test.go` (1) — `httptest` server, a real socket.
- `internal/daemon/gitstatus_test.go` (1) — git child process.
- `internal/daemon/delegation_operation_test.go` (1) — asserts real elapsed
  wall-clock (rule 7).
- `internal/workflow/loop_test.go::TestContextCancelInterrupts` — CPU-bound
  goja loop by design.
- `internal/daemon/ticket_notify_test.go::TestChiefTicketContinuityAcrossRoleTransfer`
  — bubble-able, parked on purpose; see below.

### Where the 130 candidates went

The first pass left 130 tests classified by static analysis and nothing else.
Every one now has a verdict from running it:

| outcome | count |
|---|---|
| converted and green under `-race -count=20` | 76 |
| reclassified boundary-bound, reason recorded | 52 |
| not paced after all (`internal/jobs` scanner artifact) | 2 |

The 87 conversions in this pass are 76 of those candidates plus 11 tests the
first pass had filed as boundary-bound for a reason that did not hold —
`transcript_watcher_abort_test.go` (5), `reload_test.go` (3),
`tilecontent_test.go` (2), `internal/ptybackend` (1).

Of the 52 reclassifications, 9 are the lock-parking class the first pass had no
name for, and the rest resolve to signals it already knew: a started hub, a real
socket, fsnotify, a child process.

One test stays parked on purpose, unchanged:
`ticket_notify_test.go::TestChiefTicketContinuityAcrossRoleTransfer`. It is
mechanically bubble-able. Its final assertion ("the retired chief received no
role nudge") holds only while the nudge window is parked; run the window out for
real and the retired chief *is* doorbelled, because the fixture leaves it a
personal participant on the ticket and `nudgeCount` cannot tell a personal nudge
from a role one. That needs a narrower probe or a product answer, both outside a
test-only sweep. The reason is recorded above the test.

### Timings

`go test -count=3`, same machine, sequential, `test/synctest-sweep` against its
merge base.

Whole packages:

| package | before | after |
|---|---|---|
| `internal/jobs` | 0.43 / 0.46 / 0.57s | 0.53 / 0.54 / 0.60s |
| `internal/workflow` | 4.15 / 5.00s | 3.76 / 4.01s |
| `internal/ptyworker` | 3.66s | 2.86s |
| `internal/daemon` | 425.6s | 457.5s |

The whole-package daemon number is noise: 19 converted tests out of ~1200, and
the run-to-run spread on a 7-minute package swamps the delta. The number that
means something is the converted subset alone:

| subset | before | after |
|---|---|---|
| the 20 converted daemon tests | 9.43s | 2.09s |
| the 7 converted ptyworker tests | 1.70s | 0.33s |

`internal/jobs` is flat because its paced tests already ran on an injected
`fakeClock` — the spike traded a fake clock for a fake *world*, not for speed.
That is the honest summary of the whole sweep: it buys determinism and
fidelity (production intervals actually execute), not wall-clock.

#### Second pass

Same machine, `go test -count=3`, this branch against `39c3c8c4`.

| subset | before | after |
|---|---|---|
| the 86 converted daemon tests | 94.6s | 6.0s |
| `internal/jobs` whole package | 0.68s | 0.91s |
| `internal/ptybackend` whole package | 6.46s | 5.48s |
| `internal/daemon` whole package | 403.6s | 327.0s |

The subset number is the one to read: 94.6s of it was tolerance windows and poll
loops, and 88.6s of that is gone. The whole-package figure moves this time —
86 tests is enough to show through — but a 6-minute package still has minutes of
run-to-run spread, so treat the 77s as directionally right and not as a receipt.

`internal/jobs` grows slightly because it gained a test rather than converting
one: the retention pass below now runs 25 hours of fake time.

### Finding: the retention pass had never run in a test

`TestRetentionTrimsCompletedJobsAndKeepsDeadOnes` went red on conversion. The
runner's own hourly retention ticker really fires under a fake clock — 48 times
across the test's 48-hour window — and trimmed the record before the manual
`Trim` under test could. Under the old `fakeClock` that ticker ran on *real*
time and never fired at all. The conversion parks it (`TrimInterval` 30 days)
to keep the subject singular, but the discovery stands: the periodic retention
pass has no test of its own. Worth one.

**Written in the second pass.**
`TestThePeriodicRetentionPassTrimsOnItsOwnInterval` (`internal/jobs`) is that
test. It leaves `TrimInterval` at the shipped `DefaultTrimInterval` — one hour,
the value that had never executed — and a 24-hour retention policy, then runs
the bubble clock forward and asserts three things the ticker owes: through 23
ticks it trims nothing, the tick after the policy window trims exactly the aged
completed job, and it leaves the young completed job and the dead one alone. The
runner's own log line is the receipt that the pass ran once, not zero times and
not twice: `jobs: trimmed 1 completed job(s) older than 24h0m0s`.

Mutation-checked by setting `DefaultTrimInterval` to 24h in production code:
three assertions fail. The production change was reverted.
