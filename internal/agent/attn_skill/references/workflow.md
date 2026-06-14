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

### run

    attn workflow run <script.js> [--args <json> | --args-file <path>] [--wait]
                                  [--session <id>] [--resume <runId>]
                                  [--harness <codex|claude>] [--model <m>]

- `--args <json>` / `--args-file <path>` — JSON args passed to the script as the
  global `args`. Mutually exclusive; use `--args-file` for large or heavily
  escaped payloads.
- `--wait` — run in the foreground and block until the run reaches a terminal
  status, then print the same JSON shape as `workflow result` and exit non-zero on
  failure. Without `--wait`, the run is detached to the background and the runId is
  printed immediately.
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
  "calls_done": 3
}
```

`status` is one of `running`, `completed`, `failed`, `canceled`. `calls_total` is
the number of journaled `agent()` calls; `calls_done` counts those that reached a
terminal status (`ok`, `errored`, or `skipped`).

### show

    attn workflow show <runId>

Prints the full run plus per-call status and error as pretty JSON.

### list

    attn workflow list [--session <id>]

Lists runs for a session (`runId`, `status`, `phase`, `script`, `created_at`,
`resumable`). Defaults to `ATTN_SESSION_ID`; pass an empty session to list all.
