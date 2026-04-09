#!/usr/bin/env node

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  assertPaneCoverage,
  assertPaneNativePaintCoverage,
  assertPaneNativePaintRecovered,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  shellPanes,
  waitForNewShellPane,
  waitForPaneState,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeMainPromptReady,
  ensureCodexMainPromptReady,
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
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

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

function agentVisibleContentOptions(agent, token = null, description = 'local main pane content') {
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
    await ensureClaudeMainPromptReady(client, sessionId, 45_000);
    const fixture = await promptClaudeForStructuredBlock(client, sessionId, token, 4);
    runner.writeJson('agent-fixture.json', fixture);
    return token;
  }
  await ensureCodexMainPromptReady(client, sessionId, 45_000);
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
  let splitPaneId = null;
  let baselineMainState = null;
  let baselineMainNativeMetrics = null;
  let splitMainState = null;
  let recoveredMainState = null;
  let splitOpenContentPreservation = null;
  const nativeCoverageThresholds = nativeCoverageThresholdsForAgent(options.agent);

  try {
    await runner.step('launch_app', async () => {
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      await observer.connect();
    });

    sessionId = await runner.step('create_local_session', async () => {
      const result = await client.request('create_session', {
        cwd: runner.sessionDir,
        label: `tr402-local-${options.agent}-${runner.runId}`,
        agent: options.agent,
      });
      await observer.waitForSession({ id: result.sessionId, timeoutMs: 30_000 });
      await observer.waitForWorkspace(
        result.sessionId,
        (workspace) => (workspace.panes || []).length >= 1,
        `initial workspace for ${result.sessionId}`,
        30_000,
      );
      return result.sessionId;
    });

    await runner.step('capture_baseline_main', async () => {
      await client.request('select_session', { sessionId });
      const requiredVisibleText = await prepareAgentBaseline(client, runner, sessionId, options.agent, transcriptAnchorToken);
      await waitForPaneVisible(client, sessionId, 'main', 45_000);
      baselineMainState = await assertPaneVisibleContent(
        client,
        sessionId,
        'main',
        agentVisibleContentOptions(options.agent, requiredVisibleText, `${options.agent} main pane visible content before split close scenario`),
      );
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: `${options.agent} main pane coverage before split close scenario`,
      });
      baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-baseline-main',
        sessionId,
        'main',
        {
          target: 'paneBody',
          minBusyColumnRatio: nativeCoverageThresholds.minBusyColumnRatio,
          minBusyRowRatio: nativeCoverageThresholds.minBusyRowRatio,
          minBBoxWidthRatio: nativeCoverageThresholds.minBBoxWidthRatio,
          minBBoxHeightRatio: nativeCoverageThresholds.minBBoxHeightRatio,
          description: `${options.agent} main pane native paint coverage before split close scenario`,
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    splitPaneId = await runner.step('split_from_main', async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: 'main',
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
        'main',
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = baselineMainState?.pane?.bounds?.width ?? 0;
          return width > 0 && width <= shrunkWidthThreshold(baselineWidth);
        },
        `${options.agent} main pane width to shrink after split`,
        20_000,
      );
      const thresholds = recoveryThresholdsForAgent(options.agent);
      try {
        await assertPaneVisibleContentPreserved(
          client,
          sessionId,
          'main',
          baselineMainState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: Math.max(0.5, thresholds.minNonEmptyLineRatio - 0.15),
            minCharCountRatio: Math.max(0.35, thresholds.minCharCountRatio - 0.15),
            minAnchorMatches: Math.max(2, thresholds.minAnchorMatches - 1),
            timeoutMs: 20_000,
            description: `${options.agent} main pane content preserved while split is open`,
          },
        );
        splitOpenContentPreservation = { ok: true };
      } catch (error) {
        splitOpenContentPreservation = {
          ok: false,
          error: error instanceof Error ? error.stack || error.message : String(error),
        };
        if (options.agent === 'codex') {
          throw error;
        }
        runner.log('observation:split_open_content_degraded', splitOpenContentPreservation);
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
        (workspace) => (workspace.panes || []).length === 1 && shellPanes(workspace).length === 0,
        'workspace to collapse back to one pane after closing split',
        20_000,
      );
      recoveredMainState = await waitForPaneState(
        client,
        sessionId,
        'main',
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          return width >= recoveredWidthThreshold(baselineMainState?.pane?.bounds?.width ?? 0);
        },
        `${options.agent} main pane width to recover after closing split`,
        20_000,
      );
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        'main',
        baselineMainState?.pane?.visibleContent || null,
        {
          minNonEmptyLineRatio: thresholds.minNonEmptyLineRatio,
          minCharCountRatio: thresholds.minCharCountRatio,
          minAnchorMatches: thresholds.minAnchorMatches,
          timeoutMs: 20_000,
          description: `${options.agent} main pane content recovered after closing split`,
        },
      );
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.85,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: `${options.agent} main pane coverage after closing split`,
      });
      await assertPaneNativePaintCoverage(client, runner.runDir, '03-after-close-main', sessionId, 'main', {
        target: 'paneBody',
        minBusyColumnRatio: nativeCoverageThresholds.minBusyColumnRatio,
        minBusyRowRatio: nativeCoverageThresholds.minBusyRowRatio,
        minBBoxWidthRatio: nativeCoverageThresholds.minBBoxWidthRatio,
        minBBoxHeightRatio: nativeCoverageThresholds.minBBoxHeightRatio,
        description: `${options.agent} main pane native paint coverage after closing split`,
      });
      if (baselineMainNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          '03-after-close-main-stability',
          sessionId,
          'main',
          baselineMainNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: thresholds.maxBusyColumnRatioRegression,
            maxBusyRowRatioRegression: thresholds.maxBusyRowRatioRegression,
            maxBBoxWidthRatioRegression: thresholds.maxBBoxWidthRatioRegression,
            maxBBoxHeightRatioRegression: thresholds.maxBBoxHeightRatioRegression,
            maxActivePixelRatioRegression: null,
            description: `${options.agent} main pane native paint recovery after closing split`,
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
      try {
        observer.send({ cmd: 'kill_session', id: sessionId });
        await observer.waitFor(() => {
          const session = observer.getSession(sessionId);
          return !session || session.state !== 'working' ? true : null;
        }, `cleanup kill_session ${sessionId}`, 20_000).catch(() => {});
        observer.unregisterSession(sessionId);
        await observer.waitFor(() => !observer.getSession(sessionId), `cleanup unregister ${sessionId}`, 20_000).catch(() => {});
      } catch {
        // Best-effort cleanup only.
      }
    }
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
