#!/usr/bin/env node

/**
 * Real-app scenario: the full "Present" loop in the packaged app.
 *
 * An agent (via the attn CLI, `attn present --wait`) opens a presentation from a
 * manifest -> a notice chip appears in the main window -> clicking the chip
 * opens the second Tauri window (titled "attn — present") -> a reviewer
 * submits a round over the real daemon socket -> the same blocking CLI call
 * returns the feedback to the authoring agent.
 *
 * The present window itself carries NO automation bridge, so loop mechanics
 * are asserted via the MAIN-window bridge (present_get_state /
 * present_click_chip), the real daemon socket (presentDaemon.mjs), and native
 * window enumeration (macosDriver.mjs's waitForWindowTitled), plus a
 * best-effort screenshot artifact. Diff-pixel rendering inside the present
 * window is covered by unit tests and manual cold-boot verification, not
 * here — do not try to read the present window's DOM.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { buildPresentFixtureRepo } from './presentFixtureRepo.mjs';
import { getPresentations, getPresentationRound, submitPresentationRound } from './presentDaemon.mjs';
import { currentHarnessProfile, defaultDaemonPortForProfile, socketPathForProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
// The em dash (U+2014) is load-bearing: it must match the native window title
// set in app/src-tauri/src/lib.rs exactly, or waitForWindowTitled never matches.
const PRESENT_WINDOW_TITLE = 'attn — present';
const REVIEWER_COMMENT = 'Reviewer note from the present-flow scenario.';

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

function resolveAttnBin() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(HARNESS_DIR, '../../../attn')].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error('attn binary not found (build ./attn or set ATTN_HARNESS_BIN)');
}

function startWaitingPresent(attnBin, profile, { cwd, sessionId }) {
  const child = spawn(attnBin, ['present', '--wait', '--json'], {
    cwd,
    env: {
      ...process.env,
      ATTN_PROFILE: profile,
      ATTN_SOCKET_PATH: socketPathForProfile(profile),
      ATTN_SESSION_ID: sessionId,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });

  const completion = new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('close', (code, signal) => {
      if (code !== 0) {
        reject(new Error(`attn present --wait exited with code ${code} signal ${signal}: ${stderr}`));
        return;
      }
      const brace = stdout.indexOf('{');
      resolve({ stdout, stderr, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null });
    });
  });
  // A later scenario step awaits the original promise. Attach a rejection
  // observer now as well so cleanup after an earlier failure cannot produce an
  // unhandled rejection while the harness is unwinding.
  completion.catch(() => {});

  return { completion, kill: () => child.kill('SIGTERM') };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-present-flow.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'PRESENT-FLOW',
    prefix: 'present-flow',
    metadata: { focus: 'waiting CLI -> chip -> present window -> submit round -> synchronous feedback' },
  });

  const profile = currentHarnessProfile();
  const port = defaultDaemonPortForProfile(profile);
  const attnBin = resolveAttnBin();

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });

  let sessionId = null;
  let presentationId = null;
  let waitingPresent = null;

  try {
    const { repoDir, baseSha, headSha, notedPath } = await runner.step('build_fixture', async () => {
      const fixture = buildPresentFixtureRepo(runner.sessionDir);
      runner.log('fixture_built', { repoDir: fixture.repoDir, baseSha: fixture.baseSha, headSha: fixture.headSha });
      return fixture;
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session', async () => {
      const id = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `present-${runner.runId}`,
        agent: 'shell',
        waitForInitialPaneVisible: false,
        sessionWaitMs: 30_000,
      });
      await client.request('select_session', { sessionId: id });
      return id;
    });
    runner.registerCleanup('close_session', () => client.request('close_session', { sessionId }));

    presentationId = await runner.step('open_presentation_and_wait', async () => {
      const existingIds = new Set((await getPresentations({ port })).map((p) => p.id));
      waitingPresent = startWaitingPresent(attnBin, profile, { cwd: repoDir, sessionId });
      runner.registerCleanup('stop_waiting_present', () => waitingPresent?.kill());
      const opened = await Promise.race([
        pollFor(async () => {
          const presentations = await getPresentations({ port });
          return presentations.find((p) => !existingIds.has(p.id) && p.session_id === sessionId) || null;
        }, 'attn present --wait to open a presentation', 30_000),
        waitingPresent.completion.then(({ stdout, stderr }) => {
          throw new Error(`attn present --wait returned before review: stdout=${stdout} stderr=${stderr}`);
        }),
      ]);
      const id = opened.id;
      const presentations = await getPresentations({ port });
      runner.assert(
        presentations.some((p) => p.id === id),
        'daemon get_presentations includes the opened presentation',
        { id, presentations },
      );
      return id;
    });

    await runner.step('assert_chip', async () => {
      const state = await pollFor(
        async () => {
          const s = await client.request('present_get_state');
          const notice = (s.notices || []).find((n) => n.id === presentationId);
          const chip = (s.chips || []).find((c) => c.presentationId === presentationId);
          return notice && chip ? { notice, chip } : null;
        },
        `present_get_state to include notice+chip for presentation ${presentationId}`,
        30_000,
      );
      runner.assert(
        state.notice.title === 'Present flow smoke',
        `notice title matches the manifest (got ${JSON.stringify(state.notice.title)})`,
      );
      runner.assert(
        state.chip.title === 'Present flow smoke',
        `chip title matches the manifest (got ${JSON.stringify(state.chip.title)})`,
      );
    });

    const presentWindow = await runner.step('open_present_window', async () => {
      await client.request('present_click_chip', { presentationId });
      const win = await driver.waitForWindowTitled(PRESENT_WINDOW_TITLE, { timeoutMs: 15_000 });
      runner.assert(Boolean(win), `present window "${PRESENT_WINDOW_TITLE}" opened`);
      runner.log('present_window_bounds', win);
      try {
        await client.request('capture_native_window_screenshot', {
          path: path.join(runner.runDir, 'present-window.png'),
        });
      } catch (error) {
        runner.log('present_window_screenshot_skipped', {
          error: error instanceof Error ? error.message : String(error),
        });
      }
      return win;
    });

    const submittedAt = await runner.step('submit_round', async () => {
      await submitPresentationRound(
        {
          presentationId,
          handback: true,
          verdict: 'feedback',
          comments: [{ filepath: notedPath, line_start: 1, line_end: 1, side: 'new', content: REVIEWER_COMMENT }],
        },
        { port },
      );
      const roundResult = await getPresentationRound(presentationId, { port });
      runner.assert(
        Boolean(roundResult.round?.submitted_at),
        'round has a submitted_at timestamp after submit',
        roundResult.round,
      );
      const comments = roundResult.comments || [];
      runner.assert(
        comments.some((c) => c.content === REVIEWER_COMMENT && c.side === 'new'),
        'submitted round comments include the reviewer note on side=new',
        comments,
      );
      return roundResult.round.submitted_at;
    });

    await runner.step('waiting_cli_receives_feedback', async () => {
      const { json, stderr } = await waitingPresent.completion;
      waitingPresent = null;
      runner.assert(
        stderr.includes('waiting for review of round'),
        'attn present --wait reported that it was synchronously waiting',
        { stderr },
      );
      runner.assert(json && typeof json.markdown === 'string', 'attn present --wait returned a markdown field', json);
      runner.assert(json.markdown.includes(REVIEWER_COMMENT), 'waiting CLI feedback includes the reviewer note', json.markdown);
    });

    const summary = runner.finishSuccess({ sessionId, presentationId, window: presentWindow, submittedAt, baseSha, headSha });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionId, presentationId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    waitingPresent?.kill();
    if (sessionId) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
