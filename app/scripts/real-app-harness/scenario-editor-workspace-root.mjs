#!/usr/bin/env node

// Editor tile over an arbitrary workspace root, in the packaged app
// (editor-arbitrary-roots epic, PR6). Docks a fresh editor tile with the REAL
// native ⌘⌥N into a workspace whose directory is a throwaway temp folder (not
// the Notebook root), opens a file in it via the tile's finder, and proves two
// things at once:
//   1. Root-scoped fs_read actually works for an arbitrary root — the tile
//      renders the real file content (.cm-content), not an error state.
//   2. Off-root gating (this PR) actually withholds Notebook-only chrome —
//      the backlinks/outline rail never renders for a tile bound to a root
//      other than the Notebook's.
// A second tile, docked into a workspace whose directory IS the Notebook
// root, is the positive control: the same rail CAN render there, proving step
// 2 isolates the off-root case rather than the rail being broken generally.
//
// Modeled closely on scenario-notebook-tile-finder.mjs and
// scenario-notebook-editor-undo.mjs (native ⌘⌥N dock, native finder typing).

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
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
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

// Scope every probe to the ACTIVE workspace: inactive workspaces stay mounted
// but hidden (warm set), so an unscoped selector can match a stale tile left
// open by a previous run's workspace and poison presence waits.
const ACTIVE_TILE = '.terminal-wrapper.active .workspace-dock-tile';
const FINDER_SELECTOR = `${ACTIVE_TILE} .notebook-finder`;
const EDITOR_SELECTOR = `${ACTIVE_TILE} .cm-content`;
const RAIL_SELECTOR = `${ACTIVE_TILE} .notebook-browser-rail`;

// Mirrors internal/notebook/layout.go DefaultRoot: ~/attn-notebook, or
// ~/attn-notebook-<profile> for any non-default profile. This is the notebook
// root ONLY when the daemon's `notebook.root` setting is unset (the default
// for every profile the harness creates fresh) — there is no UI-automation
// surface that reports the resolved root without opening the Settings modal,
// and adding one is out of scope here. If a profile has a custom
// notebook.root configured, the "positive control" workspace below will not
// actually land on the Notebook root and that half of the scenario will time
// out with a clear cause. (Same convention as scenario-notebook-editor-undo.mjs
// and scenario-notebook-link-nav.mjs.)
function defaultNotebookRootForProfile(profile) {
  const normalized = (profile || '').trim().toLowerCase();
  const base = path.join(os.homedir(), 'attn-notebook');
  return normalized === '' || normalized === 'default' ? base : `${base}-${normalized}`;
}

async function domSelectorPresent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector });
    return true;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) {
      return false;
    }
    // The selector resolved but the capture itself failed — html-to-image can
    // throw on some subtrees (e.g. CodeMirror's .cm-content). We asked about
    // presence, and the bridge only emits the "not found" error when the
    // element is genuinely absent, so treat any other failure as present.
    return true;
  }
}

