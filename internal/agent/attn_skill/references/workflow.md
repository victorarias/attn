# Workflows

A workflow is a deterministic JavaScript script that orchestrates one or more
headless workflow agents. The engine runs in the `attn workflow run` process,
journals every `agent()` call to the daemon, and lets you resume a prior run by
replaying the journaled prefix and re-running only what changed. Use workflows
when you want a reproducible multi-step agent pipeline you can observe, retrieve,
and resume.

## Contents

- **Authoring a script** — the `meta` block, host functions (`agent`/`parallel`/`pipeline`), the determinism hard-bans.
- **Designing a workflow** — fan-out / pipeline / verify / judge shapes; picking a model.
- **CLI** — `run` / `result` / `show` / `list`; running and monitoring a run, including that a long-running call is not a stall — don't cancel a healthy run.

## Authoring a script

A workflow script is plain JS. Its top-level body runs inside an implicit async
function, so you may `await` directly. Return the final value from the top level.

```js
export const meta = {
  name: "summarize-and-review",
  description: "Summarize a file, then review the summary.",
};

const summary = await agent("Summarize the attached notes.", {
  schema: { type: "object", properties: { summary: { type: "string" } } },
});
const review = await agent(`Review this summary: ${JSON.stringify(summary)}`);
return { summary, review };
```

### The meta block

If present, `export const meta = { ... }` MUST be the FIRST statement and MUST be
a pure object literal (no computed values, no function calls). Recognized fields:
`name`, `description`, `whenToUse`, `phases` (array of strings), `model`.

### Host functions (available to the script)

- `agent(prompt, opts?)` — run one workflow agent and resolve to its result. Returns a
  Promise. Options:
  - `schema` — a JSON Schema object. When present, the workflow agent is FORCED to
    return a structured result matching the schema (it calls a `return_result`
    tool exactly once); `agent()` resolves to that validated object. Without a
    schema, `agent()` resolves to the workflow agent's final text.
  - `label` — a human-readable label for the call.
  - `phase` — group the call under a named phase.
  - `model` — override the model for this call.
  - `isolation` — isolation mode for the call.
  - `agentType` — the workflow agent type to run.
  - A workflow agent that fails terminally resolves to `null` (it never throws
    past the `agent()` boundary), so guard results you depend on.
- `parallel(thunks)` — run an array of zero-arg thunks concurrently and resolve to
  an array of their results. A throwing thunk yields `null` in its slot;
  `parallel` never rejects.
- `pipeline(items, ...stages)` — flow each item through every stage. A stage cb is
  `(prevResult, originalItem, index)`. A throwing stage drops that item to `null`
  for its remaining stages. Resolves to an array of per-item final results.
- `phase(name)` — mark the start of a named phase for the calls that follow.
- `log(...)` — annotate the run (no-op return).

### Determinism (hard bans)

The runtime forbids reading the wall clock or non-deterministic randomness so a
resume replays identically. These THROW:

- `Date.now()` — pass an explicit timestamp through `args` instead.
- `new Date()` with no args, and any `Date()` call — construct from an explicit
  argument, e.g. `new Date(args.now)`.
- `Math.random()` — derive any randomness deterministically (e.g. a seed passed in
  via `args`, or vary by the loop/slot index).

Pass anything time- or randomness-dependent in as `args`, and vary per-item work
by its index rather than by chance.

## Designing a workflow

Reach for a workflow when the task is too big for one context, benefits from
independent perspectives, or applies the same step across many items. Match the
script's shape to the shape of the work:

- **Fan-out / cover ground** — split the work into independent slices and run them
  in `parallel`. Use when slices don't depend on each other (audit N files,
  summarize N docs).
- **Pipeline** — flow each item through ordered stages with `pipeline`. The default
  for multi-stage per-item work; there is no barrier between stages, so item A can
  reach stage 3 while item B is still in stage 1.
