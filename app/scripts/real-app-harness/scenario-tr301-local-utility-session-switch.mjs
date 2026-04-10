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
import {
  assertPaneVisibleContent,
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneState,
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
    printCommonHelp('scripts/real-app-harness/scenario-tr301-local-utility-session-switch.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-301',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr301-local-utility-session-switch',
    metadata: {
      agent: 'claude',
      focus: 'utility focus survives session switch',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const returnUtilityToken = `__TR301_RETURN_${Date.now()}__`;

  let primarySessionId = null;
  let secondarySessionId = null;
  let utilityPaneId = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    primarySessionId = await runner.step('create_primary_session', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr301-primary-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeMainPromptReady,
      });
    });

    utilityPaneId = await runner.step('create_primary_utility', async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId: primarySessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId: primarySessionId,
        targetPaneId: 'main',
        direction: 'vertical',
      });
      const utilityPane = await waitForNewShellPane(
        client,
        primarySessionId,
        existingPaneIds,
        'utility pane after primary session split',
        20_000,
      );
      await client.request('focus_pane', { sessionId: primarySessionId, paneId: utilityPane.paneId });
      await waitForPaneVisible(client, primarySessionId, utilityPane.paneId, 20_000);
      await waitForPaneInputFocus(client, primarySessionId, utilityPane.paneId, 20_000, { stableMs: 500 });
      return utilityPane.paneId;
    });

    secondarySessionId = await runner.step('create_secondary_session', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr301-secondary-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeMainPromptReady,
      });
    });

    await runner.step('switch_back_and_assert_utility_focus', async () => {
      await client.request('select_session', { sessionId: primarySessionId });
      await waitForPaneVisible(client, primarySessionId, utilityPaneId, 20_000);
      await waitForPaneState(
        client,
        primarySessionId,
        utilityPaneId,
        (state) => state?.activePaneId === utilityPaneId,
        'primary utility pane to remain the active pane after session switch',
        20_000,
      );
      await waitForPaneInputFocus(client, primarySessionId, utilityPaneId, 20_000, { stableMs: 700 });
      await client.request('type_pane_via_ui', {
        sessionId: primarySessionId,
        paneId: utilityPaneId,
        text: returnUtilityToken,
      });
      await waitForPaneText(
        client,
        primarySessionId,
        utilityPaneId,
        (text) => text.includes(returnUtilityToken),
        'utility pane text to include return focus token without extra click',
        15_000,
      );
      await assertPaneVisibleContent(client, primarySessionId, utilityPaneId, {
        contains: returnUtilityToken,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: returnUtilityToken.length,
        minMaxLineLength: returnUtilityToken.length,
        timeoutMs: 15_000,
        description: 'primary utility pane visible content after switching back',
      });
      await captureSessionArtifacts(client, runner.runDir, 'primary-after-switch', primarySessionId);
      await captureSessionArtifacts(client, runner.runDir, 'secondary-baseline', secondarySessionId);
    });

    const summary = runner.finishSuccess({
      primarySessionId,
      secondarySessionId,
      utilityPaneId,
      tokens: {
        returnUtilityToken,
      },
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (primarySessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'primary-failure', primarySessionId).catch(() => {});
    }
    if (secondarySessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'secondary-failure', secondarySessionId).catch(() => {});
    }
    const summary = runner.finishFailure(error, {
      primarySessionId,
      secondarySessionId,
      utilityPaneId,
      tokens: {
        returnUtilityToken,
      },
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (secondarySessionId) {
      await cleanupSessionViaAppClose(client, observer, secondarySessionId).catch(() => {});
    }
    if (primarySessionId) {
      await cleanupSessionViaAppClose(client, observer, primarySessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