async function waitForDomSelector(client, selector, present, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if ((await domSelectorPresent(client, selector)) === present) {
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Timed out waiting for ${selector} to be ${present ? 'present' : 'absent'}: ${description}`);
}

// Absence-under-race check: an off-root tile withholds the rail because it
// never asks the daemon for backlinks, not because a slow fetch hasn't landed
// yet. Confirm the negative twice, a beat apart, so a race with a fetch that
// SHOULD NOT exist can't slip a false pass through a single premature check.
async function assertNeverAppears(client, selector, description, settleMs = 1_500) {
  await waitForDomSelector(client, selector, false, `${description} (initial)`, 3_000);
  await new Promise((resolve) => setTimeout(resolve, settleMs));
  await waitForDomSelector(client, selector, false, `${description} (after settle)`, 3_000);
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

// Dock a fresh editor tile via the REAL native ⌘⌥N (notebook.openTile) into
// whichever workspace is currently frontmost/active — the macOS-menu →
// WebView shortcut path browser e2e cannot reach. Waits for the tile to
// appear titled "Editor" (the fresh-tile fallback title before anything is
// opened — this PR's Editor-label rename).
async function dockEditorTileNative(client, driver, workspaceId) {
  await driver.activateApp();
  await driver.pressKey('n', { command: true, option: true });
  try {
    return await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
        && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Editor'),
      'native Cmd+Opt+N to dock a fresh editor tile (titled "Editor")',
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
}

// Open a note via the tile's auto-opened finder (a fresh tile with no
// persisted path always opens straight into it): native-type the basename,
// press Enter to pick the top ranked match — the same path a user takes.
async function openNoteViaFinder(client, driver, basename) {
  await waitForDomSelector(client, FINDER_SELECTOR, true, 'fresh editor tile auto-opens its finder');
  await driver.activateApp();
  await driver.typeText(basename);
  await driver.pressEnter();
  await waitForDomSelector(client, FINDER_SELECTOR, false, 'Enter picks the note and closes the finder');
  await waitForDomSelector(client, EDITOR_SELECTOR, true, 'note opens into the live markdown editor');
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

async function openWorkspaceForCwd(client, observer, cwd, label, sessionWaitMs = 30_000) {
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs,
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
  return { sessionId, workspaceId };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-editor-workspace-root.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'editor-workspace-root');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let tempSessionId = null;
  let notebookSessionId = null;
  let positiveControlPath = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    // 0. Seed the off-root fixture BEFORE launch/dock: a README.md at the root
    //    plus a nested dir/note.md, so the tile's first fs_index already sees
    //    both — no race against a fs_watch debounce.
    const tempRoot = path.join(sessionDir, 'editor-root');
    fs.mkdirSync(path.join(tempRoot, 'dir'), { recursive: true });
    fs.writeFileSync(path.join(tempRoot, 'README.md'), '# Editor root fixture\n\nOff-root gating probe.\n', 'utf8');
    fs.writeFileSync(path.join(tempRoot, 'dir', 'note.md'), '# Nested note\n\nUnder dir/.\n', 'utf8');
    console.log(`[RealAppHarness] tempRoot=${tempRoot}`);

    // Seed a positive-control note directly in the Notebook root (a real note
    // among real notes — the same convention scenario-notebook-editor-undo.mjs
    // and scenario-notebook-link-nav.mjs use for this uncontrolled-but-known
    // location), unique to this run so it never collides across runs.
    const notebookRoot = defaultNotebookRootForProfile(currentHarnessProfile());
    fs.mkdirSync(notebookRoot, { recursive: true });
    const positiveControlBasename = `editor-root-positive-control-${runId}`;
    positiveControlPath = path.join(notebookRoot, `${positiveControlBasename}.md`);
    fs.writeFileSync(positiveControlPath, '# Positive control\n\nOn-root rail probe.\n', 'utf8');
    console.log(`[RealAppHarness] notebookRoot=${notebookRoot}`);
    console.log(`[RealAppHarness] positiveControlPath=${positiveControlPath}`);

    // 1. Off-root workspace: a normal shell session whose cwd is the temp root.
    //    ⌘⌥N's default (resolveEditorTileRoot) pins a fresh editor tile to the
    //    ACTIVE workspace's directory whenever it differs from the Notebook
    //    root — so this docks a tile bound to tempRoot, not Notebook storage.
    const off = await openWorkspaceForCwd(client, observer, tempRoot, `editor-root-off-${runId}`);
    tempSessionId = off.sessionId;

    const offRootDocked = await dockEditorTileNative(client, driver, off.workspaceId);
    console.log(`[RealAppHarness] docked off-root editor tile=${offRootDocked.tileIds[0]}`);

    // 2. Open README.md via the tile's auto-opened finder, then assert:
    //    (a) tile title becomes the file basename.
    await openNoteViaFinder(client, driver, 'README');
    await waitForWorkspaceUi(
      client,
      off.workspaceId,
      (state) => Array.isArray(state?.tileTitles) && state.tileTitles.includes('README.md'),
      'tile title becomes the opened file\'s basename',
      10_000,
    );
    await captureFrontWindowScreenshot(path.join(runDir, 'off-root-open.png'), { client }).catch((error) => {
      console.warn(`[RealAppHarness] off-root-open screenshot failed: ${error}`);
    });

    // (b) .cm-content already asserted present by openNoteViaFinder — root-
    //     scoped fs_read for an arbitrary root actually returned real bytes,
    //     not an error state (which would never mount the editor at all).

    // (c) THE off-root-gating assertion: no backlinks/outline rail. It must
    //     never appear at all — this tile was never handed backlinksNotebook,
    //     so there is no fetch in flight that could make it appear late.
    await assertNeverAppears(client, RAIL_SELECTOR, 'off-root tile withholds the backlinks/outline rail');
    console.log('[RealAppHarness] off-root tile: rail withheld as expected.');

    // 3. Positive control: a second workspace whose directory IS the Notebook
    //    root. resolveEditorTileRoot treats "directory === effective notebook
    //    root" as the rootless case, so ⌘⌥N here docks a Notebook-rooted tile
    //    (full capabilities) — the same rail should now render for a markdown
    //    note, proving step (c) isolates off-root rather than the rail being
    //    broken in general.
    const on = await openWorkspaceForCwd(client, observer, notebookRoot, `editor-root-on-${runId}`);
    notebookSessionId = on.sessionId;

    const onRootDocked = await dockEditorTileNative(client, driver, on.workspaceId);
    console.log(`[RealAppHarness] docked on-root (Notebook) editor tile=${onRootDocked.tileIds[0]}`);

    await openNoteViaFinder(client, driver, positiveControlBasename);
    await waitForDomSelector(client, RAIL_SELECTOR, true, 'on-root (Notebook) tile CAN render the backlinks/outline rail', 10_000);
    console.log('[RealAppHarness] on-root (Notebook) tile: rail rendered (positive control).');
    await captureFrontWindowScreenshot(path.join(runDir, 'on-root-rail.png'), { client }).catch((error) => {
      console.warn(`[RealAppHarness] on-root-rail screenshot failed: ${error}`);
    });

    const summary = {
      ok: true,
      runId,
      tempRoot,
      notebookRoot,
      offRoot: {
        workspaceId: off.workspaceId,
        tileId: offRootDocked.tileIds[0],
        tileTitles: offRootDocked.tileTitles,
      },
      onRoot: {
        workspaceId: on.workspaceId,
        tileId: onRootDocked.tileIds[0],
      },
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Editor tile over an arbitrary workspace root (off-root gating + positive control) passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (tempSessionId) {
      await closeWorkspacePanes(client, tempSessionId).catch(() => {});
    }
    if (notebookSessionId) {
      await closeWorkspacePanes(client, notebookSessionId).catch(() => {});
    }
    if (positiveControlPath) {
      fs.rmSync(positiveControlPath, { force: true });
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