- **Verify / adversarial** — after a producing step, spawn one or more independent
  checkers that try to REFUTE the result before you trust it. A finding that
  survives several skeptics is far stronger than one unreviewed pass.
- **Judge panel** — generate several independent attempts from different angles,
  score them, and synthesize from the winner. Use when the solution space is wide
  and a single attempt is likely to be only locally good.

Keep the data flow explicit: pass each step's output forward as a value, guard
`null` results (a failed `agent()` resolves to `null`), and label phases so the
run reads clearly in `workflow show`.

Pitfalls:

- Don't add a synchronization barrier (`await parallel(...)` between stages) unless
  a stage genuinely needs ALL prior results at once (dedup, merge, count-zero
  early-exit). Otherwise prefer `pipeline` — a barrier makes fast items wait on the
  slowest one.
- Don't fan out wider than the work warrants: each `agent()` call is a real
  headless run costing minutes and tokens.
- Don't depend on a single result without a guard or a verifier.

### Picking a model

`--model` (the run default) and the per-call `model` option both take a harness
model id; omit it to let the harness pick its default — the right choice unless you
have a reason to override. Choose by the *job each call does*, not by habit:

- **Hard reasoning, synthesis, adversarial review, ambiguous specs** — the most
  capable model. These steps set the quality ceiling of the whole run; underpowering
  them is a false economy.
- **Broad, mechanical, well-specified fan-out** (extract a field, classify,
  grep-and-summarize, transform one item) — a smaller, faster model. You run many of
  these, so a cheaper model keeps a wide fan-out affordable and is usually enough.
- **Mixed runs** — set a sensible run default with `--model`, then override only the
  few calls that need more (or less) horsepower via the per-call `model` option.
  Phase-level reasoning steps justify the upgrade; per-item workers usually don't.

When unsure, start with the harness default and pin a model only once you've seen a
step underperform.

Worked example — fan out cheap, judge expensive:

```js
export const meta = {
  name: "triage-and-deep-dive",
  description: "Classify many items cheaply, then deep-dive the risky ones.",
  phases: ["triage", "deep-dive"],
};

phase("triage");
// Many mechanical classifications: each runs on a small, fast model.
const triaged = await parallel(
  args.items.map((item) => () =>
    agent(`Classify this item as high|low risk: ${JSON.stringify(item)}`, {
      label: `triage:${item.id}`,
      model: "<small-fast-model>",
      schema: { type: "object", properties: { risk: { type: "string" } } },
    })
  )
);

phase("deep-dive");
// Only the high-risk items, reviewed on the most capable model.
const risky = args.items.filter((item, i) => triaged[i]?.risk === "high");
const reviews = await parallel(
  risky.map((item) => () =>
    agent(`Deep-dive the risk in: ${JSON.stringify(item)}`, {
      label: `review:${item.id}`,
      model: "<most-capable-model>",
    })
  )
);
return { triaged, reviews };
```

The placeholders stand in for whatever model ids your harness exposes; the point is
the *split* — cheap and wide for triage, capable and narrow for the deep dive.

## CLI

The engine runs in the `attn workflow run` process and reports to the daemon over
the unix socket. The daemon owns the store and the read-only UI.

### Running and monitoring a run

Each `agent()` call runs a real headless workflow agent (codex/claude) and routinely
takes SEVERAL MINUTES; a multi-call run can run 10+ minutes. Never assume a run
is stuck just because it is taking a long time.

Two ways to run:

- DETACHED (default, recommended for agents): `attn workflow run <script.js>`
  returns a runId immediately and the engine keeps running in the background.
  Capture the runId, then poll `attn workflow show <runId>` on your own schedule.
  This is the safe pattern when your own shell yields between checks: the run
  keeps going and you re-read its state on the next poll.
- BLOCKING: `attn workflow run <script.js> --wait` stays in the foreground for the
  FULL run duration (often many minutes) and only then prints the terminal result.
  Use it only if your caller can truly block that whole time. If your shell yields
  or times out a foreground command (e.g. a ~30s yield) you LOSE the result output
  — switch to the detached pattern and poll instead. Do NOT cancel the run just
  because the foreground command yielded.

