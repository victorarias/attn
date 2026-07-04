# Real App Harness Policy

This policy applies to packaged-app scenarios in this directory.

## Profiles (which world a run targets)

The harness honors the **one knob**, `ATTN_PROFILE`, like every other entrypoint
(see [docs/profiles.md](../../../docs/profiles.md)). Resolution order:

1. `ATTN_HARNESS_PROFILE` — explicit override when the harness must target a
   *different* world than the surrounding shell. Empty (or `default`) is the
   production escape hatch and still requires `--run-against-prod`.
2. otherwise `ATTN_PROFILE` — a shell that already selected `agent7` drives
   `attn-agent7.app` / its daemon with no extra flags.
3. otherwise the safe `dev` sibling. An unset/empty/`default` `ATTN_PROFILE`
   **never** targets production by omission.

All resources (bundle id, app path, ports, socket, deep-link scheme) come from
the single authority `attn profile resolve`; `harnessProfile.mjs` does not
re-derive them. dev/prod are fast-path literals that a drift guard in
`harnessProfile.test.mjs` asserts equal the authority. Named-profile resolution
needs `./attn` built (`make dev` / `go build -o ./attn ./cmd/attn`); override the
binary with `ATTN_HARNESS_BIN`.

## Verdict Line

Every scenario built on `createScenarioRunner` (`scenarioRunner.mjs`), and
`run-serial-matrix.mjs`, print a single machine-parseable verdict line as the
last thing they emit on that path, so a driving agent can learn pass/fail
without spelunking through step logs or JSON summaries.

- Format: `ATTN_VERDICT ` followed by compact (non-pretty-printed) JSON, all on
  one line. `formatVerdictLine`/`emitVerdict` in `common.mjs` are the only
  producers — use them instead of hand-rolling the line.
- Shape: `{ ok, scenarioId, runId, failureCount, firstFailure, artifactsDir, summaryPath, durationMs }`.
  - `firstFailure` is `null` on success, otherwise the first line of the error
    message, capped at 300 characters (never multi-line, so it cannot break
    the one-line contract).
  - `run-serial-matrix.mjs` emits the same shape with `scenarioId: 'serial-matrix'`,
    `runId: ''`, `artifactsDir: ''`, and `summaryPath: ''` (it aggregates many
    runs, each of which already printed its own verdict line).
- Consumers must take the **last** line starting with `ATTN_VERDICT `, not the
  first — a scenario's own trace/log output can print other lines afterward
  in rare cases, but the verdict line itself is written right after the
  summary/failure JSON file, so it stays reliably last among `ATTN_VERDICT`
  lines.
- Out of scope: the older ad-hoc scenarios with hand-rolled `main()` that do
  not use `createScenarioRunner` do not emit a verdict line.

## Soak Runs

`run-soak.mjs` (`pnpm run real-app:soak -- --scenario <id> --repeat 30`) runs a
single catalog scenario repeatedly and strictly serially — never in
parallel, since the packaged app is single-tenant. It parses each iteration's
`ATTN_VERDICT` line (a run counts as failed if the exit code is non-zero, the
child timed out, no verdict line was found, or `verdict.ok === false`), writes
a `soak-report.json` under the usual artifacts root, and emits its own
aggregate verdict line (`scenarioId: 'soak:<id>'`) once all iterations (or,
with `--until-violation`, the first failing iteration) have run. Use it
instead of a hand-driven loop when you need to soak one flaky-prone scenario
for confidence rather than sweep the whole catalog.

## Real-App Parity

- Scenarios must match real app usage. Do not invent command sequences that the app cannot perform.
- If workspace/session product behavior changes, update these scenarios in the same PR.
- If these scenarios pass while users can reproduce workspace/session errors in the packaged app, treat that as a test design bug.
- Real-app commands target the dev sibling (or the active `ATTN_PROFILE`) by default. Production runs must pass `--run-against-prod`; never bypass the shared production-target guard.

## Workspace Sessions

- A visible pane is a session pane. Do not model durable non-session terminals.
- Resolve pane IDs from daemon/app state. Do not hardcode legacy pane IDs such as `main` for new scenarios.
- Empty workspaces are invalid user-visible state. Tests that create or observe one should assert it is removed or hidden.
- Shortcut scenarios should exercise the documented app shortcuts or the same shortcut registry IDs used by the app.
