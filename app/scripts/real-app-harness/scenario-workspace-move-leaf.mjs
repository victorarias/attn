#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
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
import { createScenarioRunner } from './scenarioRunner.mjs';

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

  const runner = createScenarioRunner(options, {
    scenarioId: 'WORKSPACE-MOVE-LEAF',
    tier: 'tier1-local-shell',
    prefix: 'workspace-move-leaf',
    metadata: {
      agent: 'shell',
      focus: 'moving a workspace pane (leaf) from one workspace into another via move_workspace_leaf',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const createdSessionIds = [];
  const note = (m, extra) => runner.log(m, extra);

  // Cleanup, registered as soon as each resource exists so a signal mid-scenario
  // still tears them down. Runner cleanups run in REVERSE registration order, so
  // register observer/app first (they must close LAST) and the created sessions
  // last (they must close FIRST) to reproduce the effective order below: close
  // created session panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_created_sessions', async () => {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
  });

  try {
    await runner.step('launch_app', async () => {
      process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    let source;
    await runner.step('create_source_workspace', async () => {
      source = await createShellWorkspace(client, observer, path.join(runner.sessionDir, 'source'), `ws-move-source-${runner.runId}`);
      createdSessionIds.push(source.sessionId);
      note(`source workspace ready`, source);
    });

    let target;
    await runner.step('create_target_workspace', async () => {
      target = await createShellWorkspace(client, observer, path.join(runner.sessionDir, 'target'), `ws-move-target-${runner.runId}`);
      createdSessionIds.push(target.sessionId);
      note(`target workspace ready`, target);

      runner.assert(
        Boolean(source.workspaceId) && Boolean(target.workspaceId) && source.workspaceId !== target.workspaceId,
        `Unexpected workspace ids: source=${source.workspaceId} target=${target.workspaceId}`,
        { source, target },
      );
    });

    let move;
    let moved;
    let sourceUi;
    await runner.step('move_leaf', async () => {
      await client.request('select_session', { sessionId: target.sessionId });
      move = await client.request('move_workspace_leaf', {
        sourceWorkspaceId: source.workspaceId,
        targetWorkspaceId: target.workspaceId,
        leafId: source.paneId,
        anchorId: target.paneId,
        edge: 'left',
        ratio: 0.32,
      }, { timeoutMs: 20_000 });
      note(`move_workspace_leaf requested`, move);
    });

    await runner.step('verify_moved', async () => {
      moved = await waitForMovedWorkspace(client, source.sessionId, target.sessionId, target.workspaceId);
      sourceUi = await client.request('get_workspace_ui_state', { workspaceId: source.workspaceId }).catch((error) => ({ error: String(error) }));
      note(`moved pane joined target workspace`, {
        targetPaneCount: moved.targetUi.paneCount,
        sourceUi,
      });
    });

    const summary = runner.finishSuccess({
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
    });
    console.log('[RealAppHarness] Workspace pane move between workspaces passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionIds: createdSessionIds });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [...createdSessionIds].reverse()) {
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
