#!/usr/bin/env node

/**
 * Packaged-app proof for Slice 6's profile-level Automations dock panel: a
 * CLI-created definition appears in the panel via broadcast with no restart,
 * "run now" driven through the real panel controls produces a navigable run
 * and re-rendering the panel never duplicates it, a rejected run-now surfaces
 * its error inline instead of failing silently, and both the definition list
 * and run history survive a daemon+app restart.
 *
 * Two definitions are applied against one isolated profile daemon:
 *   - `automations-surface-manual-<suffix>`: trigger.type manual, policy
 *     continuity fresh (the only continuity manual trigger validation
 *     allows), launched against a FAKE codex executable (argv logger, like
 *     the Slice 5 storm-guard probe) so a run-now click reaches `delivered`
 *     fast and deterministically.
 *   - `automations-surface-scheduled-<suffix>`: trigger.type scheduled with
 *     a once-a-year cron so it never fires during the scenario window;
 *     exists only to prove the panel hides the run-now affordance for a
 *     non-manual trigger.
 *
 * Run serially (packaged-app scenarios are single-tenant):
 *   ATTN_HARNESS_PROFILE=<name> node scripts/real-app-harness/scenario-automation-surface.mjs
 */

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { parseCommonArgs, printCommonHelp, launchFreshAppAndConnect } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, resolveHarnessResources } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Poll budgets. The panel refetches on every `automations_changed` broadcast,
// so appearance/toggle/run-state changes should land fast; these windows are
// generous margin over that, not tuned to any real cadence.
const PANEL_APPEAR_TIMEOUT_MS = 30_000;
const RUN_DELIVERED_TIMEOUT_MS = 45_000;
const TOGGLE_TIMEOUT_MS = 20_000;
const RUN_ERROR_TIMEOUT_MS = 20_000;
const RESTART_READY_TIMEOUT_MS = 60_000;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function profileEnv(profile, extra = {}) {
  const env = { ...process.env, ATTN_PROFILE: profile, ...extra };
  for (const key of ['ATTN_SOCKET_PATH', 'ATTN_DB_PATH', 'ATTN_CONFIG_PATH', 'ATTN_PLUGIN_DIR']) {
    delete env[key];
  }
  return env;
}

function run(binary, args, env, options = {}) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: options.stdio || ['ignore', 'pipe', 'pipe'],
    timeout: options.timeout || 30_000,
  });
}

