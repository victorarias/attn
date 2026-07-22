#!/usr/bin/env node

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

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

function paneIds(workspace) {
  return new Set((workspace?.panes || []).map((pane) => pane.paneId));
}

async function waitForPaneCount(client, sessionId, count, description, timeoutMs = 30_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace?.panes || []).length === count && (workspace?.panes || []).every((pane) => pane.runtimeId),
    description,
    timeoutMs,
  );
}

async function waitForShellPaneReady(client, sessionId, paneId, description) {
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneAttached(client, sessionId, paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, paneId, {
    timeoutMs: 20_000,
    description,
  });
}

async function splitWithShortcut(client, sessionId, shortcutId, expectedCount) {
  const before = await client.request('get_workspace', { sessionId });
  const beforeIds = paneIds(before);
  await client.request('dispatch_shortcut', { shortcutId });
  const after = await waitForPaneCount(client, sessionId, expectedCount, `${shortcutId} created pane`, 30_000);
  const created = (after.panes || []).find((pane) => !beforeIds.has(pane.paneId));
  if (!created) {
    throw new Error(`No new pane appeared after ${shortcutId}. Before=${JSON.stringify(before)} After=${JSON.stringify(after)}`);
  }
  await waitForPaneVisible(client, sessionId, created.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, created.paneId, 20_000);
  return { workspace: after, pane: created };
}

async function waitForSessionGoneFromUi(client, sessionId, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_session_ui_state', { sessionId }).catch((error) => ({ error: String(error) }));
    if (lastState?.exists === false && lastState?.sidebarItem == null) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last UI state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForSessionAbsentFromDaemon(observer, sessionId, description, timeoutMs = 20_000) {
  await observer.waitFor(
    () => observer.getSession(sessionId) == null ? true : null,
    description,
    timeoutMs,
  );
}

async function waitForActiveSession(client, sessionId, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_state');
    if (lastState.activeSessionId === sessionId) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function assertRemainingWorkspaceSelected(runner, client, remainingSessionId, closedSessionId) {
  const remaining = await client.request('get_session_ui_state', { sessionId: remainingSessionId });
  const closed = await client.request('get_session_ui_state', { sessionId: closedSessionId });
  runner.assert(
    Boolean(remaining.selected),
    `Expected remaining session ${remainingSessionId} to be selected: ${JSON.stringify(remaining, null, 2)}`,
    remaining,
  );
  runner.assert(
    Boolean(remaining.sidebarItem),
    `Expected remaining session ${remainingSessionId} to remain in sidebar: ${JSON.stringify(remaining, null, 2)}`,
    remaining,
  );
  runner.assert(
    Boolean(remaining.workspace?.view?.sessionVisible),
    `Expected remaining workspace to stay visible: ${JSON.stringify(remaining, null, 2)}`,
    remaining,
  );
  runner.assert(
    remaining.workspace?.model?.panes?.length === 1,
    `Expected remaining workspace to have exactly one pane: ${JSON.stringify(remaining.workspace?.model, null, 2)}`,
    remaining,
  );
  runner.assert(
    closed.exists === false && closed.sidebarItem == null,
    `Expected closed session ${closedSessionId} to be absent from sidebar: ${JSON.stringify(closed, null, 2)}`,
    closed,
  );
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}

async function waitForNoSessionsInDir(client, dir, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state').catch(() => null);
    lastSessions = (state?.sessions || []).filter((session) => session.cwd === dir);
    if (lastSessions.length === 0) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for harness sessions in ${dir} to close: ${JSON.stringify(lastSessions, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-workspace-close-one-session-keeps-selection.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'WORKSPACE-CLOSE-ONE-SESSION-KEEPS-SELECTION',
    tier: 'tier1-local-shell',
    prefix: 'workspace-close-one-session-keeps-selection',
    metadata: {
      agent: 'shell',
      focus: 'closing a split session keeps the remaining workspace session selected',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let initialSessionId = null;
  let splitSessionId = null;
  const note = (m, extra) => runner.log(m, extra);

  // Cleanup, registered as soon as each resource exists so a signal mid-scenario
  // still tears them down. Runner cleanups run in REVERSE registration order, so
  // register observer/app first (they must close LAST) and the session last (it
  // must close FIRST) to reproduce the effective order below: close initial
  // workspace panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_initial_session', async () => {
      initialSessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `ws-close-one-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      runner.registerCleanup('close_initial_workspace', async () => {
        await closeWorkspacePanes(client, initialSessionId);
        await waitForNoSessionsInDir(client, runner.sessionDir);
      });
      const initialWorkspace = await waitForPaneCount(client, initialSessionId, 1, 'initial shell workspace pane');
      const initialPane = initialWorkspace.panes[0];
      await client.request('select_session', { sessionId: initialSessionId });
      await waitForShellPaneReady(client, initialSessionId, initialPane.paneId, 'initial shell pane ready');
      note(`initial shell session ready and selected`, { initialSessionId });
    });

    await runner.step('split_session', async () => {
      const split = await splitWithShortcut(client, initialSessionId, 'terminal.splitVertical', 2);
      splitSessionId = split.pane.runtimeId;
      await client.request('focus_pane', { sessionId: initialSessionId, paneId: split.pane.paneId });
      await waitForActiveSession(client, splitSessionId, 'split session selected before close');
      note(`split session created and selected`, { splitSessionId });
    });

    await runner.step('close_split_session', async () => {
      await client.request('dispatch_shortcut', { shortcutId: 'terminal.close' });
      await waitForSessionAbsentFromDaemon(observer, splitSessionId, 'split session unregistered after close');
      await waitForSessionGoneFromUi(client, splitSessionId, 'split session gone from UI/sidebar after close');
      note(`split session closed`, { splitSessionId });
    });

    await runner.step('verify_selection_restored', async () => {
      await waitForActiveSession(client, initialSessionId, 'remaining workspace session selected after close');
      await assertRemainingWorkspaceSelected(runner, client, initialSessionId, splitSessionId);
      note(`remaining workspace session selected after close`, { initialSessionId });
    });

    const summary = runner.finishSuccess({ initialSessionId, closedSessionId: splitSessionId });
    console.log('[RealAppHarness] Workspace close-one-session selection passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { initialSessionId, closedSessionId: splitSessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (initialSessionId) {
      await closeWorkspacePanes(client, initialSessionId).catch(() => {});
      await waitForNoSessionsInDir(client, runner.sessionDir).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
