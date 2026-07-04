#!/usr/bin/env node

// Packaged-app scenario for the WebGL context-loss auto-recovery fix
// (GhosttyTerminal's rendererEpoch backoff): forces the active pane's WebGL
// context to be lost via the lose_webgl_context bridge action, then asserts
// the full recovery lifecycle actually happens end to end — not just that
// the right diagnostics events were logged, but that the rebuilt renderer
// paints live output again and the pane's error overlay never shows.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  assertCommonTargetAllowed,
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { currentHarnessProfile, resolveHarnessResources } from './harnessProfile.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const OVERFLOW_TOLERANCE_PX = 2;
const RECOVERY_TIMEOUT_MS = 10_000;
const RECOVERY_OUTCOMES = ['contextLost', 'scheduled', 'recovered'];

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = { ...parseCommonArgs([]) };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir' || arg === '--artifacts') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }
  if (!options.help) assertCommonTargetAllowed(options, args);
  return { options, help: Boolean(options.help) };
}

// Same on-disk location terminalDiagnosticsLog.ts writes to (see
// $APPLOCALDATA/debug/terminal-diagnostics.jsonl), derived the same way
// harnessProfile.mjs's manifestPathForProfile derives the automation manifest
// path — both live under the profile's bundle's Application Support dir.
function terminalDiagnosticsLogPath(profile) {
  return path.join(
    os.homedir(),
    'Library',
    'Application Support',
    resolveHarnessResources(profile).bundleId,
    'debug',
    'terminal-diagnostics.jsonl',
  );
}

function readRecoveryEvents(logPath, paneId) {
  if (!fs.existsSync(logPath)) return [];
  const events = [];
  for (const line of fs.readFileSync(logPath, 'utf8').split('\n')) {
    if (!line.trim()) continue;
    let entry;
    try {
      entry = JSON.parse(line);
    } catch {
      continue;
    }
    if (entry.kind === 'recovery' && entry.pane === paneId) events.push(entry);
  }
  return events;
}

function tailLog(logPath, lines = 40) {
  if (!fs.existsSync(logPath)) return '(log file does not exist)';
  return fs.readFileSync(logPath, 'utf8').trim().split('\n').slice(-lines).join('\n');
}

// Polls the diagnostics log for `outcomes` to appear, in order, among this
// pane's recovery records (not necessarily contiguous — other lifecycle
// events interleave). Returns the matched records for the summary.
async function waitForRecoverySequence(logPath, paneId, outcomes, timeoutMs) {
  const startedAt = Date.now();
  let seen = [];
  while (Date.now() - startedAt < timeoutMs) {
    seen = readRecoveryEvents(logPath, paneId);
    let cursor = 0;
    for (const event of seen) {
      if (event.outcome === outcomes[cursor]) cursor += 1;
      if (cursor === outcomes.length) return seen;
    }
    await sleep(300);
  }
  throw new Error(
    `Timed out after ${timeoutMs}ms waiting for recovery sequence [${outcomes.join(' -> ')}] for pane ${paneId}.\n`
    + `Recovery events seen: ${JSON.stringify(seen, null, 2)}\nLog tail:\n${tailLog(logPath)}`,
  );
}

function measurePane(state) {
  const canvas = state?.pane?.dom?.canvas?.bounds;
  const container = state?.pane?.dom?.terminalContainer?.bounds;
  if (!canvas || !container) {
    throw new Error(`Pane state missing canvas/container DOM bounds:\n${JSON.stringify(state, null, 2)}`);
  }
  const overflowBottom = canvas.y + canvas.height - (container.y + container.height);
  const overflowRight = canvas.x + canvas.width - (container.x + container.width);
  const offsetTop = canvas.y - container.y;
  if (
    overflowBottom > OVERFLOW_TOLERANCE_PX
    || overflowRight > OVERFLOW_TOLERANCE_PX
    || offsetTop > OVERFLOW_TOLERANCE_PX
  ) {
    throw new Error(
      `Canvas rect escapes its terminal container: overflowBottom=${overflowBottom.toFixed(1)} `
      + `overflowRight=${overflowRight.toFixed(1)} offsetTop=${offsetTop.toFixed(1)}\n${JSON.stringify({ canvas, container }, null, 2)}`,
    );
  }
  return { canvas, container, overflowBottom, overflowRight, offsetTop };
}

async function writeMarkerAndWait(client, sessionId, paneId, marker) {
  await client.request('write_pane', { sessionId, paneId, text: `echo ${marker}`, submit: true });
  await waitForPaneText(
    client,
    sessionId,
    paneId,
    (text) => text.includes(marker),
    `pane ${paneId} painted marker ${marker}`,
    15_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-webgl-recovery.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'webgl-recovery');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const logPath = terminalDiagnosticsLogPath(currentHarnessProfile());
  const sessionLabel = `webgl-recovery-${runId}`;
  // Markers must survive as a single unwrapped line on narrow panes (~50 cols
  // in split layouts), so keep them short — uniqueness within this run's
  // paneId is all the log filter needs, not the full runId.
  const markerSuffix = runId.slice(-6);

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);
  console.log(`[RealAppHarness] diagnosticsLog=${logPath}`);

  let sessionId = null;
  try {
    await launchFreshAppAndConnect(client, observer);

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane', 20_000);
    const paneId = pane.paneId;
    await waitForPaneVisible(client, sessionId, paneId, 20_000);
    await waitForPaneAttached(client, sessionId, paneId, 20_000);
    await waitForPaneShellReady(client, sessionId, paneId, {
      timeoutMs: 20_000,
      description: 'initial shell pane ready',
    });
    await writeMarkerAndWait(client, sessionId, paneId, `PRE_${markerSuffix}`);

    const preLossState = await client.request('get_pane_state', { sessionId, paneId });
    measurePane(preLossState);
    console.log(`[RealAppHarness] pre-loss pane geometry sane for pane ${paneId}`);

    await client.request('lose_webgl_context', {});
    console.log('[RealAppHarness] lose_webgl_context requested');

    const recoveryEvents = await waitForRecoverySequence(logPath, paneId, RECOVERY_OUTCOMES, RECOVERY_TIMEOUT_MS);
    console.log(`[RealAppHarness] recovery sequence observed: ${recoveryEvents.map((event) => event.outcome).join(' -> ')}`);

    await writeMarkerAndWait(client, sessionId, paneId, `POST_${markerSuffix}`);
    console.log('[RealAppHarness] rebuilt renderer painted a fresh marker');

    const postRecoveryState = await client.request('get_pane_state', { sessionId, paneId });
    measurePane(postRecoveryState);
    if (postRecoveryState?.pane?.dom?.errorVisible) {
      throw new Error(`Pane still shows its error overlay after recovery:\n${JSON.stringify(postRecoveryState.pane.dom, null, 2)}`);
    }
    console.log('[RealAppHarness] post-recovery pane geometry sane, no error overlay');

    const summary = {
      ok: true,
      runId,
      sessionId,
      paneId,
      recoveryEvents,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] WebGL recovery scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    console.error('[RealAppHarness] WebGL recovery scenario failed.');
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    if (sessionId) {
      await captureSessionArtifacts(client, runDir, 'failure', sessionId).catch(() => {});
    }
    fs.writeFileSync(path.join(runDir, 'diagnostics-log-tail.txt'), tailLog(logPath, 200), 'utf8');
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
