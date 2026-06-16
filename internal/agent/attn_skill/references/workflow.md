# Workflows

A workflow is a deterministic JavaScript script that orchestrates one or more
subagents. The engine runs in the `attn workflow run` process, journals every
`agent()` call to the daemon, and lets you resume a prior run by replaying the
journaled prefix and re-running only what changed. Use workflows when you want a
reproducible multi-step agent pipeline you can observe, retrieve, and resume.

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

- `agent(prompt, opts?)` — run one subagent and resolve to its result. Returns a
  Promise. Options:
  - `schema` — a JSON Schema object. When present, the subagent is FORCED to
    return a structured result matching the schema (it calls a `return_result`
    tool exactly once); `agent()` resolves to that validated object. Without a
    schema, `agent()` resolves to the subagent's final text.
  - `label` — a human-readable label for the call.
  - `phase` — group the call under a named phase.
  - `model` — override the model for this call.
  - `isolation` — isolation mode for the call.
  - `agentType` — the subagent type to run.
  - A subagent that fails terminally resolves to `null` (it never throws past the
    `agent()` boundary), so guard results you depend on.
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

## CLI

The engine runs in the `attn workflow run` process and reports to the daemon over
the unix socket. The daemon owns the store and the read-only UI.

### Running and monitoring a run

Each `agent()` call runs a real headless subagent (codex/claude) and routinely
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
- `--harness <codex|claude>` — the subagent harness. Default `codex`.
- `--model <m>` — the subagent model.

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
