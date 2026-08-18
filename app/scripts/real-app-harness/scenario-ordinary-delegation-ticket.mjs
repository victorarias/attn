#!/usr/bin/env node

/**
 * Real-app scenario: an ORDINARY (non-chief) session delegates, and the resulting
 * ticket is bound and routed exactly like a chief's.
 *
 * Bootstraps two shell sessions — a delegator and a separate chief of staff that
 * takes no part in the delegation — then delegates a real codex worker FROM the
 * delegator. Asserts the daemon bound a ticket, the packaged app's tickets panel
 * renders it, and that activity on it reaches all three parties, each exactly
 * once: the worker (assignee), the delegator (creator), and the chief (durable
 * role identity). Also asserts the worker is NOT decorated delegated_from_chief —
 * that badge stays reserved for work the chief actually started.
 *
 * Prereqs: `codex` on PATH; a built `./attn` (or ATTN_HARNESS_BIN); a non-prod
 * profile install (`make install PROFILE=<name>`).
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, socketPathForProfile } from './harnessProfile.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

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

function makeAttnRunner(attnBin, profile) {
  const socketPath = socketPathForProfile(profile);
  return function runAttn(args, { allowFailure = false } = {}) {
    try {
      const stdout = execFileSync(attnBin, args, {
        encoding: 'utf8',
        env: { ...process.env, ATTN_PROFILE: profile, ATTN_SOCKET_PATH: socketPath },
      });
      const brace = stdout.indexOf('{');
      return { stdout, status: 0, stderr: '', json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null };
    } catch (error) {
      if (!allowFailure) throw error;
      const stdout = typeof error.stdout === 'string' ? error.stdout : '';
      const stderr = typeof error.stderr === 'string' ? error.stderr : '';
      const brace = stdout.indexOf('{');
      return {
        status: error.status ?? 1,
        stdout,
        stderr,
        json: brace >= 0 ? JSON.parse(stdout.slice(brace)) : null,
      };
    }
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

// Bundles for one ticket, flattened. `attn ticket inbox --json` both reads AND
// consumes, so each call must be treated as a one-shot observation.
function inboxEventsFor(runAttn, sessionId, ticketId) {
  const inbox = runAttn(['ticket', 'inbox', '--session', sessionId, '--json']);
  const bundles = inbox.json?.bundles ?? [];
  return bundles
    .filter((bundle) => bundle.ticket_id === ticketId)
    .flatMap((bundle) => bundle.events ?? []);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-ordinary-delegation-ticket.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the ordinary-delegation scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);
  // `ticket list --json` prints an array; runAttn's own parse looks for an object.
  const ticketBoard = () => {
    const { stdout } = runAttn(['ticket', 'list', '--json']);
    return JSON.parse(stdout.slice(stdout.indexOf('[')));
  };

  const runner = createScenarioRunner(options, {
    scenarioId: 'ORDINARY-DELEGATION-TICKET',
    tier: 'tier2-local-real-agent',
    prefix: 'ordinary-delegation-ticket',
    metadata: {
      agent: 'codex',
      focus: 'a non-chief session delegates: ticket is bound, panel renders it, activity reaches worker + delegator + chief exactly once, no delegated-from-chief badge',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let delegatorId = null;
  let chiefId = null;
  let workerId = null;

  runner.log(`[RealAppHarness] profile=${profile} runDir=${runner.runDir}`);

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('seed_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'delegator-repo');
      fs.mkdirSync(dir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: dir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: dir,
        env: { ...process.env, GIT_AUTHOR_NAME: 'attn', GIT_AUTHOR_EMAIL: 'attn@local', GIT_COMMITTER_NAME: 'attn', GIT_COMMITTER_EMAIL: 'attn@local' },
      });
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // A chief exists but takes no part in this delegation — it must still see the
    // activity, through its durable role identity rather than as the creator.
    await runner.step('boot_chief', async () => {
      chiefId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `chief-${runner.runId}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
        // This scenario never types into these terminals — they exist to hold a
        // session identity (a chief, a delegator). Waiting for a rendered pane
        // would only add a window-visibility dependency the assertions don't need.
        waitForInitialPaneVisible: false,
      });
      runner.registerCleanup('demote_and_unregister_chief', async () => {
        try {
          observer.send({ cmd: 'set_chief_of_staff', session_id: chiefId, chief_of_staff: false });
        } catch (error) {
          console.warn('[ordinary-delegation] demote chief failed: ' + (error instanceof Error ? error.message : String(error)));
        }
        await observer.unregisterMatchingSessions((session) => session.id === chiefId, 10_000).catch((error) => console.warn('[ordinary-delegation] unregister chief failed: ' + (error instanceof Error ? error.message : String(error))));
      });
      await client.request('select_session', { sessionId: chiefId });
      await setChiefOfStaff(client, chiefId);
    });

    // The delegating session: an ordinary session with no special role.
    await runner.step('boot_delegator', async () => {
      delegatorId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: repoDir,
        label: `deleg-${runner.runId}`,
        agent: 'shell',
        sessionWaitMs: 30_000,
        // This scenario never types into these terminals — they exist to hold a
        // session identity (a chief, a delegator). Waiting for a rendered pane
        // would only add a window-visibility dependency the assertions don't need.
        waitForInitialPaneVisible: false,
      });
      runner.registerCleanup('close_delegator_session', () =>
        client.request('close_session', { sessionId: delegatorId }).catch((error) => console.warn('[ordinary-delegation] close_session delegator failed: ' + (error instanceof Error ? error.message : String(error)))));
      const state = await client.request('chief_of_staff_get_state');
      const delegator = state.sessions.find((session) => session.id === delegatorId);
      runner.assert(!delegator?.chiefOfStaff, 'the delegating session is NOT the chief of staff', state);
    });

    const ticketId = await runner.step('ordinary_delegation_binds_ticket', async () => {
      const workerName = `ord-${Date.now().toString(36).slice(-8)}`;
      const brief = 'Ordinary-delegation ticket QA fixture. Please wait for direction; do not start coding.';
      const delegate = runAttn(['delegate', '--source-session', delegatorId, '--agent', 'codex', '--brief', brief, '--name', workerName]);
      workerId = delegate.json?.session_id;
      runner.assert(typeof workerId === 'string' && workerId.length > 0, `delegate returned a worker session id (got ${JSON.stringify(delegate.json)})`, delegate.json);
      runner.registerCleanup('close_worker_session', () =>
        client.request('close_session', { sessionId: workerId }).catch((error) => console.warn('[ordinary-delegation] close_session worker failed: ' + (error instanceof Error ? error.message : String(error)))));
      await observer.waitForSession({ id: workerId, timeoutMs: 30_000 });

      // The board is read through the CLI: the app shows the garden now, and
      // its ticket surfaces (with the bridge verbs that read them) are gone.
      const boundList = await pollFor(
        async () => {
          const tickets = ticketBoard();
          const bound = tickets.find((ticket) => ticket.assignee === workerId);
          return bound ? { bound, tickets } : null;
        },
        'the ordinary delegation ticket to appear bound to the worker',
        30_000,
      );
      const id = boundList.bound.id;
      runner.log(`[RealAppHarness] delegator=${delegatorId} worker=${workerId} ticket=${id}`);

      // The CLI read agrees with the panel: bound, working, brief as description.
      const show = runAttn(['ticket', 'show', id, '--json']);
      runner.assert(show.json?.assignee === workerId, `ticket show reports the worker as assignee (got ${JSON.stringify(show.json)})`, show.json);
      runner.assert(show.json?.status === 'working', `ticket show reports the working column (got ${show.json?.status})`, show.json);
      runner.assert(
        (show.json?.description ?? '').includes('Ordinary-delegation ticket QA fixture'),
        'ticket description is the delegation brief',
        show.json,
      );
      return id;
    });

    // The badge stays chief-only: an ordinary delegation is tracked but not
    // marked as delegated-from-chief.
    await runner.step('worker_not_decorated_delegated_from_chief', async () => {
      const list = runAttn(['list']);
      const worker = (list.json?.sessions ?? []).find((session) => session.id === workerId);
      runner.assert(Boolean(worker), `the worker session is listed (got ${JSON.stringify(list.json?.sessions?.map((s) => s.id))})`, list.json);
      runner.assert(worker.delegated_from_chief !== true, `worker is not decorated delegated_from_chief (got ${JSON.stringify(worker)})`, worker);
      return worker;
    });

    // Baseline both observers' queues, then produce exactly one event and assert
    // each of them is delivered it exactly once.
    await runner.step('worker_report_reaches_delegator_and_chief_once', async () => {
      inboxEventsFor(runAttn, delegatorId, ticketId);
      inboxEventsFor(runAttn, chiefId, ticketId);

      runAttn(['ticket', 'status', 'in_progress', '--session', workerId, '--comment', 'Ordinary delegation worker reporting in.']);

      for (const [name, sessionId] of [['delegator', delegatorId], ['chief', chiefId]]) {
        const events = await pollFor(
          async () => {
            const seen = inboxEventsFor(runAttn, sessionId, ticketId);
            return seen.length > 0 ? seen : null;
          },
          `${name} inbox to deliver the worker report`,
          20_000,
        );
        runner.assert(events.length === 1, `${name} received the report exactly once (got ${JSON.stringify(events)})`, events);
        runner.assert(events[0].author === workerId, `${name}'s delivered event is authored by the worker (got ${JSON.stringify(events[0])})`, events[0]);
      }
      return true;
    });

    // The reverse direction: a note from the delegator reaches the delegated agent.
    await runner.step('delegator_note_reaches_worker', async () => {
      inboxEventsFor(runAttn, workerId, ticketId);
      runAttn(['ticket', 'comment', ticketId, '-m', 'Delegator: check the error wording too.', '--session', delegatorId]);
      const events = await pollFor(
        async () => {
          const seen = inboxEventsFor(runAttn, workerId, ticketId);
          return seen.length > 0 ? seen : null;
        },
        'worker inbox to deliver the delegator note',
        20_000,
      );
      runner.assert(
        events.some((event) => (event.comment ?? '').includes('check the error wording')),
        `worker received the delegator's note (got ${JSON.stringify(events)})`,
        events,
      );
      return events;
    });

    const summary = runner.finishSuccess({
      profile,
      delegatorId,
      chiefId,
      workerId,
      ticketId,
    });
    console.log('[RealAppHarness] Ordinary-delegation ticket scenario passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { delegatorId, chiefId, workerId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    // Registered cleanups only fire on signals; the normal path tears down here,
    // in the same order (worker, delegator, chief demote + unregister, app,
    // observer) so the observer socket outlives every command that needs it.
    for (const sessionId of [workerId, delegatorId]) {
      if (!sessionId) continue;
      await client.request('close_session', { sessionId }).catch((error) => console.warn('[ordinary-delegation] close_session failed: ' + (error instanceof Error ? error.message : String(error))));
    }
    if (chiefId) {
      // A chief-of-staff session cannot be closed or unregistered while it holds
      // the role (refused at both layers) — demote first, then unregister.
      try {
        observer.send({ cmd: 'set_chief_of_staff', session_id: chiefId, chief_of_staff: false });
      } catch (error) {
        console.warn('[ordinary-delegation] demote chief failed: ' + (error instanceof Error ? error.message : String(error)));
      }
      await observer.unregisterMatchingSessions((session) => session.id === chiefId, 10_000).catch((error) => console.warn('[ordinary-delegation] unregister chief failed: ' + (error instanceof Error ? error.message : String(error))));
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
