#!/usr/bin/env node

// End-to-end terminal context menu in the packaged app:
// real daemon PTY -> real fish (OSC 133 block) -> native HID right-click
// -> attn's DOM context menu (the WKWebView menu must be suppressed)
// -> native click on "Copy output" -> real macOS clipboard
// -> native click on "Paste" -> clipboard text lands in the PTY.

import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver, delay } from './macosDriver.mjs';
import {
  captureSessionArtifacts,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneText,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
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

function readClipboard() {
  try {
    return execFileSync('pbpaste', { encoding: 'utf8' });
  } catch {
    return '';
  }
}

function writeClipboard(text) {
  execFileSync('pbcopy', { input: text });
}

function fishAvailable() {
  try {
    execFileSync('/bin/sh', ['-c', 'command -v fish'], { encoding: 'utf8' });
    return true;
  } catch {
    return false;
  }
}

async function waitForClipboard(expected, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let last = '';
  while (Date.now() - startedAt < timeoutMs) {
    last = readClipboard();
    if (last === expected) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`${description}: clipboard never matched.\nExpected: ${JSON.stringify(expected)}\nLast:     ${JSON.stringify(last)}`);
}

// Convert a page-CSS-pixel point into window-relative [0,1] coordinates for
// the HID driver (window bounds include the title bar; the page does not).
function windowRelativePoint(pageX, pageY, windowBounds, innerWidth, innerHeight) {
  const { width, height } = windowBounds.logicalBounds;
  const chromeX = Math.max(0, width - innerWidth);
  const chromeY = Math.max(0, height - innerHeight);
  return {
    relativeX: (chromeX / 2 + pageX) / width,
    relativeY: (chromeY + pageY) / height,
  };
}

async function waitForContextMenu(client, timeoutMs = 8_000) {
  const startedAt = Date.now();
  let state = null;
  while (Date.now() - startedAt < timeoutMs) {
    state = await client.request('get_terminal_context_menu_state', {});
    if (state?.open) {
      return state;
    }
    await delay(250);
  }
  throw new Error(`Context menu never opened. Last state: ${JSON.stringify(state)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-context-menu.mjs');
    return;
  }
  if (!fishAvailable()) {
    throw new Error('fish is required for this scenario (it emits the OSC 133 markers natively).');
  }

  // HID mouse clicks land at absolute screen positions, so the default
  // 20px-visible window park would put every click off-window. Keep the
  // whole window on screen for this scenario.
  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-CONTEXT-MENU',
    tier: 'tier1-local-shell',
    prefix: 'terminal-context-menu',
    metadata: {
      shell: 'fish',
      focus: 'native right-click DOM context menu: copy output and paste',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  const savedClipboard = readClipboard();
  let sessionId = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, wsUrl: options.wsUrl });

  // Cleanup, registered as soon as each resource type exists so a signal
  // mid-scenario still tears them down. Runner cleanups run in REVERSE
  // registration order, so register observer/app first (they must close
  // LAST), then the session-panes sweep, then the clipboard restore (it must
  // close FIRST) to reproduce the effective order below: restore clipboard,
  // close panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const pane of workspace?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    }
  });
  runner.registerCleanup('restore_clipboard', () => writeClipboard(savedClipboard));

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    let pane;
    await runner.step('create_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `context-menu-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      pane = workspace?.panes?.[0];
      runner.assert(Boolean(pane), `No pane in workspace: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell pane ready',
      });

      // The default shell may not be fish; exec fish so the PTY emits OSC 133.
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: 'exec fish' });
      await delay(1_500);
    });

    let token;
    let outputRow;
    let target;
    let windowBounds;
    await runner.step('run_command_and_locate_output', async () => {
      token = `CTXMENU_${runner.runId}`;
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `echo ${token}` });
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.split('\n').some((line) => line.trim() === token),
        'block output row rendered',
        20_000,
      );
      const read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
      outputRow = read.text.split('\n').findIndex((line) => line.trim() === token);
      runner.assert(outputRow >= 0, `Token row disappeared. Pane text:\n${read.text}`);

      windowBounds = await client.request('get_window_bounds', {});
      runner.assert(Boolean(windowBounds?.logicalBounds), `No window bounds: ${JSON.stringify(windowBounds)}`);
      const cellRect = await client.request('get_pane_cell_rect', {
        sessionId,
        paneId: pane.paneId,
        cell: { row: outputRow, col: 2 },
      });

      // Native right-click on the block's output row.
      await driver.activateApp();
      target = windowRelativePoint(
        cellRect.centerX,
        cellRect.centerY,
        windowBounds,
        cellRect.innerWidth,
        cellRect.innerHeight,
      );
    });

    let menuItemsSummary;
    await runner.step('open_menu_and_copy_output', async () => {
      await driver.rightClickWindow(target.relativeX, target.relativeY);

      const menu = await waitForContextMenu(client);
      await driver.screenshot(path.join(runner.runDir, 'context-menu-open.png'));
      const itemById = new Map(menu.items.map((item) => [item.id, item]));
      for (const required of ['copy', 'copy-command', 'copy-output', 'paste']) {
        runner.assert(itemById.has(required), `Menu is missing "${required}". Items: ${JSON.stringify(menu.items)}`);
      }
      for (const blockItem of ['copy-command', 'copy-output']) {
        runner.assert(
          !itemById.get(blockItem).disabled,
          `"${blockItem}" should be enabled on a block. Items: ${JSON.stringify(menu.items)}`,
        );
      }

      // Copy output through the menu -> real clipboard.
      writeClipboard('context-menu-sentinel');
      const copyOutput = itemById.get('copy-output');
      const copyPoint = windowRelativePoint(
        copyOutput.centerX,
        copyOutput.centerY,
        windowBounds,
        menu.innerWidth,
        menu.innerHeight,
      );
      await driver.clickWindow(copyPoint.relativeX, copyPoint.relativeY);
      await waitForClipboard(token, 'menu Copy output copies the block output');
      menuItemsSummary = menu.items.map((item) => ({ id: item.id, disabled: item.disabled }));
    });

    let pasteToken;
    await runner.step('open_menu_and_paste', async () => {
      // Paste through the menu -> clipboard text lands in the PTY (this also
      // proves the clipboard READ permission works in the packaged app).
      pasteToken = `PASTEPROBE_${runner.runId}`;
      writeClipboard(pasteToken);
      await driver.rightClickWindow(target.relativeX, target.relativeY);
      const menuAgain = await waitForContextMenu(client);
      const paste = menuAgain.items.find((item) => item.id === 'paste');
      runner.assert(Boolean(paste) && !paste.disabled, `Paste unavailable: ${JSON.stringify(menuAgain.items)}`);
      const pastePoint = windowRelativePoint(
        paste.centerX,
        paste.centerY,
        windowBounds,
        menuAgain.innerWidth,
        menuAgain.innerHeight,
      );
      await driver.clickWindow(pastePoint.relativeX, pastePoint.relativeY);
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.includes(pasteToken),
        'pasted clipboard text reaches the PTY',
        10_000,
      );
    });

    const result = runner.finishSuccess({
      sessionId,
      paneId: pane.paneId,
      token,
      pasteToken,
      outputRow,
      menuItems: menuItemsSummary,
    });
    console.log('[verify] PASS — terminal context menu: copy output and paste both matched real macOS clipboard.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'context-menu-failure', sessionId).catch(() => {});
    }
    const result = runner.finishFailure(error, { sessionId });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    writeClipboard(savedClipboard);
    if (sessionId) {
      const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
      for (const pane of workspace?.panes || []) {
        await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
