# A4 landing-arc review fixes

An adversarial review of the A4 app-registry/runtime arc as landed on main
(#843 `4fbd6f71`, #858 `cc49fd95`, #859 `b873da05`) produced 19 candidate
findings; 9 died against the code. This is what we do about the rest.

The review report itself — every confirmed finding with its evidence, and the
refutations, which are the more useful half — is the `a4-arc-review.md` artifact
on ticket `journey-review`. This doc is only the disposition.

Everything lands in one PR to `main`. Two of these change when an app gets
auto-disabled, which is existing behavior, and splitting them across an epic
would have meant reviewing the same diff twice.

## 1 — the hub starved the step that revives a remote (high)

`EnsureRemoteReady` shipped the ~93.7MB app runtime host *before*
`ensureRemoteDaemonRunning`, both out of one 60-second budget the manager
imposed at both call sites — including the one taken when the remote's daemon is
down. On a link under ~20 Mbit/s the upload consumed the budget and execution
never reached the revive, so the endpoint loop retried roughly every 90 seconds
forever. Each attempt left up to 93.7MB in the remote's `/tmp`: the staging name
carries pid+UnixNano so retries accumulate rather than overwrite, there was no
cleanup on any error path, and `/tmp` is tmpfs on many systemd distros.

A regression: before #859 a down remote whose binary already matched went
straight to starting its daemon.

**Fix.** Two phases, in the order that matters, each with its own budget:

- `makeRemoteReady` — platform, versions, the attn binary, enrollment, daemon
  start and its readiness wait. This is everything the remote needs to host
  sessions.
- `shipRemoteAppRuntime` — the sidecar, content-gated as before. It runs only
  after the daemon is up, and its failure is reported rather than returned,
  because a remote without a sidecar still runs sessions.

The manager's blanket 60s is gone; the phases own their deadlines, since one
number could only ever be right for one of them.

### The receipts behind the two budgets

Sized as tripwires past the slowest link anyone would sync a remote over —
5 Mbit/s, a bad tether — not around a measured happy path. The previous 60s was
sized for a ~40MB payload and #859 grew it to ~152.6MB without moving it, which
is what made a healthy case reach it.

| term | measured | at 5 Mbit/s |
| --- | --- | --- |
| attn binary (`attn-linux-arm64`) | 58,934,608 B | 94s |
| app runtime host (`linux_arm64`) | 93,694,096 B | 150s |
| daemon readiness wait | ≤ 35s (`remoteDaemonReadyTimeout`) | — |
| sha256 of the runtime, local | 0.17s | — |
| sha256 of the runtime, remote | 0.38s | — |
| `bun build --compile` of the runtime | 0.13s | — |
| bare ssh round trip (local VM) | 0.03s | — |

`remoteReadyBudget = 180s` (94 + 35 + round trips). `appRuntimeShipBudget =
300s` (150 + hashing + install + a possible runtime restart, generously).

Generosity costs nothing on the broken path: every ssh here carries
`ConnectTimeout=10`, so an unreachable remote fails in seconds regardless.

`TestRemoteBudgetsCoverTheArtifactsTheyCarry` fails if either budget stops
covering the artifact sizes recorded in it. It does not measure the artifacts, so
it does not notice one growing by itself — the tripwire is that a sidecar which
outgrows its budget has to update those constants to pass, and updating them is
when someone reads the arithmetic.

### Carried with it

- **The staging file is removed on every path out**, not only after a successful
  install.
- **A short upload is refused rather than installed.** ssh reports the far end's
  exit status, which says the shell ran, not that every byte arrived. The `wc -c`
  probe already existed and was only being logged; it is now compared.
- **A replaced sidecar bounces the sidecar, not the daemon.** `binariesUpdated`
  used to fold the runtime in and restart the whole remote daemon. The runtime
  is asked whether it is running and restarted only if so — starting it here
  would put a Bun process on a remote that may host no enabled app at all.

### What the OrbStack VM cannot witness

The local VM transfers 90MB in 0.24s (~3 Gbit/s), so it can prove the path works
and can never reproduce the failure. The ordering and budget-independence
properties are therefore unit-tested against the phase seam, and the live run
covers the real transfer, the cleanup, and the short-copy refusal.

## 2 — the shared runtime blames the wrong app (medium)

A non-yielding synchronous handler in one app blocks the Bun event loop for
every app. The 60s dispatch timeout then fires for apps whose handler was never
reached, `noteAppFailure` charges them, and the 15-minute stall clock
auto-disables innocent apps with a notification telling the user to fix a
handler that never ran.

`app.runtime.ping` was built for exactly this — "a liveness answer the daemon
can ask for without running app code" — and had zero callers in Go.

**Fix.** A dispatch that hits its timeout now asks before it charges anyone:

- the ping answers → the loop is turning, nothing is in this app's way, and its
  own handler is what did not return. Charged as before.
- the ping is silent → the loop is frozen. The handler holding it is charged;
  everyone else is recorded as a runtime failure with its clock cleared, and
  their error names the culprit so the reader goes and looks at the right code.

`appRuntimePingWait = 2s` is a tripwire past a localhost round trip measured at
344µs and 416µs on a live daemon — ~5,000× — and is only ever spent on a dispatch
that has already burned its whole timeout, so generosity costs nothing that was
not already lost.

### Which handler is holding the loop is the host's to say

The first build of this answered "the app that entered first", read off the
daemon's own dispatch order. Live verification falsified it. Three apps were
dispatched into a runtime frozen by `hog`, a 120s synchronous spin; the daemon
charged `bystander`, whose handler is `await ctx.collections.seen.put(...)` — the
documented shape — and let `hog` off with a runtime failure.

Dispatch order is not the order handlers hold the loop. A handler that awaits an
attn API yields immediately, so it is still unanswered when a spinner dispatched
after it freezes everything, including that first handler's own reply. Blaming
the earliest dispatch therefore charges the best-behaved app in the common case,
which is a worse failure than charging everyone.

The daemon cannot work this out; only the host knows which handler it called. So
the host announces each entry on the socket immediately before invoking the
handler (`app_runtime.entered`, a notification — nothing to answer), and the
culprit is the most recent entry with no answer yet. An entry is dropped when its
dispatch is answered, when a ping answers, or when the process dies.

An answered ping does not prove no handler is on the loop — a yielded handler
that never settles is still there. It proves nothing is *holding* it, which is
what makes the wipe safe: attribution is moot while the loop turns. The cost is
one corner. If a handler yields, has its entry wiped by a ping, and only then
resumes and spins, there is no entry naming it and attribution falls back to
charging whoever timed out, until some new dispatch enters and is announced.

The receipt this depends on: a Bun socket write issued immediately before a
synchronous spin reaches the peer at once rather than queueing behind it —
measured with a 5s spin, the frame arrived ~1ms after the write and ~5s before
the spin ended. Without that, the announcement would arrive too late to be the
witness, and the design would not work at all.

`appRuntimeAPIVersion` goes to 2: the daemon↔host wire gained a frame, and the
two are version-checked at hello because they ship together.

## 3 — one app's unhandled rejection kills the sidecar for everyone (medium)

No `unhandledRejection`/`uncaughtException` handler exists under `apphost/src`,
and `dispatch.ts` wraps only the awaited frame. A floating rejection answers
`{ok:true}`, then takes the process down for every app. Because a sidecar-wide
crash is deliberately never attributed to an app — the ruling in the A4 plan
doc — nothing is ever charged, so the one app the auto-disable rule exists to
stop is structurally exempt from it.

**Fix, and a ruling overturned.** The plan's premise — "a whole-process death
cannot name a culprit" — is false, and its conclusion made the worst thing an
app can do the one thing it could never be disabled for. The reversal is
recorded in the A4 plan doc beside the original ruling.

The host installs `unhandledRejection` and `uncaughtException` handlers, names
the app by matching the loaded bundles' content-addressed paths against the
error's stack, reports it to the daemon and then exits. The daemon counts
strikes: `appCrashStrikes = 3` inside `appCrashWindow` (= `appAutoDisableStall`,
so the auto-disable rule keeps one duration rather than two) disables the app
through the same three effects as a stall.

Why the stack and not who was running: a floating promise rejects long after its
dispatch returned, so "which app is running" routinely names an innocent —
reproduced with app A's stray rejection surfacing while app B was mid-handler.
Bun offers nothing else. Measured: `AsyncLocalStorage.getStore()` is `undefined`
inside both handlers, and `async_hooks.createHook` fires no callbacks at all
(`init types seen: {}`). The stack does carry the bundle path. A crash whose
stack names no app is charged to nobody, which is the original ruling's real
content and survives.

Why three: supervise parks the whole sidecar after `DefaultGiveUpAfter` (10)
restarts with no stability window — roughly two to three minutes of
crash-looping — and every app losing its runtime is the harm being prevented, so
the culprit has to go first. Above one, because a single crash can be a machine
event whose stack merely passes through an app's bundle.
`TestCrashStrikesFireBeforeTheSupervisorParksTheRuntime` fails if that ordering
is ever broken.

The strikes are in memory, for the same reason the stall clock is: they measure
what is breaking now, and a daemon restart genuinely does grant a fresh window
against a runtime that has also just restarted. Enabling the app clears them —
the way back has to actually be a way back.

## 4 — papercuts

- `attn app status` printed "runtime: not started — no enabled app has been due
  a fact since this daemon came up" on a daemon where the binary is missing and
  every dispatch is failing. False on both halves. It now names no cause,
  because from the CLI the two are genuinely indistinguishable — `resolveAppRuntimeHost`
  fails before the supervisor is touched, so a missing binary and a quiet daemon
  both arrive with no snapshot — and points at `attn app runtime status`, which
  resolves the binary itself.
- `attn app rollback <name> <bad>` pointed at `attn app status <name>` to list
  version ids, which only ever printed a count. Fixed at the root rather than in
  the copy: `AppStatusResult` gained `recent_versions` (protocol 233) and status
  prints the ids with the serving one marked. Capped at ten with the total count
  beside it — an app under `attn app dev` accumulates versions, and an
  unbounded list on a status call is the next landmine.
- Four comments that described something the code does not do (the runtime log's
  bound, what registers a consumer, when `declareAppCollections` runs, whose job
  collection declaration is), plus the A4 plan's "endpoints sync on a timer" —
  no ticker drives a bootstrap; a reconnect does.

## Deferred, with the reason

- **`apply` answers Ok when post-flip consumer wiring failed.** A real
  stated-contract seam — `app_apply.go` promises the reply means the new version
  is already serving, and `registerAppConsumer`'s error is only logged. Left
  alone: the only non-store error on that path is a Marshal→Unmarshal round trip
  of the same struct, the state is visible as "consumer: none" in `attn app
  status`, and it self-heals on re-apply or restart. The honest fix is a warning
  field on `AppApplyResult`, which is a protocol bump; returning an error would
  be worse, because the version is committed and serving by then.
- **The sidecar's module cache grows per applied version.** Measured ~30KB per
  version for scaffold-shaped bundles — 500 `attn app dev` saves is ~15MB — and
  `attn app runtime restart` reclaims it. Steady state grows zero. No receipt for
  harm.
- **The unsuffixed sidecar fallback.** Deliberate and tested: a checkout's
  `./attn` serves whatever profile is exported. Only the hub's copy about it was
  wrong (it promised apps park when they may instead run another profile's
  host), and that is fixed with the wording.
- **No `ControlMaster` on the hub's ssh calls.** One bootstrap opens roughly ten
  separate connections, each paying TCP+KEX+auth and an `sh -lc` login shell,
  and the readiness poll adds one per probe. Worth doing, unrelated to these
  findings, and it needs a socket path and its cleanup.

## Out of scope, verified, not ours to fix here

Both predate this arc and #859 explicitly declined to repeat the scheme:
`~/.attn/remotes/binaries` is 2.1GB across 63 per-build directories with nothing
trimming it, and `~/.attn/workers` is 11G across 1,369 logs with a single 2.9GB
file.
