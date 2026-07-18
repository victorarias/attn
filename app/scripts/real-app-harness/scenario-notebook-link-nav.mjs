#!/usr/bin/env node

// Mod-click markdown link navigation inside a notebook tile, packaged app,
// entirely through the synthetic-DOM bridge (dom_click / dom_type) — no
// MacOSDriver, no native CGEvent input at all. Proves:
//   1. ⌘-click on a bare relative link navigates to the sibling note (tile
//      title flips nav-probe -> bar).
//   2. ⌘-click on a #heading link scrolls the note: a link near the bottom
//      of the doc is virtualized OUT of the CodeMirror DOM before the jump
//      and IN after it (CM only mounts visible lines, so presence proves the
//      scroll landed).
// Modeled on scenario-notebook-editor-undo.mjs, swapping every native driver
// step for a bridge verb.

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
import {
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const FINDER_SELECTOR = '.terminal-wrapper.active .notebook-finder';
const FINDER_INPUT_SELECTOR = '.terminal-wrapper.active .notebook-finder-input';
const FINDER_OPTION_SELECTOR = '.terminal-wrapper.active .notebook-finder-option';
const DOWN_LINK = '.terminal-wrapper.active .cm-md-link[data-href="#down-below"]';
const TOP_LINK = '.terminal-wrapper.active .cm-md-link[data-href="#anchor-top"]';

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

// Mirrors internal/notebook/layout.go DefaultRoot: ~/attn-notebook, or
// ~/attn-notebook-<profile> for any non-default profile.
function defaultNotebookRootForProfile(profile) {
  const normalized = (profile || '').trim().toLowerCase();
  const base = path.join(os.homedir(), 'attn-notebook');
  return normalized === '' || normalized === 'default' ? base : `${base}-${normalized}`;
}

// Selector presence via the screenshot bridge: "not found" error == absent;
// success or any other error (html-to-image chokes inside CM subtrees, which
// still proves presence) == present.
async function domSelectorPresent(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector }, { timeoutMs: 8000 });
    return true;
  } catch (error) {
    return !String(error).includes('Screenshot selector not found in DOM');
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

// Open a note via the finder, entirely through the bridge: type the fuzzy
// query, wait for exactly one result row, mousedown-pick it (the finder picks
// on mousedown, not click — see NotebookFinder.tsx).
async function openNoteViaFinderBridge(client, workspaceId, basename, query) {
  await waitForDomSelector(client, FINDER_SELECTOR, true, `finder open for ${basename}`);
  await client.request('dom_type', { selector: FINDER_INPUT_SELECTOR, text: query });
  await new Promise((resolve) => setTimeout(resolve, 500));
  await waitForDomSelector(client, FINDER_OPTION_SELECTOR, true, `finder shows a result for "${query}"`);
  await client.request('dom_click', { selector: FINDER_OPTION_SELECTOR });
  await waitForWorkspaceUi(
    client,
    workspaceId,
    (state) => state?.tileTitles?.includes(`${basename}.md`),
    `finder opens ${basename}.md (tile title)`,
    15_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-notebook-link-nav.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'notebook-link-nav');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    // 0. Seed three probe notes on disk before anything mounts. Names are
    // chosen so each finder query fuzzy-matches exactly one note: "qnav" is
    // not a subsequence of qbar/qanchor's names and vice versa.
    const notebookRoot = defaultNotebookRootForProfile(currentHarnessProfile());
    fs.mkdirSync(notebookRoot, { recursive: true });
    const NAV = `${runId}qnav`;
    const BAR = `${runId}qbar`;
    const ANCHOR = `${runId}qanchor`;
    const navPath = path.join(notebookRoot, `${NAV}.md`);
    const barPath = path.join(notebookRoot, `${BAR}.md`);
    const anchorPath = path.join(notebookRoot, `${ANCHOR}.md`);
    const filler = Array.from({ length: 80 }, (_, i) => `Filler line ${i + 1}.`).join('\n\n');
    fs.writeFileSync(navPath, `# nav probe\n\n[bar](${BAR}.md)\n`, 'utf8');
    fs.writeFileSync(barPath, `# bar\n\nSibling of nav probe.\n\n[anchor](${ANCHOR}.md)\n`, 'utf8');
    fs.writeFileSync(anchorPath, [
      '# anchor probe',
      '',
      '[down](#down-below)',
      '',
      filler,
      '',
      '## down below',
      '',
      'You made it. [top](#anchor-top)',
      '',
    ].join('\n'), 'utf8');
    console.log(`[RealAppHarness] notebookRoot=${notebookRoot} nav=${NAV}.md bar=${BAR}.md anchor=${ANCHOR}.md`);

    // 1. A normal shell workspace to dock the notebook tile into.
    const cwd = path.join(sessionDir, 'linknav-ws');
    fs.mkdirSync(cwd, { recursive: true });
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd,
      label: `notebook-link-nav-${runId}`,
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

    // 2. Dock a notebook tile via the bridge shortcut dispatcher (no native
    // ⌘⌥N needed) — the fresh tile auto-opens its finder.
    await client.request('dispatch_shortcut', { shortcutId: 'notebook.openTile' });
    const docked = await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => Array.isArray(state?.tileIds) && state.tileIds.length === 1
        && Array.isArray(state?.tileTitles) && state.tileTitles.includes('Notebook'),
      'notebook.openTile docks a fresh notebook tile (titled "Notebook")',
      15_000,
    );
    console.log(`[RealAppHarness] docked notebook tile=${docked.tileIds[0]}`);

    // 3. Open the nav probe via the finder, bridge-only.
    await openNoteViaFinderBridge(client, workspaceId, NAV, 'qnav');
    console.log('[RealAppHarness] STEP 1 OK: nav-probe note open in the notebook tile.');

    // 4. ⌘-click the bare relative link -> navigates to the sibling note.
    await client.request('dom_click', {
      selector: `.terminal-wrapper.active .cm-md-link[data-href="${BAR}.md"]`,
      modifiers: { meta: true },
    });
    await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.tileTitles?.includes(`${BAR}.md`),
      'mod-click relative link navigates nav-probe -> bar',
    );
    console.log('[RealAppHarness] STEP 2 OK: mod-click on relative link navigated to the sibling note.');

    // 5. ⌘-click the second relative link -> navigates to the anchor probe.
    await client.request('dom_click', {
      selector: `.terminal-wrapper.active .cm-md-link[data-href="${ANCHOR}.md"]`,
      modifiers: { meta: true },
    });
    await waitForWorkspaceUi(
      client,
      workspaceId,
      (state) => state?.tileTitles?.includes(`${ANCHOR}.md`),
      'mod-click relative link navigates bar -> anchor',
    );
    console.log('[RealAppHarness] STEP 3 OK: mod-click on relative link navigated bar -> anchor.');

    // 6. Precondition: the bottom-of-doc [top] link must not be mounted yet
    // (CM virtualization), and the [down] link must be.
    await waitForDomSelector(client, DOWN_LINK, true, 'down link rendered');
    if (await domSelectorPresent(client, TOP_LINK)) {
      throw new Error('precondition failed: bottom [top] link already in DOM before the anchor jump');
    }

    // 7. ⌘-click #down-below -> scrolls the note; the [top] link enters the
    // virtualized CM DOM only once the jump lands.
    await client.request('dom_click', { selector: DOWN_LINK, modifiers: { meta: true } });
    await waitForDomSelector(client, TOP_LINK, true, 'mod-click #down-below scrolls the note into view');
    console.log('[RealAppHarness] STEP 4 OK: mod-click on #down-below scrolled the note (bottom-of-doc link now in the CM DOM).');

    const summary = {
      ok: true,
      runId,
      workspaceId,
      tileId: docked.tileIds[0],
      navPath,
      barPath,
      anchorPath,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] PASSED: bridge-only mod-click link navigation + heading jump verified.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    console.error(`[RealAppHarness] FAILED: ${error?.stack || error}`);
    throw error;
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
