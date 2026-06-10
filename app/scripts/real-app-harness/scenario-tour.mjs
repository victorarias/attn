#!/usr/bin/env node

/**
 * Real-app scenario: start an interactive Tour through the packaged CLI and
 * verify the native fullscreen Tour renders a real guide and branch diff.
 */
import { execFile, spawn } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { buildDiffFixtureRepo } from './diffFixtureRepo.mjs';
import {
  profileForAppPath,
  socketPathForProfile,
} from './harnessProfile.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

async function pollFor(fn, description, timeoutMs = 40_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

function waitForTourReady(child, timeoutMs = 30_000) {
  return new Promise((resolve, reject) => {
    let stdout = '';
    let stderr = '';
    const timeout = setTimeout(() => {
      cleanup();
      reject(new Error(`Timed out waiting for TOUR_READY. stdout=${stdout} stderr=${stderr}`));
    }, timeoutMs);
    const cleanup = () => {
      clearTimeout(timeout);
      child.stdout?.off('data', onStdout);
      child.stderr?.off('data', onStderr);
      child.off('exit', onExit);
    };
    const onStdout = (chunk) => {
      stdout += chunk.toString();
      const line = stdout.split('\n').find((entry) => entry.startsWith('TOUR_READY '));
      if (!line) return;
      cleanup();
      resolve(JSON.parse(line.slice('TOUR_READY '.length)));
    };
    const onStderr = (chunk) => {
      stderr += chunk.toString();
    };
    const onExit = (code, signal) => {
      cleanup();
      reject(new Error(`Tour listener exited before ready: code=${code} signal=${signal} stderr=${stderr}`));
    };
    child.stdout?.on('data', onStdout);
    child.stderr?.on('data', onStderr);
    child.on('exit', onExit);
  });
}

function waitForTourEvent(child, prefix, timeoutMs = 30_000) {
  return new Promise((resolve, reject) => {
    let stdout = '';
    let stderr = '';
    const timeout = setTimeout(() => {
      cleanup();
      reject(new Error(`Timed out waiting for ${prefix}. stdout=${stdout} stderr=${stderr}`));
    }, timeoutMs);
    const cleanup = () => {
      clearTimeout(timeout);
      child.stdout?.off('data', onStdout);
      child.stderr?.off('data', onStderr);
      child.off('exit', onExit);
    };
    const onStdout = (chunk) => {
      stdout += chunk.toString();
      const line = stdout.split('\n').find((entry) => entry.startsWith(prefix));
      if (!line) return;
      cleanup();
      resolve(line);
    };
    const onStderr = (chunk) => {
      stderr += chunk.toString();
    };
    const onExit = (code, signal) => {
      cleanup();
      reject(new Error(`Tour listener exited before ${prefix}: code=${code} signal=${signal} stderr=${stderr}`));
    };
    child.stdout?.on('data', onStdout);
    child.stderr?.on('data', onStderr);
    child.on('exit', onExit);
  });
}

async function captureTourScreenshot(client, screenshotPath) {
  try {
    await client.request('capture_native_window_screenshot', { path: screenshotPath });
    return screenshotPath;
  } catch (error) {
    console.warn(`[RealAppHarness] Native screenshot unavailable: ${error instanceof Error ? error.message : String(error)}`);
    try {
      const screenshot = await client.request('capture_screenshot_data');
      fs.writeFileSync(screenshotPath, Buffer.from(screenshot.pngBase64, 'base64'));
      return screenshotPath;
    } catch (fallbackError) {
      console.warn(`[RealAppHarness] DOM screenshot skipped: ${fallbackError instanceof Error ? fallbackError.message : String(fallbackError)}`);
      return null;
    }
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tour.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'tour');
  const { repoDir } = buildDiffFixtureRepo(sessionDir);
  fs.writeFileSync(
    path.join(repoDir, 'src/app.ts'),
    [
      'export function main(name: string): string {',
      '  return `Hello, ${name}!`;',
      '}',
      '',
      ...Array.from(
        { length: 180 },
        (_, index) => `export const tour_value_${String(index).padStart(3, '0')} = ${index};`,
      ),
      '',
    ].join('\n'),
    'utf8',
  );
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const profile = profileForAppPath(options.appPath);
  const binaryPath = path.join(options.appPath, 'Contents', 'MacOS', 'attn');
  const cliEnv = {
    ...process.env,
    ATTN_PROFILE: profile,
    ATTN_SOCKET_PATH: socketPathForProfile(profile),
  };
  let sessionId = null;
  let otherSessionId = null;
  let listener = null;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] fixtureRepo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `tour-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId });

    const { stdout: guideOutput } = await execFileAsync(binaryPath, [
      'tour',
      'create',
      '--session',
      sessionId,
      '--repo',
      repoDir,
      '--name',
      'real-app-tour',
    ], { env: cliEnv });
    const guidePath = guideOutput.trim();
    fs.writeFileSync(guidePath, `version: 1

summary: |
  # Packaged-app Tour

  This briefing verifies **rich Markdown**, stable diagrams, and the native diff reader.

  > Read the system from intent to implementation.

  1. Start with the application entry point.
  2. Follow the data into the table module.

  | Surface | Purpose |
  | --- | --- |
  | Guide | Explain the change |
  | Diff | Review the implementation |

  \`\`\`mermaid
  flowchart LR
    Guide --> Reader
    Reader --> Questions
    Questions --> Agent
  \`\`\`

chapters:
  - title: Entry point
    summary: |
      Establish the user-visible flow before reading support code.
    files:
      - path: src/app.ts
        view: diff
        note: |
          Start with the application entry point.
        risk: |
          Verify the new call remains compatible with the existing startup path.
        annotations:
          - anchor: "export function main("
            note: "This is the behavior under review."

skip:
  - data/table.ts
`, 'utf8');

    listener = spawn(binaryPath, [
      'tour',
      'start',
      '--session',
      sessionId,
      '--guide',
      guidePath,
      '--name',
      'Real App Tour',
      '--base',
      'origin/main',
    ], {
      env: cliEnv,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const ready = await waitForTourReady(listener);
    const { stdout: changedFilesOutput } = await execFileAsync('git', [
      '-C',
      repoDir,
      'diff',
      '--name-only',
      'origin/main',
    ]);
    const expectedChangedFileCount = changedFilesOutput.trim().split('\n').filter(Boolean).length;

    const state = await pollFor(
      async () => {
        const current = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
        return current.panelOpen && current.briefingOpen && current.renderedLineCount > 0 && current.mermaidCount > 0 ? current : null;
      },
      'fullscreen Tour to render the first-open briefing and real diff',
    );

    assert(ready.connection_state === 'connected', `listener connected (got ${ready.connection_state})`);
    assert(state.title === 'Real App Tour', `Tour title rendered (got ${JSON.stringify(state.title)})`);
    assert(state.connectionText === 'Agent listening', `listener state rendered (got ${JSON.stringify(state.connectionText)})`);
    assert(state.summaryText.includes('Packaged-app Tour'), 'guide summary rendered');
    assert(state.mermaidCount === 1, `Mermaid diagram rendered once (got ${state.mermaidCount})`);
    assert(state.briefingFontSize >= 16, `briefing uses a readable font size (got ${state.briefingFontSize}px)`);
    assert(
      state.diagramViewportBounds?.height >= 350,
      `diagram has a substantial zoom viewport (got ${state.diagramViewportBounds?.height}px)`,
    );
    assert(state.diagramZoomText === '100%', `diagram starts at 100% zoom (got ${JSON.stringify(state.diagramZoomText)})`);
    assert(state.reviewSubmitVisible, 'review submission is visible while conversation is closed');
    assert(state.files.some((file) => file.path === 'src/app.ts' && file.selected), 'curated file is selected');
    assert(
      state.totalFileCount === expectedChangedFileCount,
      `coverage ledger counts all ${expectedChangedFileCount} changed files (got ${state.totalFileCount})`,
    );
    assert(state.skippedFileCount === 1, `coverage ledger counts the skipped file (got ${state.skippedFileCount})`);
    assert(state.selectedFile === 'src/app.ts', `selected file is src/app.ts (got ${JSON.stringify(state.selectedFile)})`);
    assert(state.diffViewPresent, 'Tour DiffView mounted');
    assert(state.renderedLineCount > 0, `rendered diff lines present (got ${state.renderedLineCount})`);
    assert(!state.conversationOpen, 'conversation is hidden by default');
    assert(state.errorText === '', `no Tour panel error (got ${JSON.stringify(state.errorText)})`);
    assert(
      state.panelBounds?.width >= state.viewportWidth - 1 && state.panelBounds?.height >= state.viewportHeight - 1,
      `Tour fills the viewport (got ${state.panelBounds?.width}x${state.panelBounds?.height}/${state.viewportWidth}x${state.viewportHeight})`,
    );

    const briefingScreenshotPath = await captureTourScreenshot(client, path.join(runDir, 'tour-briefing.png'));
    const zoomedState = await client.request('tour_zoom_diagram');
    assert(zoomedState.diagramZoomText === '125%', `diagram zoom control works (got ${JSON.stringify(zoomedState.diagramZoomText)})`);

    await client.request('tour_close_briefing');
    const readingState = await pollFor(
      async () => {
        const current = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
        return !current.briefingOpen && current.renderedLineCount > 0 ? current : null;
      },
      'Tour briefing to close onto the reading workspace',
    );
    assert(readingState.selectedFile === 'src/app.ts', 'reading workspace keeps the selected file');
    assert(
      readingState.diffScrollRange > 500,
      `Tour diff owns a substantial scroll range (got ${readingState.diffScrollRange})`,
    );
    assert(
      readingState.mainScrollRange <= 1,
      `outer reading column does not compete for diff scrolling (got ${readingState.mainScrollRange})`,
    );

    await client.request('tour_toggle_conversation');
    const conversationState = await pollFor(
      async () => {
        const current = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
        return current.conversationOpen ? current : null;
      },
      'Tour conversation drawer to open on demand',
    );
    assert(conversationState.conversationText.includes('No questions yet.'), 'empty conversation state rendered on demand');

    const pendingNote = 'Please verify the packaged pending-note path.';
    const scrolledState = await client.request('tour_scroll_diff', { top: 900 });
    assert(scrolledState.diffScrollTop > 500, `Tour diff can scroll deeply (got ${scrolledState.diffScrollTop})`);
    const typedState = await client.request('tour_type_file_note', { note: pendingNote });
    assert(
      typedState.scrollSamples.every((top) => Math.abs(top - scrolledState.diffScrollTop) <= 1),
      `typing feedback keeps the packaged diff anchored (got ${JSON.stringify(typedState.scrollSamples)})`,
    );
    const feedbackReady = waitForTourEvent(listener, 'FEEDBACK_READY ');
    await client.request('tour_send_review');
    const feedbackLine = await feedbackReady;
    const feedbackEvent = JSON.parse(feedbackLine.slice('FEEDBACK_READY '.length));
    assert(
      feedbackEvent.markdown.includes(pendingNote),
      `listener receives the pending file note (got ${JSON.stringify(feedbackEvent.markdown)})`,
    );
    const { stdout: fetchedEventOutput } = await execFileAsync(binaryPath, [
      'tour',
      'event',
      '--tour',
      ready.tour_id,
      '--event',
      feedbackEvent.id,
    ], { env: cliEnv });
    const fetchedEvent = JSON.parse(fetchedEventOutput);
    assert(fetchedEvent.id === feedbackEvent.id, 'stored Tour event can be fetched by id');
    assert(
      fetchedEvent.markdown === feedbackEvent.markdown,
      'stored Tour event preserves the submitted review feedback',
    );
    const submittedState = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
    assert(submittedState.reviewSubmitText === 'Review sent', `review reaches the listener (got ${JSON.stringify(submittedState.reviewSubmitText)})`);

    await client.request('tour_press_escape');
    const escapedConversation = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
    assert(escapedConversation.panelOpen && !escapedConversation.conversationOpen, 'Escape closes conversation before the Tour');

    const screenshotPath = await captureTourScreenshot(client, path.join(runDir, 'tour.png'));

    await client.request('tour_press_escape');
    await pollFor(
      async () => {
        const current = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
        return !current.panelOpen ? current : null;
      },
      'Escape to dismiss the fullscreen Tour',
    );

    otherSessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `tour-switch-${runId}`,
      agent: 'shell',
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId: otherSessionId });
    await client.request('select_session', { sessionId });
    await new Promise((resolve) => setTimeout(resolve, 1_000));
    const reopenedState = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
    assert(!reopenedState.panelOpen, 'returning to the workspace does not reopen a dismissed Tour');

    const summary = {
      ok: true,
      runId,
      sessionId,
      tourId: ready.tour_id,
      guidePath,
      fileCount: state.fileCount,
      selectedFile: state.selectedFile,
      renderedLineCount: state.renderedLineCount,
      briefingScreenshot: briefingScreenshotPath,
      screenshot: screenshotPath,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Tour scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (listener && listener.exitCode === null) {
      listener.kill('SIGTERM');
    }
    if (sessionId) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    if (otherSessionId) {
      await client.request('close_session', { sessionId: otherSessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
