#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
  createRunContext,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
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

async function waitForPicker(client, title, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('location_picker_get_state');
    if (lastState?.open && lastState.title === title) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${title}. Last picker state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function selectTerminalAgent(driver, client) {
  await driver.activateApp();
  await driver.pressKey('t', { option: true });
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < 5_000) {
    lastState = await client.request('location_picker_get_state');
    if (lastState?.selectedAgent === 'Terminal') {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Alt+T did not select Terminal. Last picker state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function submitTerminalLocation({ client, driver, cwd, expectedTitle }) {
  fs.mkdirSync(cwd, { recursive: true });
  await waitForPicker(client, expectedTitle);
  await selectTerminalAgent(driver, client);
  await client.request('location_picker_set_path', { value: cwd });
  await client.request('location_picker_submit_path');
}

async function waitForShellSession({ client, observer, cwd, description }) {
  const session = await observer.waitForSession({ directory: cwd, timeoutMs: 30_000 });
  if (session.agent !== 'shell') {
    throw new Error(`Expected ${description} to create a terminal session, got agent=${session.agent}`);
  }
  const workspace = await waitForSessionWorkspace(
    client,
    session.id,
    (entry) => (entry?.panes || []).some((pane) => pane.sessionId === session.id && pane.runtimeId),
    `${description} workspace pane`,
    30_000,
  );
  const pane = (workspace.panes || []).find((entry) => entry.sessionId === session.id);
  if (!pane) {
    throw new Error(`No pane for ${description}: ${JSON.stringify(workspace, null, 2)}`);
  }
  await waitForPaneVisible(client, session.id, pane.paneId, 20_000);
  await waitForPaneAttached(client, session.id, pane.paneId, 20_000);
  await waitForPaneShellReady(client, session.id, pane.paneId, {
    timeoutMs: 20_000,
    description: `${description} shell prompt`,
  });
  return { session, pane };
}

async function waitForWorkspaceSessionCount(client, workspaceId, count, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_state');
    const sessions = (lastState.sessions || []).filter((entry) => entry.workspaceId === workspaceId);
    if (sessions.length === count) {
      return sessions;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
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
    printCommonHelp('scripts/real-app-harness/scenario-workspace-creation-shortcuts.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'workspace-creation-shortcuts');
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

    const workspaceDir = path.join(sessionDir, 'workspace-a');
    await driver.activateApp();
    await driver.pressKey('t', { command: true });
    await submitTerminalLocation({
      client,
      driver,
      cwd: workspaceDir,
      expectedTitle: 'New Workspace Location',
    });
    const first = await waitForShellSession({
      client,
      observer,
      cwd: workspaceDir,
      description: 'Cmd+T workspace',
    });
    createdSessionIds.push(first.session.id);
    const workspaceId = first.session.workspace_id;
    if (!workspaceId) {
      throw new Error(`Created session has no workspace_id: ${JSON.stringify(first.session, null, 2)}`);
    }

    const verticalDir = path.join(sessionDir, 'session-vertical');
    await driver.activateApp();
    await driver.pressKey('n', { command: true });
    await submitTerminalLocation({
      client,
      driver,
      cwd: verticalDir,
      expectedTitle: 'New Session Location',
    });
    const vertical = await waitForShellSession({
      client,
      observer,
      cwd: verticalDir,
      description: 'Cmd+N session split',
    });
    createdSessionIds.push(vertical.session.id);
    if (vertical.session.workspace_id !== workspaceId) {
      throw new Error(`Cmd+N created session in wrong workspace: ${vertical.session.workspace_id} !== ${workspaceId}`);
    }
    await waitForWorkspaceSessionCount(client, workspaceId, 2, 'Cmd+N session to join selected workspace');

    const horizontalDir = path.join(sessionDir, 'session-horizontal');
    await driver.activateApp();
    await driver.pressKey('n', { command: true, shift: true });
    await submitTerminalLocation({
      client,
      driver,
      cwd: horizontalDir,
      expectedTitle: 'New Session Location',
    });
    const horizontal = await waitForShellSession({
      client,
      observer,
      cwd: horizontalDir,
      description: 'Cmd+Shift+N horizontal session split',
    });
    createdSessionIds.push(horizontal.session.id);
    if (horizontal.session.workspace_id !== workspaceId) {
      throw new Error(`Cmd+Shift+N created session in wrong workspace: ${horizontal.session.workspace_id} !== ${workspaceId}`);
    }
    const workspace = await waitForSessionWorkspace(
      client,
      first.session.id,
      (entry) => (entry?.panes || []).length === 3 && (entry?.workspace?.layout?.splits || []).some((split) => split.direction === 'horizontal'),
      'Cmd+Shift+N horizontal split in selected workspace',
      30_000,
    );

    const summary = {
      ok: true,
      runId,
      workspaceId,
      sessionIds: createdSessionIds,
      paneIds: workspace.panes.map((pane) => pane.paneId),
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Workspace creation shortcuts passed.');
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
