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
import { MacOSDriver } from './macosDriver.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
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

async function waitForPaneCount(client, sessionId, count, description, timeoutMs = 30_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace?.panes || []).length === count && (workspace?.panes || []).every((pane) => pane.runtimeId),
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

async function focusAppForNativeShortcut(driver) {
  await driver.activateApp();
  await driver.clickWindow(0.5, 0.5);
}

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
  const startedAt = Date.now();
  while (Date.now() - startedAt < 15_000) {
    const next = await client.request('get_state');
    const remainingHarnessSessions = (next.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
    if (remainingHarnessSessions.length === 0) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error('Timed out clearing existing harness sessions before workspace switching scenario');
}

async function createShellWorkspace(client, observer, cwd, label) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const workspace = await waitForPaneCount(client, sessionId, 1, `initial pane for ${label}`);
  await client.request('select_session', { sessionId });
  await waitForShellPaneReady(client, sessionId, workspace.panes[0].paneId, `initial shell pane ready for ${label}`);
  return {
    sessionId,
    firstPane: workspace.panes[0],
  };
}

async function addSplit(client, sessionId) {
  const before = await client.request('get_workspace', { sessionId });
  const beforeIds = new Set((before.panes || []).map((pane) => pane.paneId));
  await client.request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
  const after = await waitForPaneCount(client, sessionId, 2, `split pane for ${sessionId}`);
  const created = (after.panes || []).find((pane) => !beforeIds.has(pane.paneId));
  if (!created) {
    throw new Error(`No split pane appeared for ${sessionId}. Before=${JSON.stringify(before)} After=${JSON.stringify(after)}`);
  }
  await waitForFreshSplitPaneAttached(client, sessionId, created.paneId);
  return created;
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

async function assertWorkspaceVisible(client, visibleSessionId, hiddenSessionId, expectedPaneCount) {
  const visible = await client.request('get_session_ui_state', { sessionId: visibleSessionId });
  const hidden = await client.request('get_session_ui_state', { sessionId: hiddenSessionId });
  if (!visible.workspace?.view?.sessionVisible) {
    throw new Error(`Expected ${visibleSessionId} workspace to be visible: ${JSON.stringify(visible, null, 2)}`);
  }
  if (hidden.workspace?.view?.sessionVisible) {
    throw new Error(`Expected ${hiddenSessionId} workspace to be hidden: ${JSON.stringify(hidden, null, 2)}`);
  }
  if (visible.workspace?.model?.panes?.length !== expectedPaneCount) {
    throw new Error(`Expected ${expectedPaneCount} visible panes for ${visibleSessionId}: ${JSON.stringify(visible.workspace?.model, null, 2)}`);
  }
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

async function waitForNoSessionsUnderDir(client, dir, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state').catch(() => null);
    lastSessions = (state?.sessions || []).filter((session) => session.cwd?.startsWith(dir));
    if (lastSessions.length === 0) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for harness sessions under ${dir} to close: ${JSON.stringify(lastSessions, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-workspace-switching.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'workspace-switching');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({
    appPath: options.appPath,
  });
  const createdSessionIds = [];

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    const workspaceA = await createShellWorkspace(client, observer, path.join(sessionDir, 'alpha'), `ws-switch-alpha-${runId}`);
    createdSessionIds.push(workspaceA.sessionId);
    const splitA = await addSplit(client, workspaceA.sessionId);
    createdSessionIds.push(splitA.runtimeId);
    const tokenA1 = `WSSWITCH_A1_${Date.now()}`;
    const tokenA2 = `WSSWITCH_A2_${Date.now()}`;
    await writeAndAssertToken(client, workspaceA.sessionId, workspaceA.firstPane, tokenA1);
    await writeAndAssertToken(client, workspaceA.sessionId, splitA, tokenA2);

    const workspaceB = await createShellWorkspace(client, observer, path.join(sessionDir, 'beta'), `ws-switch-beta-${runId}`);
    createdSessionIds.push(workspaceB.sessionId);
    const splitB = await addSplit(client, workspaceB.sessionId);
    createdSessionIds.push(splitB.runtimeId);
    const tokenB1 = `WSSWITCH_B1_${Date.now()}`;
    const tokenB2 = `WSSWITCH_B2_${Date.now()}`;
    await writeAndAssertToken(client, workspaceB.sessionId, workspaceB.firstPane, tokenB1);
    await writeAndAssertToken(client, workspaceB.sessionId, splitB, tokenB2);

    await client.request('select_session', { sessionId: workspaceA.sessionId });
    await assertWorkspaceVisible(client, workspaceA.sessionId, workspaceB.sessionId, 2);
    await client.request('select_session', { sessionId: workspaceB.sessionId });
    await assertWorkspaceVisible(client, workspaceB.sessionId, workspaceA.sessionId, 2);

    await focusAppForNativeShortcut(driver);
    await driver.pressKey('1', { command: true });
    await waitForActiveSession(client, workspaceA.sessionId, 'Cmd+1 selecting first workspace session');
    await assertWorkspaceVisible(client, workspaceA.sessionId, workspaceB.sessionId, 2);

    await driver.pressKey('2', { command: true });
    await waitForActiveSession(client, workspaceB.sessionId, 'Cmd+2 selecting second workspace session');
    await assertWorkspaceVisible(client, workspaceB.sessionId, workspaceA.sessionId, 2);

    await waitForPaneText(client, workspaceB.sessionId, workspaceB.firstPane.paneId, (text) => text.includes(tokenB1), 'workspace B first token after switching', 15_000);
    await waitForPaneText(client, workspaceB.sessionId, splitB.paneId, (text) => text.includes(tokenB2), 'workspace B split token after switching', 15_000);

    await client.request('focus_pane', { sessionId: workspaceB.sessionId, paneId: splitB.paneId });
    await client.request('dispatch_shortcut', { shortcutId: 'terminal.close' });
    await waitForPaneCount(client, workspaceB.sessionId, 1, 'workspace B remains after closing one split');

    await client.request('select_session', { sessionId: workspaceA.sessionId });
    await assertWorkspaceVisible(client, workspaceA.sessionId, workspaceB.sessionId, 2);
    try {
      await waitForPaneText(client, workspaceA.sessionId, workspaceA.firstPane.paneId, (text) => text.includes(tokenA1), 'workspace A first token after workspace B close', 15_000);
      await waitForPaneText(client, workspaceA.sessionId, splitA.paneId, (text) => text.includes(tokenA2), 'workspace A split token after workspace B close', 15_000);
    } catch (error) {
      await captureSessionArtifacts(client, runDir, 'workspace-a-token-failure', workspaceA.sessionId).catch(() => {});
      await captureSessionArtifacts(client, runDir, 'workspace-b-token-failure', workspaceB.sessionId).catch(() => {});
      await capturePaneTexts(client, runDir, 'workspace-a-token-failure', workspaceA.sessionId, [workspaceA.firstPane, splitA]).catch(() => {});
      await capturePaneTexts(client, runDir, 'workspace-b-token-failure', workspaceB.sessionId, [workspaceB.firstPane, splitB]).catch(() => {});
      throw error;
    }

    const summary = {
      ok: true,
      runId,
      workspaceA: { firstSessionId: workspaceA.sessionId, splitSessionId: splitA.runtimeId },
      workspaceB: { firstSessionId: workspaceB.sessionId, closedSplitSessionId: splitB.runtimeId },
      tokens: [tokenA1, tokenA2, tokenB1, tokenB2],
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Workspace switching passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const sessionId of createdSessionIds.reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, sessionDir).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
