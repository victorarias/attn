#!/usr/bin/env node

// Tile-only (sessionless) workspace: dock a markdown tile, close the last
// terminal so the workspace survives as a pure docked-tile workspace, then
// prove it is both selectable and renders its tile.
//
// Regression for figgy's review on PR #257: selecting a sessionless workspace
// used to be a no-op and the workspace did not render its docked tile.
// https://github.com/victorarias/attn/pull/257#pullrequestreview-4413194398

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { socketPathForProfile } from './harnessProfile.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(__dirname, '../../..');
const ATTN_BIN = path.join(REPO_ROOT, 'attn');

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

async function waitForWorkspaceUi(client, workspaceId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await client.request('get_workspace_ui_state', { workspaceId }).catch((error) => ({ error: String(error) }));
    if (predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last workspace UI state:\n${JSON.stringify(last, null, 2)}`);
}

async function waitForSessionAbsentFromDaemon(observer, sessionId, description, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (observer.getSession(sessionId) == null ? true : null),
    description,
    timeoutMs,
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

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tile-only-workspace-select.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'tile-only-workspace-select');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const socketPath = socketPathForProfile();
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);
  console.log(`[RealAppHarness] attn socket=${socketPath}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    // 1. A normal shell workspace with one terminal.
    const cwd = path.join(sessionDir, 'notes-ws');
    fs.mkdirSync(cwd, { recursive: true });
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd,
      label: `tile-only-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane');
    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    await waitForPaneShellReady(client, sessionId, pane.paneId, {
      timeoutMs: 20_000,
      description: 'shell prompt ready',
    });

    const workspace = await client.request('get_workspace', { sessionId });
    const workspaceId = workspace.workspaceId;
    if (!workspaceId) {
      throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
    }

    // 2. Dock a markdown tile the way a user does: `attn open <file.md>`.
    const markdownPath = path.join(cwd, 'notes.md');
    fs.writeFileSync(markdownPath, '# Tile only notes\n\nDocked content.\n', 'utf8');
    const openOutput = execFileSync(ATTN_BIN, ['open', markdownPath, '--session', sessionId], {
      env: { ...process.env, ATTN_SOCKET_PATH: socketPath },
      encoding: 'utf8',
    });
    console.log(`[RealAppHarness] attn open -> ${openOutput.trim()}`);

    const docked = await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1 && state.paneCount === 1,
      'docked markdown tile to appear alongside the terminal pane',
    );
    const tileId = docked.tileIds[0];
    console.log(`[RealAppHarness] docked tileId=${tileId}`);

    // 3. Close the only terminal. The workspace must survive on its tile alone.
    await client.request('close_pane', { sessionId, paneId: pane.paneId });
    await waitForSessionAbsentFromDaemon(observer, sessionId, 'terminal session unregistered after closing last pane');
    const afterClose = await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.rendered === true && state.paneCount === 0 && Array.isArray(state?.tileIds) && state.tileIds.includes(tileId),
      'tile-only workspace to keep rendering its docked tile with no panes',
    );
    sessionId = null; // closed; nothing to clean up under this id

    // 4. Select the now-sessionless workspace by id (the sidebar click / ⌘1–9 path).
    await client.request('select_workspace', { workspaceId });
    const afterSelect = await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.active === true && state.sessionVisible === true && state.tileBodyFocused === true,
      'tile-only workspace to become active, visible, and focus its tile body after selection',
    );
    if (!afterSelect.tileIds.includes(tileId) || afterSelect.paneCount !== 0) {
      throw new Error(`Selected tile-only workspace lost its tile or grew panes: ${JSON.stringify(afterSelect, null, 2)}`);
    }
    // Title is derived from the markdown H1, not the file basename.
    if (!afterSelect.tileTitles?.includes('Tile only notes')) {
      throw new Error(`Tile header did not derive its title from the markdown H1: ${JSON.stringify(afterSelect, null, 2)}`);
    }

    const summary = {
      ok: true,
      runId,
      workspaceId,
      tileId,
      afterClose,
      afterSelect,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Tile-only workspace select+render passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
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
