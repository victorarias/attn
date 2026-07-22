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
import {
  captureSessionArtifacts,
  scrollPaneToTop,
  waitForFirstWorkspacePane,
  waitForNewShellPane,
  waitForPaneAttached,
  waitForPaneState,
  waitForPaneText,
} from './scenarioAssertions.mjs';

function withTimeout(promise, timeoutMs) {
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      setTimeout(() => reject(new Error(`Timed out after ${timeoutMs}ms`)), timeoutMs);
    }),
  ]);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-ghostty-scrollback-anchor.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'GHOSTTY-SCROLLBACK-ANCHOR',
    tier: 'tier1-local-shell',
    prefix: 'scenario-ghostty-scrollback-anchor',
    metadata: {
      focus: 'manual scroll position remains anchored while shell output streams',
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const seedAnchor = `SCROLL_HEAD_${Date.now()}`;
  const seedEnd = `SCROLL_SEED_END_${Date.now()}`;
  const streamEnd = `SCROLL_STREAM_END_${Date.now()}`;
  let sessionId = null;
  let shellPaneId = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => {
      return createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `ghostty-scroll-${runner.runId}`,
        agent: 'codex',
        waitForInitialPaneVisible: false,
      });
    });

    shellPaneId = await runner.step('open_shell_pane', async () => {
      const initialPane = await waitForFirstWorkspacePane(client, sessionId, 'initial pane for scrollback split', 20_000);
      // Baseline must be captured after the initial pane exists, or the split
      // helper can mistake the initial pane for the new shell pane.
      const workspace = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspace.panes || []).map((pane) => pane.paneId));
      existingPaneIds.add(initialPane.paneId);
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialPane.paneId,
        direction: 'vertical',
      });
      const shellPane = await waitForNewShellPane(client, sessionId, existingPaneIds, 'scrollback shell pane', 30_000);
      await waitForPaneAttached(client, sessionId, shellPane.paneId, 20_000);
      return shellPane.paneId;
    });

    await runner.step('fill_and_scroll_to_anchor', async () => {
      await client.request('write_pane', {
        sessionId,
        paneId: shellPaneId,
        text: `printf '${seedAnchor}\\n'; jot -w 'SEED_%03d' 100 1; printf '${seedEnd}\\n'`,
      });
      await waitForPaneText(
        client,
        sessionId,
        shellPaneId,
        (text) => text.includes(seedEnd),
        'seed scrollback output',
        20_000,
      );
      await scrollPaneToTop(client, sessionId, shellPaneId);
      await waitForPaneState(
        client,
        sessionId,
        shellPaneId,
        (state) => (state?.pane?.visibleContent?.lines || []).join('\n').includes(seedAnchor),
        'seed anchor visible at top of shell scrollback',
        20_000,
      );
      await captureSessionArtifacts(client, runner.runDir, '01-scrolled-anchor', sessionId);
    });

    await runner.step('stream_output_without_losing_anchor', async () => {
      // Typing snaps the viewport to the bottom, so the stream is started first
      // (with a delay) and the viewport re-anchored before output arrives — the
      // assertion then isolates whether STREAMING OUTPUT moves the viewport.
      await client.request('write_pane', {
        sessionId,
        paneId: shellPaneId,
        text: `sh -c 'sleep 3; i=1; while [ "$i" -le 40 ]; do printf "STREAM_%03d\\n" "$i"; sleep 0.02; i=$((i+1)); done; printf "${streamEnd}\\n"'`,
      });
      await scrollPaneToTop(client, sessionId, shellPaneId);
      await waitForPaneState(
        client,
        sessionId,
        shellPaneId,
        (state) => (state?.pane?.visibleContent?.lines || []).join('\n').includes(seedAnchor),
        'seed anchor re-anchored before stream output begins',
        10_000,
      );
      await waitForPaneText(
        client,
        sessionId,
        shellPaneId,
        (text) => text.includes(streamEnd),
        'streaming shell output completion',
        20_000,
      );
      const settledState = await client.request('get_pane_state', { sessionId, paneId: shellPaneId });
      await captureSessionArtifacts(client, runner.runDir, '02-after-stream', sessionId);
      const visibleText = (settledState?.pane?.visibleContent?.lines || []).join('\n');
      runner.assert(visibleText.includes(seedAnchor), 'streaming output preserves manually scrolled viewport anchor', {
        seedAnchor,
        visibleText,
      });
      runner.assert(!visibleText.includes(streamEnd), 'streaming output does not snap scrolled viewport to live tail', {
        streamEnd,
        visibleText,
      });
    });

    const summary = runner.finishSuccess({ sessionId, shellPaneId, seedAnchor, seedEnd, streamEnd });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId).catch(() => {});
    }
    const summary = runner.finishFailure(error, { sessionId, shellPaneId, seedAnchor, seedEnd, streamEnd });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    try {
      await withTimeout(cleanupSessionViaAppClose(client, observer, sessionId), 15_000);
    } catch {}
    try {
      await withTimeout(client.quitApp(), 5_000);
    } catch {}
    try {
      await withTimeout(observer.disconnect(), 5_000);
    } catch {}
  }
}

main()
  .then(() => {
    process.exit(process.exitCode ?? 0);
  })
  .catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
