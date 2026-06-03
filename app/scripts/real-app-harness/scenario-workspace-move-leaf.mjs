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
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  return {
    options: parseCommonArgs(args),
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await sleep(200);
  }
}

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
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
  const pane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for ${label}`);
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: `shell prompt ready for ${label}`,
  });
  const workspace = await client.request('get_workspace', { sessionId });
  return {
    sessionId,
    paneId: pane.paneId,
    workspaceId: workspace.workspaceId,
  };
}

async function waitForMovedWorkspace(client, sourceSessionId, targetSessionId, targetWorkspaceId, timeoutMs = 25_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state').catch((error) => ({ error: String(error) }));
    const source = state.sessions?.find((session) => session.id === sourceSessionId);
    const target = state.sessions?.find((session) => session.id === targetSessionId);
    const targetWorkspace = await client.request('get_workspace', { sessionId: targetSessionId }).catch((error) => ({ error: String(error) }));
    const targetUi = await client.request('get_workspace_ui_state', { workspaceId: targetWorkspaceId }).catch((error) => ({ error: String(error) }));
    last = { state, source, target, targetWorkspace, targetUi };

    const targetPaneSessionIds = new Set((targetWorkspace.panes || []).map((pane) => pane.sessionId));
    if (
      source?.workspaceId === targetWorkspaceId
      && target?.workspaceId === targetWorkspaceId
      && targetWorkspace?.workspaceId === targetWorkspaceId
      && (targetWorkspace.panes || []).length === 2
      && targetPaneSessionIds.has(sourceSessionId)
      && targetPaneSessionIds.has(targetSessionId)
      && targetUi?.active === true
      && targetUi?.sessionVisible === true
      && targetUi?.paneCount === 2
    ) {
      return last;
    }

    await sleep(200);
  }
  throw new Error(`Timed out waiting for moved pane to join target workspace. Last state:\n${JSON.stringify(last, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-workspace-move-leaf.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'workspace-move-leaf');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const createdSessionIds = [];

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    const source = await createShellWorkspace(client, observer, path.join(sessionDir, 'source'), `ws-move-source-${runId}`);
    createdSessionIds.push(source.sessionId);
    const target = await createShellWorkspace(client, observer, path.join(sessionDir, 'target'), `ws-move-target-${runId}`);
    createdSessionIds.push(target.sessionId);

    if (!source.workspaceId || !target.workspaceId || source.workspaceId === target.workspaceId) {
      throw new Error(`Unexpected workspace ids: source=${source.workspaceId} target=${target.workspaceId}`);
    }

    await client.request('select_session', { sessionId: target.sessionId });
    const move = await client.request('move_workspace_leaf', {
      sourceWorkspaceId: source.workspaceId,
      targetWorkspaceId: target.workspaceId,
      leafId: source.paneId,
      anchorId: target.paneId,
      edge: 'left',
      ratio: 0.32,
    }, { timeoutMs: 20_000 });

    const moved = await waitForMovedWorkspace(client, source.sessionId, target.sessionId, target.workspaceId);
    const sourceUi = await client.request('get_workspace_ui_state', { workspaceId: source.workspaceId }).catch((error) => ({ error: String(error) }));

    const summary = {
      ok: true,
      runId,
      source,
      target,
      move,
      moved: {
        sourceWorkspaceIdAfterMove: moved.source.workspaceId,
        targetWorkspaceIdAfterMove: moved.target.workspaceId,
        paneSessionIds: moved.targetWorkspace.panes.map((pane) => pane.sessionId),
        targetPaneCount: moved.targetUi.paneCount,
      },
      sourceUi,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Workspace pane move between workspaces passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const sessionId of createdSessionIds.reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
