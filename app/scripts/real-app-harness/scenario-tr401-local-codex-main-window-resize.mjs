#!/usr/bin/env node

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import { getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import {
  assertPaneCoverage,
  assertPaneVisibleContent,
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneState,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { ensureCodexInitialPanePromptReady } from './scenarioAgents.mjs';

function normalizeBaselineWindowBounds(bounds) {
  return {
    x: 80,
    y: 80,
    width: Math.max(bounds.width, 1_280),
    height: Math.max(bounds.height, 800),
  };
}

function narrowWindowBounds(bounds) {
  const width = Math.max(700, Math.floor(bounds.width * 0.55));
  if (width >= bounds.width) {
    throw new Error(`Computed narrow target does not reduce bounds: ${JSON.stringify(bounds)}`);
  }
  return {
    x: bounds.x,
    y: bounds.y,
    width,
    height: bounds.height,
  };
}

function codexHeaderOptions(description) {
  return {
    contains: 'OpenAI Codex',
    allowWrappedContains: true,
    minNonEmptyLines: 2,
    minDenseLines: 0,
    minCharCount: 20,
    minMaxLineLength: 12,
    timeoutMs: 20_000,
    description,
  };
}

function assertCompleteCodexHeaderFrame(state, description) {
  const lines = state?.pane?.visibleContent?.lines || [];
  const text = lines.join('\n');
  // Codex may print extra bordered boxes (e.g. the "Update available!" banner
  // when a newer release exists), so border counts over the whole pane are not
  // meaningful. Anchor on the box that CONTAINS the header line instead: the
  // nearest ╭ line above it and the nearest ╰ line below it must both be
  // complete at the current width.
  const headerCount = (text.match(/OpenAI Codex/g) || []).length;
  const headerIndex = lines.findIndex((line) => line.includes('OpenAI Codex'));
  let topBorder = null;
  for (let i = headerIndex; i >= 0; i -= 1) {
    if (lines[i].includes('╭')) { topBorder = lines[i]; break; }
  }
  let bottomBorder = null;
  for (let i = headerIndex; i >= 0 && i < lines.length; i += 1) {
    if (lines[i].includes('╰')) { bottomBorder = lines[i]; break; }
  }
  const completeTopBorder = topBorder != null && topBorder.trimEnd().endsWith('╮');
  const completeBottomBorder = bottomBorder != null && bottomBorder.trimEnd().endsWith('╯');
  if (headerCount !== 1 || headerIndex < 0 || !completeTopBorder || !completeBottomBorder) {
    throw new Error(`${description}: expected one complete Codex header frame, found ${headerCount} headers, topBorderComplete=${completeTopBorder}, bottomBorderComplete=${completeBottomBorder}\n${text}`);
  }
}

async function waitForCompleteCodexHeaderFrame(client, sessionId, paneId, description, timeoutMs = 5_000) {
  const startedAt = Date.now();
  let lastState = null;
  let lastError = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId });
    try {
      assertCompleteCodexHeaderFrame(lastState, description);
      return lastState;
    } catch (error) {
      lastError = error;
    }
    await sleep(100);
  }
  throw lastError || new Error(`Timed out waiting for ${description}`);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr401-local-codex-main-window-resize.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-401-CODEX-MAIN',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr401-local-codex-main-window-resize',
    metadata: {
      agent: 'codex',
      focus: 'fresh Codex initial pane window resize header preservation',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let baselineWindow = null;
  let narrowWindow = null;
  let restoredWindow = null;
  let initialPaneId = null;
  let baselineMain = null;
  let narrowMain = null;
  let restoredMain = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    baselineWindow = await runner.step('normalize_window_bounds', async () => {
      const currentBounds = await getFrontWindowBounds(client.bundleId, { client });
      return setFrontWindowBounds(normalizeBaselineWindowBounds(currentBounds), { client });
    });

    sessionId = await runner.step('create_codex_session', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr401-codex-main-${runner.runId}`,
        agent: 'codex',
        waitForInitialPaneVisible: false,
      });
    });

    await runner.step('capture_baseline_header', async () => {
      await client.request('select_session', { sessionId });
      const readiness = await ensureCodexInitialPanePromptReady(client, sessionId, 45_000);
      initialPaneId = readiness.paneId || (await waitForFirstWorkspacePane(client, sessionId, 'Codex initial pane', 20_000)).paneId;
      await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);
      baselineMain = await assertPaneVisibleContent(
        client,
        sessionId,
        initialPaneId,
        codexHeaderOptions('Codex header visible before initial-pane window resize'),
      );
      baselineMain = await waitForCompleteCodexHeaderFrame(client, sessionId, initialPaneId, 'Codex baseline header frame');
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    narrowWindow = await runner.step('narrow_window_and_capture_header', async () => {
      const nextWindow = await setFrontWindowBounds(narrowWindowBounds(baselineWindow), { client });
      narrowMain = await waitForPaneState(
        client,
        sessionId,
        initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = baselineMain?.pane?.bounds?.width ?? 0;
          return width > 0 && width <= Math.floor(baselineWidth * 0.65);
        },
        'Codex initial pane width to shrink after window resize',
        20_000,
      );
      await captureSessionArtifacts(client, runner.runDir, '02-narrow-before-assert', sessionId);
      narrowMain = await assertPaneVisibleContent(
        client,
        sessionId,
        initialPaneId,
        codexHeaderOptions('Codex header visible after narrowing main-only window'),
      );
      narrowMain = await waitForCompleteCodexHeaderFrame(client, sessionId, initialPaneId, 'Codex narrowed header frame');
      await captureSessionArtifacts(client, runner.runDir, '02-narrow-after-assert', sessionId);
      await assertPaneCoverage(client, sessionId, initialPaneId, {
        minWidthRatio: 0.75,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'Codex initial pane coverage after narrowing single-pane window',
      });
      return nextWindow;
    });

    restoredWindow = await runner.step('restore_window_and_capture_header', async () => {
      const nextWindow = await setFrontWindowBounds(baselineWindow, { client });
      restoredMain = await waitForPaneState(
        client,
        sessionId,
        initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = baselineMain?.pane?.bounds?.width ?? 0;
          return width >= Math.floor(baselineWidth * 0.95);
        },
        'Codex initial pane width to restore after window resize',
        20_000,
      );
      await captureSessionArtifacts(client, runner.runDir, '03-restored-before-assert', sessionId);
      restoredMain = await assertPaneVisibleContent(
        client,
        sessionId,
        initialPaneId,
        codexHeaderOptions('Codex header visible after restoring main-only window'),
      );
      restoredMain = await waitForCompleteCodexHeaderFrame(client, sessionId, initialPaneId, 'Codex restored header frame');
      await captureSessionArtifacts(client, runner.runDir, '03-restored-after-assert', sessionId);
      return nextWindow;
    });

    const summary = runner.finishSuccess({
      sessionId,
      windowBounds: { baselineWindow, narrowWindow, restoredWindow },
      paneBounds: {
        baselineMain: baselineMain?.pane?.bounds ?? null,
        narrowMain: narrowMain?.pane?.bounds ?? null,
        restoredMain: restoredMain?.pane?.bounds ?? null,
      },
      artifacts: { runDir: runner.runDir, trace: runner.tracePath },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId).catch(() => {});
    }
    const summary = runner.finishFailure(error, {
      sessionId,
      windowBounds: { baselineWindow, narrowWindow, restoredWindow },
      paneBounds: {
        baselineMain: baselineMain?.pane?.bounds ?? null,
        narrowMain: narrowMain?.pane?.bounds ?? null,
        restoredMain: restoredMain?.pane?.bounds ?? null,
      },
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
