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
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  waitForFirstWorkspacePane,
  waitForNewShellPane,
  waitForPaneState,
  waitForPaneVisible,
  waitForSessionWorkspace,
  tokenAnchorIgnorePatterns,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeInitialPanePromptReady,
  ensureCodexInitialPanePromptReady,
  preTrustClaudeFolder,
  promptClaudeForStructuredBlock,
} from './scenarioAgents.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
    agent: process.env.ATTN_LOCAL_CLOSE_REDRAW_AGENT || 'codex',
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
  options.agent = String(options.agent || 'codex').toLowerCase();
  if (options.agent !== 'codex' && options.agent !== 'claude') {
    throw new Error(`Unsupported agent for local close-redraw scenario: ${options.agent}`);
  }

  return {
    options,
    help: Boolean(options.help),
  };
}

function recoveredWidthThreshold(baselineWidth) {
  if (!Number.isFinite(baselineWidth) || baselineWidth <= 0) {
    return 0;
  }
  return Math.max(240, Math.floor(baselineWidth * 0.9));
}

function shrunkWidthThreshold(baselineWidth) {
  if (!Number.isFinite(baselineWidth) || baselineWidth <= 0) {
    return Number.POSITIVE_INFINITY;
  }
  return Math.ceil(baselineWidth * 0.75);
}

function agentVisibleContentOptions(agent, token = null, description = 'local initial pane content') {
  if (agent === 'claude') {
    return {
      contains: token,
      minNonEmptyLines: 4,
      minDenseLines: 1,
      minCharCount: 120,
      minMaxLineLength: 18,
      timeoutMs: 45_000,
      description,
    };
  }
  return {
    contains: 'OpenAI Codex',
    allowWrappedContains: true,
    minNonEmptyLines: 2,
    minDenseLines: 0,
    minCharCount: 20,
    minMaxLineLength: 12,
    timeoutMs: 45_000,
    description,
  };
}

function recoveryThresholdsForAgent(agent) {
  if (agent === 'claude') {
    return {
      minNonEmptyLineRatio: 0.75,
      minCharCountRatio: 0.6,
      minAnchorMatches: 3,
      maxBusyColumnRatioRegression: 0.1,
      maxBusyRowRatioRegression: 0.08,
      maxBBoxWidthRatioRegression: 0.1,
      maxBBoxHeightRatioRegression: 0.08,
    };
  }
  return {
    minNonEmptyLineRatio: 0.7,
    minCharCountRatio: 0.55,
    minAnchorMatches: 2,
    maxBusyColumnRatioRegression: null,
    maxBusyRowRatioRegression: null,
    maxBBoxWidthRatioRegression: null,
    maxBBoxHeightRatioRegression: null,
  };
}

function nativeCoverageThresholdsForAgent(agent) {
  if (agent === 'claude') {
    return {
      minBusyColumnRatio: 0.35,
      minBusyRowRatio: 0.12,
      minBBoxWidthRatio: 0.35,
      minBBoxHeightRatio: 0.12,
    };
  }
  return {
    minBusyColumnRatio: 0.35,
    minBusyRowRatio: 0.08,
    minBBoxWidthRatio: 0.35,
    minBBoxHeightRatio: 0.12,
  };
}

