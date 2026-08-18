# Real App Harness Policy

This policy applies to packaged-app scenarios in this directory.

## The Display Must Be Awake

Scenarios that drive input need a live display. Against a dark display or a
locked screen the input still reaches the app, but the window-server work the
app's handling depends on — menu-bar key equivalents, first responder,
rendering — does not run, so some keystrokes take effect and others silently do
nothing, and the run fails on a product assertion instead of on the display.

`InputDriver.swift` refuses every input-posting command in that state and names
which condition it found, so the failure describes itself. Read the same
findings without touching the app via `driver.displayState()` (the driver's
observation-only `display_state` command): `{ screenLocked, displayCount,
asleepCount, blockReason }`.

If a run fails on an assertion that looks like a product regression, check
whether the display was awake before believing it — `pmset -g log | grep
"Display is turned"` gives the history. A failed window screenshot in the
failure artifacts is the same symptom wearing another hat.

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

## Which Model a Scenario Burns

A scenario that boots a real agent spends real money on it, and nothing in the
harness caps that. **Never `fable`** — it is the most expensive model available,
and a scenario fixture asks an agent to count to 2000, not to think. The same
rule covers every way a scenario runs: a single run, a soak, a matrix leg, and a
recording. A recorded run is a run.

Unpinned is not neutral. With no pin the launch falls through to the agent's own
default, which is whatever the machine's owner picked for their day — on the
maintainer's machine that is fable. So a scenario that says nothing about the
model has chosen the expensive one.

So the pin is not left to each scenario to remember.
`launchFreshAppAndConnect` — which every scenario goes through — pins
`default_model_<agent>` to the cheapest model that agent offers (the catalog's
"(cheap)" entries: claude `haiku`, codex `gpt-5.4-mini`) as soon as the daemon
socket is up. A scenario that boots an agent needs to do nothing at all.

It puts the setting back. The prior value arrives on `initial_state`, and the
run writes it back as the process ends, straight to the daemon over its own
short-lived socket — scenarios tear down in whatever order suits them, some
through the runner's cleanup registry and some in their own `finally`, and by
then the app is usually gone, while the daemon outlives every launch. The
runner's cleanup calls the same restore for the signal path. Both the pin and
the restore print a `[harness]` line naming the key and the value they found.
`dev` is a profile the maintainer also uses by hand; a run that silently leaves
it pinned is a bug, not a saving.

`ATTN_HARNESS_LAUNCH_MODEL_CLAUDE` / `..._CODEX` override one agent for one run:
a model id pins that instead, and `inherit` leaves the setting alone. Inheriting
is the expensive path — that is why it has to be named rather than reached by
omission.

A scenario that genuinely needs a stronger model pins it after the launch helper
and says why in a comment beside the pin. An unexplained expensive pin reads as
an oversight and gets downgraded by whoever touches it next.

Mechanics, if you need them: `default_model_<agent>` pins every interactive
launch on that daemon; a per-spawn pin and `chief_model_<agent>` outrank it
(`resolveLaunchModel`, `internal/daemon/ws_settings.go`). Empty means
unconfigured, which is how the setting goes back.

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
child timed out, or a verdict line is present with `verdict.ok === false`; a
missing verdict line on a clean exit is a pass, since most catalog scenarios
predate the verdict contract — it's recorded as `verdictMissing` per run plus
a top-level `verdictMissingCount` in the report), writes a `soak-report.json`
under the usual artifacts root, and emits its own aggregate verdict line
(`scenarioId: 'soak:<id>'`) once all iterations (or, with `--until-violation`,
the first failing iteration) have run. Use it instead of a hand-driven loop
when you need to soak one flaky-prone scenario for confidence rather than
sweep the whole catalog. Catalog entries marked `soakOnly: true` (e.g.
`focus-probe`) are resolvable by the soak runner but excluded from
`run-serial-matrix.mjs` entirely, so matrix behavior never changes when a
soak-only probe is added.

## Recordings

