#!/usr/bin/env node

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  assertPaneCoverage,
  assertPaneNativePaintCoverage,
  assertPaneNativePaintStable,
  assertPaneVisibleContent,
  assertPaneUsesVisibleWidth,
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { ensureClaudeMainPromptReady, promptClaudeForStructuredBlock } from './scenarioAgents.mjs';

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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr101-claude-main-split.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-101',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr101-claude-main-split',
    metadata: {
      agent: 'claude',
      focus: 'split from main preserves source pane content',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let utilityPaneId = null;
  let baselineMainNativeMetrics = null;
  const agentToken = `__TR101_AGENT_${Date.now()}__`;
  const shellToken = `__TR101_SHELL_${Date.now()}__`;

  try {
    await runner.step('launch_app', async () => {
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      await observer.connect();
    });

    sessionId = await runner.step('create_claude_session', async () => {
      const result = await client.request('create_session', {
        cwd: runner.sessionDir,
        label: `tr101-${runner.runId}`,
        agent: 'claude',
      });
      await observer.waitForSession({ id: result.sessionId, timeoutMs: 30_000 });
      return result.sessionId;
    });

    await runner.step('prepare_main_prompt', async () => {
      await ensureClaudeMainPromptReady(client, sessionId, 45_000);
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
    });

    await runner.step('prompt_structured_agent_block', async () => {
      const fixture = await promptClaudeForStructuredBlock(client, sessionId, agentToken, 8);
      runner.writeJson('agent-fixture.json', fixture);
      await waitForPaneText(
        client,
        sessionId,
        'main',
        (text) => text.includes(agentToken),
        'main pane text to include structured agent token',
        45_000,
      );
      await assertPaneVisibleContent(client, sessionId, 'main', {
        contains: agentToken,
        minNonEmptyLines: 4,
        minDenseLines: 3,
        minCharCount: 140,
        minMaxLineLength: 40,
        timeoutMs: 45_000,
        description: 'main pane visible agent content before split',
      });
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.85,
        minHeightRatio: 0.75,
        timeoutMs: 20_000,
        description: 'main pane coverage before split',
      });
      await assertPaneUsesVisibleWidth(client, sessionId, 'main', {
        minMaxOccupiedWidthRatio: 0.7,
        minWideLineCount: 4,
        minMedianOccupiedWidthRatio: 0.7,
        timeoutMs: 20_000,
        description: 'main pane visible width usage before split',
      });
      baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        'before-split-main',
        sessionId,
        'main',
        {
        target: 'paneBody',
        minBusyColumnRatio: 0.5,
        minBusyRowRatio: 0.2,
        minBBoxWidthRatio: 0.5,
        minBBoxHeightRatio: 0.2,
        description: 'main pane native paint coverage before split',
        },
      );
    });

    utilityPaneId = await runner.step('split_from_main', async () => {
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
        'new utility pane after split from main',
        20_000,
      );
      return newPane.paneId;
    });

    await runner.step('assert_source_and_target_after_split', async () => {
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
      await assertPaneVisibleContent(client, sessionId, 'main', {
        contains: agentToken,
        minNonEmptyLines: 4,
        minDenseLines: 3,
        minCharCount: 140,
        minMaxLineLength: 40,
        timeoutMs: 30_000,
        description: 'main pane visible agent content after split',
      });
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.8,
        minHeightRatio: 0.75,
        timeoutMs: 20_000,
        description: 'main pane coverage after split',
      });
      await assertPaneUsesVisibleWidth(client, sessionId, 'main', {
        minMaxOccupiedWidthRatio: 0.6,
        minWideLineCount: 3,
        minMedianOccupiedWidthRatio: 0.55,
        timeoutMs: 20_000,
        description: 'main pane visible width usage after split',
      });
      await assertPaneNativePaintCoverage(client, runner.runDir, 'after-split-main', sessionId, 'main', {
        target: 'paneBody',
        minBusyColumnRatio: 0.42,
        minBusyRowRatio: 0.16,
        minBBoxWidthRatio: 0.42,
        minBBoxHeightRatio: 0.16,
        description: 'main pane native paint coverage after split',
      });
      if (!baselineMainNativeMetrics) {
        throw new Error('Missing baseline main-pane native metrics before split');
      }
      await assertPaneNativePaintStable(
        client,
        runner.runDir,
        'after-split-main-stability',
        sessionId,
        'main',
        baselineMainNativeMetrics,
        {
          target: 'paneBody',
          maxBusyColumnRatioDelta: 0.1,
          maxBusyRowRatioDelta: 0.12,
          maxBBoxWidthRatioDelta: 0.08,
          maxBBoxHeightRatioDelta: 0.08,
          maxActivePixelRatioDelta: 0.04,
          description: 'main pane native paint stability after split',
        },
      );

      await client.request('focus_pane', { sessionId, paneId: utilityPaneId });
      await waitForPaneVisible(client, sessionId, utilityPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, utilityPaneId, 20_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: utilityPaneId, text: shellToken });
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => text.includes(shellToken),
        'new utility pane text to include shell token',
        15_000,
      );
      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: shellToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: shellToken.length,
        minMaxLineLength: shellToken.length,
        timeoutMs: 15_000,
        description: 'utility pane visible content after split',
      });
      await assertPaneCoverage(client, sessionId, utilityPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.75,
        timeoutMs: 20_000,
        description: 'utility pane coverage after split',
      });
    });

    await runner.step('capture_artifacts', async () => {
      await captureSessionArtifacts(client, runner.runDir, 'final', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      utilityPaneId,
      tokens: { agentToken, shellToken },
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId);
    }
    const summary = runner.finishFailure(error, {
      sessionId,
      utilityPaneId,
      tokens: { agentToken, shellToken },
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
