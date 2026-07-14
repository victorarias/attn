#!/usr/bin/env node

// End-to-end markdown-link Cmd+click in the packaged app:
// real daemon PTY -> plain `./alpha.md` path text in a shell pane
// -> plain native click (must stay selection, no tile)
// -> native Cmd+click -> markdown tile docks into the workspace,
//    bound to the pane's session (tile_session_id in the daemon layout)
// -> Cmd+click a second file docks a SECOND tile
// -> Cmd+click the first file again REUSES its tile (no duplicate).

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
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

async function pollUntil(probe, description, timeoutMs = 15_000, intervalMs = 250) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  for (;;) {
    last = await probe();
    if (last.ok) return last.value;
    if (Date.now() >= deadline) {
      throw new Error(`Timed out waiting for ${description}. Last: ${JSON.stringify(last.value)}`);
    }
    await delay(intervalMs);
  }
}

function collectMarkdownTiles(layout) {
  if (!layout?.layout_json) return [];
  let root;
  try {
    root = JSON.parse(layout.layout_json);
  } catch {
    return [];
  }
  const tiles = [];
  const walk = (node) => {
    if (!node || typeof node !== 'object') return;
    if (node.tile_kind === 'markdown') tiles.push(node);
    for (const child of node.children || []) walk(child);
  };
  walk(root);
  return tiles;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-terminal-md-link.mjs');
    return;
  }

  // HID mouse clicks land at absolute screen positions, so the default
  // 20px-visible window park would put every click off-window. Keep the
  // whole window on screen for this scenario.
  if (process.env.ATTN_HARNESS_PARK_VISIBLE_PX === undefined) {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX = '800';
  }

  // Path-link detection checks file existence through Tauri's fs scope, which
  // only allows $HOME/** AND does not match dot-directories — the default
  // tmpdir session root (or anything under a hidden dir) would make every
  // detected path fail the existence check and never become a link.
  if (!process.env.ATTN_REAL_APP_SESSION_ROOT && !process.argv.includes('--session-root-dir')) {
    options.sessionRootDir = path.join(os.homedir(), 'Library', 'Caches', 'attn-harness', 'real-app-sessions');
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'terminal-md-link');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  // Markdown fixtures live in the session cwd so short relative paths
  // (`./alpha.md`) resolve against the pane's cwd without line wrapping.
  fs.writeFileSync(path.join(sessionDir, 'alpha.md'), '# Alpha Doc\n\nHello from **alpha**.\n\n- one\n- two\n', 'utf8');
  fs.writeFileSync(path.join(sessionDir, 'beta.md'), '# Beta Doc\n\nHello from *beta*.\n', 'utf8');

  try {
    await launchFreshAppAndConnect(client, observer);

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `md-link-${runId}`,
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
    const workspaceId = workspace.workspaceId;
    if (!workspaceId) {
      throw new Error(`No workspaceId on workspace: ${JSON.stringify(workspace)}`);
    }
    await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
    await waitForPaneShellReady(client, sessionId, pane.paneId, {
      timeoutMs: 20_000,
      description: 'shell pane ready',
    });

    // `echo ./alpha.md` prints the bare relative path on its own line; the
    // exact-trim match below skips the echoed command line (which carries the
    // `echo ` prefix), so clicks always land on plain output text.
    const echoPath = async (relPath) => {
      await client.request('write_pane', { sessionId, paneId: pane.paneId, text: `echo ${relPath}` });
      await waitForPaneText(
        client,
        sessionId,
        pane.paneId,
        (text) => text.split('\n').some((line) => line.trim() === relPath),
        `${relPath} echoed as plain output`,
        20_000,
      );
    };

    // Re-derive the click point fresh each time: docking a tile resizes the
    // pane, which reflows text and invalidates any cached row/col geometry.
    const clickTargetFor = async (relPath) => {
      const read = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
      const lines = read.text.split('\n');
      const row = lines.findIndex((line) => line.trim() === relPath);
      if (row < 0) {
        throw new Error(`Path line for ${relPath} not found. Pane text:\n${read.text}`);
      }
      const col = lines[row].indexOf(relPath) + Math.floor(relPath.length / 2);
      const windowBounds = await client.request('get_window_bounds', {});
      if (!windowBounds?.logicalBounds) {
        throw new Error(`No window bounds: ${JSON.stringify(windowBounds)}`);
      }
      const cellRect = await client.request('get_pane_cell_rect', {
        sessionId,
        paneId: pane.paneId,
        cell: { row, col },
      });
      return windowRelativePoint(
        cellRect.centerX,
        cellRect.centerY,
        windowBounds,
        cellRect.innerWidth,
        cellRect.innerHeight,
      );
    };

    const markdownTileIds = async () => {
      const state = await client.request('get_workspace_ui_state', { workspaceId });
      return (state.tileIds || []).filter((id) => id.startsWith('tile-markdown'));
    };

    const waitForMarkdownTileCount = (expected, description) => pollUntil(
      async () => {
        const ids = await markdownTileIds();
        return { ok: ids.length === expected, value: ids };
      },
      description,
      15_000,
    );

    // Path detection is hover-lazy with an async existence check; a plain
    // click both asserts "no navigation" AND warms the detection cache at the
    // exact click point before the Cmd+click that must act on it.
    const cmdClickPath = async (relPath) => {
      const target = await clickTargetFor(relPath);
      await driver.clickWindow(target.relativeX, target.relativeY);
      await delay(750);
      await driver.clickWindow(target.relativeX, target.relativeY, { modifiers: { command: true } });
    };

    await echoPath('./alpha.md');
    await echoPath('./beta.md');

    await driver.activateApp();

    // Plain click must stay selection: no markdown tile appears.
    const plainTarget = await clickTargetFor('./alpha.md');
    await driver.clickWindow(plainTarget.relativeX, plainTarget.relativeY);
    await delay(1_500);
    const tilesAfterPlainClick = await markdownTileIds();
    if (tilesAfterPlainClick.length !== 0) {
      throw new Error(`Plain click must not open a markdown tile, but found: ${JSON.stringify(tilesAfterPlainClick)}`);
    }

    // Cmd+click alpha docks the first markdown tile.
    await cmdClickPath('./alpha.md');
    const tilesAfterAlpha = await waitForMarkdownTileCount(1, 'alpha markdown tile docked');
    const alphaTileId = tilesAfterAlpha[0];
    if (!/^tile-markdown-[0-9a-f]{16}$/.test(alphaTileId)) {
      throw new Error(`Unexpected markdown tile id: ${alphaTileId}`);
    }

    // The daemon layout must record the tile bound to the pane's session.
    const alphaTiles = await pollUntil(
      async () => {
        const tiles = collectMarkdownTiles(observer.workspacesBySessionId.get(sessionId));
        return { ok: tiles.length === 1, value: tiles };
      },
      'daemon layout carries the alpha markdown tile',
      15_000,
    );
    const alphaNode = alphaTiles[0];
    if (!alphaNode.tile_params?.endsWith('/alpha.md')) {
      throw new Error(`Alpha tile params should end with /alpha.md: ${JSON.stringify(alphaNode)}`);
    }
    if (alphaNode.tile_session_id !== sessionId) {
      throw new Error(`Alpha tile must be bound to session ${sessionId}: ${JSON.stringify(alphaNode)}`);
    }

    // Cmd+click beta docks a SECOND, distinct markdown tile.
    await cmdClickPath('./beta.md');
    const tilesAfterBeta = await waitForMarkdownTileCount(2, 'beta markdown tile docked alongside alpha');
    if (!tilesAfterBeta.includes(alphaTileId)) {
      throw new Error(`Alpha tile disappeared after opening beta: ${JSON.stringify(tilesAfterBeta)}`);
    }
    const betaTileId = tilesAfterBeta.find((id) => id !== alphaTileId);
    const betaTiles = await pollUntil(
      async () => {
        const tiles = collectMarkdownTiles(observer.workspacesBySessionId.get(sessionId));
        return { ok: tiles.length === 2, value: tiles };
      },
      'daemon layout carries both markdown tiles',
      15_000,
    );
    const betaNode = betaTiles.find((node) => node.tile_id === betaTileId);
    if (!betaNode?.tile_params?.endsWith('/beta.md')) {
      throw new Error(`Beta tile params should end with /beta.md: ${JSON.stringify(betaTiles)}`);
    }
    if (betaNode.tile_session_id !== sessionId) {
      throw new Error(`Beta tile must be bound to session ${sessionId}: ${JSON.stringify(betaNode)}`);
    }

    // Cmd+click alpha again REUSES the existing tile: still exactly two tiles,
    // same ids as before.
    await cmdClickPath('./alpha.md');
    await delay(2_000);
    const tilesAfterReuse = await markdownTileIds();
    if (tilesAfterReuse.length !== 2 || !tilesAfterReuse.includes(alphaTileId) || !tilesAfterReuse.includes(betaTileId)) {
      throw new Error(
        `Re-cmd+click alpha must reuse its tile (expected exactly [${alphaTileId}, ${betaTileId}]), got: ${JSON.stringify(tilesAfterReuse)}`,
      );
    }

    await client.request('capture_native_window_screenshot', {
      path: path.join(runDir, 'success-window.png'),
    }).catch(() => {});

    const summary = {
      ok: true,
      runId,
      sessionId,
      workspaceId,
      paneId: pane.paneId,
      alphaTileId,
      betaTileId,
      alphaTileParams: alphaNode.tile_params,
      betaTileParams: betaNode.tile_params,
      tileSessionId: alphaNode.tile_session_id,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Terminal markdown-link Cmd+click passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runDir, 'md-link-failure', sessionId).catch(() => {});
    }
    throw error;
  } finally {
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
