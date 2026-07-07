#!/usr/bin/env node

// End-to-end OSC 8 hyperlink Cmd+click in the packaged app:
// real daemon PTY (TERM_PROGRAM=ghostty) -> OSC 8 hyperlink label
// -> plain native click (must stay selection, no navigation)
// -> native Cmd+click -> real GET request lands on a local HTTP server.

import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
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

function startProbeServer() {
  return new Promise((resolve, reject) => {
    const hits = [];
    const server = http.createServer((req, res) => {
      hits.push(req.url);
      res.writeHead(200, { 'content-type': 'text/plain' });
      res.end('ok');
    });
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      resolve({ server, hits, port });
    });
  });
}

function closeServer(server) {
  return new Promise((resolve) => server.close(() => resolve()));
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-osc8-link.mjs');
    return;
  }

  // HID mouse clicks land at absolute screen positions, so the default
  // 20px-visible window park would put every click off-window. Keep the
  // whole window on screen for this scenario.
  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'terminal-osc8-link');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;
  const { server, hits, port } = await startProbeServer();

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);
  console.log(`[RealAppHarness] probe server port=${port}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `osc8-link-${runId}`,
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

    // Live-verify the daemon pins TERM_PROGRAM=ghostty for the PTY, which is
    // what gates OSC 8 hyperlink rendering/opening in the real terminal.
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: 'echo "TP=$TERM_PROGRAM"' });
    await waitForPaneText(
      client,
      sessionId,
      pane.paneId,
      (text) => text.split('\n').some((line) => line.trim() === 'TP=ghostty'),
      'TERM_PROGRAM=ghostty echoed',
      20_000,
    );
    const tpRead = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
    if (!tpRead.text.split('\n').some((line) => line.trim() === 'TP=ghostty')) {
      throw new Error(`TERM_PROGRAM was not pinned to ghostty. Pane text:\n${tpRead.text}`);
    }

    // Print an OSC 8 hyperlink whose visible label carries no URL text, so a
    // successful navigation can only have come from following the escape's
    // URI, not from clicking on visible text that happens to look like a link.
    const label = 'CLICK_ME_LINK';
    const url = `http://127.0.0.1:${port}/osc8-hit`;
    // Build the OSC 8 escape as literal shell source text (backslash-e, not an
    // actual ESC byte): printf itself turns \e into ESC and \\ into a single
    // backslash when it interprets the format string inside the pane's shell.
    const esc = '\\e';
    const st = `${esc}\\\\`; // string terminator: ESC + a single literal backslash
    const printfCommand = `printf '${esc}]8;;${url}${st}${label}${esc}]8;;${st}\\n'`;
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: printfCommand });
    const paneState = await waitForPaneText(
      client,
      sessionId,
      pane.paneId,
      (text) => text.includes(label),
      'OSC 8 link label rendered',
      20_000,
    );
    const read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
    const lines = read.text.split('\n');
    const labelRow = lines.findIndex((line) => line.includes(label));
    if (labelRow < 0) {
      throw new Error(`Link label row disappeared. Pane text:\n${read.text}`);
    }
    const labelCol = lines[labelRow].indexOf(label) + Math.floor(label.length / 2);

    const windowBounds = await client.request('get_window_bounds', {});
    if (!windowBounds?.logicalBounds) {
      throw new Error(`No window bounds: ${JSON.stringify(windowBounds)}`);
    }
    const cellRect = await client.request('get_pane_cell_rect', {
      sessionId,
      paneId: pane.paneId,
      cell: { row: labelRow, col: labelCol },
    });
    const target = windowRelativePoint(
      cellRect.centerX,
      cellRect.centerY,
      windowBounds,
      cellRect.innerWidth,
      cellRect.innerHeight,
    );

    await driver.activateApp();

    // Plain click must stay selection: no navigation, no HTTP hit.
    await driver.clickWindow(target.relativeX, target.relativeY);
    await delay(1_000);
    if (hits.length > 0) {
      throw new Error(`Plain click on the OSC 8 label must not navigate, but the probe server saw: ${JSON.stringify(hits)}`);
    }

    // Cmd+click must open the link: a GET request lands on the probe server.
    await driver.clickWindow(target.relativeX, target.relativeY, { modifiers: { command: true } });
    const deadline = Date.now() + 10_000;
    while (hits.length === 0 && Date.now() < deadline) {
      await delay(250);
    }
    if (hits.length === 0) {
      throw new Error('Cmd+click on the OSC 8 label never reached the probe server.');
    }
    if (!hits.includes('/osc8-hit')) {
      throw new Error(`Probe server received unexpected path(s): ${JSON.stringify(hits)}`);
    }

    const summary = {
      ok: true,
      runId,
      sessionId,
      paneId: pane.paneId,
      label,
      url,
      labelRow,
      labelCol,
      hits,
      paneRows: paneState?.size?.rows ?? null,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Terminal OSC 8 hyperlink Cmd+click passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runDir, 'osc8-link-failure', sessionId).catch(() => {});
    }
    throw error;
  } finally {
    await closeServer(server).catch(() => {});
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
