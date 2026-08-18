# Auto mode's denial ledger

Status: in progress (2026-08-18)

## The problem

Auto mode blocks a tool call, hands the model the denial contract, and
reports the denial to attn's daemon through the suite's relay
(`plugins/attn-pi/suite/core.ts`, `reportDenial`). That report is
fire-and-forget by design: a bare pi has no relay at all, and a relay that
will not answer drops it. No ack, no retry, no local fallback.

Observed 2026-08-17/18: auto mode's circuit breaker announced "4 calls
refused in a row" in two separate episodes. `attn automode denials` held 2 of
the first episode's 4 and 0 of the second's. A plugin reload had killed the
old session's relay socket, so every denial after it vanished. The forensics
surface goes blind exactly during the incidents it exists for, and the
breaker's counters and the queryable record disagree with no trace.

## The invariant

A denial that happened is a denial you can read later, from any session,
including a bare pi with no attn attached.

## The shape

**Write it locally, at decision time, unconditionally.** A new
`automode/ledger.ts` appends one JSON line per denial before the relay report
is attempted. It lives at the automode layer, not the suite layer, so the
standalone configuration (`automode/standalone.ts`, `pi -e automode.js`)
records too. The relay report stays exactly what it is today: a best-effort
mirror for the live surfaces.

**Where the file lives.** `ATTN_PI_AUTOMODE_DENIAL_LOG` when something set it
— the pi driver injects it at spawn/resume from the value the daemon puts in
the plugin runtime's environment (`ATTN_AUTOMODE_DENIAL_LOG`), so an attn
session's ledger is profile-scoped and a remote session's ledger lands on the
remote host beside the daemon that will read it. Otherwise
`<pi agent dir>/attn-automode-denials.jsonl`, next to the config file bare pi
already reads (`PI_CODING_AGENT_DIR`, else `~/.pi/agent`).

**The fence.** The ledger must not become a path an agent writes policy
through. `automode/paths.ts` gains one rule: a last path segment beginning
`attn-automode` is protected wherever it sits, which covers the config file,
the ledger, and its rotated generation. Under the default location the `.pi`
directory rule already covered it; the new rule keeps the fence when
`PI_CODING_AGENT_DIR` or the env override points elsewhere.

**Reconciliation, daemon-side.** `attn automode denials` is not the only
reader — the app's settings surface reads the same rows over the WebSocket
(`internal/daemon/ws_automode.go`). So the reconcile happens where both meet:
before either handler serves the list, the daemon reads the ledger and
inserts what the store lacks. A watermark in `settings` keeps a record from
being re-imported after the store's own row cap trims it away.

## No silent clipping

- The ledger keeps an active file and one rotated generation. Rotation past
  that drops records, and the count travels: the new active file opens with a
  `{"type":"rotated","dropped":N}` marker, the reader sums the markers, and
  `AutoModeDenialsResult` carries `ledger_dropped` so the CLI prints a footer
  naming how many older denials were lost. A protocol bump comes with it.
- A malformed line is counted and reported the same way rather than skipped
  quietly.

## Receipt for the rotation size

Measured 2026-08-18 by writing one through `DenialLedger`: a fat record — a
piped-curl bash command with a full classifier reason — is **476 bytes**. The
cap is 4 MiB per generation, so one generation holds ~8,800 denials against a
circuit breaker that stops a session at 20 denials since the user last spoke.
Two generations are kept, so ~17,600 records survive before anything is
dropped at all. Capacity is free until used; this is a tripwire
for a loop nobody is watching, not a budget.

## Not in scope

The breaker, the classifier walk, and the denial contract text
(`automode/denial.ts`) are untouched. Promotion and policy storage semantics
are untouched.