async function prepareAgentBaseline(client, runner, sessionId, agent, token) {
  if (agent === 'claude') {
    await ensureClaudeInitialPanePromptReady(client, sessionId, 45_000);
    const fixture = await promptClaudeForStructuredBlock(client, sessionId, token, 4);
    runner.writeJson('agent-fixture.json', fixture);
    return token;
  }
  await ensureCodexInitialPanePromptReady(client, sessionId, 45_000);
  return 'OpenAI Codex';
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr402-local-close-redraw.mjs');
    console.log(`Additional options:
  --agent <codex|claude>        Local agent to exercise (default: codex)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-402',
    tier: 'tier2-local-real-agent',
    prefix: `scenario-tr402-local-${options.agent}-close-redraw`,
    metadata: {
      agent: options.agent,
      focus: 'first-launch split close recovery',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const transcriptAnchorToken = `${options.agent === 'claude' ? 'TR402CLAUDE' : 'TR402CODEX'}${Date.now()}`;
  let sessionId = null;
  let initialPaneId = null;
  let splitPaneId = null;
  let baselineMainState = null;
  let baselineMainNativeMetrics = null;
  let baselineAnchorToken = null;
  let splitMainState = null;
  let recoveredMainState = null;
  let splitOpenContentPreservation = null;
  const nativeCoverageThresholds = nativeCoverageThresholdsForAgent(options.agent);

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('normalize_window_origin_for_capture', async () => {
      const bounds = await getFrontWindowBounds(client.bundleId, { client });
      await setFrontWindowBounds({ ...bounds, x: 80, y: 80 }, { client });
    });

    if (options.agent === 'claude') {
      const trustedFolder = preTrustClaudeFolder(runner.sessionDir);
      runner.log('claude:pre_trust_folder', { folder: trustedFolder });
    }

    sessionId = await runner.step('create_local_session', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr402-local-${options.agent}-${runner.runId}`,
        agent: options.agent,
        waitForInitialPaneVisible: false,
      });
    });

    await runner.step('capture_baseline_main', async () => {
      await client.request('select_session', { sessionId });
      const requiredVisibleText = await prepareAgentBaseline(client, runner, sessionId, options.agent, transcriptAnchorToken);
      baselineAnchorToken = requiredVisibleText;
      initialPaneId = (await waitForFirstWorkspacePane(client, sessionId, `initial ${options.agent} pane`, 20_000)).paneId;
      await waitForPaneVisible(client, sessionId, initialPaneId, 45_000);
      baselineMainState = await assertPaneVisibleContent(
        client,
        sessionId,
        initialPaneId,
        agentVisibleContentOptions(options.agent, requiredVisibleText, `${options.agent} initial pane visible content before split close scenario`),
      );
      await assertPaneCoverage(client, sessionId, initialPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: `${options.agent} initial pane coverage before split close scenario`,
      });
      baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-baseline-initial-pane',
        sessionId,
        initialPaneId,
        {
          target: 'paneBody',
          minBusyColumnRatio: nativeCoverageThresholds.minBusyColumnRatio,
          minBusyRowRatio: nativeCoverageThresholds.minBusyRowRatio,
          minBBoxWidthRatio: nativeCoverageThresholds.minBBoxWidthRatio,
          minBBoxHeightRatio: nativeCoverageThresholds.minBBoxHeightRatio,
          description: `${options.agent} initial pane native paint coverage before split close scenario`,
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    splitPaneId = await runner.step('split_from_main', async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialPaneId,
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        `new utility pane after local ${options.agent} split`,
        30_000,
      );
      splitMainState = await waitForPaneState(
        client,
        sessionId,
        initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = baselineMainState?.pane?.bounds?.width ?? 0;
          return width > 0 && width <= shrunkWidthThreshold(baselineWidth);
        },
        `${options.agent} initial pane width to shrink after split`,
        20_000,
      );
      if (options.agent === 'claude') {
        splitOpenContentPreservation = {
          ok: null,
          skipped: true,
          reason: 'Claude welcome/header reflows materially while split is open; post-close recovery is the decisive check.',
        };
      } else {
        splitOpenContentPreservation = {
          ok: null,
          skipped: true,
          reason: 'Codex welcome banner is full-width box drawing that cannot reflow into a narrow split; post-close recovery is the decisive check.',
        };
      }
      await captureSessionArtifacts(client, runner.runDir, '02-after-split', sessionId);
      return newPane.paneId;
    });

    await runner.step('close_split_and_assert_main_recovers', async () => {
      const thresholds = recoveryThresholdsForAgent(options.agent);
      await client.request('focus_pane', { sessionId, paneId: splitPaneId });
      await waitForPaneVisible(client, sessionId, splitPaneId, 20_000);
      await client.request('close_pane', { sessionId, paneId: splitPaneId });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => {
          const panes = workspace.panes || [];
          return panes.length === 1 && panes[0].paneId === initialPaneId;
        },
        'workspace to collapse back to one pane after closing split',
        20_000,
      );
      recoveredMainState = await waitForPaneState(
        client,
        sessionId,
        initialPaneId,
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          return width >= recoveredWidthThreshold(baselineMainState?.pane?.bounds?.width ?? 0);
        },
        `${options.agent} initial pane width to recover after closing split`,
        20_000,
      );
      // No scroll: captures assert on the live bottom viewport — a real agent
      // transcript outgrows one screen, so the welcome header legitimately
      // scrolls away, and the content-preserved check below is the recovery
      // signal.
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        initialPaneId,
        baselineMainState?.pane?.visibleContent || null,
        {
          minNonEmptyLineRatio: thresholds.minNonEmptyLineRatio,
          minCharCountRatio: thresholds.minCharCountRatio,
          minAnchorMatches: thresholds.minAnchorMatches,
          // Anchor only on token lines (claude echo/reflow flake).
          ignoreAnchorPatterns: tokenAnchorIgnorePatterns(baselineAnchorToken),
          timeoutMs: 20_000,
          description: `${options.agent} initial pane content recovered after closing split`,
        },
      );
      await assertPaneCoverage(client, sessionId, initialPaneId, {
        minWidthRatio: 0.85,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: `${options.agent} initial pane coverage after closing split`,
      });
      await assertPaneNativePaintCoverage(client, runner.runDir, '03-after-close-initial-pane', sessionId, initialPaneId, {
        target: 'paneBody',
        minBusyColumnRatio: nativeCoverageThresholds.minBusyColumnRatio,
        minBusyRowRatio: nativeCoverageThresholds.minBusyRowRatio,
        minBBoxWidthRatio: nativeCoverageThresholds.minBBoxWidthRatio,
        minBBoxHeightRatio: nativeCoverageThresholds.minBBoxHeightRatio,
        description: `${options.agent} initial pane native paint coverage after closing split`,
      });
      if (baselineMainNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          '03-after-close-initial-pane-stability',
          sessionId,
          initialPaneId,
          baselineMainNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: thresholds.maxBusyColumnRatioRegression,
            maxBusyRowRatioRegression: thresholds.maxBusyRowRatioRegression,
            maxBBoxWidthRatioRegression: thresholds.maxBBoxWidthRatioRegression,
            maxBBoxHeightRatioRegression: thresholds.maxBBoxHeightRatioRegression,
            maxActivePixelRatioRegression: null,
            description: `${options.agent} initial pane native paint recovery after closing split`,
          },
        );
      }
      await captureSessionArtifacts(client, runner.runDir, '03-after-close', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      splitPaneId,
      agent: options.agent,
      transcriptAnchorToken: transcriptAnchorToken,
      splitOpenContentPreservation,
      widths: {
        baselineMainWidth: baselineMainState?.pane?.bounds?.width ?? null,
        splitMainWidth: splitMainState?.pane?.bounds?.width ?? null,
        recoveredMainWidth: recoveredMainState?.pane?.bounds?.width ?? null,
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
      splitPaneId,
      agent: options.agent,
      transcriptAnchorToken,
      splitOpenContentPreservation,
      widths: {
        baselineMainWidth: baselineMainState?.pane?.bounds?.width ?? null,
        splitMainWidth: splitMainState?.pane?.bounds?.width ?? null,
        recoveredMainWidth: recoveredMainState?.pane?.bounds?.width ?? null,
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
