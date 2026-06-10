#!/usr/bin/env node

// End-to-end command-block copy in the packaged app:
// real daemon PTY -> real fish (native OSC 133 markers) -> block click-select
// -> native Cmd+C / Cmd+Shift+C -> real macOS clipboard.

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createRunContext,
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-block-copy.mjs');
    return;
  }
  if (!fishAvailable()) {
    throw new Error('fish is required for this scenario (it emits the OSC 133 markers natively).');
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'terminal-block-copy');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  const savedClipboard = readClipboard();
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `block-copy-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId });
    const workspace = await client.request('get_workspace', { sessionId });
    const pane = workspace?.panes?.[0];
    if (!pane) {
      throw new Error(`No pane in workspace: ${JSON.stringify(workspace)}`);
    }
    await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
    await waitForPaneShellReady(client, sessionId, pane.paneId, {
      timeoutMs: 20_000,
      description: 'shell pane ready',
    });

    // The default shell may not be fish; exec fish so the PTY emits OSC 133.
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: 'exec fish' });
    await delay(1_500);

    const token = `BLOCKCOPY_${runId}`;
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `echo ${token}` });
    // The block's output row is the line that is exactly the token (the typed
    // command line carries the `echo ` prefix).
    const paneState = await waitForPaneText(
      client,
      sessionId,
      pane.paneId,
      (text) => text.split('\n').some((line) => line.trim() === token),
      'block output row rendered',
      20_000,
    );
    const read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
    const outputRow = read.text.split('\n').findIndex((line) => line.trim() === token);
    if (outputRow < 0) {
      throw new Error(`Token row disappeared. Pane text:\n${read.text}`);
    }

    // Plain click on the output row selects the surrounding command block.
    await client.request('click_pane_cell', {
      sessionId,
      paneId: pane.paneId,
      cell: { row: outputRow, col: 2 },
    });
    await client.request('focus_pane', { sessionId, paneId: pane.paneId });

    await driver.activateApp();
    writeClipboard('block-copy-sentinel');
    await driver.pressKey('c', { command: true, shift: true });
    await waitForClipboard(`echo ${token}`, 'Cmd+Shift+C copies the command');

    writeClipboard('block-copy-sentinel');
    await driver.pressKey('c', { command: true });
    await waitForClipboard(`echo ${token}\n${token}`, 'Cmd+C copies command+output');

    const summary = {
      ok: true,
      runId,
      sessionId,
      paneId: pane.paneId,
      token,
      outputRow,
      paneRows: paneState?.size?.rows ?? null,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Terminal block copy passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runDir, 'block-copy-failure', sessionId).catch(() => {});
    }
    throw error;
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
