# A4 landing-arc review fixes

An adversarial review of the A4 app-registry/runtime arc as landed on main
(#843 `4fbd6f71`, #858 `cc49fd95`, #859 `b873da05`) produced 19 candidate
findings; 9 died against the code. This is what we do about the rest.

The review report itself — every confirmed finding with its evidence, and the
refutations, which are the more useful half — is the `a4-arc-review.md` artifact
on ticket `journey-review`. This doc is only the disposition.

Findings land on `epic/a4-review-fixes` in three PRs, because two of them change
when an app gets auto-disabled and that is existing behavior worth reviewing as
one diff.

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
covering its artifact, so growing the sidecar again cannot silently re-create
this bug.

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

## 2 — the shared runtime blames the wrong app (medium, PR 2)

A non-yielding synchronous handler in one app blocks the Bun event loop for
every app. The 60s dispatch timeout then fires for apps whose handler was never
reached, `noteAppFailure` charges them, and the 15-minute stall clock
auto-disables innocent apps with a notification telling the user to fix a
handler that never ran.

`app.runtime.ping` was built for exactly this — "a liveness answer the daemon
can ask for without running app code" — and has zero callers in Go.

## 3 — one app's unhandled rejection kills the sidecar for everyone (medium, PR 2)

No `unhandledRejection`/`uncaughtException` handler exists under `apphost/src`,
and `dispatch.ts` wraps only the awaited frame. A floating rejection answers
`{ok:true}`, then takes the process down for every app. Because a sidecar-wide
crash is deliberately never attributed to an app — the ruling in the A4 plan
doc — nothing is ever charged, so the one app the auto-disable rule exists to
stop is structurally exempt from it.

The daemon-side non-attribution stays; the fix belongs in the host, which knows
which app's frame was in flight.

## 4 — papercuts (PR 3)

- `attn app status` prints "runtime: not started — no enabled app has been due a
  fact since this daemon came up" on a daemon where the binary is missing and
  every dispatch is failing. False on both halves.
- `attn app rollback <name> <bad>` points at `attn app status <name>` to list
  version ids. It never lists them, and there is no `attn app versions`. The
  daemon's own refusals already carry the list.
- Four comments that describe something the code does not do (the runtime log's
  bound, what registers a consumer, when `declareAppCollections` runs, whose job
  collection declaration is).

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
