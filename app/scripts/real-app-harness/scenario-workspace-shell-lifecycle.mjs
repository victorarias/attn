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
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';

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

async function splitWithShortcut(client, sessionId, shortcutId, expectedCount) {
  const before = await client.request('get_workspace', { sessionId });
  const beforeIds = paneIds(before);
  await client.request('dispatch_shortcut', { shortcutId });
  const after = await waitForPaneCount(client, sessionId, expectedCount, `${shortcutId} created pane`, 30_000);
  const created = (after.panes || []).find((pane) => !beforeIds.has(pane.paneId));
  if (!created) {
    throw new Error(`No new pane appeared after ${shortcutId}. Before=${JSON.stringify(before)} After=${JSON.stringify(after)}`);
  }
  return { workspace: after, pane: created };
}

async function waitForShellPaneReady(client, sessionId, paneId, description) {
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneAttached(client, sessionId, paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, paneId, {
    timeoutMs: 20_000,
    description,
  });
}

async function waitForFreshSplitPaneAttached(client, sessionId, paneId) {
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneAttached(client, sessionId, paneId, 20_000);
}

async function writeAndAssertToken(client, sessionId, pane, token) {
  await client.request('write_pane', {
    sessionId,
    paneId: pane.paneId,
    text: `printf '${token}\\n'`,
  });
  await waitForPaneText(
    client,
    sessionId,
    pane.paneId,
    (text) => text.includes(token),
    `pane ${pane.paneId} contains ${token}`,
    20_000,
  );
}

async function capturePaneTexts(client, runDir, prefix, sessionId, panes) {
  const payload = {};
  for (const pane of panes) {
    payload[pane.paneId] = await client.request('read_pane_text', {
      sessionId,
      paneId: pane.paneId,
    }).catch((error) => ({ error: error instanceof Error ? error.message : String(error) }));
  }
  fs.writeFileSync(path.join(runDir, `${prefix}-pane-texts.json`), `${JSON.stringify(payload, null, 2)}\n`, 'utf8');
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
    printCommonHelp('scripts/real-app-harness/scenario-workspace-shell-lifecycle.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'workspace-shell-lifecycle');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);
    await client.request('set_terminal_runtime_trace', { enabled: true }).catch(() => {});

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `ws-life-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    let workspace = await waitForPaneCount(client, sessionId, 1, 'initial shell workspace pane');
    const initialPane = workspace.panes[0];
    await client.request('select_session', { sessionId });
    await waitForShellPaneReady(client, sessionId, initialPane.paneId, 'initial shell pane ready');

    const vertical = await splitWithShortcut(client, sessionId, 'terminal.splitVertical', 2);
    workspace = vertical.workspace;
    const verticalPane = vertical.pane;
    await waitForFreshSplitPaneAttached(client, sessionId, verticalPane.paneId);

    await client.request('focus_pane', { sessionId, paneId: verticalPane.paneId });
    const horizontal = await splitWithShortcut(client, sessionId, 'terminal.splitHorizontal', 3);
    workspace = horizontal.workspace;
    const horizontalPane = horizontal.pane;
    await waitForFreshSplitPaneAttached(client, sessionId, horizontalPane.paneId);

    const tokenA = `WSLIFE_A_${Date.now()}`;
    const tokenB = `WSLIFE_B_${Date.now()}`;
    const tokenC = `WSLIFE_C_${Date.now()}`;
    await writeAndAssertToken(client, sessionId, initialPane, tokenA);
    await writeAndAssertToken(client, sessionId, verticalPane, tokenB);
    await writeAndAssertToken(client, sessionId, horizontalPane, tokenC);

    await client.request('focus_pane', { sessionId, paneId: verticalPane.paneId });
    await client.request('dispatch_shortcut', { shortcutId: 'terminal.close' });
    workspace = await waitForPaneCount(client, sessionId, 2, 'workspace remains after closing one session pane');
    if ((workspace.panes || []).some((pane) => pane.paneId === verticalPane.paneId)) {
      throw new Error(`Closed pane ${verticalPane.paneId} is still present: ${JSON.stringify(workspace, null, 2)}`);
    }

    try {
      await waitForPaneText(client, sessionId, initialPane.paneId, (text) => text.includes(tokenA), 'initial pane token survived close', 15_000);
      await waitForPaneText(client, sessionId, horizontalPane.paneId, (text) => text.includes(tokenC), 'remaining split token survived close', 15_000);
    } catch (error) {
      await captureSessionArtifacts(client, runDir, 'token-survival-failure', sessionId).catch(() => {});
      await capturePaneTexts(client, runDir, 'token-survival-failure', sessionId, [initialPane, verticalPane, horizontalPane]).catch(() => {});
      throw error;
    }

    const summary = {
      ok: true,
      runId,
      sessionId,
      closedPaneId: verticalPane.paneId,
      remainingPaneIds: workspace.panes.map((pane) => pane.paneId),
      tokens: [tokenA, tokenB, tokenC],
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Workspace shell lifecycle passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
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
