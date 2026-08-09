# Plan: sometimes-assertions catalog — vacuous-green detection

## Goal

attn has been burned repeatedly by tests that pass without exercising the
interesting path: a verify gate whose agents had died reporting an empty
`confirmed` list, mutation tests surviving because another path happened to
cover the input, harness legs that never reached the state they exist for. The
common shape is that the assertion still holds while the situation it was
written about stopped arising. Line coverage cannot see any of it — the lines
still run. It is a **condition** that went missing, not a location.

Antithesis's *sometimes assertion* is the off-the-shelf idea: a deliberately
placed mark saying "this interesting state was reached at least once during the
run", with the run failing when a cataloged mark was never hit.

This spike answers one question: **can that mechanism be small enough to be worth
having in a Go unit-test suite?** Like the synctest/rapid/Toxiproxy spike
(PR #799) it ends in adopt or drop; a mechanism that needs heavy plumbing is a
drop with a written reason.

## Shape

- A tiny `internal/testinv` (test inventory): register a state, mark the site,
  check at end of run.
- Marks placed in 2–3 real packages, chosen where a mark would genuinely catch a
  future vacuous pass.
- A mutation receipt: show a change that leaves every test green and turns the
  run red only because a cataloged state stopped happening.

## Design constraints

- **Per-package.** `go test` runs each package in its own process, so a global
  in-memory registry cannot see the suite. The catalog is per-package and the
  package's `TestMain` checks it. Cross-package aggregation is optional —
  sketched below, built only if it stays small.
- **Zero overhead unused, near-zero used.** A package that registers nothing pays
  nothing; a mark is one atomic bool.
- **Reads as a sentence.** `testinv.Sometimes(...)` at package scope,
  `.Reached()` at the site.
- **Test-only.** Marks live in test files or test-only helpers. Whether
  production code should ever carry one is a later decision, not this spike's.
- **Honest failure.** A state that stops being reached names itself loudly. A
  silent catalog is worse than none.

## Boundaries

- No new dependencies.
- No production code changes.
- Test safety rules apply as everywhere.

## Implementation Steps

- [x] `internal/testinv`: `Sometimes` / `Mark.Reached` / `Run(m)`, with its own
      tests
- [x] Place marks in `internal/bus` and `internal/jobs`
- [x] Mutation receipts: for each mark, what breaks it and what else notices
- [x] Cost receipts: per-call and per-package
- [x] Findings + adopt/drop verdict appended here
- [x] Changelog fragment (`kind: internal`, `area: testing`)

## Decisions

- Two packages carry marks, not three. The third candidate — the daemon's
  `classifierObservation` staleness rejection — was dropped on purpose, and why
  is a finding rather than a shortfall (see "The limit that decides where a mark
  can go").

## Open Questions

- Does the catalog earn its keep against a suite whose assertions are already
  this thorough, or is every interesting state already asserted somewhere?

---

# Findings

**Verdict: adopt, narrowly.** The mechanism is 200 lines with no dependencies and
costs nothing measurable, and it catches a class of decay that nothing else in
the suite can see. But it is a scalpel, not a policy: three of the four marks
placed here are redundant with assertions that already exist, and only one earned
its keep on the spot. The rule for reaching for it is at the end.

Measurements are from an M5 Mac, Go 1.25.3, reproducible from the commands
quoted.

## What was built

`internal/testinv`, imported only by test files:

```go
// internal/bus/testinv_test.go — the package's whole catalog, in one file
var sawMultiEventBatch = testinv.Sometimes("the log is read forward into a batch holding more than one event")

func TestMain(m *testing.M) { os.Exit(testinv.Run(m)) }

// internal/bus/bus_test.go — inside memStore.Since, the test double the Bus reads through
if len(out) > 1 {
	sawMultiEventBatch.Reached()
}
```

`Run` runs the package's tests and then checks the inventory. A state that was
never reached turns a passing run into a failing one and says so:

```
PASS

--- FAIL: testinv (1 cataloged state never reached this run)
    NEVER REACHED: the log is read forward into a batch holding more than one event
        cataloged at bus/testinv_test.go:24
    The tests still pass, so nothing else will say this. A cataloged state
    that stops happening means the suite no longer exercises the path it was
    written for. Restore the state, or drop the entry and say why.
```

The `PASS` above the `FAIL` is the whole point: every test in the package passed.

## The receipt

**Change `DefaultBatchSize` from 200 to 1 in `internal/bus/bus.go`.** One token.
Plausible as a memory tweak. It retires the drain's batch loop — the paging read,
the per-event cursor advance, the failure streak cleared on each success — and
`go test ./internal/bus` prints exactly the output above: **every test passes,
and the catalog is the only thing that notices.**

The test with the most to lose is `TestBackoffDoesNotRatchetAcrossSuccessfulDeliveries`,
which seeds a 30-event backlog specifically so "the batch loop never sees an
empty batch, which is the only place the older code cleared the streak" — its own
comment. With a batch size of 1 that setup is decorative and the test asserts
nothing it was written to assert. It stays green. Its precondition is exactly the
kind of thing a test cannot assert about itself, and exactly what a cataloged
state is for.

## The four marks, and what actually notices when each breaks

| Mark | Mutation | Mark red? | Anything else red? |
|---|---|---|---|
| bus: a batch holds more than one event | `DefaultBatchSize` 200 → 1 | yes | **no — nothing** |
| bus: a handler is given an event it has already been given | advance the cursor on handler error (at-most-once) | yes | yes: `TestFailingHandlerRedeliversAndDoesNotAdvance`, `TestStatusReportsLagAndLiveness` |
| jobs: dispatch withholds a job whose scheduled time has not arrived | retry at `now` instead of `now + backoff` | no¹ | yes: 3 tests |
| jobs: a coalescing trigger lands on a running job | drop the `Requeued` flag | n/a² | yes: `TestATriggerArrivingMidRunRunsTheJobAgain` deadlocks |

¹ The mark covers backoff *and* the coalescing debounce, so removing only the
backoff leaves it reached by the debounce tests. Nothing found removes all
withholding while the suite stays green.

² The run never finishes, so the inventory is never checked — see the limits
below.

**Read the table honestly: one mark out of four caught something nothing else
did.** That is not a disappointment, it is the calibration. In a suite whose
tests assert as specifically as this repo's, most interesting states are already
asserted by *some* test. What a cataloged state adds there is durability: the
claim belongs to the run, not to the test that happens to satisfy it, so it
survives that test being rewritten, weakened, or deleted. That is real but it is
insurance, not detection — and insurance you only want on the states that matter.

Where the catalog is not insurance but detection is the first row: a
**precondition** a test needs and cannot check, whose disappearance leaves the
assertion technically true and completely empty.

## The limit that decides where a mark can go

Marks are test-only, so a state has to be observable from the test side. In
practice that means the best host is a **test double the production code calls
through** — this package's in-memory `Store`, its fake clock, its recording
handler. All four marks live in one: `memStore.Since` and `recorder.handle` in
bus, `memStore.Eligible` and `memStore.Save` in jobs. The real code produces the
condition; the double observes it; production carries nothing.

The third candidate site did not survive that constraint, and it is worth
recording why. `internal/daemon`'s classifier-staleness path — "a slow classifier
verdict raced a newer live signal and was dropped" — is a textbook sometimes
state: timing-dependent, unassertable, and precisely the thing a refactor could
silently stop exercising. But the drop happens inside the resolver, is not
visible through any test double, and the daemon's test seams do not surface it.
Marking it would mean instrumenting production code.

**So the reach of this mechanism is exactly the reach of a package's test
doubles.** A leaf package with a clean injected seam gets marks for free. A
package whose interesting states are internal to production code does not, and no
amount of cleverness in `testinv` changes that. If those states ever need
covering, that is a separate decision about production marks, with a real cost
(the mark ships) and a real question (what does it do outside a test run) — not a
follow-up to this spike.

## Limits worth knowing before placing a mark

- **A filtered run checks nothing, and you will not see it say so.** `-run`,
  `-skip`, `-short`, and `-list` cannot be expected to reach the whole inventory,
  so `Run` reports what it skipped rather than failing. The notice is written for
  all four, but `go test` suppresses a passing binary's output, so a plain
  `go test ./internal/bus -run Foo` prints a bare `ok` — pair it with `-v` to see
  the notice. This is the one place the mechanism is quieter than it should be,
  and it is not fixable from inside `Run`: on a passing run there is no stream
  that reaches the terminal. So a mark defends CI's unfiltered
  `scripts/test-go.sh`, and never a developer's inner loop. Plan around that
  rather than expecting a filtered run to warn you.
- **A run that never returns is never checked.** `Run` checks after `m.Run()`, so
  a panic or a test timeout skips the report entirely (row 4 above). The catalog
  is a check on a completed run, not a diagnostic for a broken one.
- **Do not catalog a state behind a conditional skip.** A test that skips when a
  tool is missing takes its state with it, and the run goes red for the
  environment rather than for the code.
- **Do not catalog a state the suite reaches only sometimes.** This is a
  deterministic unit suite, not a fuzzer: a genuinely racy condition — "the
  publish raced the drain" — would fire the catalog on the runs where the
  interleaving did not happen, and a flake generator is worse than no catalog at
  all. Antithesis can make this claim because it controls the schedule; `go test`
  does not. **Catalog what the suite reaches deterministically but incidentally.**
  That is the whole target.

## Cost

- **Per call:** `Reached` on an already-reached mark is **3.0 ns/op**
  (`go test -run XXX -bench BenchmarkReached -benchtime 3s ./internal/testinv`)
  — an atomic load and a predicted branch. It reads before it writes, so a mark
  on a hot path does not add a contended store per hit.
- **Per package:** indistinguishable from noise. Baseline measured on the
  pre-change files via `go test -overlay`, three runs each:
  `internal/bus` **0.580–0.766s → 0.606–0.714s**, `internal/jobs`
  **0.299–0.623s → 0.372–0.598s**.
- **Unused:** a package that never calls `Sometimes` has an empty catalog and
  `Run` is `m.Run`. Packages that do not import it pay nothing at all.
- `go test -race -count=3` on `internal/testinv`, `internal/bus`,
  `internal/jobs`: clean.

## Not built: suite-level aggregation

Per-package is enough for what this does. A package whose catalog is incomplete
fails, which fails `make test`, which is the outcome that matters. Aggregation
would add one thing worth having and one cost:

- **Sketch.** `Run` writes `$ATTN_TESTINV_DIR/<package>.json` (`{what, site,
  reached}` per entry) when the env var is set. `make test` sets it to a temp dir
  and a summary step merges the files: total cataloged, total reached, and — the
  part per-package cannot do — states reached by a *different* package's run than
  the one that cataloged them, plus a whole-suite view that could re-enable
  checking for filtered runs.
- **Cost.** A writer, a reader in `tools/`, a Makefile step, and a temp dir whose
  absence has to fail open. That is more surface than the mechanism it reports
  on.

Build it if the catalog grows past a handful of entries across several packages
and the per-package view stops being enough. Not before.

## The rule, if this is adopted

**Catalog a state when a test depends on a situation it cannot assert about
itself — a precondition, a delay, a backlog, a collision — and the test would
still pass if that situation stopped arising.** Put the mark in a test double the
real code calls through. Do not catalog what an assertion already demands
(that is redundancy, unless the assertion is one you specifically expect to
outlive), and do not catalog what the suite reaches only some of the time.

A good check before adding one: *if this condition silently stopped happening
tomorrow, which test would go red?* If the answer is "none", it belongs in the
catalog. If the answer names a test, it probably does not.

## Follow-ups

- If the verdict holds, a bullet in AGENTS.md's "Testing tools" section stating
  the rule above — the same shape PR #802 gave the synctest/rapid/Toxiproxy
  adoptions, and for the same reason: the receipts live in a plan doc, the rule
  has to live where an agent will read it.
- Placing marks opportunistically when a vacuous-green bug is found — the same
  discipline as adding a regression test, one entry at a time — rather than as a
  sweep.
- Production marks (`internal/daemon`'s classifier staleness and friends) remain
  an open, separate question. This spike deliberately does not open it.
- Suite-level aggregation, per the sketch above, when the catalog outgrows the
  per-package view.
