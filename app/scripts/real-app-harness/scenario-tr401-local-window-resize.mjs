#!/usr/bin/env node

import {
  createSessionAndWaitForInitialPane,
  assertCommonTargetAllowed,
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
  assertPaneNativePaintCoverage,
  assertPaneNativePaintRecovered,
  assertPaneUsesVisibleWidth,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  waitForFirstWorkspacePane,
  waitForNewShellPane,
  waitForPaneAttached,
  waitForPaneState,
  waitForPaneText,
  waitForPaneVisible,
  tokenAnchorIgnorePatterns,
} from './scenarioAssertions.mjs';
import {
  ensureCodexInitialPanePromptReady,
  ensureClaudeInitialPanePromptReady,
  promptClaudeForStructuredBlock,
} from './scenarioAgents.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = {
    ...parseCommonArgs([]),
    agent: process.env.ATTN_LOCAL_WINDOW_RESIZE_AGENT || 'claude',
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--agent') options.agent = args[++index] || options.agent;
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }
  if (!options.help) assertCommonTargetAllowed(options, args);
  options.agent = String(options.agent || 'claude').toLowerCase();
  if (options.agent !== 'codex' && options.agent !== 'claude') {
    throw new Error(`Unsupported agent for local window-resize scenario: ${options.agent}`);
  }
  return {
    options,
    help: Boolean(options.help),
  };
}

function normalizeBaselineWindowBounds(bounds) {
  return {
    x: 80,
    y: 80,
    width: Math.max(bounds.width, 1_280),
    height: Math.max(bounds.height, 800),
  };
}

function shrunkWindowBounds(bounds) {
  const width = Math.floor(bounds.width * 0.78);
  const height = Math.floor(bounds.height * 0.82);
  if (width >= bounds.width || height >= bounds.height) {
    throw new Error(`Computed shrink target does not reduce bounds: ${JSON.stringify(bounds)}`);
  }
  return {
    x: bounds.x,
    y: bounds.y,
    width,
    height,
  };
}

function paneShrinkThreshold(bounds) {
  return {
    width: Math.floor(bounds.width * 0.9),
    height: Math.floor(bounds.height * 0.9),
  };
}

function paneRecoveryThreshold(bounds) {
  return {
    width: Math.floor(bounds.width * 0.95),
    height: Math.floor(bounds.height * 0.95),
  };
}

function initialPaneContentOptions(token, description, phase = 'baseline') {
  if (phase === 'shrunk') {
    return {
      contains: token,
      allowWrappedContains: true,
      minNonEmptyLines: 3,
      minDenseLines: 1,
      minCharCount: 90,
      minMaxLineLength: 16,
      timeoutMs: 30_000,
      description,
    };
  }
  return {
    contains: token,
    allowWrappedContains: true,
    minNonEmptyLines: 4,
    minDenseLines: 1,
    minCharCount: 120,
    minMaxLineLength: 18,
    timeoutMs: 45_000,
    description,
  };
}

function utilityContentOptions(token, description, phase = 'baseline') {
  if (phase === 'shrunk') {
    return {
      contains: token,
      allowWrappedContains: true,
      minNonEmptyLines: 3,
      minDenseLines: 1,
      minCharCount: 80,
      minMaxLineLength: 16,
      timeoutMs: 20_000,
      description,
    };
  }
  return {
    contains: token,
    allowWrappedContains: true,
    minNonEmptyLines: 4,
    minDenseLines: 1,
    minCharCount: 110,
    minMaxLineLength: 18,
    timeoutMs: 20_000,
    description,
  };
}

function widthUsageOptions(description, phase = 'baseline') {
  if (phase === 'shrunk') {
    return {
      minMaxOccupiedWidthRatio: 0.55,
      minWideLineCount: 2,
      minMedianOccupiedWidthRatio: 0.4,
      timeoutMs: 20_000,
      description,
    };
  }
  return {
    minMaxOccupiedWidthRatio: 0.62,
    minWideLineCount: 3,
    minMedianOccupiedWidthRatio: 0.46,
    timeoutMs: 20_000,
    description,
  };
}

const nativeCoverageThresholds = {
  minBusyColumnRatio: 0.3,
  minBusyRowRatio: 0.08,
  minBBoxWidthRatio: 0.3,
  minBBoxHeightRatio: 0.1,
};

