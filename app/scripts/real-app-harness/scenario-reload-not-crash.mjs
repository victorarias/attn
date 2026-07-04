#!/usr/bin/env node

/**
 * Real-app scenario: reloading an agent is a lifecycle transition, not a crash.
 *
 * Regression coverage for the 2026-07-04 incident: reloading a delegated agent
 * (session actions -> Reload = frontend ptyKill + ptySpawn of the same id) made
 * the daemon treat the worker exit as a mid-flight death — the bound ticket was
 * stamped Crashed and a reconcile classifier task was enqueued against a healthy
 * session. The fix threads reload:true through kill_session so handlePTYExit
 * skips exactly the ticket seam for that one exit.
 *
 * The scenario proves BOTH sides of the seam in the packaged app:
 *
 *   1. boot a real codex agent, bind a ticket to it (`ticket take` + status),
 *   2. put it mid-turn (state=working) and reload it via the real UI path
 *      (reload_session bridge action -> reloadSession -> kill_session reload:true),
 *      then assert: ticket NOT crashed, no reconcile task minted, agent respawned,
 *   3. boot a SECOND agent (claude) with its own ticket, put it mid-turn, and
 *      SIGKILL its pty-worker for real, then assert: that ticket IS crashed and
 *      a reconcile task exists — the reload carve-out did not widen into a
 *      crash-detection hole, and the first session's reload mark did not leak
 *      across sessions. (A separate session because the resumed codex
 *      conversation cannot run another real turn in this environment — the
 *      crash seam itself is per-exit and identical for first or respawned
 *      workers.)
 *
 * Prereqs: `codex` and `claude` on PATH; a built `./attn` (or
 * ATTN_HARNESS_BIN); a non-prod profile install with the automation layer
 * (defaults to the dev sibling).
 */
import fs from 'node:fs';
import os from 'node:os';
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
import { ensureClaudePromptReadyViaPty, ensureCodexPromptReadyViaPty, preTrustClaudeFolder } from './scenarioAgents.mjs';
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

async function pollFor(fn, description, timeoutMs = 30_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
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

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile },
    }).trim();
    // The profile banner goes to stderr, so --json stdout is pure JSON
    // (object or array depending on the command).
    const json = stdout.startsWith('{') || stdout.startsWith('[') ? JSON.parse(stdout) : null;
    return { stdout, json };
  };
}

// The bound ticket's current status, from the CLI's list (authoritative store
// read; no dependency on broadcast timing).
function ticketStatus(runAttn, ticketId) {
  const { json } = runAttn(['ticket', 'list', '--all', '--json']);
  const tickets = Array.isArray(json) ? json : [];
  const ticket = tickets.find((t) => t.id === ticketId);
  return ticket?.status || null;
}

