#!/usr/bin/env node

/**
 * Real-app scenario: render a real diff in the packaged app's review panel.
 *
 * Builds a throwaway git repo with a controlled branch diff (see
 * diffFixtureRepo.mjs), points a session at it, opens the diff-detail dock
 * panel, and asserts — via the `diff_get_state` automation action — that the
 * @pierre/diffs viewer actually rendered real, highlighted diff lines inside
 * the WKWebView. Captures screenshots as artifacts.
 */
import fs from 'node:fs';
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
import { buildDiffFixtureRepo } from './diffFixtureRepo.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-diff-review.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'diff-review');
  const { repoDir } = buildDiffFixtureRepo(sessionDir);
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] fixtureRepo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `diff-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId });

    // Open the diff-detail dock panel. It needs an active git-repo session;
    // once open it fetches the branch diff and auto-selects the first file.
    await client.request('dispatch_shortcut', { shortcutId: 'dock.diffDetail' });

    const state = await pollFor(
      async () => {
        const s = await client.request('diff_get_state');
        const ready = s.panelOpen && s.fileCount > 0 && s.diffViewPresent && s.renderedLineCount > 0;
        return ready ? s : null;
      },
      'diff-detail panel to render a real diff',
      40_000,
    );

    // Best-effort: the harness may park the window offscreen, which breaks
    // region capture. The pixel-free diff_get_state assertions below are the
    // authoritative signal; a screenshot is just a nice-to-have artifact.
    const screenshotPath = path.join(runDir, 'diff-unified.png');
    let screenshotCaptured = false;
    try {
      await client.request('capture_native_window_screenshot', { path: screenshotPath });
      screenshotCaptured = true;
    } catch (error) {
      console.warn(`[RealAppHarness] Screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
    }

    // Verify the controlled fixture produced the diff we expect.
    const statuses = new Set(state.files.map((f) => f.status));
    const names = state.files.map((f) => f.name);
    assert(state.panelOpen, 'diff-detail panel is open');
    assert(state.diffViewPresent, 'DiffView (diffs-container) mounted');
    assert(state.renderedLineCount > 0, `rendered diff lines present (got ${state.renderedLineCount})`);
    assert(state.selectedFile.length > 0, 'a file is auto-selected');
    assert(state.layout.includes('Unified'), `default layout is Unified (got ${JSON.stringify(state.layout)})`);
    assert(statuses.has('modified'), `has a modified file (statuses=${[...statuses].join(',')})`);
    assert(statuses.has('added'), `has an added file (statuses=${[...statuses].join(',')})`);
    assert(statuses.has('deleted'), `has a deleted file (statuses=${[...statuses].join(',')})`);

    const summary = {
      ok: true,
      runId,
      sessionId,
      fixtureRepo: repoDir,
      fileCount: state.fileCount,
      files: names,
      statuses: [...statuses],
      selectedFile: state.selectedFile,
      renderedLineCount: state.renderedLineCount,
      commentThreadCount: state.commentThreadCount,
      layout: state.layout,
      screenshot: screenshotCaptured ? screenshotPath : null,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Diff review scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
