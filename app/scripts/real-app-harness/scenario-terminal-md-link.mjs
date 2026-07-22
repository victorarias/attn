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

  const runner = createScenarioRunner(options, {
    scenarioId: 'TERMINAL-MD-LINK',
    tier: 'tier1-local-shell',
    prefix: 'terminal-md-link',
    metadata: {
      focus: 'markdown-path Cmd+click docks/reuses a tile bound to the pane session',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, wsUrl: options.wsUrl });

  // Markdown fixtures live in the session cwd so short relative paths
  // (`./alpha.md`) resolve against the pane's cwd without line wrapping.
  fs.writeFileSync(path.join(runner.sessionDir, 'alpha.md'), '# Alpha Doc\n\nHello from **alpha**.\n\n- one\n- two\n', 'utf8');
  fs.writeFileSync(path.join(runner.sessionDir, 'beta.md'), '# Beta Doc\n\nHello from *beta*.\n', 'utf8');

  // Cleanup, registered as soon as each resource type exists so a signal
  // mid-scenario still tears them down. Runner cleanups run in REVERSE
  // registration order, so register observer/app first (they must close
  // LAST) and the session-panes sweep last (it must close FIRST) to
  // reproduce the effective order below: close panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_session_panes', async () => {
    if (!sessionId) return;
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    for (const pane of workspace?.panes || []) {
      await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    }
  });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    let pane;
    let workspaceId;
    await runner.step('create_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `md-link-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId });
      const workspace = await client.request('get_workspace', { sessionId });
      pane = workspace?.panes?.[0];
      runner.assert(Boolean(pane), `No pane in workspace: ${JSON.stringify(workspace)}`);
      workspaceId = workspace.workspaceId;
      runner.assert(Boolean(workspaceId), `No workspaceId on workspace: ${JSON.stringify(workspace)}`);
      await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
      await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
      await waitForPaneShellReady(client, sessionId, pane.paneId, {
        timeoutMs: 20_000,
        description: 'shell pane ready',
      });
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

    await runner.step('echo_paths', async () => {
      await echoPath('./alpha.md');
      await echoPath('./beta.md');
      await driver.activateApp();
    });

    await runner.step('plain_click_stays_selection', async () => {
      // Plain click must stay selection: no markdown tile appears.
      const plainTarget = await clickTargetFor('./alpha.md');
      await driver.clickWindow(plainTarget.relativeX, plainTarget.relativeY);
      await delay(1_500);
      const tilesAfterPlainClick = await markdownTileIds();
      runner.assert(
        tilesAfterPlainClick.length === 0,
        `Plain click must not open a markdown tile, but found: ${JSON.stringify(tilesAfterPlainClick)}`,
      );
    });

    let alphaTileId;
    let alphaNode;
    await runner.step('cmd_click_alpha_docks_tile', async () => {
      // Cmd+click alpha docks the first markdown tile.
      await cmdClickPath('./alpha.md');
      const tilesAfterAlpha = await waitForMarkdownTileCount(1, 'alpha markdown tile docked');
      alphaTileId = tilesAfterAlpha[0];
      runner.assert(
        /^tile-markdown-[0-9a-f]{16}$/.test(alphaTileId),
        `Unexpected markdown tile id: ${alphaTileId}`,
      );

      // The daemon layout must record the tile bound to the pane's session.
      const alphaTiles = await pollUntil(
        async () => {
          const tiles = collectMarkdownTiles(observer.workspacesBySessionId.get(sessionId));
          return { ok: tiles.length === 1, value: tiles };
        },
        'daemon layout carries the alpha markdown tile',
        15_000,
      );
      alphaNode = alphaTiles[0];
      runner.assert(
        Boolean(alphaNode.tile_params?.endsWith('/alpha.md')),
        `Alpha tile params should end with /alpha.md: ${JSON.stringify(alphaNode)}`,
      );
      runner.assert(
        alphaNode.tile_session_id === sessionId,
        `Alpha tile must be bound to session ${sessionId}: ${JSON.stringify(alphaNode)}`,
      );
    });

    let betaTileId;
    let betaNode;
    await runner.step('cmd_click_beta_docks_second_tile', async () => {
      // Cmd+click beta docks a SECOND, distinct markdown tile.
      await cmdClickPath('./beta.md');
      const tilesAfterBeta = await waitForMarkdownTileCount(2, 'beta markdown tile docked alongside alpha');
      runner.assert(
        tilesAfterBeta.includes(alphaTileId),
        `Alpha tile disappeared after opening beta: ${JSON.stringify(tilesAfterBeta)}`,
      );
      betaTileId = tilesAfterBeta.find((id) => id !== alphaTileId);
      const betaTiles = await pollUntil(
        async () => {
          const tiles = collectMarkdownTiles(observer.workspacesBySessionId.get(sessionId));
          return { ok: tiles.length === 2, value: tiles };
        },
        'daemon layout carries both markdown tiles',
        15_000,
      );
      betaNode = betaTiles.find((node) => node.tile_id === betaTileId);
      runner.assert(
        Boolean(betaNode?.tile_params?.endsWith('/beta.md')),
        `Beta tile params should end with /beta.md: ${JSON.stringify(betaTiles)}`,
      );
      runner.assert(
        betaNode.tile_session_id === sessionId,
        `Beta tile must be bound to session ${sessionId}: ${JSON.stringify(betaNode)}`,
      );
    });

    await runner.step('cmd_click_alpha_again_reuses_tile', async () => {
      // Cmd+click alpha again REUSES the existing tile: still exactly two
      // tiles, same ids as before.
      await cmdClickPath('./alpha.md');
      await delay(2_000);
      const tilesAfterReuse = await markdownTileIds();
      runner.assert(
        tilesAfterReuse.length === 2 && tilesAfterReuse.includes(alphaTileId) && tilesAfterReuse.includes(betaTileId),
        `Re-cmd+click alpha must reuse its tile (expected exactly [${alphaTileId}, ${betaTileId}]), got: ${JSON.stringify(tilesAfterReuse)}`,
      );

      await client.request('capture_native_window_screenshot', {
        path: path.join(runner.runDir, 'success-window.png'),
      }).catch(() => {});
    });

    const result = runner.finishSuccess({
      sessionId,
      workspaceId,
      paneId: pane.paneId,
      alphaTileId,
      betaTileId,
      alphaTileParams: alphaNode.tile_params,
      betaTileParams: betaNode.tile_params,
      tileSessionId: alphaNode.tile_session_id,
    });
    console.log('[verify] PASS — terminal markdown-link: docked, second tile, and reuse on re-click all matched.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'md-link-failure', sessionId).catch(() => {});
    }
    const result = runner.finishFailure(error, { sessionId });
    console.error(result.error);
    process.exitCode = 1;
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
