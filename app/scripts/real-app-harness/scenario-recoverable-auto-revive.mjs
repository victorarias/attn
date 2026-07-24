#!/usr/bin/env node

/**
 * Real-app scenario: a recoverable session auto-revives when reopened after a
 * daemon (machine) restart, instead of dead-ending on the attach banner.
 *
 * Repro of the reported bug: after a computer restart, reopening attn showed
 * recoverable sessions stuck on "[Failed to attach PTY: Error: session not
 * found: <id>]" — the user had to click Reload on each one. The daemon marks
 * such sessions' state recoverable on startup but spawns no worker (lazy revive);
 * the pane-mount attach path never invoked the resume-respawn that the reload
 * button runs, so it just printed the banner.
 *
 * The scenario reproduces the exact conditions in the packaged app:
 *
 *   1. boot a real claude agent (its worker is alive),
 *   2. simulate a machine restart: quit the app, stop the daemon, SIGKILL the
 *      pty-worker, then start a fresh daemon. Startup recovery finds the dead
 *      worker and marks the session state recoverable (no worker running),
 *   3. wait until the daemon reports state=recoverable (so the reopened app's
 *      attach lands AFTER recovery and returns "session not found", matching a
 *      human-paced relaunch — attach/spawn are blocked mid-recovery),
 *   4. reopen the app and assert: a new pty-worker respawns for the session,
 *      the recoverable state clears (worker adopted), and the pane does NOT show
 *      the "Failed to attach PTY" banner.
 *
 * Prereqs: `claude` on PATH; a non-prod profile install of THIS branch
 * (make install PROFILE=<name>); run with ATTN_PROFILE / ATTN_HARNESS_PROFILE
 * set to that profile. Daemon lifecycle is driven through the app's bundled
 * binary so the restarted daemon matches the app under test.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { ensureClaudePromptReadyViaPty, preTrustClaudeFolder } from './scenarioAgents.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function assert(condition, message) {
  if (!condition) throw new Error(`Assertion failed: ${message}`);
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 300) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

// The app's bundled binary — daemon lifecycle must run through the exact build
// under test, not an unrelated ./attn on PATH.
function resolveAppBin(appPath) {
  const bin = path.join(appPath, 'Contents/MacOS/attn');
  if (!fs.existsSync(bin)) throw new Error(`app binary not found at ${bin}`);
  return bin;
}

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    return execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile },
    }).trim();
  };
}

// The pty-worker process for a session (worker backend runs one `attn pty-worker
// --session-id <id>` per session).
function workerPid(sessionId) {
  try {
    const out = execFileSync('pgrep', ['-f', `pty-worker.*--session-id ${sessionId}`], { encoding: 'utf8' });
    const pids = out.trim().split('\n').filter(Boolean).map(Number);
    return pids[0] || null;
  } catch {
    return null;
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-recoverable-auto-revive.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('this scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const appBin = resolveAppBin(options.appPath);
  const runAttn = makeAttnRunner(appBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, 'recoverable-auto-revive');

  const repoDir = path.join(sessionDir, 'target-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;
  const evidence = { runId, profile, steps: [] };
  const note = (m, extra) => { console.log(`[recoverable-auto-revive] ${m}`); evidence.steps.push({ t: Date.now(), m, ...(extra || {}) }); };
  const saveEvidence = (verdict) => {
    evidence.verdict = verdict;
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  };

  console.log(`[recoverable-auto-revive] profile=${profile} runDir=${runDir} repo=${repoDir}`);

  try {
    // 1) Boot a real claude agent; its worker must be alive.
    await launchFreshAppAndConnect(client, observer);
    preTrustClaudeFolder(repoDir);
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `revive-${runId.slice(-6)}`,
      agent: 'claude',
      sessionWaitMs: 30_000,
      promptReadyFn: ensureClaudePromptReadyViaPty,
      promptReadyTimeoutMs: 90_000,
    });
    await client.request('select_session', { sessionId });
    await pollFor(() => (workerPid(sessionId) ? true : null), 'initial pty-worker alive', 20_000);
    note('claude session ready, worker alive', { sessionId });

    // 2) Simulate a machine restart: quit app, stop daemon, kill the worker,
    //    then start a fresh daemon whose startup recovery marks it recoverable.
    await client.quitApp();
    await observer.close();
    runAttn(['daemon', 'stop']);
    const deadPid = workerPid(sessionId);
    if (deadPid) {
      try { process.kill(deadPid, 'SIGKILL'); } catch { /* already gone */ }
    }
    await pollFor(() => (workerPid(sessionId) ? null : true), 'pty-worker gone after kill', 15_000);
    runAttn(['daemon', 'ensure']);
    note('daemon restarted with worker dead', { killedPid: deadPid });

    // 3) Wait until recovery has marked the session recoverable, with no worker.
    await observer.connect();
    await pollFor(
      () => (observer.getSession(sessionId)?.state === 'recoverable' ? true : null),
      'session state recoverable after restart',
      30_000,
    );
    assert(workerPid(sessionId) === null, 'no pty-worker running after restart');
    note('session recoverable, no worker (machine-restart state reproduced)');

    // 4) Reopen the app (Victor's "re-open attn"): the pane mounts, its attach
    //    returns session-not-found, and the fix auto-revives via resume.
    await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
    await client.request('select_session', { sessionId });
    note('app reopened; awaiting auto-revive');

    const revivedPid = await pollFor(
      () => (workerPid(sessionId) ? workerPid(sessionId) : null),
      'auto-respawned pty-worker after reopen',
      60_000,
    );
    await pollFor(
      () => (s => s && s.state !== 'recoverable')(observer.getSession(sessionId)),
      'recoverable state cleared once the revived worker is adopted',
      30_000,
    );
    note('worker auto-respawned and recoverable cleared', { revivedPid });

    // The pane must not have dead-ended on the attach banner.
    const pane = await waitForFirstWorkspacePane(client, sessionId, 'pane after revive', 20_000);
    // Give replay/redraw a beat to settle before reading.
    await delay(2_000);
    const paneText = (await client.request('read_pane_text', { sessionId, paneId: pane.paneId }))?.text || '';
    evidence.paneTextTail = paneText.slice(-1200);
    assert(!/Failed to attach PTY/.test(paneText), 'pane must NOT show the "Failed to attach PTY" banner after auto-revive');
    note('no attach-failure banner; auto-revive succeeded');

    saveEvidence('pass');
    console.log(`[recoverable-auto-revive] PASS runDir=${runDir}`);
  } catch (error) {
    if (sessionId) {
      try {
        const pane = await waitForFirstWorkspacePane(client, sessionId, 'pane for failure dump', 5_000);
        const text = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
        evidence.failurePaneText = (text?.text || '').slice(-2000);
        console.error(`[recoverable-auto-revive] pane at failure:\n${evidence.failurePaneText}`);
      } catch { /* best effort */ }
    }
    saveEvidence(`fail: ${error?.message || error}`);
    console.error(`[recoverable-auto-revive] FAIL: ${error?.stack || error}`);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

await main();