function utilitySeedCommand(token, lineCount = 6) {
  return `node -e "for (let i = 1; i <= ${lineCount}; i += 1) console.log('${token} line ' + String(i).padStart(2, '0') + ' render width coverage payload ' + 'X'.repeat(40))"`;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr401-local-window-resize.mjs');
    console.log(`Additional options:
  --agent <codex|claude>        Local agent to exercise (default: claude)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-401',
    tier: 'tier2-local-real-agent',
    prefix: `scenario-tr401-local-${options.agent}-window-resize`,
    metadata: {
      agent: options.agent,
      focus: 'split-session window resize render health',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const agentToken = options.agent === 'claude' ? `TR401CLAUDE${Date.now()}` : 'OpenAI Codex';
  const shellToken = `__TR401_SHELL_${Date.now()}__`;

  let sessionId = null;
  let initialPaneId = null;
  let utilityPaneId = null;
  let baselineWindow = null;
  let shrunkWindow = null;
  let restoredWindow = null;
  let baselineMainState = null;
  let baselineUtilityState = null;
  let shrunkMainState = null;
  let shrunkUtilityState = null;
  let restoredMainState = null;
  let restoredUtilityState = null;
  let baselineMainNativeMetrics = null;
  let baselineUtilityNativeMetrics = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    baselineWindow = await runner.step('normalize_window_bounds', async () => {
      const currentBounds = await getFrontWindowBounds(client.bundleId, { client });
      const targetBounds = normalizeBaselineWindowBounds(currentBounds);
      return setFrontWindowBounds(targetBounds, { client });
    });

    sessionId = await runner.step('create_local_session', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr401-local-${options.agent}-${runner.runId}`,
        agent: options.agent,
        waitForInitialPaneVisible: false,
      });
    });

    await runner.step('prepare_split_baseline', async () => {
      await client.request('select_session', { sessionId });
      if (options.agent === 'claude') {
        const readiness = await ensureClaudeInitialPanePromptReady(client, sessionId, 45_000);
        initialPaneId = readiness.paneId;
      } else {
        const readiness = await ensureCodexInitialPanePromptReady(client, sessionId, 45_000);
        initialPaneId = readiness.paneId;
      }
      initialPaneId ||= (await waitForFirstWorkspacePane(client, sessionId, 'initial pane before window resize baseline', 20_000)).paneId;
      await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);

      if (options.agent === 'claude') {
        const fixture = await promptClaudeForStructuredBlock(client, sessionId, agentToken, 6);
        initialPaneId = fixture.paneId;
        runner.writeJson('agent-fixture.json', fixture);
        await waitForPaneText(
          client,
          sessionId,
          initialPaneId,
          (text) => text.includes(agentToken),
          'initial pane text to include structured agent token before resize',
          45_000,
        );
      }

      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialPaneId,
        direction: 'vertical',
      });
      const utilityPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'new utility pane for resize scenario',
        30_000,
      );
      utilityPaneId = utilityPane.paneId;

      await client.request('focus_pane', { sessionId, paneId: utilityPaneId });
      await waitForPaneVisible(client, sessionId, utilityPaneId, 20_000);
      const attachWait = await waitForPaneAttached(client, sessionId, utilityPaneId, 20_000);
      runner.log('pane:runtime_attached', { paneId: utilityPaneId, elapsedMs: attachWait.elapsedMs });
      await client.request('write_pane', {
        sessionId,
        paneId: utilityPaneId,
        text: utilitySeedCommand(shellToken),
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => text.includes(`${shellToken} line 06`),
        'utility pane text to include generated shell resize token',
        20_000,
      );

      await client.request('select_session', { sessionId });
      baselineMainState = await assertPaneVisibleContent(
        client,
        sessionId,
        initialPaneId,
        initialPaneContentOptions(agentToken, 'initial pane visible content before window resize'),
      );
      baselineUtilityState = await assertPaneVisibleContent(
        client,
        sessionId,
        utilityPaneId,
        utilityContentOptions(shellToken, 'utility pane visible content before window resize'),
      );

      await Promise.all([
        assertPaneCoverage(client, sessionId, initialPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: 'initial pane coverage before window resize',
        }),
        assertPaneCoverage(client, sessionId, utilityPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: 'utility pane coverage before window resize',
        }),
        assertPaneUsesVisibleWidth(
          client,
          sessionId,
          initialPaneId,
          widthUsageOptions('initial pane width usage before window resize'),
        ),
        assertPaneUsesVisibleWidth(
          client,
          sessionId,
          utilityPaneId,
          widthUsageOptions('utility pane width usage before window resize'),
        ),
      ]);

      baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-baseline-initial-pane',
        sessionId,
        initialPaneId,
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'initial pane native paint coverage before window resize',
        },
      );
      baselineUtilityNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-baseline-utility',
        sessionId,
        utilityPaneId,
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'utility pane native paint coverage before window resize',
        },
      );

      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    shrunkWindow = await runner.step('shrink_window_and_assert', async () => {
      const targetBounds = shrunkWindowBounds(baselineWindow);
      const nextWindow = await setFrontWindowBounds(targetBounds, { client });

      const mainThreshold = paneShrinkThreshold(baselineMainState?.pane?.bounds || {});
      const utilityThreshold = paneShrinkThreshold(baselineUtilityState?.pane?.bounds || {});

      shrunkMainState = await waitForPaneState(
        client,
        sessionId,
        initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width > 0 && height > 0 && width <= mainThreshold.width && height <= mainThreshold.height;
        },
        'initial pane bounds to shrink after window resize',
        20_000,
      );
      shrunkUtilityState = await waitForPaneState(
        client,
        sessionId,
        utilityPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width > 0 && height > 0 && width <= utilityThreshold.width && height <= utilityThreshold.height;
        },
        'utility pane bounds to shrink after window resize',
        20_000,
      );

      await Promise.all([
        waitForPaneVisible(client, sessionId, initialPaneId, 20_000),
        waitForPaneVisible(client, sessionId, utilityPaneId, 20_000),
      ]);

      await Promise.all([
        assertPaneVisibleContent(
          client,
          sessionId,
          initialPaneId,
          initialPaneContentOptions(agentToken, 'initial pane visible content after shrinking window', 'shrunk'),
        ),
        assertPaneVisibleContent(
          client,
          sessionId,
          utilityPaneId,
          utilityContentOptions(shellToken, 'utility pane visible content after shrinking window', 'shrunk'),
        ),
        assertPaneCoverage(client, sessionId, initialPaneId, {
          minWidthRatio: 0.75,
          minHeightRatio: 0.7,
          timeoutMs: 20_000,
          description: 'initial pane coverage after shrinking window',
        }),
        assertPaneCoverage(client, sessionId, utilityPaneId, {
          minWidthRatio: 0.75,
          minHeightRatio: 0.7,
          timeoutMs: 20_000,
          description: 'utility pane coverage after shrinking window',
        }),
        assertPaneUsesVisibleWidth(
          client,
          sessionId,
          initialPaneId,
          widthUsageOptions('initial pane width usage after shrinking window', 'shrunk'),
        ),
        assertPaneUsesVisibleWidth(
          client,
          sessionId,
          utilityPaneId,
          widthUsageOptions('utility pane width usage after shrinking window', 'shrunk'),
        ),
      ]);

      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '02-shrunk-initial-pane',
        sessionId,
        initialPaneId,
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'initial pane native paint coverage after shrinking window',
        },
      );
      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '02-shrunk-utility',
        sessionId,
        utilityPaneId,
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'utility pane native paint coverage after shrinking window',
        },
      );

      // The app resizes fit-driven geometry WITHOUT reflow
      // (resizeGhosttyWithoutReflow, app/src/utils/ghosttyResize.ts), so
      // shrinking the window truncates scrollback line tails permanently —
      // restoring the window cannot bring them back. Re-capture the utility
      // pane's post-shrink content here (after it has settled) so
      // restore_window_and_assert can verify preservation against this
      // narrow-width capture instead of the unreachable pre-shrink baseline.
      shrunkUtilityState = await client.request('get_pane_state', { sessionId, paneId: utilityPaneId });

      await captureSessionArtifacts(client, runner.runDir, '02-shrunk', sessionId);
      return nextWindow;
    });

    restoredWindow = await runner.step('restore_window_and_assert', async () => {
      const nextWindow = await setFrontWindowBounds(baselineWindow, { client });

      const mainThreshold = paneRecoveryThreshold(baselineMainState?.pane?.bounds || {});
      const utilityThreshold = paneRecoveryThreshold(baselineUtilityState?.pane?.bounds || {});

      restoredMainState = await waitForPaneState(
        client,
        sessionId,
        initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width >= mainThreshold.width && height >= mainThreshold.height;
        },
        'initial pane bounds to recover after restoring window',
        20_000,
      );
      restoredUtilityState = await waitForPaneState(
        client,
        sessionId,
        utilityPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width >= utilityThreshold.width && height >= utilityThreshold.height;
        },
        'utility pane bounds to recover after restoring window',
        20_000,
      );

      await Promise.all([
        assertPaneVisibleContent(
          client,
          sessionId,
          initialPaneId,
          initialPaneContentOptions(agentToken, 'initial pane visible content after restoring window'),
        ),
        assertPaneVisibleContent(
          client,
          sessionId,
          utilityPaneId,
          utilityContentOptions(shellToken, 'utility pane visible content after restoring window'),
        ),
        assertPaneVisibleContentPreserved(
          client,
          sessionId,
          initialPaneId,
          baselineMainState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.6,
            minCharCountRatio: 0.5,
            minAnchorMatches: 3,
            // Anchor only on token lines (claude echo/reflow flake).
            ignoreAnchorPatterns: tokenAnchorIgnorePatterns(agentToken),
            timeoutMs: 20_000,
            description: 'initial pane content preserved after restoring window',
          },
        ),
        assertPaneVisibleContentPreserved(
          client,
          sessionId,
          utilityPaneId,
          shrunkUtilityState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.55,
            minCharCountRatio: 0.45,
            minAnchorMatches: 3,
            timeoutMs: 20_000,
            description: 'utility pane shrunk-width content preserved after restoring window',
          },
        ),
        assertPaneCoverage(client, sessionId, initialPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: 'initial pane coverage after restoring window',
        }),
        assertPaneCoverage(client, sessionId, utilityPaneId, {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: 'utility pane coverage after restoring window',
        }),
        assertPaneUsesVisibleWidth(
          client,
          sessionId,
          initialPaneId,
          widthUsageOptions('initial pane width usage after restoring window'),
        ),
        assertPaneUsesVisibleWidth(
          client,
          sessionId,
          utilityPaneId,
          widthUsageOptions('utility pane width usage after restoring window'),
        ),
      ]);

      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '03-restored-initial-pane',
        sessionId,
        initialPaneId,
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'initial pane native paint coverage after restoring window',
        },
      );
      await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '03-restored-utility',
        sessionId,
        utilityPaneId,
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'utility pane native paint coverage after restoring window',
        },
      );

      // Regression thresholds are deliberately loose because the underlying
      // metric now counts occupied cells, not anti-aliased pixels. Under a
      // full window-resize cycle, agent UIs (e.g. Claude's header box) do
      // not always re-expand their rendering to the restored width, so a
      // "same-size" pane often has ~30% fewer busy columns than baseline
      // despite content being intact. Visible-content assertions above
      // already cover the "did the text come back" invariant; what this
      // check still guards is a pane going blank or losing most of its
      // vertical coverage, which even these loose thresholds catch.
      if (baselineMainNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          '03-restored-initial-pane-stability',
          sessionId,
          initialPaneId,
          baselineMainNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: 0.4,
            maxBusyRowRatioRegression: 0.2,
            maxBBoxWidthRatioRegression: 0.4,
            maxBBoxHeightRatioRegression: 0.3,
            maxActivePixelRatioRegression: null,
            description: 'initial pane native paint recovery after restoring window',
          },
        );
      }
      if (baselineUtilityNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          '03-restored-utility-stability',
          sessionId,
          utilityPaneId,
          baselineUtilityNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: 0.4,
            maxBusyRowRatioRegression: 0.2,
            maxBBoxWidthRatioRegression: 0.4,
            maxBBoxHeightRatioRegression: 0.3,
            maxActivePixelRatioRegression: null,
            description: 'utility pane native paint recovery after restoring window',
          },
        );
      }

      await captureSessionArtifacts(client, runner.runDir, '03-restored', sessionId);
      return nextWindow;
    });

    const summary = runner.finishSuccess({
      sessionId,
      utilityPaneId,
      tokens: { agentToken, shellToken },
      windowBounds: {
        baselineWindow,
        shrunkWindow,
        restoredWindow,
      },
      paneBounds: {
        baselineMain: baselineMainState?.pane?.bounds ?? null,
        baselineUtility: baselineUtilityState?.pane?.bounds ?? null,
        shrunkMain: shrunkMainState?.pane?.bounds ?? null,
        shrunkUtility: shrunkUtilityState?.pane?.bounds ?? null,
        restoredMain: restoredMainState?.pane?.bounds ?? null,
        restoredUtility: restoredUtilityState?.pane?.bounds ?? null,
      },
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId).catch(() => {});
    }
    const summary = runner.finishFailure(error, {
      sessionId,
      utilityPaneId,
      tokens: { agentToken, shellToken },
      windowBounds: {
        baselineWindow,
        shrunkWindow,
        restoredWindow,
      },
      paneBounds: {
        baselineMain: baselineMainState?.pane?.bounds ?? null,
        baselineUtility: baselineUtilityState?.pane?.bounds ?? null,
        shrunkMain: shrunkMainState?.pane?.bounds ?? null,
        shrunkUtility: shrunkUtilityState?.pane?.bounds ?? null,
        restoredMain: restoredMainState?.pane?.bounds ?? null,
        restoredUtility: restoredUtilityState?.pane?.bounds ?? null,
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
