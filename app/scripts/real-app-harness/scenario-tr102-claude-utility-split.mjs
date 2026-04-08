#!/usr/bin/env node

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  assertPaneCoverage,
  assertPaneVisibleContent,
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { ensureClaudeMainPromptReady } from './scenarioAgents.mjs';

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
    printCommonHelp('scripts/real-app-harness/scenario-tr102-claude-utility-split.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-102',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr102-claude-utility-split',
    metadata: {
      agent: 'claude',
      focus: 'split from utility preserves source and target utility panes',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let firstShellPaneId = null;
  let secondShellPaneId = null;
  const firstToken = `__TR102_ONE_${Date.now()}__`;
  const secondToken = `__TR102_TWO_${Date.now()}__`;
  const revisitToken = `__TR102_REVISIT_${Date.now()}__`;

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
        label: `tr102-${runner.runId}`,
        agent: 'claude',
      });
      await observer.waitForSession({ id: result.sessionId, timeoutMs: 30_000 });
      return result.sessionId;
    });

    await runner.step('prepare_main_prompt', async () => {
      await ensureClaudeMainPromptReady(client, sessionId, 45_000);
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
    });

    firstShellPaneId = await runner.step('split_from_main_for_first_utility', async () => {
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
        'first utility pane',
        20_000,
      );
      return newPane.paneId;
    });

    await runner.step('seed_first_utility_content', async () => {
      await client.request('focus_pane', { sessionId, paneId: firstShellPaneId });
      await waitForPaneVisible(client, sessionId, firstShellPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, firstShellPaneId, 20_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: firstShellPaneId, text: firstToken });
      await waitForPaneText(
        client,
        sessionId,
        firstShellPaneId,
        (text) => text.includes(firstToken),
        'first utility pane token',
        15_000,
      );
      await assertPaneVisibleContent(client, sessionId, firstShellPaneId, {
        contains: firstToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: firstToken.length,
        minMaxLineLength: firstToken.length,
        timeoutMs: 15_000,
        description: 'first utility pane visible content before second split',
      });
    });

    secondShellPaneId = await runner.step('split_from_first_utility', async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: firstShellPaneId,
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'second utility pane after split from first utility',
        20_000,
      );
      return newPane.paneId;
    });

    await runner.step('assert_both_utility_panes_after_split', async () => {
      await assertPaneVisibleContent(client, sessionId, firstShellPaneId, {
        contains: firstToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: firstToken.length,
        minMaxLineLength: firstToken.length,
        timeoutMs: 15_000,
        description: 'first utility pane content preserved after split',
      });
      await assertPaneCoverage(client, sessionId, firstShellPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.75,
        timeoutMs: 20_000,
        description: 'first utility pane coverage after split',
      });

      await client.request('focus_pane', { sessionId, paneId: secondShellPaneId });
      await waitForPaneVisible(client, sessionId, secondShellPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, secondShellPaneId, 20_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: secondShellPaneId, text: secondToken });
      await waitForPaneText(
        client,
        sessionId,
        secondShellPaneId,
        (text) => text.includes(secondToken),
        'second utility pane token',
        15_000,
      );
      await assertPaneVisibleContent(client, sessionId, secondShellPaneId, {
        contains: secondToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: secondToken.length,
        minMaxLineLength: secondToken.length,
        timeoutMs: 15_000,
        description: 'second utility pane visible content after split',
      });
      await assertPaneCoverage(client, sessionId, secondShellPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.75,
        timeoutMs: 20_000,
        description: 'second utility pane coverage after split',
      });
    });

    await runner.step('revisit_first_utility', async () => {
      await client.request('focus_pane', { sessionId, paneId: firstShellPaneId });
      await waitForPaneVisible(client, sessionId, firstShellPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, firstShellPaneId, 20_000);
      await client.request('type_pane_via_ui', { sessionId, paneId: firstShellPaneId, text: revisitToken });
      await waitForPaneText(
        client,
        sessionId,
        firstShellPaneId,
        (text) => text.includes(revisitToken),
        'revisited first utility pane token',
        15_000,
      );
      await assertPaneVisibleContent(client, sessionId, firstShellPaneId, {
        contains: revisitToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: revisitToken.length,
        minMaxLineLength: revisitToken.length,
        timeoutMs: 15_000,
        description: 'first utility pane visible content after revisit',
      });
    });

    await runner.step('capture_artifacts', async () => {
      await captureSessionArtifacts(client, runner.runDir, 'final', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      panes: {
        firstShellPaneId,
        secondShellPaneId,
      },
      tokens: {
        firstToken,
        secondToken,
        revisitToken,
      },
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
      panes: {
        firstShellPaneId,
        secondShellPaneId,
      },
      tokens: {
        firstToken,
        secondToken,
        revisitToken,
      },
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
