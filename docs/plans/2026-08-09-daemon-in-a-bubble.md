# Design: daemon in a bubble — a transport seam for deterministic daemon tests

## Question

The testing spike (`2026-08-09-testing-spike-synctest-rapid-toxiproxy.md`,
merged as #799) adopted `testing/synctest` and recorded one limit: a goroutine
blocked on a real fd is not durably blocked, so fake time never advances and
nothing touching a real socket can run in a bubble.

This asks the ocean-boiling follow-up: give the daemon an in-memory transport
so whole-daemon logic tests run under synctest at production tick rates.

## Answer, up front

**The probe says yes. The design says no — build the seam nowhere, and take
the bubble anyway.**

In-memory transports are fully bubble-compatible, including attn's exact
WebSocket library. The seam is buildable. But it targets a bottleneck attn does
not have: **26 of 1217 `internal/daemon` tests stand up a listener**, and those
26 run the real `Daemon.Start()`, which spawns a login shell and `gh` — and an
outstanding child process pins the bubble clock to real time (measured: 30.01s
versus 0.00s). The seam would not deliver its own prize without also gutting
`Start()`, which is precisely the leak of test concerns into production wiring
that AGENTS.md rules out.

Meanwhile the thing actually blocking daemon tests from a bubble is one
goroutine `store.New()` starts, and it costs **zero production changes** to
avoid. With that one move, the slowest test in the package went from 10.54s to
0.06s while catching the same mutation, and a 65-second production turn
lifecycle became assertable in 30ms.

Recommendation: **adopt the bubble, decline the seam.** Details and the
condition that would reopen it are in [Recommendation](#recommendation).

---

## 1. The probe — is in-memory transport bubble-compatible?

Throwaway module, Go 1.25.3, M-series Mac. Every result below is from a probe
run for this design; the probes are disposable and are not committed.

### The transport itself: yes, completely

| # | Probe | Result |
|---|---|---|
| P1 | Goroutine blocked on `net.Pipe` **Read** | Durably blocked. A 1h sleep elsewhere completed; fake elapsed exactly `1h0m0s` |
| P2 | Goroutine blocked on `net.Pipe` **Write** (unbuffered) | Durably blocked; exactly `30m0s` |
| P3 | `synctest.Wait()` with a pipe reader parked | Returns. "Nothing was read yet" is assertable |
| P4 | `SetReadDeadline` on a `net.Pipe` | Fires on the **bubble** clock — exactly `5s` fake, no real cost |
| P5 | *Control:* goroutine blocked on a real **TCP** read | Bubble never advances. A 1s sleep hung until the 25s test timeout |

P4 is the one worth calling out: `net.Pipe`'s deadlines are implemented on Go
timers, so they are bubbled too. A production read timeout is exercisable in
fake time — not merely tolerated.

### The listener: yes

`memListener` is ~40 lines — an unbuffered `chan net.Conn`, `Accept` selecting
on it and a closed channel, `Dial` handing over one half of a `net.Pipe`.

| # | Probe | Result |
|---|-------|--------|
| P6 | Goroutine parked in `Accept()` on the channel-backed listener | Durably blocked; fake `2h` advanced past it |
| P7 | Real `net/http` **Server + Client** over `memListener` | Full request/response. The handler observed bubble time (`2000-01-01T02:30`) |
| P8 | `http.Server.Shutdown` inside the bubble | Clean; `Serve` returned, no residue |
| P9 | **`nhooyr.io/websocket` v1.8.17** — attn's exact version — handshake over `memListener`, server pushing a 30s heartbeat | Two frames at fake `01:00:30` and `01:01:00`; elapsed exactly `60s`. **3/3 runs, zero real time** |

P9 is the gate, and it passes with room to spare: the real handshake, the real
frame codec, the production heartbeat interval, deterministic.

### Two findings that cost more than they look

**A bubble demands a complete shutdown.** P9's first run failed — every
assertion passed, then `panic: deadlock: main bubble goroutine has exited but
blocked goroutines remain`, naming `Serve`'s `Accept`. This is the same class
as the spike's `store.New()` trap. Any goroutine still parked when the body
returns is a hard failure. That is a *feature* — it makes goroutine leaks
non-ignorable — but it is a real bill, paid per test, and it is paid in
teardown code that today's tests do not have.

**`synctest.Wait()` is a happens-before edge. `time.Sleep()` is not.**

| # | Probe | Result |
|---|-------|--------|
| P10/P11 | Unguarded var written by a bubbled timer/goroutine, read after `synctest.Wait()` | Clean under `-race`, 10/10 |
| P12 | Same var **read**, then slept past the write, then `Wait()` and read again | **DATA RACE**, immediately |

So a bubble does not serialize anything; it only fakes the clock. State written
by a timer callback still needs its own lock, and the failure mode is a read
that happens *before* a write, which reads like sequential code. My own first
daemon probe had exactly this bug.

The upside: in a bubble the race is deterministic rather than a one-in-a-hundred
CI flake. `-race` stays mandatory, and it becomes far more useful.

### The hard boundary: child processes

| # | Probe | Wall clock |
|---|-------|-----------|
| P13 | Short subprocess (`/bin/echo`), then a fake 1m sleep | 0.01s — fine |
| P15 | Child running `/bin/sleep 30`; main sleeps a **fake hour** then kills it | **30.01s** |
| P16 | Control: identical bubble, no child | **0.00s** |

`cmd.Wait()` blocks on a real process. While any child is outstanding, the
bubble is not "all durably blocked" and **fake time does not advance at all**.

This does not error. It silently degrades a bubbled test to real-time pacing —
the worst possible failure mode, because the test still passes and still looks
deterministic. It is the single most important constraint in this document.

---

## 2. The seam map — where attn's daemon actually meets the world

| Boundary | Where | Bubble verdict |
|---|---|---|
| Unix command socket | `daemon.go:968` `net.Listen("unix", …)` | Swappable (P6/P7 shape). **Barely used by tests** |
| WebSocket listener + hub | `daemon.go:2025` `net.Listen("tcp", …)`, `websocket.go` | Swappable, proven end to end (P9) |
| SQLite | `store.New()` / `store.NewWithDB` | **Already fine** — but must be *opened outside* the bubble (see below) |
| Filesystem reads/writes | transcripts, notebook, fs | Already fine. Transient blocking |
| **fsnotify watchers** | `internal/notebook/watcher.go` | **Real kqueue fds — excluded.** A boundary the brief did not list |
| **Child processes** | login shell, `gh`, `git`, PTY worker, plugin drivers | **Excluded, and toxic** — P15: pins the clock silently |
| Real PTYs | `internal/ptyworker` | Excluded (a child process) |
| Time, timers, tickers | everywhere | The prize |

### The blocker is not a socket

`store.New()` opens an in-memory SQLite through `database/sql`, which starts a
`connectionOpener` goroutine that lives as long as the DB. Built inside a
bubble it joins the bubble and never exits:

```
panic: deadlock: main bubble goroutine has exited but blocked goroutines remain
goroutine 39 [select (durable), synctest bubble 1]:
database/sql.(*DB).connectionOpener(...)
```

Every `NewForTesting` daemon carries one. This — not any socket — is what stops
`internal/daemon` tests from running in a bubble today. The fix is to construct
the daemon *outside* `synctest.Test` and drive it inside. No production change,
no seam, no interface.

### Why the seam would not pay for itself

The tests that would need it call the real `Daemon.Start()`
(`daemon_test.go:203, 237, 271, 294, 349, 384, 422, 516`). `Start()`
unconditionally launches:

- `go d.warmLoginShellEnvCache()` → `pty.ReadLoginShellEnv` execs a login shell
- `go d.refreshGitHubHosts()` → `github.RequireGHVersion` execs `gh`
- the worker PTY backend startup probe (a child), unless disabled
- plugin supervisors, the PID lock, backups, sweeps, hub manager

By P15, each outstanding child pins the bubble clock to real time. A transport
seam gets you a bubbled listener and a daemon that still paces on the wall
clock — determinism theatre. Making it real means inverting the process
boundary too: a `ProcessRunner` interface threaded through PTY spawn, git,
`gh`, plugins and the login-shell warm, plus a `FileWatcher` interface for
fsnotify. That is dependency inversion of every boundary the daemon has, in a
4,067-line `daemon.go`, so that ~26 tests can be deterministic.

Said out loud, since it should be: the maximal version is a `Daemon` that takes
`Transport`, `ProcessRunner`, `Clock` and `FileWatcher` at construction and
never touches the world directly. It is a coherent design and it is what a
simulation-tested system looks like. It is also weeks of work on the most
load-bearing file in the repo, it makes every production wiring path read
through an indirection that exists for tests, and the thing it buys is already
90% available for free. Priced and declined.

---

## 3. The harness — what it actually looks like

`internal/daemon/testharness.go` is **not** the thing to extend.
`NewTestHarnessBuilder` has **5 call sites** in 146 test files;
`NewForTesting` has **582**. The harness is nearly vestigial. The real
convention is "construct with `NewForTesting`, drive methods directly, assert
on the store and on hooks."

So the bubble harness is not a harness. It is one helper and a discipline:

```go
// Build outside the bubble: store.New() starts a database/sql goroutine that
// lives as long as the DB, and anything born in a bubble must die in it.
func bubbleDaemon(t *testing.T) *Daemon {
	t.Helper()
	return NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
}
```

Used with two rules: **long-lived resources outside, code under test inside**,
and **stop inside the bubble what you started inside it** (`defer` within the
body — `t.Cleanup` runs too late).

### The worked example from the brief, verified

"A session goes working, a turn opens, the countdown fires at its production
interval, the turn settles." Auto-settle is a 30s arm plus a 15s countdown,
with a 5s quiet window that holds the countdown while the user interacts.
Today's test (`TestAutoSettle_RealTimersSettleTheTurn`) cannot afford that: it
pins both windows to `"1"` — below the documented 5s/3s floors, so silently
clamped — and polls for 5 real seconds.

Bubbled, at the real defaults, with nothing configured:

```go
func TestTurnLifecycleAtProductionConstants(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "auto-settle.sock")) // outside
	synctest.Test(t, func(t *testing.T) {
		defer d.stopAutoSettleTimers()
		// … seed a waiting_input session, enable auto-settle, open its turn …
		d.applyState(sessionStateChange{sessionID: id, state: protocol.StateWorking, cause: liveSignal{}})

		time.Sleep(29 * time.Second)           // still arming
		synctest.Wait(); assertOwed(t, d, id)

		time.Sleep(15 * time.Second)           // t=44s, countdown running
		synctest.Wait(); assertOwed(t, d, id)
		d.noteAutoSettleActivity(id)           // the user touches the session

		time.Sleep(2 * time.Second)            // t=46s, past the original deadline
		synctest.Wait(); assertOwed(t, d, id)  // the hold worked

		time.Sleep(13 * time.Second)           // t=59s, quiet released at 49, fresh countdown
		synctest.Wait(); assertOwed(t, d, id)  // resumed countdown runs FULL, by design

		time.Sleep(6 * time.Second)            // t=65s
		synctest.Wait(); assertSettled(t, d, id)
	})
}
```

**65 seconds of production behavior, 10 runs under `-race` in 2.16s total.**

Note the second-to-last assertion. A held countdown deliberately resumes *full*
rather than with its remainder ("a frozen bar is drawn full", `auto_settle.go`).
That rule is documented in a comment and asserted nowhere, because asserting it
costs a minute of real time. My first draft of this probe got it wrong and the
bubble caught me. **That is the return: not speed, testability of rules we
currently only write down.**

No transport. No seam. No production change.

---

## 4. Cost

### For the recommended path (bubble, no seam)

- **Production code changed: none.**
- Per converted test: build the daemon outside `synctest.Test`, `defer` the
  stops inside, replace poll loops with `synctest.Wait()`, delete the shortened
  window overrides. Typically a smaller test than the one it replaces.
- Two new failure modes to learn, both listed above (teardown completeness; the
  `Sleep`-is-not-an-edge race rule). Both are loud and deterministic, not flaky.
- Migration is per-test and coexists by construction — `synctest.Test` is a
  wrapper, so a converted test and an unconverted one sit in the same file.
  No big bang, nothing to switch over.
- Deletable once their last user converts: `nudgeWindowOverride`,
  `ticketBufferWindowOverride`, and hand-fire helpers like `fireNudgeNow` —
  production fields and test helpers that exist only to dodge real time.
  Observation hooks (`nudgeFireHook`, `autoSettleFireHook`, …) stay: synctest
  fakes the clock, it does not observe anything.

### For the seam (not recommended)

- New `Transport`/`Listener` abstraction across `daemon.go`'s two `net.Listen`
  sites, `initHTTPServer`, `listenHTTP`, `runHTTPServer`, `Stop`, plus dialing
  in `cmd/attn` and the hub.
- **And**, to actually get fake time, a `ProcessRunner` seam through PTY spawn,
  git, `gh`, plugin supervision and the login-shell warm; plus a `FileWatcher`
  seam for fsnotify.
- Payoff without the process seam: bubbled sockets, real-time pacing. Nothing.

---

## 5. What this unlocks, and against what

`internal/daemon`: **1217 tests, 112.7s** serial. 34 tests take ≥0.5s and
account for **68.1s — 60% of the package's wall clock.** Classified against the
boundary map:

| Class | Tests | Wall clock | Verdict |
|---|---|---|---|
| **A. Pure time** — transcript-watcher halts, auto-settle, daily-narrate, reload | 10 | **≈31s** | **Bubble today, no seam** |
| **B. fsnotify** — `fs_watch`, notebook watchers | 10 | ≈9s | Needs an FS-event seam. Separate, smaller question |
| **C. Socket/WS** — `daemon_test.go`, `stop_terminality`, `testharness` | 11 | ≈15s | The seam's target. Blocked anyway by `Start()`'s children |
| **D. Real subprocess** — plugin driver, codex resume, toxiproxy, delegate | 4 | ≈12s | Permanently excluded; toxiproxy deliberately so |

The transport seam's specific prize is **Class C: ~26 socket-bound test
functions (2% of the package), ~15s** — and it does not collect even that
without the process seam.

Against Track B2's triage: **no overlap, and no competition.** B2 converts
store and logic tests. Class A is daemon-side, socket-free, and available with
the same technique B2 is already using. This design's contribution is the rule
that makes Class A reachable — *build outside, drive inside* — which B2 can
adopt immediately.

### The proof, measured

`TestAHaltFromBeforeTheSessionStartedIsIgnored`, the slowest test in the
package, converted with no production change:

|  | Original | Bubbled |
|---|---|---|
| Passing run | **10.54s** (10.88, 10.88, 10.95 repeated) | **0.06s** |
| 20 runs under `-race` | — | **2.76s total** |
| Against a mutant (`staleTranscriptAbort` guard disabled) | Fails, 3/3 subtests, **10.87s** | Fails, 3/3 subtests, **0.49s** |

Same assertions, same mutation caught, **176× faster**, and the "nothing
happened" claim is now backed by `synctest.Wait()` — the watcher has nothing
left to do — rather than by a 2-second sleep and hope.

Two more, at production constants that no current test can afford: the nudge
countdown firing at its real 30s window with `nudgeWindowOverride` deleted, and
the keystroke splice guard's full `rearm → doorbell` sequence — which today is
asserted by hand-firing the timer because the real one cannot be waited on.

---

## Recommendation

**No-go on the transport seam. Go on the bubble.**

1. **Do not build a transport seam.** It is technically sound and it solves a
   problem attn does not have. Revisit only if the daemon's socket-bound test
   count grows a lot, *and* the process boundary has been inverted for some
   other reason — the seam is worthless before then.
2. **Adopt "build outside, drive inside"** as the rule that extends the spike's
   synctest verdict to `internal/daemon`. One helper, no production change.
3. **Convert Class A opportunistically** — the transcript-watcher halt tests
   first; they are the slowest in the package and the conversion is mechanical.
   Not a sweep: convert when a test is touched or flakes, as the spike already
   recommends.
4. **Write new time-dependent daemon tests bubbled from the start**, at
   production constants. That is where the real return is: rules like the
   full-countdown resume become assertable instead of merely commented.
5. **Treat child processes as the exclusion criterion**, not sockets. A test
   that spawns anything gets no fake time and no warning. Worth a line in
   `AGENTS.md` next to the existing synctest guidance.
6. **Class B (fsnotify) is a separate ticket** if its ~9s ever justifies one.

### Open question for discussion

The teardown bill is the one thing I would want a second opinion on before
converting in volume. A bubble turns every surviving goroutine into a hard
failure, which means each converted test must stop exactly what it started. On
today's tests that is one or two `defer`s. On a test that drives more of the
daemon it could grow into a "stop everything" helper that is really `Stop()`
minus the parts that touch the world — and if that helper starts drifting from
`Stop()`, it becomes its own landmine. Worth watching from the first few
conversions rather than designing for now.
