#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createRunContext,
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

async function assertRemainingWorkspaceSelected(client, remainingSessionId, closedSessionId) {
  const remaining = await client.request('get_session_ui_state', { sessionId: remainingSessionId });
  const closed = await client.request('get_session_ui_state', { sessionId: closedSessionId });
  if (!remaining.selected) {
    throw new Error(`Expected remaining session ${remainingSessionId} to be selected: ${JSON.stringify(remaining, null, 2)}`);
  }
  if (!remaining.sidebarItem) {
    throw new Error(`Expected remaining session ${remainingSessionId} to remain in sidebar: ${JSON.stringify(remaining, null, 2)}`);
  }
  if (!remaining.workspace?.view?.sessionVisible) {
    throw new Error(`Expected remaining workspace to stay visible: ${JSON.stringify(remaining, null, 2)}`);
  }
  if (remaining.workspace?.model?.panes?.length !== 1) {
    throw new Error(`Expected remaining workspace to have exactly one pane: ${JSON.stringify(remaining.workspace?.model, null, 2)}`);
  }
  if (closed.exists !== false || closed.sidebarItem != null) {
    throw new Error(`Expected closed session ${closedSessionId} to be absent from sidebar: ${JSON.stringify(closed, null, 2)}`);
  }
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

  const { runId, runDir, sessionDir } = createRunContext(options, 'workspace-close-one-session-keeps-selection');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let initialSessionId = null;
  let splitSessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    initialSessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `ws-close-one-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    const initialWorkspace = await waitForPaneCount(client, initialSessionId, 1, 'initial shell workspace pane');
    const initialPane = initialWorkspace.panes[0];
    await client.request('select_session', { sessionId: initialSessionId });
    await waitForShellPaneReady(client, initialSessionId, initialPane.paneId, 'initial shell pane ready');

    const split = await splitWithShortcut(client, initialSessionId, 'terminal.splitVertical', 2);
    splitSessionId = split.pane.runtimeId;
    await client.request('focus_pane', { sessionId: initialSessionId, paneId: split.pane.paneId });
    await waitForActiveSession(client, splitSessionId, 'split session selected before close');

    await client.request('dispatch_shortcut', { shortcutId: 'terminal.close' });

    await waitForSessionAbsentFromDaemon(observer, splitSessionId, 'split session unregistered after close');
    await waitForSessionGoneFromUi(client, splitSessionId, 'split session gone from UI/sidebar after close');
    await waitForActiveSession(client, initialSessionId, 'remaining workspace session selected after close');
    await assertRemainingWorkspaceSelected(client, initialSessionId, splitSessionId);

    const summary = {
      ok: true,
      runId,
      initialSessionId,
      closedSessionId: splitSessionId,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Workspace close-one-session selection passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (initialSessionId) {
      await closeWorkspacePanes(client, initialSessionId).catch(() => {});
      await waitForNoSessionsInDir(client, sessionDir).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