`ATTN_HARNESS_RECORD=1` records the app window of every
`createScenarioRunner` scenario to `<runDir>/recording-NN.mp4` — one segment
per app launch, listed in `<runDir>/recording.json` — with zero per-scenario
wiring (`windowRecording.mjs`, wired in `scenarioRunner.mjs`). The env var
passes through `run-serial-matrix.mjs` and `run-soak.mjs` to every leg.
Capture is `WindowRecorder.swift`, compiled on demand like `InputDriver.swift`
and using ScreenCaptureKit's desktop-independent window filter: it records the
window's own content even parked almost fully off-screen (how
`uiAutomationClient` keeps bridge-driven windows) or occluded —
`screencapture -v` records black there, which is why it is not used (receipts
in the swift header). It needs the Screen Recording permission of whatever
context runs the harness; the binary is codesigned so the grant survives
rebuilds. An app relaunch rotates to a new segment. Recording failures trace
and never fail a run. Publish a segment as PR evidence with
`scripts/pr-evidence.sh publish` (repo root). Default off: a matrix run must
not pay recording CPU/disk unasked. Ad-hoc scenarios without the runner do
not record, matching the verdict-line contract's scope.

## Real-App Parity

- Scenarios must match real app usage. Do not invent command sequences that the app cannot perform.
- If workspace/session product behavior changes, update these scenarios in the same PR.
- If these scenarios pass while users can reproduce workspace/session errors in the packaged app, treat that as a test design bug.
- Real-app commands target the dev sibling (or the active `ATTN_PROFILE`) by default. Production runs must pass `--run-against-prod`; never bypass the shared production-target guard.

## Screenshot Crop / Scale

`captureFrontWindowScreenshot` (and `capture-app-screenshot.mjs`'s `--crop`/`--max-dim`
flags) can crop to a window-relative region and downscale the PNG at capture time via
`sips -Z`, so an agent that actually looks at the image pays far fewer tokens. `--crop`
accepts `x,y,WxH` (e.g. `0,0,800x600`) or the all-comma `x,y,w,h` form; a crop is clamped
to the window's bounds and only throws if it does not overlap the window at all. Prefer
these over capturing full-resolution, full-window screenshots when only a sub-region
matters for the assertion.

## Workspace Sessions

- A visible pane is a session pane. Do not model durable non-session terminals.
- Resolve pane IDs from daemon/app state. Do not hardcode legacy pane IDs such as `main` for new scenarios.
- Empty workspaces are invalid user-visible state. Tests that create or observe one should assert it is removed or hidden.
- Shortcut scenarios should exercise the documented app shortcuts or the same shortcut registry IDs used by the app.

## Remote Endpoint Scenarios

Remote scenarios (tr205, tr402-remote, tr502, tr504, bridge-remote-hub) default
to the local OrbStack VM `attn-remote` (`attn-remote@orb`).
`ATTN_HARNESS_REMOTE_SSH_TARGET` retargets all of them at once; each
scenario's own per-scenario env var still wins over it. Provision or repair
the VM with `pnpm run real-app:provision-remote`. The hub daemon
installs/updates the attn binary on the remote automatically (internal/hub
bootstrapper), so the VM never needs a manual attn install.

`scenario-app-reconcile.mjs` (the app-reconcile exit proof) takes a Linux
witness on the same VM, but it drives the `attn` CLI there rather than a
session, so it has two extra knobs: `ATTN_HARNESS_REMOTE_ATTN` names the remote
binary (default `attn`; the hub bootstrapper installs `attn-<profile>` beside
`attn-app-runtime-<profile>` in `~/.local/bin`, and a hand-staged cross-build
lands the same way) and `ATTN_HARNESS_REMOTE_PROFILE` selects the profile it
runs under. The leg skips itself, loudly in the trace, when no attn is found —
reachability is not what that scenario is testing. `attn app apply` bundles
with bun, so the VM needs it; `real-app:provision-remote` installs it.

TR-205's matrix legs (`tr205-probe-codex`, `tr205-probe-claude`) run
`attn _probe-tui` on the remote instead of a live agent: a deterministic
agent-mimicking TUI whose styles are pinned to codex/claude VT vocabulary
recorded under `internal/probetui/testdata`, with mirror tests in
`internal/probetui` enforcing both directions. The probe reads no credentials,
so the TR-205 legs run unattended. Re-capture the probe vocabulary with
`go run ./cmd/agent-mirror`.
