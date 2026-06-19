# Resume-Invalidation Spec (R-spec) — Native Fidelity Target for E1

**Status:** testable spec, pre-E1. Source of truth: design doc §2 / §6 + native workflow
resume semantics. This pins the **native** behavior the E1 engine must replicate exactly.
A divergence from anything below is a bug, not a design choice.

**Scope.** This doc specifies *what gets cached vs. re-run on resume* and *how ordinals are
assigned*, stated so each row maps directly to a test case. It does **not** cover the goja
realm bans, the result/validation layer (§5), or writable-mode security (§7) except where the
side-effect contract (§5 of this doc) intersects resume.

**Definitions used throughout:**
- `ordinal` — the **structural** call-site path of an `agent()` call (see §2). Stable across
  re-runs regardless of subagent timing.
- `promptHash` — hash of the **resolved** prompt string passed to `agent()`.
- `schemaHash` — hash of the `schema` option (the empty/absent schema hashes to a fixed
  `"none"` sentinel, so adding *or* removing a schema is a change).
- `args` — the verbatim trigger input. `args` is part of run identity but is **not** itself an
  ordinal; it influences hits only through the prompt/schema values it produces.
- **journaled call** — a row in `workflow_agent_calls` with `status ∈ {ok, skipped, errored}`
  and a `result_json` (possibly `null`). Only `ok` rows carry a replayable non-null result;
  `skipped`/`errored` rows replay as `null` (the error→null contract, §5 of the design doc).

---

## 1. Prefix-invalidation matrix

The engine resumes by scanning journaled calls **in structural ordinal order** and replaying
the longest **unchanged prefix**. The first ordinal that is missing or mismatches is the
**divergence boundary**; that call and **everything structurally after it** run live. There is
no partial-call caching — a call is either a full cache hit or it runs live.

Columns: **cache hit** = result replayed from journal, no subagent spawned. **runs live** =
subagent spawned this resume.

| # | Change relative to the journaled run | Cache hit | Runs live | Rule |
|---|---|---|---|---|
| R1 | **Identical script + identical args** | **all journaled calls** | none (only un-journaled tail, if the prior run was killed mid-flight) | Every ordinal matches on all three keys → 100% hit. The only live work is calls past where the prior run stopped. |
| R2 | **Upstream prompt edit** (edit the prompt of the call at ordinal *k*) | ordinals `< k` | ordinal *k* and **all** structurally-later ordinals | `promptHash` mismatch at *k* is the divergence boundary. Downstream is invalidated **transitively**, even if those downstream prompts are byte-identical, because their inputs may now differ. |
| R3 | **Downstream-only edit** (edit the prompt/schema of a *later* call only; ordinals `< k` untouched) | ordinals `< k` (the entire upstream prefix) | ordinal *k* onward | Upstream prefix stays cached. This is the payoff case: editing the tail of a workflow re-runs only the tail. |
| R4 | **Args change** (different `--args`, same script) | only calls whose resolved `promptHash` **and** `schemaHash` are unchanged by the new args, **up to the first one that changes** | from the first call whose prompt or schema differs, onward | Args invalidate **indirectly**: a call is a hit iff the new args produce the same prompt+schema at that ordinal. A call that ignores args still hits; the first arg-dependent call is the boundary. (Native treats same-script+same-args as the guaranteed-100% case; differing args degrade to the prompt/schema predicate.) |
| R5 | **Per-call schema change** (add, remove, or alter `schema` on the call at ordinal *k*; prompt unchanged) | ordinals `< k` | ordinal *k* onward | `schemaHash` mismatch at *k* is a divergence boundary on its own — even with an identical `promptHash`. A previously text call that gains a schema (or loses one) re-runs at *k* and downstream. |

**Cross-cutting rules these rows encode:**
- Invalidation is **prefix-and-suffix**: never a hole. You cannot cache ordinal *k+1* while
  re-running *k*. (Testable: assert no cached call has a live-run structural ancestor.)
