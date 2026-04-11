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
  assertPaneStyleSummaryPreserved,
  assertPaneVisibleContent,
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneStyle,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
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

function formattingFixtureCommand(token) {
  const payload = [
    `\u001b[1;31m${token}-bold-red\u001b[0m plain\n`,
    `\u001b[4;38;2;12;180;220m${token}-underline-rgb\u001b[0m plain\n`,
    `\u001b[7;30;48;5;214m${token}-inverse-palette-bg\u001b[0m plain\n`,
    `\u001b[3;38;5;45;48;2;24;24;96m${token}-italic-rgb-bg\u001b[0m plain\n`,
  ].join('');
  const hexPayload = Buffer.from(payload, 'utf8').toString('hex');
  return `python3 -c 'import sys;sys.stdout.buffer.write(bytes.fromhex("${hexPayload}"))'`;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr204-local-relaunch-formatting.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-204',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr204-local-relaunch-formatting',
    metadata: {
      agent: 'claude',
      focus: 'relaunch formatting preservation through replay',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const formatToken = `TR204FMT${Date.now()}`;
  const expectedLastToken = `${formatToken}-italic-rgb-bg`;

  let sessionId = null;
  let utilityPaneId = null;
  let baselineStyle = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `tr204-local-formatting-${runner.runId}`,
        agent: 'claude',
        promptReadyFn: ensureClaudeMainPromptReady,
      });
    });

    utilityPaneId = await runner.step('seed_formatted_utility_output', async () => {
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
        'utility pane for formatting fixture',
        20_000,
      );
      await client.request('focus_pane', { sessionId, paneId: utilityPane.paneId });
      await waitForPaneVisible(client, sessionId, utilityPane.paneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, utilityPane.paneId, 20_000, { stableMs: 400 });
      await client.request('write_pane', {
        sessionId,
        paneId: utilityPane.paneId,
        text: formattingFixtureCommand(formatToken),
      });
      await waitForPaneText(
        client,
        sessionId,
        utilityPane.paneId,
        (text) => text.includes(expectedLastToken),
        'formatted utility pane text before relaunch',
        20_000,
      );
      baselineStyle = await waitForPaneStyle(
        client,
        sessionId,
        utilityPane.paneId,
        (style) => {
          const summary = style?.summary || {};
          return (
            (summary.styledCellCount || 0) >= 40 &&
            (summary.styledLineCount || 0) >= 4 &&
            (summary.boldCellCount || 0) >= 8 &&
            (summary.italicCellCount || 0) >= 8 &&
            (summary.underlineCellCount || 0) >= 8 &&
            (summary.inverseCellCount || 0) >= 8 &&
            (summary.fgPaletteCellCount || 0) >= 8 &&
            (summary.fgRgbCellCount || 0) >= 8 &&
            (summary.bgPaletteCellCount || 0) >= 8 &&
            (summary.bgRgbCellCount || 0) >= 8 &&
            (summary.uniqueStyleCount || 0) >= 4
          );
        },
        'formatted utility pane style before relaunch',
        20_000,
      );
      await assertPaneVisibleContent(client, sessionId, utilityPane.paneId, {
        contains: expectedLastToken,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 80,
        minMaxLineLength: 18,
        timeoutMs: 20_000,
        description: 'formatted utility pane visible content before relaunch',
      });
      runner.writeJson('formatting-baseline-style.json', baselineStyle);
      await captureSessionArtifacts(client, runner.runDir, '01-pre-relaunch', sessionId);
      return utilityPane.paneId;
    });

    await runner.step('relaunch_and_verify_formatting_restore', async () => {
      await relaunchAppAndConnect(client, observer);
      await client.request('select_session', { sessionId });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => (workspace.panes || []).some((pane) => pane.paneId === utilityPaneId),
        `restored workspace for ${sessionId}`,
        30_000,
      );
      await waitForPaneVisible(client, sessionId, utilityPaneId, 20_000);
      await waitForPaneText(
        client,
        sessionId,
        utilityPaneId,
        (text) => text.includes(expectedLastToken),
        'formatted utility pane text after relaunch',
        20_000,
      );
      const restoredStyle = await assertPaneStyleSummaryPreserved(
        client,
        sessionId,
        utilityPaneId,
        baselineStyle?.style || null,
        {
          minStyledCellRatio: 0.85,
          minStyledLineRatio: 0.75,
          minBoldCellRatio: 0.75,
          minUnderlineCellRatio: 0.75,
          minInverseCellRatio: 0.75,
          minFgPaletteCellRatio: 0.75,
          minFgRgbCellRatio: 0.75,
          minBgPaletteCellRatio: 0.75,
          minBgRgbCellRatio: 0.75,
          minUniqueStyleRatio: 0.75,
          timeoutMs: 20_000,
          description: 'formatted utility pane style after relaunch',
        },
      );
      await assertPaneVisibleContent(client, sessionId, utilityPaneId, {
        contains: expectedLastToken,
        minNonEmptyLines: 4,
        minDenseLines: 1,
        minCharCount: 80,
        minMaxLineLength: 18,
        timeoutMs: 20_000,
        description: 'formatted utility pane visible content after relaunch',
      });
      runner.writeJson('formatting-restored-style.json', restoredStyle);
      await captureSessionArtifacts(client, runner.runDir, '02-post-relaunch', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      utilityPaneId,
      token: formatToken,
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
      token: formatToken,
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