Reading progress from `attn workflow show <runId>` (re-read it each poll):

- `status: running` means the engine is still working; `completed` / `failed` /
  `canceled` are terminal.
- `phase` is the title of the phase currently executing.
- `progress` summarizes `calls_done` / `calls_running` / `calls_total`.
- `calls[]` lists every journaled call. The entry with `status: running` is the
  call IN FLIGHT right now; its `label`, `phase`, `model`, and a climbing
  `elapsed_seconds` say exactly what is running and that it is advancing. `ok` /
  `errored` / `skipped` entries are finished.

Progressing vs stuck: a run is progressing if `status` is `running` AND some
`calls[]` entry has `status: running` (its `elapsed_seconds` climbing across
polls). A long single `agent()` call is NORMAL — a steady `calls_done` while one
call is in flight is not a stall. Do not run `attn workflow cancel <runId>` on a
run that is still progressing.

### run

    attn workflow run <script.js> [--args <json> | --args-file <path>] [--wait]
                                  [--session <id>] [--resume <runId>]
                                  [--harness <codex|claude>] [--model <m>]

- `--args <json>` / `--args-file <path>` — JSON args passed to the script as the
  global `args`. Mutually exclusive; use `--args-file` for large or heavily
  escaped payloads.
- `--wait` — stay in the foreground and block until the run reaches a terminal
  status. This can be MANY MINUTES (the full run duration); then it prints the same
  JSON shape as `workflow result` and exits non-zero on failure. Without `--wait`,
  the run is detached to the background and the runId is printed immediately; poll
  `workflow show` to monitor it. Prefer the detached form (see "Running and
  monitoring a run" above) unless your caller can block for the entire run.
- `--session <id>` — attach the run to a session. Defaults to `ATTN_SESSION_ID`.
- `--resume <runId>` — resume a prior run, replaying its journaled prefix and
  re-running the first divergent call (and everything structurally after it).
- `--harness <codex|claude>` — the agent harness. Default `codex`.
- `--model <m>` — the workflow agent model.

`run` prints the runId (no `--wait`) or the terminal result JSON (`--wait`).

### result

    attn workflow result <runId> [--wait]

Prints the frozen result shape and exits 0 only when `status` is `completed`
(non-zero on `failed`/`canceled`). With `--wait`, polls until the run is terminal.

```json
{
  "status": "completed",
  "result": { },
  "error": "only present on failure",
  "phase": "current-or-final phase",
  "calls_total": 3,
  "calls_done": 3,
  "calls_running": 0
}
```

`status` is one of `running`, `completed`, `failed`, `canceled`. `calls_total` is
the number of journaled `agent()` calls; `calls_done` counts those that reached a
terminal status (`ok`, `errored`, or `skipped`); `calls_running` is the in-flight
count. A steady `calls_done` while `status` is still `running` is EXPECTED while
the current call is in flight (each call takes minutes) and is not a stall.
`result` does not surface the in-flight call — use `workflow show` to see the
running call (its `label` / `phase` / `model` / `elapsed_seconds`) before deciding
a run is stuck.

### show

    attn workflow show <runId>

This is the monitoring command: use it (not `result`) to watch live progress. It
prints the run `status`, current `phase`, a `progress` summary
(`calls_done` / `calls_running` / `calls_total`), and a `calls[]` array. Scan
`calls[]` for the entry with `status: running` — the call in flight, with its
`label`, `phase`, `model`, and a climbing `elapsed_seconds`. See "Running and
monitoring a run" for distinguishing a progressing run from a stuck one.

### list

    attn workflow list [--session <id>]

Lists runs for a session (`runId`, `status`, `phase`, `script`, `created_at`,
`resumable`). Defaults to `ATTN_SESSION_ID`; pass an empty session to list all.
