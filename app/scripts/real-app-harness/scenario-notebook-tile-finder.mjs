#!/usr/bin/env node

// Notebook tile finder, in the packaged app. Dock a notebook tile with the REAL
// native ⌘⌥N keystroke (so the macOS-menu → WebView shortcut path is exercised,
// not a synthetic DOM dispatch), confirm the tile's fuzzy finder auto-opens and
// renders in the WKWebView, then prove the native ⌘P re-summon works after the
// finder is dismissed — the one path browser e2e cannot cover (Playwright never
// hits the native menu, and jsdom never lays out the overlay).
//
// The sequence is ordered so a focus regression fails loudly: Esc must actually
// CLOSE the finder (proving focus was inside the tile, not stolen by the terminal
// on dock), and the follow-up ⌘P must REOPEN it (proving the close restored focus
// into the tile so the tile-scoped keydown still fires).

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
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
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

// The finder overlay is tile-scoped DOM, so capture_screenshot_data(selector)
// resolves when it is mounted and throws ("selector not found") when it is not —
// a clean presence probe that doubles as a screenshot of the overlay subtree.
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
    printCommonHelp('scripts/real-app-harness/scenario-notebook-tile-finder.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'notebook-tile-finder');
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
    const cwd = path.join(sessionDir, 'finder-ws');
    fs.mkdirSync(cwd, { recursive: true });
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd,
      label: `notebook-finder-${runId}`,
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

    // 2. Dock a notebook tile via the REAL native ⌘⌥N (notebook.openTile). This is
    //    the macOS-menu → WebView shortcut path the browser e2e can't reach. Native
    //    key delivery (CGEvents) requires the controlling process to hold macOS
    //    Accessibility permission AND attn to be frontmost — like every native-input
    //    scenario here. Without it the keystroke is silently dropped, so surface that
    //    as a clear cause rather than a mystery "tile never appeared" timeout.
    await driver.activateApp();
    await driver.pressKey('n', { command: true, option: true });

    let docked;
    try {
      docked = await waitForWorkspaceUi(
        client,
        workspaceId,
        (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
          && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Notebook'),
        'native Cmd+Opt+N to dock a fresh notebook tile (titled "Notebook")',
        15_000,
      );
    } catch (dockError) {
      const frontmost = await driver.frontmostBundleId().catch(() => '(unknown)');
      throw new Error(
        `${dockError.message}\n\nThe native Cmd+Opt+N did not reach the app. This scenario `
        + `needs native keyboard input: grant Accessibility permission to the process running it `
        + `and keep attn frontmost. Frontmost app was "${frontmost}" (expected "${driver.bundleId}").`,
      );
    }
    console.log(`[RealAppHarness] docked notebook tile=${docked.tileIds[0]}`);

    // 3. A fresh tile opens straight into the finder — confirm it actually rendered
    //    in the WKWebView (not just that the tile mounted).
    await waitForFinder(client, true, 'fresh notebook tile auto-opens its finder');
    await captureFrontWindowScreenshot(path.join(runDir, 'finder-open.png'), { client }).catch((error) => {
      console.warn(`[RealAppHarness] finder-open screenshot failed: ${error}`);
    });

    // 4. Esc must CLOSE the finder. If it doesn't, focus was not inside the tile
    //    (e.g. the terminal stole it on dock) — a real bug, surfaced here.
    await driver.activateApp();
    await driver.pressKey('Escape');
    await waitForFinder(client, false, 'Esc dismisses the finder');

    // 5. Native ⌘P must RE-SUMMON it. This proves both that ⌘P reaches the WebView
    //    (no native Print item swallows it) and that closing the finder restored
    //    focus into the tile so the tile-scoped keydown still fires.
    await driver.activateApp();
    await driver.pressKey('p', { command: true });
    await waitForFinder(client, true, 'native Cmd+P re-summons the finder after Esc');
    await captureFrontWindowScreenshot(path.join(runDir, 'finder-resummoned.png'), { client }).catch((error) => {
      console.warn(`[RealAppHarness] finder-resummoned screenshot failed: ${error}`);
    });

    const summary = {
      ok: true,
      runId,
      workspaceId,
      tileId: docked.tileIds[0],
      tileTitles: docked.tileTitles,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Notebook tile finder (native Cmd+Opt+N dock, Cmd+P re-summon) passed.');
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
