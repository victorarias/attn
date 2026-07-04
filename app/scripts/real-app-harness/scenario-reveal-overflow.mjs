#!/usr/bin/env node

// Packaged-app repro for a reveal-time clip: a hidden workspace's terminal
// keeps its old, larger grid while the window shrinks around it. When the
// workspace is revealed again, its model grid (rows x cellHeight) can be
// taller than the container it renders into, and the reveal refit's late
// retry historically only fired for too-TINY grids, not too-TALL ones — so
// the pane stayed clipped indefinitely until something unrelated (e.g. a
// window nudge) re-triggered a fit.
//
// To land workspace A on a genuinely too-tall grid (not just re-measure the
// same size), this enlarges the window while A is active (confirming A's
// pane actually grew into the extra height), switches to workspace B, shrinks
// the window back to its original size while A is hidden, then reveals A and
// asserts the revealed pane's canvas converges inside its terminal container
// within a short deadline — the reveal-time backstop's invariant. Every
// window-height transition is asserted to actually change (never a no-op),
// so a vacuous run can never report pass.

import fs from 'node:fs';
import path from 'node:path';
import {
  assertCommonTargetAllowed,
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import {
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const OVERFLOW_TOLERANCE_PX = 2;
const WINDOW_ENLARGE_HEIGHT_PX = 250;
const REVEAL_CONVERGENCE_DEADLINE_MS = 600;
const REVEAL_CONVERGENCE_POLL_MS = 100;
const ENLARGE_REFIT_DEADLINE_MS = 5_000;
// A's container must grow by at least half the enlarge delta to count as
// proof it actually refit taller, not just noise/rounding.
const ENLARGE_REFIT_MIN_GROWTH_PX = WINDOW_ENLARGE_HEIGHT_PX / 2;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
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
  if (!options.help) {
    assertCommonTargetAllowed(options, args);
  }
  return {
    options,
    help: Boolean(options.help),
  };
}

async function createShellWorkspace(client, observer, cwd, label) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for ${label}`);
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: `initial shell pane ready for ${label}`,
  });
  return { sessionId, paneId: pane.paneId, label };
}

function boundsOf(dom, key) {
  const bounds = dom?.[key]?.bounds;
  if (!bounds) {
    return null;
  }
  return bounds;
}

// Measures the active pane's canvas rect against its terminal container rect.
// Returns null measurements (treated as "not yet clean") when DOM nodes are
// missing, since a mid-reveal pane can transiently lack them.
async function measurePane(client, sessionId, paneId) {
  const state = await client.request('get_pane_state', { sessionId, paneId });
  const dom = state?.pane?.dom;
  const canvas = boundsOf(dom, 'canvas');
  const container = boundsOf(dom, 'terminalContainer');
  if (!canvas || !container) {
    return { clean: false, reason: 'missing dom bounds', canvas, container };
  }
  const overflowBottom = canvas.y + canvas.height - (container.y + container.height);
  const overflowRight = canvas.x + canvas.width - (container.x + container.width);
  const clean = overflowBottom <= OVERFLOW_TOLERANCE_PX && overflowRight <= OVERFLOW_TOLERANCE_PX;
  return { clean, overflowBottom, overflowRight, canvas, container };
}

// Polls measurePane every REVEAL_CONVERGENCE_POLL_MS until either a clean
// read is observed or the deadline elapses, returning the sequence of
// measurements taken (for evidence) and whether it ever converged.
async function waitForRevealConvergence(client, sessionId, paneId) {
  const deadline = Date.now() + REVEAL_CONVERGENCE_DEADLINE_MS;
  const measurements = [];
  for (;;) {
    const measurement = await measurePane(client, sessionId, paneId);
    measurements.push({ atMs: REVEAL_CONVERGENCE_DEADLINE_MS - (deadline - Date.now()), ...measurement });
    if (measurement.clean) {
      return { converged: true, measurements };
    }
    if (Date.now() >= deadline) {
      return { converged: false, measurements };
    }
    await sleep(REVEAL_CONVERGENCE_POLL_MS);
  }
}

// Polls measurePane until the pane's container has grown to at least
// minHeight, or throws. Used to prove the pre-shrink enlarge actually landed
// A on a taller grid — without this, a window resize that the pane never
// refit to would let the scenario "pass" without exercising anything.
async function waitForContainerHeightAtLeast(client, sessionId, paneId, minHeight, description, timeoutMs = ENLARGE_REFIT_DEADLINE_MS) {
  const deadline = Date.now() + timeoutMs;
  let lastMeasurement = null;
  for (;;) {
    lastMeasurement = await measurePane(client, sessionId, paneId);
    if (lastMeasurement.container && lastMeasurement.container.height >= minHeight) {
      return lastMeasurement;
    }
    if (Date.now() >= deadline) {
      throw new Error(
        `Timed out waiting for ${description} (wanted container height >= ${minHeight}). Last measurement: ${JSON.stringify(lastMeasurement)}`,
      );
    }
    await sleep(REVEAL_CONVERGENCE_POLL_MS);
  }
}

// A window-height transition that turns out to be a no-op (e.g. clamped by
// the OS, or the harness already parked at the target size) would let this
// scenario "pass" without ever exercising the bug. Fail loudly instead.
function assertHeightChanged(beforeHeight, afterHeight, label) {
  if (beforeHeight === afterHeight) {
    throw new Error(`${label}: window height did not change (stayed ${beforeHeight}) — scenario would pass vacuously`);
  }
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await sleep(200);
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-reveal-overflow.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'reveal-overflow');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  let workspaceA;
  let workspaceB;

  try {
    await launchFreshAppAndConnect(client, observer);

    // Workspace A is created (and selected) alone, so it is unambiguously
    // the active workspace while we enlarge the window under it.
    workspaceA = await createShellWorkspace(client, observer, path.join(sessionDir, 'ws-a'), `revealoverflow-${runId}-a`);
    console.log(`[RealAppHarness] created workspace A: sessionId=${workspaceA.sessionId}`);

    const originalBounds = await getFrontWindowBounds(client.bundleId, { client });
    const baselineMeasurement = await measurePane(client, workspaceA.sessionId, workspaceA.paneId);
    const baselineContainerHeight = baselineMeasurement.container?.height ?? 0;

    const enlargedBounds = {
      x: originalBounds.x,
      y: originalBounds.y,
      width: originalBounds.width,
      height: originalBounds.height + WINDOW_ENLARGE_HEIGHT_PX,
    };
    console.log(`[RealAppHarness] enlarging window from height=${originalBounds.height} to height=${enlargedBounds.height} (workspace A active)`);
    const appliedEnlargedBounds = await setFrontWindowBounds(enlargedBounds, { client });
    assertHeightChanged(originalBounds.height, appliedEnlargedBounds.height, 'enlarge');

    // Prove A actually refit taller, not just that the window resized.
    await waitForContainerHeightAtLeast(
      client,
      workspaceA.sessionId,
      workspaceA.paneId,
      baselineContainerHeight + ENLARGE_REFIT_MIN_GROWTH_PX,
      'workspace A pane to refit to the enlarged window',
    );
    console.log('[RealAppHarness] confirmed workspace A refit to a taller grid at the enlarged window size');

    // Creating workspace B selects it, hiding A while it still holds the
    // tall grid from the enlarged window.
    workspaceB = await createShellWorkspace(client, observer, path.join(sessionDir, 'ws-b'), `revealoverflow-${runId}-b`);
    console.log(`[RealAppHarness] created workspace B: sessionId=${workspaceB.sessionId} (workspace A now hidden)`);

    const shrunkBounds = {
      x: originalBounds.x,
      y: originalBounds.y,
      width: originalBounds.width,
      height: originalBounds.height,
    };
    console.log(`[RealAppHarness] shrinking window back from height=${appliedEnlargedBounds.height} to height=${shrunkBounds.height} (workspace A hidden)`);
    const appliedShrunkBounds = await setFrontWindowBounds(shrunkBounds, { client });
    assertHeightChanged(appliedEnlargedBounds.height, appliedShrunkBounds.height, 'shrink');

    // Reveal workspace A while it still holds the too-tall grid from before
    // the shrink — this is the moment the reveal-time backstop must catch.
    console.log(`[RealAppHarness] revealing workspace A (sessionId=${workspaceA.sessionId})`);
    await client.request('select_session', { sessionId: workspaceA.sessionId });
    await waitForPaneVisible(client, workspaceA.sessionId, workspaceA.paneId, 20_000);

    const { converged, measurements } = await waitForRevealConvergence(client, workspaceA.sessionId, workspaceA.paneId);

    fs.writeFileSync(path.join(runDir, 'measurements.json'), `${JSON.stringify(measurements, null, 2)}\n`, 'utf8');

    if (!converged) {
      console.error(
        `[RealAppHarness] VIOLATION: revealed pane did not converge within ${REVEAL_CONVERGENCE_DEADLINE_MS}ms.`,
      );
      await captureSessionArtifacts(client, runDir, 'violation', workspaceA.sessionId).catch(() => {});
      console.error(`[RealAppHarness] Evidence written to ${runDir}`);
      process.exitCode = 1;
      return;
    }

    const summary = {
      ok: true,
      runId,
      convergedAfterMeasurements: measurements.length,
      originalWindowHeightPx: originalBounds.height,
      enlargedWindowHeightPx: appliedEnlargedBounds.height,
      baselineContainerHeightPx: baselineContainerHeight,
      deadlineMs: REVEAL_CONVERGENCE_DEADLINE_MS,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log(`[RealAppHarness] Reveal-overflow scenario passed after ${measurements.length} measurement(s).`);
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const workspace of [workspaceB, workspaceA]) {
      if (workspace) {
        await closeWorkspacePanes(client, workspace.sessionId).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
