#!/usr/bin/env node

import {
  createSessionAndWaitForMain,
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
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneState,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeMainPromptReady,
  promptClaudeForStructuredBlock,
} from './scenarioAgents.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = parseCommonArgs(args);
  return {
    options,
    help: args.includes('--help') || args.includes('-h'),
  };
}

function normalizeBaselineWindowBounds(bounds) {
  return {
    x: bounds.x,
    y: bounds.y,
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

function mainContentOptions(token, description, phase = 'baseline') {
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
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-401',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr401-local-window-resize',
    metadata: {
      agent: 'claude',
      focus: 'split-session window resize render health',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const agentToken = `TR401CLAUDE${Date.now()}`;
  const shellToken = `__TR401_SHELL_${Date.now()}__`;

  let sessionId = null;
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
      const currentBounds = await getFrontWindowBounds('com.attn.manager', { client });
      const targetBounds = normalizeBaselineWindowBounds(currentBounds);
      return setFrontWindowBounds(targetBounds, { client, bundleId: 'com.attn.manager' });
    });

    sessionId = await runner.step('create_claude_session', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr401-local-claude-${runner.runId}`,
        agent: 'claude',
        waitForMainVisible: false,
      });
    });

    await runner.step('prepare_split_baseline', async () => {
      await client.request('select_session', { sessionId });
      await ensureClaudeMainPromptReady(client, sessionId, 45_000);
      await waitForPaneVisible(client, sessionId, 'main', 20_000);

      const fixture = await promptClaudeForStructuredBlock(client, sessionId, agentToken, 6);
      runner.writeJson('agent-fixture.json', fixture);

      await waitForPaneText(
        client,
        sessionId,
        'main',
        (text) => text.includes(agentToken),
        'main pane text to include structured agent token before resize',
        45_000,
      );

      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: 'main',
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
      await waitForPaneInputFocus(client, sessionId, utilityPaneId, 20_000);
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
        'main',
        mainContentOptions(agentToken, 'main pane visible content before window resize'),
      );
      baselineUtilityState = await assertPaneVisibleContent(
        client,
        sessionId,
        utilityPaneId,
        utilityContentOptions(shellToken, 'utility pane visible content before window resize'),
      );

      await Promise.all([
        assertPaneCoverage(client, sessionId, 'main', {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: 'main pane coverage before window resize',
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
          'main',
          widthUsageOptions('main pane width usage before window resize'),
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
        '01-baseline-main',
        sessionId,
        'main',
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'main pane native paint coverage before window resize',
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
      const nextWindow = await setFrontWindowBounds(targetBounds, { client, bundleId: 'com.attn.manager' });

      const mainThreshold = paneShrinkThreshold(baselineMainState?.pane?.bounds || {});
      const utilityThreshold = paneShrinkThreshold(baselineUtilityState?.pane?.bounds || {});

      shrunkMainState = await waitForPaneState(
        client,
        sessionId,
        'main',
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width > 0 && height > 0 && width <= mainThreshold.width && height <= mainThreshold.height;
        },
        'main pane bounds to shrink after window resize',
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
        waitForPaneVisible(client, sessionId, 'main', 20_000),
        waitForPaneVisible(client, sessionId, utilityPaneId, 20_000),
      ]);

      await Promise.all([
        assertPaneVisibleContent(
          client,
          sessionId,
          'main',
          mainContentOptions(agentToken, 'main pane visible content after shrinking window', 'shrunk'),
        ),
        assertPaneVisibleContent(
          client,
          sessionId,
          utilityPaneId,
          utilityContentOptions(shellToken, 'utility pane visible content after shrinking window', 'shrunk'),
        ),
        assertPaneCoverage(client, sessionId, 'main', {
          minWidthRatio: 0.75,
          minHeightRatio: 0.7,
          timeoutMs: 20_000,
          description: 'main pane coverage after shrinking window',
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
          'main',
          widthUsageOptions('main pane width usage after shrinking window', 'shrunk'),
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
        '02-shrunk-main',
        sessionId,
        'main',
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'main pane native paint coverage after shrinking window',
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

      await captureSessionArtifacts(client, runner.runDir, '02-shrunk', sessionId);
      return nextWindow;
    });

    restoredWindow = await runner.step('restore_window_and_assert', async () => {
      const nextWindow = await setFrontWindowBounds(baselineWindow, { client, bundleId: 'com.attn.manager' });

      const mainThreshold = paneRecoveryThreshold(baselineMainState?.pane?.bounds || {});
      const utilityThreshold = paneRecoveryThreshold(baselineUtilityState?.pane?.bounds || {});

      restoredMainState = await waitForPaneState(
        client,
        sessionId,
        'main',
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const height = state?.pane?.bounds?.height ?? 0;
          return width >= mainThreshold.width && height >= mainThreshold.height;
        },
        'main pane bounds to recover after restoring window',
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
          'main',
          mainContentOptions(agentToken, 'main pane visible content after restoring window'),
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
          'main',
          baselineMainState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.6,
            minCharCountRatio: 0.5,
            minAnchorMatches: 3,
            ignoreAnchorPatterns: [
              /^\s*$/u,
              /^\s*[│╭╰].*$/u,
            ],
            timeoutMs: 20_000,
            description: 'main pane content preserved after restoring window',
          },
        ),
        assertPaneVisibleContentPreserved(
          client,
          sessionId,
          utilityPaneId,
          baselineUtilityState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.55,
            minCharCountRatio: 0.45,
            minAnchorMatches: 3,
            timeoutMs: 20_000,
            description: 'utility pane content preserved after restoring window',
          },
        ),
        assertPaneCoverage(client, sessionId, 'main', {
          minWidthRatio: 0.78,
          minHeightRatio: 0.72,
          timeoutMs: 20_000,
          description: 'main pane coverage after restoring window',
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
          'main',
          widthUsageOptions('main pane width usage after restoring window'),
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
        '03-restored-main',
        sessionId,
        'main',
        {
          target: 'paneBody',
          ...nativeCoverageThresholds,
          description: 'main pane native paint coverage after restoring window',
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

      if (baselineMainNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          '03-restored-main-stability',
          sessionId,
          'main',
          baselineMainNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: 0.12,
            maxBusyRowRatioRegression: 0.1,
            maxBBoxWidthRatioRegression: 0.12,
            maxBBoxHeightRatioRegression: 0.1,
            maxActivePixelRatioRegression: null,
            description: 'main pane native paint recovery after restoring window',
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
            maxBusyColumnRatioRegression: 0.12,
            maxBusyRowRatioRegression: 0.1,
            maxBBoxWidthRatioRegression: 0.12,
            maxBBoxHeightRatioRegression: 0.1,
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
