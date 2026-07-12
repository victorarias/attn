#!/usr/bin/env node

// Cmd+W from inside a docked notebook tile, in the packaged app. In the packaged
// app the native "Close Pane" menu item claims ⌘W and dispatches session.close —
// NOT the DOM terminal.close path browser e2e exercises — so a focus-aware close
// has to live in the session.close handler too. The reported bug: with a notebook
// tile docked beside a terminal, ⌘W while focus is in the tile closed the terminal
// pane (ending that session) instead of the note you were looking at.
//
// This scenario docks a notebook tile with the REAL native ⌘⌥N, dismisses its
// auto-finder so focus rests inside the tile (the "reading a note" state), then
// presses the REAL native ⌘W and asserts the tile is undocked while the terminal
// pane/session survives. Browser e2e can't cover this — Playwright never hits the
// native menu that turns ⌘W into session.close.

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
  waitForFirstWorkspacePane,
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

const FINDER_SELECTOR = '.notebook-finder';

async function finderPresent(client) {
  return client
    .request('capture_screenshot_data', { selector: FINDER_SELECTOR })
    .then(() => true)
    .catch(() => false);
}

async function waitForFinder(client, present, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if ((await finderPresent(client)) === present) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for finder to be ${present ? 'present' : 'absent'}: ${description}`);
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
    printCommonHelp('scripts/real-app-harness/scenario-notebook-tile-close.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'notebook-tile-close');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    // 1. A normal shell workspace to dock the notebook tile into.
    const cwd = path.join(sessionDir, 'close-ws');
    fs.mkdirSync(cwd, { recursive: true });
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd,
      label: `notebook-close-${runId}`,
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
    const terminalPaneId = pane.paneId;

    // 2. Dock a notebook tile via the REAL native ⌘⌥N (notebook.openTile).
    await driver.activateApp();
    await driver.pressKey('n', { command: true, option: true });
    const docked = await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1,
      'native Cmd+Opt+N to dock a fresh notebook tile',
      15_000,
    );
    const tileId = docked.tileIds[0];
    console.log(`[RealAppHarness] docked notebook tile=${tileId}`);

    // 3. A fresh tile auto-opens its finder; dismiss it with Esc so focus rests on
    //    the tile itself (the "reading a note" state) rather than the finder input.
    //    Esc closing the finder also proves focus is inside the tile, not stolen by
    //    the terminal on dock.
    await waitForFinder(client, true, 'fresh notebook tile auto-opens its finder');
    await driver.activateApp();
    await driver.pressKeyCode(53); // Esc — InputDriver's --key map only covers printable keys
    await waitForFinder(client, false, 'Esc dismisses the finder, leaving focus in the tile');

    // 4. The REAL native ⌘W. In the packaged app this fires the native "Close Pane"
    //    menu item → session.close. With focus inside the notebook tile it must
    //    UNDOCK THE TILE, leaving the terminal pane/session untouched.
    await driver.activateApp();
    await driver.pressKey('w', { command: true });

    const afterClose = await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 0,
      'native Cmd+W to undock the focused notebook tile',
      15_000,
    );

    // The terminal pane (and its session) must survive — the whole point of the fix.
    const workspaceAfter = await client.request('get_workspace', { sessionId });
    const panesAfter = workspaceAfter?.panes ?? [];
    const terminalSurvived = panesAfter.some((p) => p.paneId === terminalPaneId);
    if (!terminalSurvived) {
      throw new Error(
        `Cmd+W in the notebook tile closed the terminal pane instead of the tile. `
        + `Expected pane ${terminalPaneId} to survive; panes after = ${JSON.stringify(panesAfter)}`,
      );
    }
    const state = await client.request('get_state');
    const sessionSurvived = (state.sessions || []).some((s) => s.id === sessionId);
    if (!sessionSurvived) {
      throw new Error(`Cmd+W in the notebook tile ended session ${sessionId} instead of closing the tile.`);
    }

    const summary = {
      ok: true,
      runId,
      workspaceId,
      tileId,
      terminalPaneId,
      tileIdsAfter: afterClose.tileIds,
      panesAfter: panesAfter.map((p) => p.paneId),
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Notebook tile Cmd+W close (native menu → session.close → undock tile) passed.');
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