// Reconcile tasks minted for the ticket, straight from the profile DB's durable
// task table (TaskID = "reconcile:<ticketId>").
function reconcileTaskCount(profile, ticketId) {
  const dbPath = path.join(os.homedir(), `.attn-${profile}`, 'attn.db');
  const out = execFileSync('sqlite3', [dbPath, `SELECT COUNT(*) FROM tasks WHERE kind='reconcile' AND subject='${ticketId}';`], { encoding: 'utf8' });
  return Number(out.trim());
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

// Submit a prompt that keeps the agent busy for a while, and wait until the
// daemon says the session is working — the crash seam only differs from the
// clean-rest path for mid-flight states. Retries the submit: right after a
// reload the resumed codex TUI can look prompt-ready (stale replayed pane
// text) while still swallowing input.
async function driveAgentToWorking(client, observer, sessionId, note) {
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${sessionId}`, 20_000);
  const prompt = 'Count from 1 to 40, one number per line, then say done. Do not use tools.';
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: prompt, submit: false });
    await delay(1_200);
    await client.request('write_pane', { sessionId, paneId: pane.paneId, text: '\r', submit: false });
    try {
      await pollFor(
        () => (observer.getSession(sessionId)?.state === 'working' ? true : null),
        `${sessionId} to start working (attempt ${attempt})`,
        12_000,
        300,
      );
      note(`agent is mid-turn (working) after attempt ${attempt}`);
      return;
    } catch (error) {
      if (attempt === 3) throw error;
      note(`submit attempt ${attempt} did not reach working; retrying`);
      await delay(3_000);
    }
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-reload-not-crash.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the reload-not-crash scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, 'reload-not-crash');

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
  let crashSessionId = null;
  const evidence = { runId, profile, steps: [] };
  const note = (m, extra) => { console.log(`[reload-not-crash] ${m}`); evidence.steps.push({ t: Date.now(), m, ...(extra || {}) }); };
  const saveEvidence = (verdict) => {
    evidence.verdict = verdict;
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(evidence, null, 2)}\n`, 'utf8');
  };

  console.log(`[reload-not-crash] profile=${profile} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    // 1) Boot a real codex agent and bind a ticket to it.
    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `reload-target-${runId.slice(-6)}`,
      agent: 'codex',
      sessionWaitMs: 30_000,
      promptReadyFn: ensureCodexPromptReadyViaPty,
      promptReadyTimeoutMs: 90_000,
    });
    await client.request('select_session', { sessionId });
    note('target agent ready', { sessionId });

    const created = runAttn(['ticket', 'new', '--title', `Reload fixture ${runId.slice(-6)}`, '--session', sessionId, '--json']);
    const ticketId = created.json?.ticket_id;
    assert(typeof ticketId === 'string' && ticketId.length > 0, `ticket new returned an id (got ${JSON.stringify(created.json)})`);
    runAttn(['ticket', 'take', ticketId, '--session', sessionId, '--confirm']);
    // Take assigns but does not change the column; report working like a real
    // agent would — the incident's ticket was in Working when the reload hit.
    runAttn(['ticket', 'status', 'in_progress', '--session', sessionId, '--comment', 'harness: starting work']);
    const statusAfterTake = ticketStatus(runAttn, ticketId);
    assert(statusAfterTake === 'working', `ticket bound and working after take (got ${statusAfterTake})`);
    note('ticket bound to agent', { ticketId, status: statusAfterTake });

    // 2) Reload mid-turn: the ticket must stay put and no reconcile task minted.
    await driveAgentToWorking(client, observer, sessionId, note);
    await client.request('reload_session', { sessionId }, { timeoutMs: 45_000 });
    note('reload_session completed');
    await pollFor(() => (workerPid(sessionId) ? true : null), 'respawned pty-worker after reload', 20_000, 300);
    // Give any (buggy) crash/reconcile write a moment to land before asserting.
    await delay(2_000);

    const statusAfterReload = ticketStatus(runAttn, ticketId);
    assert(statusAfterReload === 'working', `ticket unchanged after reload (got ${statusAfterReload})`);
    const tasksAfterReload = reconcileTaskCount(profile, ticketId);
    assert(tasksAfterReload === 0, `no reconcile task after reload (got ${tasksAfterReload})`);
    note('reload left the ticket alone', { statusAfterReload, tasksAfterReload });

    // 3) Real crash, on a fresh second session: SIGKILL its worker mid-turn.
    //    The crash stamp and the reconcile enqueue must both still fire.
    //    Claude here, not codex: a killed claude fires no Stop hook, so the
    //    daemon still sees the session mid-flight when the worker death lands
    //    (codex turns in this environment can end instantly and settle idle
    //    before crash detection runs, hiding the seam under test).
    preTrustClaudeFolder(repoDir);
    crashSessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `crash-target-${runId.slice(-6)}`,
      agent: 'claude',
      sessionWaitMs: 30_000,
      promptReadyFn: ensureClaudePromptReadyViaPty,
      promptReadyTimeoutMs: 90_000,
    });
    await client.request('select_session', { sessionId: crashSessionId });
    const crashCreated = runAttn(['ticket', 'new', '--title', `Crash fixture ${runId.slice(-6)}`, '--session', crashSessionId, '--json']);
    const crashTicketId = crashCreated.json?.ticket_id;
    assert(crashTicketId, 'crash-leg ticket created');
    runAttn(['ticket', 'take', crashTicketId, '--session', crashSessionId, '--confirm']);
    runAttn(['ticket', 'status', 'in_progress', '--session', crashSessionId, '--comment', 'harness: starting work']);
    note('crash-leg ticket bound', { crashTicketId });

    await driveAgentToWorking(client, observer, crashSessionId, note);
    const pid = workerPid(crashSessionId);
    assert(pid, `found pty-worker pid for ${crashSessionId}`);
    process.kill(pid, 'SIGKILL');
    note('killed pty-worker', { pid });

    // A SIGKILLed worker does NOT produce an immediate PTY exit: the daemon's
    // worker-backend poller retries the dead socket and only forces the exit
    // after "unreachable for 30s". The crash stamp lands after that window, so
    // poll well past it.
    const crashed = await pollFor(
      () => (ticketStatus(runAttn, crashTicketId) === 'crashed' ? true : null),
      `ticket ${crashTicketId} to be stamped crashed after a real worker death`,
      90_000,
      500,
    );
    assert(crashed, 'ticket crashed after real kill');
    const tasksAfterCrash = reconcileTaskCount(profile, crashTicketId);
    assert(tasksAfterCrash === 1, `reconcile task minted for the real crash (got ${tasksAfterCrash})`);
    // And the reload-leg ticket must STILL be untouched.
    const reloadTicketFinal = ticketStatus(runAttn, ticketId);
    assert(reloadTicketFinal === 'working', `reload-leg ticket still working at the end (got ${reloadTicketFinal})`);
    note('real crash still detected; reload ticket untouched', { tasksAfterCrash, reloadTicketFinal });

    saveEvidence('pass');
    console.log(`[reload-not-crash] PASS runDir=${runDir}`);
  } catch (error) {
    if (sessionId) {
      try {
        const pane = await waitForFirstWorkspacePane(client, sessionId, 'pane for failure dump', 5_000);
        const text = await client.request('read_pane_text', { sessionId, paneId: pane.paneId });
        evidence.failurePaneText = (text?.text || '').slice(-2000);
        console.error(`[reload-not-crash] pane at failure:\n${evidence.failurePaneText}`);
      } catch { /* best effort */ }
    }
    saveEvidence(`fail: ${error?.message || error}`);
    console.error(`[reload-not-crash] FAIL: ${error?.stack || error}`);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
    if (crashSessionId) await client.request('close_session', { sessionId: crashSessionId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

await main();
