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
import { createScenarioRunner } from './scenarioRunner.mjs';

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

  const runner = createScenarioRunner(options, {
    scenarioId: 'TILE-ONLY-WORKSPACE-SELECT',
    tier: 'tier1-local-shell',
    prefix: 'tile-only-workspace-select',
    metadata: {
      agent: 'shell',
      focus: 'sessionless (tile-only) workspace select + render',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const socketPath = socketPathForProfile();
  let sessionId = null;
  const note = (m, extra) => runner.log(m, extra);

  runner.log(`[RealAppHarness] attn socket=${socketPath}`);

  // Cleanup, registered as soon as each resource exists so a signal mid-scenario
  // still tears them down. Runner cleanups run in REVERSE registration order, so
  // register observer/app first (they must close LAST); the session pane close is
  // registered later and guarded by `sessionId` so it is a no-op once the pane is
  // closed as part of the scenario itself (step 3).
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    // 1. A normal shell workspace with one terminal.
    const cwd = path.join(runner.sessionDir, 'notes-ws');
    await runner.step('create_shell_session', async () => {
      fs.mkdirSync(cwd, { recursive: true });
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `tile-only-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      runner.registerCleanup('close_session_panes', () => (sessionId ? closeWorkspacePanes(client, sessionId) : null));
      const pane = await waitForFirstWorkspacePane(client, sessionId, 'initial workspace pane');
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell prompt ready',
      });
      note(`shell session ready and selected`, { sessionId });
    });

    // 2. Dock a markdown tile the way a user does: `attn open <file.md>`.
    const { workspaceId, tileId, pane } = await runner.step('dock_markdown_tile', async () => {
      const workspace = await client.request('get_workspace', { sessionId });
      const id = workspace.workspaceId;
      if (!id) {
        throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
      }
      const initialPane = await waitForFirstWorkspacePane(client, sessionId, 'workspace pane before close');

      const markdownPath = path.join(cwd, 'notes.md');
      fs.writeFileSync(markdownPath, '# Tile only notes\n\nDocked content.\n', 'utf8');
      const openOutput = execFileSync(ATTN_BIN, ['open', markdownPath, '--session', sessionId], {
        env: { ...process.env, ATTN_SOCKET_PATH: socketPath },
        encoding: 'utf8',
      });
      note(`attn open -> ${openOutput.trim()}`);

      const docked = await waitForWorkspaceUi(
        client,
        id,
        (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1 && state.paneCount === 1,
        'docked markdown tile to appear alongside the terminal pane',
      );
      note(`docked tileId=${docked.tileIds[0]}`);
      return { workspaceId: id, tileId: docked.tileIds[0], pane: initialPane };
    });

    // 3. Close the only terminal. The workspace must survive on its tile alone.
    const afterClose = await runner.step('close_terminal_pane', async () => {
      await client.request('close_pane', { sessionId, paneId: pane.paneId });
      await waitForSessionAbsentFromDaemon(observer, sessionId, 'terminal session unregistered after closing last pane');
      const state = await waitForWorkspaceUi(
        client,
        workspaceId,
        (s) => s?.rendered === true && s.paneCount === 0 && Array.isArray(s?.tileIds) && s.tileIds.includes(tileId),
        'tile-only workspace to keep rendering its docked tile with no panes',
      );
      sessionId = null; // closed; nothing to clean up under this id
      return state;
    });

    // 4. Select the now-sessionless workspace by id (the sidebar click / ⌘1–9 path).
    const afterSelect = await runner.step('select_tile_only_workspace', async () => {
      await client.request('select_workspace', { workspaceId });
      const state = await waitForWorkspaceUi(
        client,
        workspaceId,
        (s) => s?.active === true && s.sessionVisible === true && s.tileBodyFocused === true,
        'tile-only workspace to become active, visible, and focus its tile body after selection',
      );
      runner.assert(
        state.tileIds.includes(tileId) && state.paneCount === 0,
        `Selected tile-only workspace lost its tile or grew panes: ${JSON.stringify(state, null, 2)}`,
        state,
      );
      // Title is derived from the markdown H1, not the file basename.
      runner.assert(
        state.tileTitles?.includes('Tile only notes'),
        `Tile header did not derive its title from the markdown H1: ${JSON.stringify(state, null, 2)}`,
        state,
      );
      return state;
    });

    const summary = runner.finishSuccess({ workspaceId, tileId, afterClose, afterSelect });
    console.log('[RealAppHarness] Tile-only workspace select+render passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
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
