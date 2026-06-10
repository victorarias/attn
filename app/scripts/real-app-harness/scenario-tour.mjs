#!/usr/bin/env node

/**
 * Real-app scenario: start an interactive Tour through the packaged CLI and
 * verify the native Tour dock renders a real guide and branch diff.
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tour.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'tour');
  const { repoDir } = buildDiffFixtureRepo(sessionDir);
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
  Packaged-app Tour regression coverage.

files:
  - path: src/app.ts
    view: diff
    note: |
      Start with the application entry point.
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

    const state = await pollFor(
      async () => {
        const current = await client.request('tour_get_state', {}, { timeoutMs: 5_000 });
        return current.panelOpen && current.renderedLineCount > 0 ? current : null;
      },
      'Tour dock to render the guide and real diff',
    );

    assert(ready.connection_state === 'connected', `listener connected (got ${ready.connection_state})`);
    assert(state.title === 'Real App Tour', `Tour title rendered (got ${JSON.stringify(state.title)})`);
    assert(state.connectionText === 'Agent listening', `listener state rendered (got ${JSON.stringify(state.connectionText)})`);
    assert(state.summaryText.includes('Packaged-app Tour regression coverage'), 'guide summary rendered');
    assert(state.files.some((file) => file.path === 'src/app.ts' && file.selected), 'curated file is selected');
    assert(state.files.some((file) => file.path === 'data/table.ts'), 'skipped file is present');
    assert(state.selectedFile === 'src/app.ts', `selected file is src/app.ts (got ${JSON.stringify(state.selectedFile)})`);
    assert(state.diffViewPresent, 'Tour DiffView mounted');
    assert(state.renderedLineCount > 0, `rendered diff lines present (got ${state.renderedLineCount})`);
    assert(state.conversationText.includes('No questions yet.'), 'empty conversation state rendered');
    assert(state.errorText === '', `no Tour panel error (got ${JSON.stringify(state.errorText)})`);

    const screenshotPath = path.join(runDir, 'tour.png');
    let screenshotCaptured = false;
    try {
      await client.request('capture_native_window_screenshot', { path: screenshotPath });
      screenshotCaptured = true;
    } catch (error) {
      console.warn(`[RealAppHarness] Screenshot skipped: ${error instanceof Error ? error.message : String(error)}`);
    }

    const summary = {
      ok: true,
      runId,
      sessionId,
      tourId: ready.tour_id,
      guidePath,
      fileCount: state.fileCount,
      selectedFile: state.selectedFile,
      renderedLineCount: state.renderedLineCount,
      screenshot: screenshotCaptured ? screenshotPath : null,
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
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
