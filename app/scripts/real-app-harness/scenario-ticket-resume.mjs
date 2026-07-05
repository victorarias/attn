#!/usr/bin/env node

/**
 * Real-app scenario: resuming a ticket whose bound agent session was CLOSED
 * respawns the agent — the exact bug fixed by the daemon-owned `ticket_resume`
 * command.
 *
 * Repro path, driven end-to-end through the packaged app with no human clicks:
 *   1. Bootstrap a chief-of-staff and delegate a real codex worker (mints a
 *      ticket bound to the worker session, carrying cwd + last_agent_id).
 *   2. Open the TicketDetailPanel the real way (Dashboard "View ticket") and
 *      confirm it offers Resume (button present) while the worker is alive.
 *   3. CLOSE the worker session (close_session → daemon unregister removes the
 *      store row). This is the failing precondition: a ticket whose bound
 *      session no longer exists.
 *   4. Reopen the panel and confirm Resume is STILL offered — the button gates
 *      on ticket metadata (cwd + last_agent_id), which survives the close.
 *   5. Click the real Resume button (ticket_resume automation → the real
 *      frontend onResume → sendTicketResume → daemon `ticket_resume` composite
 *      → register/pane/spawn). The OLD frontend orchestration raced and rolled
 *      back with "Session spawn arguments were not prepared."; the daemon-owned
 *      composite must respawn cleanly.
 *   6. Assert the bound session reappears (respawned under the same id the
 *      ticket points at) and the ticket stays bound — with no error surfaced.
 *
 * Prereqs: `codex` on PATH; a built `./attn` (or ATTN_HARNESS_BIN); a non-prod
 * profile install with the automation layer (`make install PROFILE=<name>` /
 * `make dev`). Single-tenant: never run packaged-app scenarios in parallel.
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

function makeAttnRunner(attnBin, profile) {
  return function runAttn(args) {
    const stdout = execFileSync(attnBin, args, {
      encoding: 'utf8',
      env: { ...process.env, ATTN_PROFILE: profile },
    });
    const brace = stdout.indexOf('{');
    return { stdout, json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
  };
}

async function setChiefOfStaff(client, sessionId) {
  const before = await client.request('chief_of_staff_get_state');
  if (before.sessions.find((session) => session.id === sessionId)?.chiefOfStaff) {
    return;
  }
  await client.request('chief_of_staff_open_actions', { sessionId });
  await client.request('chief_of_staff_toggle');
  const afterToggle = await client.request('chief_of_staff_get_state');
  if (afterToggle.transferPrompt) {
    await client.request('chief_of_staff_confirm_transfer');
  }
  await pollFor(
    async () => {
      const state = await client.request('chief_of_staff_get_state');
      return state.sessions.find((session) => session.id === sessionId)?.chiefOfStaff ? state : null;
    },
    `session ${sessionId} to become chief-of-staff`,
    15_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-ticket-resume.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the ticket resume scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const { runId, runDir, sessionDir } = createRunContext(options, 'ticket-resume');

  // Minimal git repo for the chief's cwd (delegation places the worker here).
  const repoDir = path.join(sessionDir, 'chief-repo');
  fs.mkdirSync(repoDir, { recursive: true });
  execFileSync('git', ['init', '-q'], { cwd: repoDir });
  execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
    cwd: repoDir,
    env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let chiefId = null;
  let workerId = null;
  let resumedId = null;

  console.log(`[RealAppHarness] profile=${profile} runDir=${runDir} repo=${repoDir}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    // 1) Chief session + chief-of-staff role.
    chiefId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: repoDir,
      label: `chief-${runId}`,
      agent: 'shell',
      sessionWaitMs: 30_000,
    });
    await client.request('select_session', { sessionId: chiefId });
    await setChiefOfStaff(client, chiefId);

    // 2) Delegate a real codex worker → mints the bound ticket.
    const workerName = `rsm-${Date.now().toString(36).slice(-8)}`;
    const brief = 'Ticket resume QA fixture. Please wait for direction; do not start coding.';
    const delegate = runAttn(['delegate', '--source-session', chiefId, '--agent', 'codex', '--brief', brief, '--name', workerName]);
    workerId = delegate.json?.session_id;
    assert(typeof workerId === 'string' && workerId.length > 0, `delegate returned a worker session id (got ${JSON.stringify(delegate.json)})`);
    await observer.waitForSession({ id: workerId, timeoutMs: 30_000 });

    // The FE learns about the ticket via broadcasts; wait until it is bound.
    const boundList = await pollFor(
      async () => {
        const { tickets } = await client.request('ticket_list');
        const bound = tickets.find((ticket) => ticket.assignee === workerId);
        return bound ? { bound, tickets } : null;
      },
      'the delegated ticket to appear bound to the worker',
      30_000,
    );
    const ticketId = boundList.bound.id;
    console.log(`[RealAppHarness] worker=${workerId} ticket=${ticketId}`);

    // 3) Open the panel the real way and confirm Resume is offered while alive.
    await client.request('ticket_open_via_dashboard', { sessionId: workerId });
    const aliveState = await pollFor(
      async () => {
        const state = await client.request('ticket_detail_get_state');
        return state.present && state.ticketId === ticketId ? state : null;
      },
      'ticket panel to render for the bound worker',
      20_000,
    );
    assert(aliveState.canResume === true, 'panel offers Resume while the worker is alive');

    // 4) CLOSE the worker session — the failing precondition. The daemon
    // unregisters it and removes the store row.
    await client.request('close_session', { sessionId: workerId });
    await pollFor(
      async () => (observer.getSession(workerId) ? null : true),
      'worker session to be unregistered after close',
      20_000,
    );
    console.log('[RealAppHarness] worker session closed (store row gone)');

    // 5) Reopen the panel and confirm Resume is STILL offered — the button gates
    // on ticket metadata (cwd + last_agent_id), which survives the close.
    await client.request('ticket_close_detail').catch(() => {});
    await client.request('ticket_open_detail', { ticketId });
    const closedState = await pollFor(
      async () => {
        const state = await client.request('ticket_detail_get_state');
        return state.present && !state.loading && state.ticketId === ticketId ? state : null;
      },
      'ticket panel to reopen after the session closed',
      20_000,
    );
    assert(closedState.canResume === true, 'panel STILL offers Resume after the bound session was closed');

    // 6) Click the real Resume button → the full daemon-owned resume path.
    await client.request('ticket_resume');

    // The daemon respawns under the id the ticket points at (its assignee stays
    // the same), so the closed session reappears — that is the fix. The OLD
    // frontend orchestration rolled back here with a spawn-args error and never
    // respawned.
    const respawned = await observer.waitForSession({ id: workerId, timeoutMs: 30_000 });
    resumedId = respawned?.id ?? workerId;
    console.log(`[RealAppHarness] ticket resumed → session ${resumedId} respawned`);

    // Ticket stays bound to the resumed session.
    const stillBound = await pollFor(
      async () => {
        const { tickets } = await client.request('ticket_list');
        const bound = tickets.find((ticket) => ticket.id === ticketId);
        return bound && bound.assignee === resumedId ? bound : null;
      },
      'ticket to remain bound to the resumed session',
      20_000,
    );
    assert(stillBound.assignee === resumedId, `ticket bound to the resumed session (assignee=${stillBound.assignee})`);

    const summary = {
      ok: true,
      runId,
      profile,
      chiefId,
      workerId,
      ticketId,
      resumedId,
      resumedSameId: resumedId === workerId,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Ticket resume scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const id of [resumedId, workerId, chiefId]) {
      if (id) {
        await client.request('close_session', { sessionId: id }).catch(() => {});
      }
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
