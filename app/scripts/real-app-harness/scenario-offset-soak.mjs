#!/usr/bin/env node

// Randomized soak repro for a hard-to-reproduce bug: a terminal pane's
// rendered canvas ends up offset/clipped at the bottom (or side) of its
// pane, persisting until remount. Suspected triggers: switching among 4+
// workspaces (warm-set virtualization churn), font-size (UI scale) changes
// made while other workspaces are hidden, window resizes, splitting/closing
// panes (a real prod incident clipped in a narrow 292px-wide split), and
// re-activating a cold workspace mid-attach-replay (the replay storm resizes
// the canvas dozens of times over ~1s before settling — bouncing away and
// back interrupts that window).
//
// This drives the real packaged dev app through a seeded, weighted random
// walk of those actions and asserts, after every step, that the active
// workspace's canvas DOM rect sits inside its terminal container's rect.
// First violation stops the run, dumps evidence, and exits non-zero.

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
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const OVERFLOW_TOLERANCE_PX = 2;
const FONT_SCALE_MIN = 0.7;
const FONT_SCALE_MAX = 1.5;
const FONT_SCALE_STEP = 0.1;
const MAX_PANES_PER_WORKSPACE = 4;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = {
    ...parseCommonArgs([]),
    iterations: 150,
    workspaceCount: 6,
    seed: 1,
    warmLimit: 3,
    settleMs: 400,
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir' || arg === '--artifacts') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--iterations') options.iterations = Number.parseInt(args[++index], 10);
    else if (arg === '--workspaces') options.workspaceCount = Number.parseInt(args[++index], 10);
    else if (arg === '--seed') options.seed = Number.parseInt(args[++index], 10);
    else if (arg === '--warm-limit') options.warmLimit = Number.parseInt(args[++index], 10);
    else if (arg === '--settle-ms') options.settleMs = Number.parseInt(args[++index], 10);
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

// Deterministic PRNG (mulberry32) so a given --seed always drives the same
// action sequence.
function mulberry32(seed) {
  let state = seed >>> 0;
  return function next() {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function pickInt(rand, min, max) {
  return min + Math.floor(rand() * (max - min + 1));
}

function pickFrom(rand, list) {
  return list[Math.floor(rand() * list.length) % list.length];
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function fullScreenTuiCommand(markerLabel) {
  // Enters the alternate screen, redraws TOP/BOTTOM markers (with live
  // cols/rows) on every SIGWINCH, then idles. Painted content at the exact
  // bottom row makes bottom-clipping visually real, and the readout exposes
  // stale PTY size after a resize.
  //
  // markerLabel must be short (e.g. "s0") — the run's long, timestamped
  // session label does not fit some narrow panes, and a wrapped marker line
  // both scrolls the TOP row off-screen and garbles the BOTTOM row. Each
  // line is also truncated to the live terminal width (`printf '%.*s'`) so a
  // future narrow pane can never wrap, regardless of label length.
  return (
    `bash -c 'r(){ c=$(tput cols); l=$(tput lines); clear; ` +
    `top="TOP ${markerLabel} cols=$c rows=$l"; ` +
    `bot="BOTTOM ${markerLabel} cols=$c rows=$l"; ` +
    `printf "%.*s" "$((c-1))" "$top"; ` +
    `tput cup $((l-1)) 0; ` +
    `printf "%.*s" "$((c-1))" "$bot"; }; ` +
    `trap r WINCH; tput smcup; r; while :; do sleep 0.2; done'`
  );
}

async function createTuiWorkspace(client, observer, cwd, sessionLabel, markerLabel) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label: sessionLabel,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const workspace = await waitForSessionWorkspace(
    client,
    sessionId,
    (ws) => (ws?.panes || []).length === 1 && ws.panes[0].runtimeId,
    `initial pane for ${sessionLabel}`,
  );
  const pane = workspace.panes[0];
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: `initial shell pane ready for ${sessionLabel}`,
  });
  await client.request('write_pane', { sessionId, paneId: pane.paneId, text: fullScreenTuiCommand(markerLabel) });
  await waitForPaneText(
    client,
    sessionId,
    pane.paneId,
    (text) => text.includes(`TOP ${markerLabel}`) && text.includes(`BOTTOM ${markerLabel}`),
    `full-screen TUI painted for ${sessionLabel}`,
    20_000,
  );
  return {
    sessionId,
    workspaceId: workspace.workspaceId,
    label: sessionLabel,
    // Bookkeeping for the panes we've injected the full-screen TUI into, so
    // split/close_split actions know what exists and can hand out unique
    // marker labels. measureActiveWorkspacePanes does NOT read this — it
    // always re-fetches live panes from get_workspace — this is only for
    // action selection and marker-label bookkeeping.
    panes: [{ paneId: pane.paneId, markerLabel }],
    nextPaneMarker: 1,
  };
}

