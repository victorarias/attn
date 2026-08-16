#!/usr/bin/env node
// One-off verification for docs/plans/2026-08-16-snapshot-restore.md: a session
// with deep scrollback keeps it across an app relaunch. Before this change the
// worker retained 10,000 BYTES of history (289 rows at 200 columns), so the
// early lines below were gone by the time the client reattached.

import {
  createSessionAndWaitForInitialPane,
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
  captureSessionArtifacts,
  scrollPaneToTop,
  waitForFirstWorkspacePane,
  waitForNewShellPane,
  waitForPaneAttached,
  waitForPaneState,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';

const LINES = 2000;

async function main() {
  const args = process.argv.slice(2);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-snapshot-scrollback-restore.mjs');
    return;
  }
  const options = parseCommonArgs(args);
  const runner = createScenarioRunner(options, {
    scenarioId: 'SNAPSHOT-SCROLLBACK-RESTORE',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-snapshot-scrollback-restore',
    metadata: { focus: 'deep scrollback survives a relaunch restore' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  const anchor = `SNAPANCHOR${Date.now()}`;
  const tail = `${anchor}-TAIL`;
  let sessionId = null;
  let shellPaneId = null;

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: runner.sessionDir,
      label: `snapshot-scrollback-${runner.runId}`,
      agent: 'codex',
      waitForInitialPaneVisible: false,
    }));

    shellPaneId = await runner.step('open_shell_pane', async () => {
      const initialPane = await waitForFirstWorkspacePane(client, sessionId, 'initial pane', 20_000);
      const workspace = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspace.panes || []).map((pane) => pane.paneId));
      existingPaneIds.add(initialPane.paneId);
      await client.request('split_pane', { sessionId, targetPaneId: initialPane.paneId, direction: 'vertical' });
      const shellPane = await waitForNewShellPane(client, sessionId, existingPaneIds, 'scrollback shell pane', 30_000);
      await waitForPaneAttached(client, sessionId, shellPane.paneId, 20_000);
      return shellPane.paneId;
    });

    await runner.step('seed_deep_scrollback', async () => {
      await client.request('write_pane', {
        sessionId,
        paneId: shellPaneId,
        text: `printf '${anchor}\\n'; jot -w 'SNAPROW_%05d' ${LINES} 1; printf '${tail}\\n'`,
      });
      await waitForPaneText(client, sessionId, shellPaneId, (t) => t.includes(tail), 'seeded output', 60_000);
      await captureSessionArtifacts(client, runner.runDir, '01-seeded', sessionId);
    });

    await runner.step('relaunch_and_read_restored_scrollback', async () => {
      await relaunchAppAndConnect(client, observer);
      await client.request('select_session', { sessionId });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => (workspace.panes || []).some((pane) => pane.paneId === shellPaneId),
        `restored workspace for ${sessionId}`,
        30_000,
      );
      await waitForPaneVisible(client, sessionId, shellPaneId, 20_000);
      await waitForPaneText(client, sessionId, shellPaneId, (t) => t.includes(tail), 'restored tail', 30_000);

      const payload = await client.request('read_pane_text', { sessionId, paneId: shellPaneId }, { timeoutMs: 20_000 });
      const body = typeof payload?.text === 'string' ? payload.text : '';
      const rows = (body.match(/SNAPROW_\d{5}/g) || []);
      runner.assert(rows.length > 1000, 'restored buffer carries deep scrollback', {
        restoredRows: rows.length,
        seededRows: LINES,
        firstRow: rows[0] || null,
      });

      await scrollPaneToTop(client, sessionId, shellPaneId);
      await waitForPaneState(
        client,
        sessionId,
        shellPaneId,
        (state) => (state?.pane?.visibleContent?.lines || []).join('\n').includes('SNAPROW_000'),
        'early scrollback rows reachable after restore',
        20_000,
      );
      await captureSessionArtifacts(client, runner.runDir, '02-restored', sessionId);
    });
    if (sessionId) {
      await cleanupSessionViaAppClose(runner, client, sessionId).catch(() => {});
    }
    runner.finishSuccess({ sessionId, shellPaneId });
  } catch (error) {
    runner.finishFailure(error, { sessionId, shellPaneId });
    throw error;
  } finally {
    observer.close?.();
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
