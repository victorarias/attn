#!/usr/bin/env node

// Native ⌘Z/⇧⌘Z inside a notebook tile's editor, in the packaged app. AGENTS.md
// "Critical Pattern 8": a macOS native menu accelerator claims a key equivalent
// before it ever reaches the WebView. This PR removes the predefined Edit > Undo
// item in app/src-tauri/src/lib.rs `app_menu()` (Redo was already removed) so ⌘Z
// reaches CodeMirror's history keymap inside the notebook editor instead of being
// swallowed. Browser e2e cannot cover this: Playwright delivers ⌘Z as a plain DOM
// keydown, never through the native menu, so a regression here would pass e2e and
// unit tests while being silently dead in the installed app (the exact failure
// mode "toggleZoom was dead" hit for ⇧⌘Z before that fix).
//
// The assertion is disk-based, not DOM-based: type a probe string into a real note
// via native keystrokes, poll the file on disk for the autosaved (debounced) probe
// text, then native ⌘Z until the file is back to its original content, then native
// ⇧⌘Z until the probe text reappears. That proves both keys actually reached
// CodeMirror's undo/redo, not just that "something" happened in the DOM.

import fs from 'node:fs';
import path from 'node:path';
import {
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
import { createScenarioRunner } from './scenarioRunner.mjs';

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

// Scope probes to the ACTIVE workspace: inactive workspaces stay mounted but
// hidden (warm set), so an unscoped selector can match a stale finder/editor
// left open by a previous run's workspace and poison presence waits.
const FINDER_SELECTOR = '.terminal-wrapper.active .notebook-finder';
const EDITOR_SELECTOR = '.terminal-wrapper.active .cm-content';
const ORIGINAL_CONTENT = '# Undo Probe\n\nThis paragraph exists before the probe types anything.\n';
const PROBE_SUFFIX = ' UNDOPROBE';

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

// Autosave is debounced 700ms (NotebookSurface AUTOSAVE_DELAY_MS); poll the file
// on disk rather than guessing a sleep duration.
async function waitForFileContent(filePath, predicate, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    try {
      last = fs.readFileSync(filePath, 'utf8');
    } catch (error) {
      last = `<read error: ${error instanceof Error ? error.message : String(error)}>`;
    }
    if (typeof last === 'string' && predicate(last)) {
      return last;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`Timed out waiting for ${description}. Last file content:\n${last}`);
}

// Press a key up to `maxPresses` times, polling disk after each press, and
// return as soon as the file matches `predicate`. CodeMirror's history groups
// adjacent fast typing into one undo step, so one press is the common case, but
// press count is not guaranteed, so retry rather than asserting an exact count.
async function pressUntilFileMatches(driver, filePath, key, modifiers, predicate, description, maxPresses = 5) {
  for (let attempt = 1; attempt <= maxPresses; attempt += 1) {
    await driver.activateApp();
    await driver.pressKey(key, modifiers);
    try {
      return await waitForFileContent(filePath, predicate, `${description} (press ${attempt}/${maxPresses})`, 3_000);
    } catch {
      // Not there yet; try another press.
    }
  }
  const finalContent = fs.readFileSync(filePath, 'utf8');
  throw new Error(`${description}: still not satisfied after ${maxPresses} presses of ${key}. Final content:\n${finalContent}`);
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
    printCommonHelp('scripts/real-app-harness/scenario-notebook-editor-undo.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'NOTEBOOK-EDITOR-UNDO',
    tier: 'tier1-local-shell',
    prefix: 'notebook-editor-undo',
    metadata: {
      agent: 'shell',
      focus: 'native Cmd+Z / Shift+Cmd+Z inside a notebook tile editor',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  let sessionId = null;
  let probeFilePath = null;

  runner.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  // Cleanup, registered as soon as each resource exists so a signal mid-scenario
  // still tears them down. Runner cleanups run in REVERSE registration order, so
  // register observer/app first (they must close LAST).
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('remove_probe_file', () => {
    if (probeFilePath) {
      fs.rmSync(probeFilePath, { force: true });
    }
  });

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    // 0. Seed a note in the session's working directory before the app launches — ⌘⌥N roots
    //    the editor tile at the active workspace's directory (handleOpenNotebookTile/
    //    resolveEditorTileRoot), so that directory is where the tile's finder looks.
    const { cwd, probeBasename } = await runner.step('seed_probe_note', async () => {
      const dir = path.join(runner.sessionDir, 'undo-ws');
      fs.mkdirSync(dir, { recursive: true });
      const basename = `undo-probe-${Date.now().toString(36)}`;
      probeFilePath = path.join(dir, `${basename}.md`);
      fs.writeFileSync(probeFilePath, ORIGINAL_CONTENT, 'utf8');
      runner.log(`[RealAppHarness] probeFilePath=${probeFilePath}`);
      return { cwd: dir, probeBasename: basename };
    });

    // 1. A normal shell workspace to dock the notebook tile into.
    const { workspaceId } = await runner.step('create_shell_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `notebook-undo-${runner.runId}`,
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

      const workspace = await client.request('get_workspace', { sessionId });
      const id = workspace.workspaceId;
      if (!id) {
        throw new Error(`Could not resolve workspace id for session ${sessionId}: ${JSON.stringify(workspace)}`);
      }
      return { workspaceId: id };
    });

    // 2. Dock a notebook tile via the REAL native ⌘⌥N (notebook.openTile) — the
    //    macOS-menu → WebView shortcut path browser e2e can't reach.
    const docked = await runner.step('dock_notebook_tile', async () => {
      await driver.activateApp();
      await driver.pressKey('n', { command: true, option: true });

      let result;
      try {
        // A pathless notebook tile is titled 'Editor' (deriveTileTitle, tilePresentation.ts).
        result = await waitForWorkspaceUi(
          client,
          workspaceId,
          (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
            && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Editor'),
          'native Cmd+Opt+N to dock a fresh notebook tile (titled "Editor")',
          15_000,
        );
      } catch (dockError) {
        const frontmost = await driver.frontmostBundleId().catch(() => '(unknown)');
        throw new Error(
          `${dockError.message}\n\nThis scenario needs native keyboard input: grant Accessibility `
          + `permission to the process running it and keep attn frontmost. Frontmost app was `
          + `"${frontmost}" (expected "${driver.bundleId}").`,
        );
      }
      runner.log(`[RealAppHarness] docked notebook tile=${result.tileIds[0]}`);
      return result;
    });

    // 3. A fresh tile auto-opens its finder. Type the probe note's basename and
    //    press Enter to open it — the same native path a user takes.
    await runner.step('open_probe_note_via_finder', async () => {
      await waitForDomSelector(client, FINDER_SELECTOR, true, 'fresh notebook tile auto-opens its finder');
      await driver.activateApp();
      await driver.typeText(probeBasename);
      // The finder's file index loads async — pressing Enter before the probe row
      // renders is a no-op (pick(undefined)), so wait until a result is listed,
      // exactly like a real user waiting to SEE the match before confirming.
      await waitForDomSelector(
        client,
        '.terminal-wrapper.active .notebook-finder .notebook-finder-option',
        true,
        'finder lists the probe note before Enter',
        15_000,
      );
      await driver.pressEnter();
      await waitForDomSelector(client, FINDER_SELECTOR, false, 'Enter picks the probe note and closes the finder');

      // 4. Confirm the editor actually rendered before trying to type into it.
      await waitForDomSelector(client, EDITOR_SELECTOR, true, 'note opens into the live markdown editor');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'editor-open.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] editor-open screenshot failed: ${error}`);
      });
    });

    // 5. Opening a note via the finder does NOT focus the editor (NotebookSurface
    //    restores focus to whatever had it before the finder opened) — a real user
    //    clicks into the note body before typing, so do the same with a REAL native
    //    click. The docked tile occupies roughly the right quarter of the window,
    //    with the note header card in its upper part, so aim at the note body low
    //    in the tile. CodeMirror adds `.cm-focused` to its root while focused —
    //    verify that landed (retrying once) instead of typing blind into the PTY.
    await runner.step('focus_editor_with_native_click', async () => {
      await driver.activateApp();
      let editorFocused = false;
      for (let attempt = 0; attempt < 2 && !editorFocused; attempt++) {
        await driver.clickWindow(0.85, 0.85);
        try {
          await waitForDomSelector(client, '.terminal-wrapper.active .cm-editor.cm-focused', true, 'native click focuses the CodeMirror editor', 5_000);
          editorFocused = true;
        } catch (error) {
          if (attempt === 1) throw error;
        }
      }
    });

    // 6. Native typing into the editor, then wait for the debounced (700ms)
    //    autosave to land the probe text on disk. This is the proof typing
    //    reached CodeMirror at all, before undo/redo are exercised.
    await runner.step('type_probe_text_and_autosave', async () => {
      await driver.activateApp();
      await driver.typeText(PROBE_SUFFIX);
      await waitForFileContent(
        probeFilePath,
        (content) => content.includes(PROBE_SUFFIX.trim()),
        'autosave to persist the typed probe text',
      );
      runner.log('[RealAppHarness] probe text landed on disk via native typing + autosave.');
    });

    // 7. THE assertion: native ⌘Z must reach CodeMirror's undo, not a swallowed
    //    Edit > Undo menu item. Retry the press (grouped-typing undo is usually
    //    one step, but is not guaranteed) until the file is back to original.
    await runner.step('native_cmdz_undo', async () => {
      await pressUntilFileMatches(
        driver,
        probeFilePath,
        'z',
        { command: true },
        (content) => content === ORIGINAL_CONTENT,
        'native Cmd+Z to undo the probe text back to original content',
      );
      runner.log('[RealAppHarness] native Cmd+Z undid the probe text (menu no longer swallows it).');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'after-undo.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] after-undo screenshot failed: ${error}`);
      });
    });

    // 8. Native ⇧⌘Z must reach CodeMirror's redo, not terminal.toggleZoom (the
    //    exact menu-trap bug pattern AGENTS.md documents for ⇧⌘Z elsewhere).
    await runner.step('native_shift_cmdz_redo', async () => {
      await pressUntilFileMatches(
        driver,
        probeFilePath,
        'z',
        { command: true, shift: true },
        (content) => content.includes(PROBE_SUFFIX.trim()),
        'native Shift+Cmd+Z to redo the probe text',
      );
      runner.log('[RealAppHarness] native Shift+Cmd+Z redid the probe text (menu no longer swallows it).');
      await captureFrontWindowScreenshot(path.join(runner.runDir, 'after-redo.png'), { client }).catch((error) => {
        runner.log(`[RealAppHarness] after-redo screenshot failed: ${error}`);
      });
    });

    // 9. Confirm ⇧⌘Z landed in CodeMirror's redo and NOT terminal.toggleZoom —
    //    the exact confusion this fix disambiguates (see AGENTS.md Critical
    //    Pattern 8, the toggleZoom precedent). get_session_ui_state's
    //    workspace.view.zoomedPaneId is the existing UI-state surface for this;
    //    no new automation surface needed.
    await runner.step('assert_no_pane_zoom', async () => {
      const sessionUiState = await client.request('get_session_ui_state', { sessionId });
      const zoomedPaneId = sessionUiState?.workspace?.view?.zoomedPaneId ?? null;
      runner.assert(
        !zoomedPaneId,
        `Shift+Cmd+Z zoomed pane ${zoomedPaneId} instead of (only) redoing in the editor — `
        + `terminal.toggleZoom fired alongside/instead of CodeMirror redo.`,
        sessionUiState,
      );
    });

    const summary = runner.finishSuccess({
      workspaceId,
      tileId: docked.tileIds[0],
      probeFilePath,
    });
    console.log('[RealAppHarness] Notebook editor native Cmd+Z undo / Shift+Cmd+Z redo passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionId, probeFilePath });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    if (probeFilePath) {
      fs.rmSync(probeFilePath, { force: true });
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