// Waits for a new pane to appear in the workspace, regardless of pane kind.
// scenarioAssertions.mjs's waitForNewShellPane filters to kind === 'shell',
// but these workspaces' panes are kind 'agent' (agent shell wrapper), so
// that filter never matches here and the wait times out even though the
// split succeeded. Mirror its resolution logic (prefer the workspace's
// activePaneId if it's new, else the first new pane) without the kind filter.
async function waitForNewPane(client, sessionId, existingPaneIds, description, timeoutMs = 20_000) {
  const workspace = await waitForSessionWorkspace(
    client,
    sessionId,
    (ws) => (ws?.panes || []).some((pane) => !existingPaneIds.has(pane.paneId)),
    description,
    timeoutMs,
  );
  const newPanes = (workspace.panes || []).filter((pane) => !existingPaneIds.has(pane.paneId));
  return newPanes.find((pane) => pane.paneId === workspace.activePaneId) || newPanes[0];
}

// Writes the same full-screen TUI marker script into a freshly split pane
// and waits for it to paint, mirroring what createTuiWorkspace does for a
// workspace's initial pane.
async function seedTuiIntoPane(client, sessionId, paneId, markerLabel, description) {
  await client.request('write_pane', { sessionId, paneId, text: fullScreenTuiCommand(markerLabel) });
  await waitForPaneText(
    client,
    sessionId,
    paneId,
    (text) => text.includes(`TOP ${markerLabel}`) && text.includes(`BOTTOM ${markerLabel}`),
    description,
    20_000,
  );
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
}

function boundsOf(dom, key) {
  const bounds = dom?.[key]?.bounds;
  if (!bounds) {
    return null;
  }
  return bounds;
}