- A call's *journaled status* does not protect it. A `skipped`/`errored` row at ordinal `< k`
  still counts as a matching prefix entry and **replays as `null`** — it is not retried on
  resume just because it failed before. (Retry is a re-author/args concern, not a resume one.)
- Adding a brand-new call **between** two existing ones changes the structural ordinals of the
  calls after it → those become "missing at the new ordinal" → divergence at the insertion
  point. (Falls out of R2's transitivity; called out because it is a common edit.)

---

## 2. Structural-ordinal assignment rule

An ordinal is a **path**, not a counter over invocation order. Encode it as the ordered join
of structural segments from the run root down to the call site. The canonical encoding is a
dotted/`·`-joined path; the **only hard requirement** is that the same *logical* call produces
the same path on every re-run, independent of subagent completion timing.

Segments, in nesting order:

1. **`phase` segment** — *not* used for identity. `phase(title)` is a progress annotation
   (§2 design doc) and must **not** appear in the ordinal; otherwise reordering/renaming a
   phase label would invalidate calls that did not change. (Testable: renaming a `phase()`
   string yields 100% cache hit.)
2. **call-site id** — a stable identifier for the lexical `agent()` call site in the script
   (e.g. source position or a stable index assigned at parse time). Two textually distinct
   `agent()` calls never share a call-site id; the same call site reached twice shares it.
3. **iteration / slot path** — the structural index of *which* iteration/branch reached the
   call site, composed of:

| Construct | Segment | Rule (testable) |
|---|---|---|
| **`parallel(thunks)`** | **slot index** = the position of the thunk in the array passed to `parallel`, 0-based. | The agent in thunk *i* always gets slot *i*, regardless of which thunk's subagent finishes first. Two parallel runs that complete in opposite order produce **identical** ordinals. |
| **`pipeline(items, ...stages)`** | **(item index, stage index)** pair: `item` = 0-based position in `items`; `stage` = 0-based position of the stage in the stage list. | Item *m* at stage *s* always gets `(m, s)`. Item flow has no barrier, so completion is interleaved across items — but `(m, s)` is purely positional, so a post-`await` continuation observes the *correct* journaled result for *its* `(m, s)`, not whichever resolved first. |
| **loops / repeated call site** (`for`, `while`, `.map`, recursion that re-hits the same `agent()` line) | **per-call-site counter** = a monotonic counter scoped to *that call site*, incremented each time control reaches it, in deterministic JS execution order. | The 1st time the line runs → counter 0, 2nd → 1, etc. Because the surrounding control flow is deterministic JS (no banned non-determinism), this counter is reproducible. It is **per call site**, so two different loops do not share a counter. |
| **nested `workflow(name, args)`** (one level, §2) | **workflow segment** prefixing the child's path. | A nested workflow's calls live under their own structural sub-path so they never collide with the parent's call sites. Shares the parent cap/counter/abort but has its own ordinal namespace. |

**Why structural and not temporal (the load-bearing reason):** under `parallel`/`pipeline`,
continuations after an `await` resume in **promise-resolution order**, which depends on real
subagent wall-clock timing. A counter that increments at "the order results come back" would
assign a *different* ordinal to the *same logical call* across runs, so resume would replay the
wrong journaled result into the wrong continuation. Positional segments (slot, item×stage,
per-call-site counter over deterministic control flow) are invariant to that timing. (See §4.)

---

## 3. Cache-hit predicate

A journaled call at structural ordinal *N* is a **cache hit** iff **all three** hold:

```
ordinal == N            (structural path equality)
AND promptHash matches
AND schemaHash matches
```

Resume scans journaled calls in structural-ordinal order; the **first** ordinal that is missing
from the journal, or present but failing the predicate, is the divergence boundary. That call
and every structurally-later call run live; everything strictly before it is replayed from the
journal.

Notes for the builder:
- All three keys are already columns in `workflow_agent_calls` (`ordinal`, `prompt_hash`,
  `schema_hash`). The predicate is a pure function of the journal row + the live call — no
  subagent spawn needed to decide a hit.
- `resolved_model` / `agent_type` are journaled (R6) but are **out of the hit predicate** in
  this spec. A model change is a separate identity concern; if E-phase work decides model must
  invalidate, add it as a fourth key here — but the native fidelity target is the three keys
  above. Flag, don't silently widen.
- The absent-schema case must hash to a stable sentinel so "add schema" and "remove schema"
  both flip `schemaHash` (this is what makes R5 work for the text→schema transition).

---

## 4. Divergent control flow after a journaled result

**Case.** A workflow branches on a journaled result:

```js
const verdict = await agent("classify the bug", { schema: VerdictSchema }); // ordinal A
if (verdict.severity === "high") {
  await agent("write a hotfix");        // ordinal B-high (call site B)
} else {
  await agent("file a backlog ticket"); // ordinal C-low (call site C)
}
```

On the original run, suppose `verdict.severity === "high"`, so ordinal A and ordinal B-high are
journaled; C was never reached. On resume with the **same** script+args:
- A is a cache hit (structural ordinal A, prompt+schema match) → its journaled object is
  replayed, so `verdict.severity` is again `"high"`.
- The branch deterministically takes the `high` arm again → reaches **call site B** → ordinal
  B-high → cache hit. C is never reached, never expected, never live.

**Why structural ordinals keep this valid (and temporal ordinals would break it):** the ordinal
of the hotfix call is tied to **call site B + its iteration path**, not to "the 2nd agent() to
return." If ordinals were temporal/invocation-counter, then any change in resolution timing —
or any earlier `parallel` slot finishing in a different order — would shift the counter and the
engine would try to replay A's-neighbor result into B's slot, or replay B's result into a call
that the new control flow routes to C. Structural ordinals make the journaled result *bound to
the program location that produced it*, so branch-dependent continuations always observe their
own result.

**Divergence sub-case (testable):** if an upstream edit flips the branch (e.g. the prompt at A
changes so the resumed result would now be `"low"`), A mismatches `promptHash` → A is the
divergence boundary → A re-runs live, the branch is re-evaluated live, and C (not B) runs live.
The journaled B-high row is simply never consulted because its structural ordinal is not reached
on the live path. No stale B result leaks into the C arm. (Assert: a journaled ordinal that the
live structural walk never visits is inert — never replayed, never errored on.)

---

## 5. Side-effect / replay contract

Resume replays **journaled results, not file mutations.** When the engine replays a cached
prefix, it returns each call's `result_json` to the script **without re-spawning the subagent**,
which means the file-system side effects that subagent performed on the original run are **not
re-applied** during resume.

Consequences the builder and tests must honor:
- **Read-only and `isolation:'worktree'` agents are safe to resume.** Read-only agents mutate
  nothing; worktree-isolated agents' mutations were either consumed by a later stage's input
  (which is itself replayed or re-run consistently) or discarded with the throwaway worktree.
- **Writable working-tree agents (§7) are not transactional across resume.** Resuming a
  workflow that had writable agents assumes the working tree is **in the state the journal
  left it**. The engine does not snapshot/restore the tree. If a later stage depends on an
  earlier writable agent's mutations, and that earlier call is replayed (not re-run), the
  mutations must already be on disk — the engine guarantees the *result value*, not the
  *world state*.
- **Recommendation (carried from design doc §6):** for any agent whose mutations a later stage
  depends on, prefer `isolation:'worktree'` so the dependency flows through the **returned
  result** (replayable) rather than through ambient file state (not replayed). This keeps
  resume correctness independent of working-tree drift.

**Testable assertion:** a resume of a prefix containing a writable agent spawns **zero**
subagents for the cached calls and performs **zero** file writes attributable to those calls;
only the live tail (divergence boundary onward) may spawn and mutate.