function runJSON(binary, args, env) {
  return JSON.parse(run(binary, args, env));
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(250);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

async function waitForDaemonReady(binary, daemonEnv) {
  await poll(() => {
    try {
      runJSON(binary, ['automation', 'list'], daemonEnv);
      return { ready: true };
    } catch {
      return null;
    }
  }, 'profile daemon', RESTART_READY_TIMEOUT_MS);
}

// --- fixture location: both definitions use `location.type: directory`,
// which only requires an existing absolute directory (no git needed). ---

function createFixture(root) {
  const dir = fs.mkdtempSync(path.join(root, 'automations-surface-'));
  return fs.realpathSync(dir);
}

// --- fake codex probe: an argv logger, not a real agent, so run-now reaches
// delivered fast and deterministically (mirrors the Slice 5 storm-guard
// probe in scenario-automation-scheduled-cleanup.mjs). ---

function createCodexProbe(root) {
  const log = path.join(root, 'codex-invocations.jsonl');
  const executable = path.join(root, 'codex-probe.mjs');
  fs.writeFileSync(
    executable,
    `#!/usr/bin/env node\nimport fs from 'node:fs';\nfs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({argv: process.argv.slice(2), at: new Date().toISOString()}) + '\\n');\nsetInterval(() => {}, 1000);\n`,
    { mode: 0o700 },
  );
  return { executable, log };
}

// --- automation definition YAML ----------------------------------------

const API_VERSION = 'attn.dev/automations/v1alpha1';

function manualDefinitionYAML({ id, locationPath, enabled, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 6 packaged automations-panel proof (manual)
enabled: ${enabled}
trigger:
  type: manual
prompt: |
  Slice 6 packaged automations-panel proof. Do nothing; this executable is a test double.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice6-manual-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
policy:
  continuity: fresh
`;
}

function scheduledDefinitionYAML({ id, locationPath, enabled, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 6 packaged automations-panel proof (non-manual)
enabled: ${enabled}
trigger:
  type: scheduled
  schedule:
    cron: "0 0 1 1 *"
    time_zone: UTC
prompt: |
  Slice 6 non-manual trigger fixture. Never fires during the scenario window
  (once-a-year cron); exists only to prove the panel hides run-now for a
  non-manual trigger.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice6-scheduled-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
policy:
  continuity: fresh
  catch_up: latest
`;
}

// --- panel bridge helpers ------------------------------------------------

function findDefinitionRow(state, definitionId) {
  return (state?.definitions || []).find((row) => row.id === definitionId) || null;
}

function currentRuns(state) {
  // collectAutomationsUiState() only renders a runs section for the
  // currently-selected definition, so this is scoped to whichever
  // definition automations_select_definition last selected.
  return state?.runs || [];
}

async function closeAndReopenPanel(client) {
  // No automations_close_panel bridge verb exists (openAutomationsPanel in
  // App.tsx is open-only — calling automations_open_panel again on an
  // already-open panel is a no-op and would not force a refetch); the real
  // close button also has no data-testid. Drive it through the generic
  // dom_click verb instead, the documented house convention for controls
  // without a dedicated bridge verb.
  await client.request('dom_click', { selector: '.automations-panel__close' });
  return client.request('automations_open_panel');
}

async function captureFailureEvidence(runner, client) {
  try {
    const state = await client.request('automations_get_state');
    runner.writeJson('failure-automations-state.json', state);
  } catch (error) {
    runner.log('failure_evidence_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    await captureFrontWindowScreenshot(path.join(runner.runDir, 'failure.png'), { client });
  } catch (error) {
    runner.log('failure_evidence_screenshot_error', { error: error instanceof Error ? error.message : String(error) });
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automation-surface.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation surface scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = path.join(resources.appPath, 'Contents', 'MacOS', 'attn');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-SURFACE',
    tier: 'tier2-local-fake-agent',
    prefix: 'automation-surface',
    metadata: { profile, panel: 'profile-level Automations dock panel' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const suffix = Date.now().toString(36);
  const manualID = `automations-surface-manual-${suffix}`;
  const scheduledID = `automations-surface-scheduled-${suffix}`;
  const manualDefinitionFile = path.join(runner.sessionDir, 'manual.yml');
  const scheduledDefinitionFile = path.join(runner.sessionDir, 'scheduled.yml');

  let daemonEnv = null;
  let fixturePath = null;
  let probe = null;
  let manualApplied = false;
  let scheduledApplied = false;
  let firstRunId = '';

  try {
    daemonEnv = profileEnv(profile);
    fixturePath = createFixture(runner.sessionDir);
    probe = createCodexProbe(runner.sessionDir);

    await runner.step('restart_isolated_daemon', async () => {
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('launch_packaged_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // Leg 1 (create via CLI, appears in UI via broadcast): apply a
    // manual-trigger definition via the bundled CLI, then confirm the panel
    // renders it without a restart.
    await runner.step('leg1_apply_manual_and_panel_shows_it', async () => {
      fs.writeFileSync(
        manualDefinitionFile,
        manualDefinitionYAML({ id: manualID, locationPath: fixturePath, enabled: true, executable: probe.executable }),
      );
      runJSON(binary, ['automation', 'apply', '--file', manualDefinitionFile], daemonEnv);
      manualApplied = true;

      await client.request('automations_open_panel');
      const state = await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, manualID) ? current : null;
      }, `manual definition ${manualID} to appear in the panel`, PANEL_APPEAR_TIMEOUT_MS);
      const row = findDefinitionRow(state, manualID);
      runner.assert(row.trigger === 'Manual', 'manual definition renders trigger label "Manual"', row);
      runner.assert(row.enabled === true, 'manual definition renders enabled', row);
      runner.assert(row.canRunNow === true, 'manual definition renders the run-now affordance', row);
    });

    // Leg 2 (run-now via UI produces a navigable run; no duplicate run from
    // repeated UI response handling): run now through the real button,
    // confirm exactly one delivered, navigable run, then close+reopen the
    // panel (a fresh fetch, not a second click) and confirm it is still
    // exactly one run.
    await runner.step('leg2_run_now_and_navigable', async () => {
      await client.request('automations_select_definition', { definitionId: manualID });
      await client.request('automations_run_now', { definitionId: manualID });
      const state = await poll(async () => {
        const current = await client.request('automations_get_state');
        const delivered = currentRuns(current).filter((r) => r.state === 'delivered');
        return delivered.length >= 1 ? current : null;
      }, 'run-now run to reach delivered', RUN_DELIVERED_TIMEOUT_MS);
      const runs = currentRuns(state);
      runner.assert(runs.length === 1, 'exactly one run exists after a single run-now click', runs);
      runner.assert(runs[0].state === 'delivered', 'the run reached delivered', runs[0]);
      runner.assert(runs[0].navigable === true, 'the delivered run is navigable (its ticket exists)', runs[0]);
      firstRunId = runs[0].id;

      const reopened = await closeAndReopenPanel(client);
      const reopenedRuns = currentRuns(reopened);
      runner.assert(reopenedRuns.length === 1, 'reopening the panel does not duplicate the run row', reopenedRuns);
      runner.assert(reopenedRuns[0].id === firstRunId, 'reopening the panel shows the same run id', reopenedRuns);
    });

    // Leg 3 (request failure is shown, not hidden): a non-manual definition
    // has no run-now affordance; a disabled manual definition still renders
    // one (by design), and clicking it surfaces the daemon's rejection
    // inline rather than hiding it, without creating a new run row.
    await runner.step('leg3_failure_shown_not_hidden', async () => {
      fs.writeFileSync(
        scheduledDefinitionFile,
        scheduledDefinitionYAML({ id: scheduledID, locationPath: fixturePath, enabled: true, executable: probe.executable }),
      );
      runJSON(binary, ['automation', 'apply', '--file', scheduledDefinitionFile], daemonEnv);
      scheduledApplied = true;
      const withScheduled = await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, scheduledID) ? current : null;
      }, `scheduled definition ${scheduledID} to appear in the panel`, PANEL_APPEAR_TIMEOUT_MS);
      const scheduledRow = findDefinitionRow(withScheduled, scheduledID);
      runner.assert(scheduledRow.trigger.startsWith('Scheduled'), 'non-manual definition renders a Scheduled trigger label', scheduledRow);
      runner.assert(scheduledRow.canRunNow === false, 'non-manual definition has no run-now affordance', scheduledRow);

      await client.request('automations_toggle_enabled', { definitionId: manualID });
      await poll(async () => {
        const current = await client.request('automations_get_state');
        const row = findDefinitionRow(current, manualID);
        return row && row.enabled === false ? current : null;
      }, 'manual definition to render disabled after broadcast', TOGGLE_TIMEOUT_MS);

      await client.request('automations_run_now', { definitionId: manualID });
      const rejected = await poll(async () => {
        const current = await client.request('automations_get_state');
        const row = findDefinitionRow(current, manualID);
        return row && row.runError ? current : null;
      }, 'daemon rejection of run-now on a disabled definition to render inline', RUN_ERROR_TIMEOUT_MS);
      const rejectedRow = findDefinitionRow(rejected, manualID);
      runner.assert(rejectedRow.canRunNow === true, 'run-now still renders for a disabled manual definition (the rejection is surfaced, not hidden by the button disappearing)', rejectedRow);
      runner.assert(rejectedRow.runError.toLowerCase().includes('disabled'), 'the inline run error names the daemon rejection reason', rejectedRow);
      runner.assert(currentRuns(rejected).length === 1, 'the rejected run-now click did not create a new run row', currentRuns(rejected));

      await client.request('automations_toggle_enabled', { definitionId: manualID });
      await poll(async () => {
        const current = await client.request('automations_get_state');
        const row = findDefinitionRow(current, manualID);
        return row && row.enabled === true ? current : null;
      }, 'manual definition to render enabled again after broadcast', TOGGLE_TIMEOUT_MS);
    });

    // Leg 4 (restart preserves list and navigation): stop the daemon, quit
    // the app, bring both back up (same mechanics as
    // scenario-automation-scheduled-cleanup.mjs's restart leg), then confirm
    // both definitions and the prior run still render correctly.
    await runner.step('leg4_restart_preserves_list_and_navigation', async () => {
      await client.quitApp();
      await observer.close();
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
      await launchFreshAppAndConnect(client, observer);

      await client.request('automations_open_panel');
      const state = await poll(async () => {
        const current = await client.request('automations_get_state');
        return findDefinitionRow(current, manualID) && findDefinitionRow(current, scheduledID) ? current : null;
      }, 'both definitions to render after restart', RESTART_READY_TIMEOUT_MS);
      const manualRow = findDefinitionRow(state, manualID);
      const scheduledRow = findDefinitionRow(state, scheduledID);
      runner.assert(manualRow.enabled === true, 'manual definition is still enabled after restart', manualRow);
      runner.assert(scheduledRow.enabled === true, 'scheduled definition is still enabled after restart', scheduledRow);

      await client.request('automations_select_definition', { definitionId: manualID });
      const withRuns = await poll(async () => {
        const current = await client.request('automations_get_state');
        return currentRuns(current).length >= 1 ? current : null;
      }, 'manual definition run history to render after restart', RESTART_READY_TIMEOUT_MS);
      const runs = currentRuns(withRuns);
      runner.assert(runs.length === 1, 'exactly one run still exists after restart', runs);
      runner.assert(runs[0].id === firstRunId, 'the surviving run is the same run created before restart', runs[0]);
      runner.assert(runs[0].navigable === true, 'the run is still navigable after restart', runs[0]);
    });

    runner.finishSuccess({ profile, manualID, scheduledID, firstRunId, fixturePath });
  } catch (error) {
    await captureFailureEvidence(runner, client).catch(() => {});
    runner.finishFailure(error, { profile, manualID, scheduledID, firstRunId, fixturePath });
    throw error;
  } finally {
    // Disable both definitions before the fixture directory disappears: an
    // enabled `directory`-location definition re-validates its path on
    // future observation, so leaving one enabled against a deleted temp root
    // would spam errors on this profile forever (same defensive ordering as
    // scenario-automation-scheduled-cleanup.mjs's finally block).
    if (daemonEnv && fixturePath && fs.existsSync(fixturePath)) {
      if (manualApplied) {
        try {
          fs.writeFileSync(
            manualDefinitionFile,
            manualDefinitionYAML({ id: manualID, locationPath: fixturePath, enabled: false, executable: probe.executable }),
          );
          run(binary, ['automation', 'apply', '--file', manualDefinitionFile], daemonEnv);
        } catch {}
      }
      if (scheduledApplied) {
        try {
          fs.writeFileSync(
            scheduledDefinitionFile,
            scheduledDefinitionYAML({ id: scheduledID, locationPath: fixturePath, enabled: false, executable: probe.executable }),
          );
          run(binary, ['automation', 'apply', '--file', scheduledDefinitionFile], daemonEnv);
        } catch {}
      }
    }
    if (fixturePath) {
      try { fs.rmSync(fixturePath, { recursive: true, force: true }); } catch {}
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    // daemonEnv never diverged from a plain profile daemon here (no mock
    // provider, no CODEX_HOME override), so a single idempotent `ensure` is
    // enough to leave a healthy daemon behind.
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