// Checks every pane of the active workspace's canvas against its terminal
// container. Returns a list of per-pane measurements plus any violations
// (missing DOM nodes count as a violation too — that is itself evidence of
// a broken render, not something to silently skip).
async function measureActiveWorkspacePanes(client, sessionId) {
  const workspace = await client.request('get_workspace', { sessionId });
  const measurements = [];
  const violations = [];
  for (const pane of workspace.panes || []) {
    const state = await client.request('get_pane_state', { sessionId, paneId: pane.paneId });
    const dom = state?.pane?.dom;
    const canvas = boundsOf(dom, 'canvas');
    const container = boundsOf(dom, 'terminalContainer');
    if (!canvas || !container) {
      violations.push({
        paneId: pane.paneId,
        reason: 'missing dom bounds',
        canvas,
        container,
      });
      continue;
    }
    const domOffsetTop = canvas.y - container.y;
    const domOverflowBottom = canvas.y + canvas.height - (container.y + container.height);
    const domOverflowRight = canvas.x + canvas.width - (container.x + container.width);
    const measurement = {
      paneId: pane.paneId,
      canvas,
      container,
      domOffsetTop,
      domOverflowBottom,
      domOverflowRight,
    };
    measurements.push(measurement);
    if (
      domOverflowBottom > OVERFLOW_TOLERANCE_PX ||
      domOffsetTop > OVERFLOW_TOLERANCE_PX ||
      domOverflowRight > OVERFLOW_TOLERANCE_PX
    ) {
      violations.push({ ...measurement, reason: 'canvas rect escapes terminal container rect' });
    }
  }
  return { measurements, violations };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-offset-soak.mjs');
    console.log(`Additional options:
  --iterations <n>     Number of random action steps to run (default: 150)
  --workspaces <n>     Number of shell workspaces to create (default: 6)
  --seed <n>           PRNG seed for deterministic action sequence (default: 1)
  --warm-limit <n>     Warm workspace virtualization limit (default: 3)
  --settle-ms <n>      Settle delay before each post-step assertion (default: 400)
`);
    return;
  }
  if (!Number.isInteger(options.iterations) || options.iterations <= 0) {
    throw new Error(`--iterations must be a positive integer, got: ${options.iterations}`);
  }
  if (!Number.isInteger(options.workspaceCount) || options.workspaceCount < 2) {
    throw new Error(`--workspaces must be an integer >= 2, got: ${options.workspaceCount}`);
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'offset-soak');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const rand = mulberry32(options.seed);

  const workspaces = [];
  const trace = [];
  let maxOverflowSeen = 0;
  let transientCount = 0;
  const transientSteps = [];
  const TRANSIENT_RECHECK_DELAY_MS = 1_500;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);
  console.log(`[RealAppHarness] seed=${options.seed} iterations=${options.iterations} workspaces=${options.workspaceCount} warmLimit=${options.warmLimit}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    await client.request('set_warm_workspace_limit', { limit: options.warmLimit }).catch((error) => {
      console.warn(`[RealAppHarness] set_warm_workspace_limit failed: ${error.message}`);
    });

    for (let index = 0; index < options.workspaceCount; index += 1) {
      const sessionLabel = `offsetsoak-${runId}-${index}`;
      const markerLabel = `s${index}`; // short: must fit inside any pane width without wrapping
      const workspace = await createTuiWorkspace(client, observer, path.join(sessionDir, `ws${index}`), sessionLabel, markerLabel);
      workspaces.push(workspace);
      console.log(`[RealAppHarness] created workspace ${index}: sessionId=${workspace.sessionId} workspaceId=${workspace.workspaceId} marker=${markerLabel}`);
    }

    let activeIndex = workspaces.length - 1; // last created workspace is selected

    // Recency of activation, oldest-first: index 0 is the workspace least
    // recently made active, i.e. the one most likely evicted from the warm
    // set and coldest on next re-activation. Setup activated workspaces in
    // creation order, so that's the initial recency order too.
    const recency = workspaces.map((_, index) => index);
    const markActive = (index) => {
      const position = recency.indexOf(index);
      if (position !== -1) {
        recency.splice(position, 1);
      }
      recency.push(index);
    };

    const switchTo = async (index) => {
      const target = workspaces[index];
      await client.request('select_session', { sessionId: target.sessionId });
      activeIndex = index;
      markActive(index);
    };

    const otherIndex = (excludeIndex) => {
      let candidate = excludeIndex;
      while (candidate === excludeIndex) {
        candidate = pickInt(rand, 0, workspaces.length - 1);
      }
      return candidate;
    };

    // The coldest OTHER workspace: the least-recently-active entry in
    // `recency` that isn't excludeIndex.
    const leastRecentlyActiveIndex = (excludeIndex) => {
      for (const index of recency) {
        if (index !== excludeIndex) {
          return index;
        }
      }
      return excludeIndex;
    };

    let windowBounds = await getFrontWindowBounds(client.bundleId, { client }).catch(() => null);
    let fontScaleSteps = 0; // relative to default 1.0, in FONT_SCALE_STEP units

    for (let step = 1; step <= options.iterations; step += 1) {
      const roll = rand();
      let action;
      const params = {};

      // Weighted action distribution: switch 35%, font_scale 20%,
      // window_resize 15%, rapid_double_switch 5%, bounce_switch 10%,
      // split 10%, close_split 5%.
      if (roll < 0.35) {
        action = 'switch';
        params.switchToIndex = otherIndex(activeIndex);
        await switchTo(params.switchToIndex);
      } else if (roll < 0.55) {
        action = 'font_scale';
        const doReset = rand() < 0.15;
        if (doReset) {
          params.shortcut = 'ui.resetFontSize';
          fontScaleSteps = 0;
        } else {
          const increase = rand() < 0.5;
          const atMax = fontScaleSteps >= Math.round((FONT_SCALE_MAX - 1) / FONT_SCALE_STEP);
          const atMin = fontScaleSteps <= -Math.round((1 - FONT_SCALE_MIN) / FONT_SCALE_STEP);
          const goUp = increase && !atMax ? true : (!increase && !atMin ? false : !atMax);
          params.shortcut = goUp ? 'ui.increaseFontSize' : 'ui.decreaseFontSize';
          fontScaleSteps += goUp ? 1 : -1;
        }
        await client.request('dispatch_shortcut', { shortcutId: params.shortcut });
        params.switchToIndex = otherIndex(activeIndex);
        await switchTo(params.switchToIndex);
      } else if (roll < 0.70) {
        action = 'window_resize';
        if (!windowBounds) {
          windowBounds = await getFrontWindowBounds(client.bundleId, { client });
        }
        const deltaW = pickInt(rand, 20, 80) * pickFrom(rand, [1, -1]);
        const deltaH = pickInt(rand, 20, 80) * pickFrom(rand, [1, -1]);
        const nextBounds = {
          x: windowBounds.x,
          y: windowBounds.y,
          width: Math.max(900, Math.min(2200, windowBounds.width + deltaW)),
          height: Math.max(600, Math.min(1400, windowBounds.height + deltaH)),
        };
        params.fromBounds = windowBounds;
        params.toBounds = nextBounds;
        windowBounds = await setFrontWindowBounds(nextBounds, { client });
        params.switchToIndex = otherIndex(activeIndex);
        await switchTo(params.switchToIndex);
      } else if (roll < 0.75) {
        action = 'rapid_double_switch';
        const first = otherIndex(activeIndex);
        const second = otherIndex(first);
        params.first = first;
        params.second = second;
        await switchTo(first);
        await switchTo(second);
      } else if (roll < 0.85) {
        // Best persistent-bug candidate: re-activate the coldest workspace
        // (most likely evicted from the warm set), interrupt its attach
        // replay storm (dozens of resizeLocal events over ~1s before it
        // settles) with an away-and-back bounce, then land back on it so the
        // usual post-step measurement checks it mid-settle.
        action = 'bounce_switch';
        const coldTarget = leastRecentlyActiveIndex(activeIndex);
        params.coldTargetIndex = coldTarget;
        await switchTo(coldTarget);

        const delay1Ms = pickInt(rand, 100, 600);
        params.delay1Ms = delay1Ms;
        await delay(delay1Ms);

        const bounceAwayIndex = otherIndex(coldTarget);
        params.bounceAwayIndex = bounceAwayIndex;
        await switchTo(bounceAwayIndex);

        const delay2Ms = pickInt(rand, 100, 600);
        params.delay2Ms = delay2Ms;
        await delay(delay2Ms);

        await switchTo(coldTarget);
      } else if (roll < 0.95) {
        action = 'split';
        const activeWorkspace = workspaces[activeIndex];
        const workspaceState = await client.request('get_workspace', { sessionId: activeWorkspace.sessionId });
        const existingPanes = workspaceState.panes || [];
        if (existingPanes.length >= MAX_PANES_PER_WORKSPACE) {
          action = 'split_fallback_switch';
          params.reason = `pane cap reached (${existingPanes.length}/${MAX_PANES_PER_WORKSPACE})`;
          params.switchToIndex = otherIndex(activeIndex);
          await switchTo(params.switchToIndex);
        } else {
          const direction = pickFrom(rand, ['vertical', 'horizontal']);
          params.direction = direction;
          const existingPaneIds = new Set(existingPanes.map((pane) => pane.paneId));
          await client.request('split_pane', { sessionId: activeWorkspace.sessionId, direction });
          const newPane = await waitForNewPane(
            client,
            activeWorkspace.sessionId,
            existingPaneIds,
            `new split pane for ${activeWorkspace.label}`,
            20_000,
          );
          await waitForPaneVisible(client, activeWorkspace.sessionId, newPane.paneId, 20_000);
          await waitForPaneAttached(client, activeWorkspace.sessionId, newPane.paneId, 20_000);
          await waitForPaneShellReady(client, activeWorkspace.sessionId, newPane.paneId, {
            timeoutMs: 20_000,
            description: `split pane ready for ${activeWorkspace.label}`,
          });
          const markerLabel = `s${activeIndex}p${activeWorkspace.nextPaneMarker}`;
          activeWorkspace.nextPaneMarker += 1;
          await seedTuiIntoPane(
            client,
            activeWorkspace.sessionId,
            newPane.paneId,
            markerLabel,
            `full-screen TUI painted for split pane ${markerLabel}`,
          );
          activeWorkspace.panes.push({ paneId: newPane.paneId, markerLabel });
          params.paneId = newPane.paneId;
          params.markerLabel = markerLabel;
        }
      } else {
        action = 'close_split';
        const activeWorkspace = workspaces[activeIndex];
        const workspaceState = await client.request('get_workspace', { sessionId: activeWorkspace.sessionId });
        const existingPanes = workspaceState.panes || [];
        if (existingPanes.length <= 1) {
          action = 'close_split_fallback_switch';
          params.reason = 'workspace has only one pane';
          params.switchToIndex = otherIndex(activeIndex);
          await switchTo(params.switchToIndex);
        } else {
          // Never close the first pane — pick uniformly among the rest.
          const closeIndex = 1 + pickInt(rand, 0, existingPanes.length - 2);
          const paneToClose = existingPanes[closeIndex];
          params.paneId = paneToClose.paneId;
          await client.request('close_pane', { sessionId: activeWorkspace.sessionId, paneId: paneToClose.paneId });
          activeWorkspace.panes = activeWorkspace.panes.filter((pane) => pane.paneId !== paneToClose.paneId);
        }
      }

      await delay(options.settleMs);

      const activeWorkspace = workspaces[activeIndex];
      const { measurements, violations } = await measureActiveWorkspacePanes(client, activeWorkspace.sessionId);
      const stepMaxOverflow = measurements.reduce(
        (max, measurement) => Math.max(max, measurement.domOffsetTop, measurement.domOverflowBottom, measurement.domOverflowRight),
        0,
      );
      maxOverflowSeen = Math.max(maxOverflowSeen, stepMaxOverflow);

      const traceEntry = {
        step,
        action,
        params,
        activeWorkspaceIndex: activeIndex,
        activeSessionId: activeWorkspace.sessionId,
        maxOverflowPx: stepMaxOverflow,
      };
      trace.push(traceEntry);
      console.log(
        `[RealAppHarness] step=${step} action=${action} active=${activeIndex} maxOverflowPx=${stepMaxOverflow.toFixed(1)}`,
      );

      if (violations.length > 0) {
        // Production incidents show transient clips that self-heal within
        // ~1s (a RAF self-heal path exists). Don't fail on the first sighting
        // — wait and re-measure the same workspace before deciding whether
        // this is the persistent bug (survives until remount) or a transient
        // one we should log and keep soaking past.
        console.warn(`[RealAppHarness] suspect clip at step ${step}, rechecking after ${TRANSIENT_RECHECK_DELAY_MS}ms...`);
        await delay(TRANSIENT_RECHECK_DELAY_MS);
        const confirm = await measureActiveWorkspacePanes(client, activeWorkspace.sessionId);

        if (confirm.violations.length === 0) {
          transientCount += 1;
          transientSteps.push(step);
          traceEntry.transient = true;
          traceEntry.firstMeasurements = measurements;
          traceEntry.confirmMeasurements = confirm.measurements;
          console.warn(`[RealAppHarness] TRANSIENT clip at step ${step}, self-healed`);
          continue;
        }

        console.error(`[RealAppHarness] VIOLATION at step ${step}: canvas escaped its terminal container.`);
        fs.writeFileSync(path.join(runDir, 'trace.json'), `${JSON.stringify(trace, null, 2)}\n`, 'utf8');
        fs.writeFileSync(
          path.join(runDir, 'violation.json'),
          `${JSON.stringify(
            {
              step,
              action,
              params,
              activeWorkspaceIndex: activeIndex,
              first: { violations, measurements },
              confirm: { violations: confirm.violations, measurements: confirm.measurements },
            },
            null,
            2,
          )}\n`,
          'utf8',
        );
        const workspace = await client.request('get_workspace', { sessionId: activeWorkspace.sessionId }).catch(() => null);
        const allPaneStates = {};
        for (const pane of workspace?.panes || []) {
          allPaneStates[pane.paneId] = await client
            .request('get_pane_state', { sessionId: activeWorkspace.sessionId, paneId: pane.paneId })
            .catch((error) => ({ error: error instanceof Error ? error.message : String(error) }));
        }
        fs.writeFileSync(
          path.join(runDir, 'violation-pane-states.json'),
          `${JSON.stringify(allPaneStates, null, 2)}\n`,
          'utf8',
        );
        await captureSessionArtifacts(client, runDir, 'violation', activeWorkspace.sessionId).catch(() => {});
        for (const violation of confirm.violations) {
          const paneText = await client.request('read_pane_text', {
            sessionId: activeWorkspace.sessionId,
            paneId: violation.paneId,
          }).catch((error) => ({ error: error instanceof Error ? error.message : String(error) }));
          fs.writeFileSync(
            path.join(runDir, `violation-pane-${violation.paneId}-text.json`),
            `${JSON.stringify(paneText, null, 2)}\n`,
            'utf8',
          );
        }

        const historyTail = trace.slice(-15);
        console.error('[RealAppHarness] Last 15 steps before violation:');
        for (const entry of historyTail) {
          console.error(`  step=${entry.step} action=${entry.action} active=${entry.activeWorkspaceIndex} maxOverflowPx=${entry.maxOverflowPx.toFixed(1)}${entry.transient ? ' (transient)' : ''}`);
        }
        console.error(`[RealAppHarness] seed=${options.seed} failing step=${step}. Evidence written to ${runDir}`);
        process.exitCode = 1;
        return;
      }
    }

    fs.writeFileSync(path.join(runDir, 'trace.json'), `${JSON.stringify(trace, null, 2)}\n`, 'utf8');
    const summary = {
      ok: true,
      runId,
      seed: options.seed,
      iterations: options.iterations,
      workspaceCount: options.workspaceCount,
      warmLimit: options.warmLimit,
      maxOverflowPxSeen: maxOverflowSeen,
      transientCount,
      transientSteps,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log(`[RealAppHarness] Offset soak passed: ${options.iterations} steps, no persistent violation, maxOverflowPxSeen=${maxOverflowSeen.toFixed(1)}, transientCount=${transientCount}.`);
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const workspace of workspaces.reverse()) {
      await closeWorkspacePanes(client, workspace.sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
