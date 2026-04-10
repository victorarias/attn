#!/usr/bin/env node

import {
  createSessionAndWaitForMain,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  relaunchAppAndConnect,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  assertPaneCoverage,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneState,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr201-local-relaunch-existing-split.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-201',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr201-local-relaunch-existing-split',
    metadata: {
      agent: 'claude',
      focus: 'relaunch preserves existing split session',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let sessionId = null;
  let utilityPaneId = null;
  let baselineMainVisibleContent = null;
  const utilityToken = `TR201SHELL${Date.now()}`;
  const agentToken = `TR201CLAUDE${Date.now()}`;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr201-local-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeMainPromptReady,
      });
    });

    utilityPaneId = await runner.step('prepare_split_session_before_relaunch', async () => {
      const fixture = await promptClaudeForStructuredBlock(client, sessionId, agentToken, 4);
      runner.writeJson('agent-fixture.json', fixture);

      await waitForPaneText(
        client,
        sessionId,
        'main',
        (text) => text.includes(agentToken),
        'main pane text before relaunch',
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
        'utility pane before relaunch',
        20_000,
      );

      const baselineMainState = await assertPaneVisibleContent(client, sessionId, 'main', {
        contains: agentToken,
        allowWrappedContains: true,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 90,
        minMaxLineLength: 16,
        timeoutMs: 30_000,
        description: 'main pane visible content before relaunch',
      });
      baselineMainVisibleContent = baselineMainState?.pane?.visibleContent || null;
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'main pane coverage before relaunch',
      });

      await client.request('focus_pane', { sessionId, paneId: utilityPane.paneId });
      await waitForPaneVisible(client, sessionId, utilityPane.paneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, utilityPane.paneId, 20_000, { stableMs: 400 });
      await waitForPaneState(
        client,
        sessionId,
        utilityPane.paneId,
        (state) => Boolean(state?.renderHealth?.flags?.terminalReady),
        'utility pane terminal ready before relaunch seed',
        20_000,
      );
      await client.request('type_pane_via_ui', {
        sessionId,
        paneId: utilityPane.paneId,
        text: utilityToken,
      });
      await client.request('write_pane', {
        sessionId,
        paneId: utilityPane.paneId,
        text: '\r',
        submit: false,
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPane.paneId,
        (text) => text.includes(utilityToken),
        'utility pane token before relaunch',
        15_000,
      );
      await assertPaneVisibleContent(client, sessionId, utilityPane.paneId, {
        contains: utilityToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: utilityToken.length,
        minMaxLineLength: utilityToken.length,
        timeoutMs: 15_000,
        description: 'utility pane visible content before relaunch',
      });
      await assertPaneCoverage(client, sessionId, utilityPane.paneId, {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'utility pane coverage before relaunch',
      });

      await captureSessionArtifacts(client, runner.runDir, '01-pre-relaunch', sessionId);
      return utilityPane.paneId;
    });

    await runner.step('relaunch_and_verify_existing_split', async () => {
      await relaunchAppAndConnect(client, observer);
      await client.request('select_session', { sessionId });

      const restoredWorkspace = await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => {
          const paneIds = new Set((workspace.panes || []).map((pane) => pane.paneId));
          return paneIds.has('main') && paneIds.has(utilityPaneId);
        },
        `restored split workspace for ${sessionId}`,
        30_000,
      );
      runner.assert((restoredWorkspace.panes || []).length >= 2, 'restored workspace still exposes both split panes', {
        sessionId,
        utilityPaneId,
        paneCount: (restoredWorkspace.panes || []).length,
      });

      await waitForPaneVisible(client, sessionId, 'main', 20_000);
      await waitForPaneVisible(client, sessionId, utilityPaneId, 20_000);

      await assertPaneVisibleContent(client, sessionId, 'main', {
        contains: agentToken,
        allowWrappedContains: true,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 90,
        minMaxLineLength: 16,
        timeoutMs: 30_000,
        description: 'main pane visible content after relaunch',
      });
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        'main',
        baselineMainVisibleContent,
        {
          minNonEmptyLineRatio: 0.75,
          minCharCountRatio: 0.7,
          minAnchorMatches: 2,
          timeoutMs: 20_000,
          description: 'main pane content preserved after relaunch',
        },
      );
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'main pane coverage after relaunch',
      });

      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: utilityToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: utilityToken.length,
        minMaxLineLength: utilityToken.length,
        timeoutMs: 20_000,
        description: 'utility pane visible content after relaunch',
      });
      await assertPaneCoverage(client, sessionId, utilityPaneId, {
        minWidthRatio: 0.78,
        minHeightRatio: 0.72,
        timeoutMs: 20_000,
        description: 'utility pane coverage after relaunch',
      });

      await captureSessionArtifacts(client, runner.runDir, '02-post-relaunch', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      utilityPaneId,
      tokens: {
        agentToken,
        utilityToken,
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
      tokens: {
        agentToken,
        utilityToken,
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
