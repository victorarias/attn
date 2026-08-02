#!/usr/bin/env node

/**
 * Real-app scenario: snoozing an agent in the queue.
 *
 * Settle answers *I dealt with this*. Snooze answers *not now*: it closes the
 * open turn and stops the next one from opening until a time the user named.
 * The claim only exists end to end — the daemon writes the settle and the
 * deadline in one statement, suppresses the turn-open while the deadline holds,
 * arms a timer, and the sidebar draws the row in its own section with the time
 * it comes back.
 *
 * The run drives two real Claude agents in two workspaces and asserts, against
 * the rendered DOM (`queue_get_state`) and the daemon's own broadcast, that:
 *
 *   1. snoozing from a queue row's menu closes that turn, takes the row out of
 *      both bands, and puts it in the Snoozed section with a wake time,
 *   2. snoozing the agent the user is looking at hands over the next agent that
 *      owes a turn, the same way settling does,
 *   3. the deferral actually holds: the snoozed agent runs, stops in a state
 *      that would open a turn, and no turn opens,
 *   4. waking early puts it back — and at the *tail*, behind the turn that has
 *      been owed longer, because the clock on what you owe starts when you said
 *      you would come back,
 *   5. the wake timer does the same thing on its own when the deadline passes,
 *   6. a deferral survives a daemon restart, deadline and all,
 *   7. break-through: an agent whose process dies clears its own snooze and
 *      opens a turn immediately, because that is what the user could not have
 *      anticipated.
 *
 * The menu's shortest choice is 30 minutes, which is the product's answer and
 * not a testable wait, so the timed legs send `snooze_turn` with a few-second
 * deadline over the daemon socket. The menu itself is exercised in full for leg
 * 1 — what the six buttons compute is unit-tested in snoozeDurations.test.ts.
 *
 * Prereqs: `claude` on PATH; a non-production profile install with the
 * automation layer; a built `./attn` (or ATTN_HARNESS_BIN) for the restart step.
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
  relaunchAppAndConnect,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { preTrustClaudeFolder, ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const TURN_OPENING_STATES = new Set(['waiting_input', 'pending_approval', 'unknown', 'idle']);

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 500) {
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

const queueState = (client) => client.request('queue_get_state');
const turnIds = (queue) => (queue.turns || []).map((row) => row.id);
const snoozedIds = (queue) => (queue.snoozed?.rows || []).map((row) => row.id);

// Claude treats a fast multi-line write as a paste, so the submit has to be a
// lone carriage return a beat later.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(600);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

async function createAgent(client, observer, runner, dirName, label) {
  const cwd = path.join(runner.sessionDir, dirName);
  fs.mkdirSync(cwd, { recursive: true });
  preTrustClaudeFolder(cwd);
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'claude',
    sessionWaitMs: 60_000,
    promptReadyFn: ensureClaudePromptReadyViaPty,
    promptReadyTimeoutMs: 90_000,
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  return { sessionId, paneId: pane.paneId, runtimeId: pane.runtimeId, cwd };
}

function questionPrompt(token) {
  return [
    `I am thinking about ${token} but I have not decided anything yet.`,
    'Ask me exactly one short clarifying question about it and then stop and wait for my answer.',
    'Do not use any tools and do not answer it yourself.',
  ].join(' ');
}

// Generous on purpose: how long a live agent takes to stop says nothing about
// whether the queue reacts to the state it stops in.
const AGENT_STOP_TIMEOUT_MS = 240_000;

async function driveToStop(client, observer, agent, token, description) {
  await submitPrompt(client, agent.sessionId, agent.paneId, questionPrompt(token));
  // It has to be seen working first, or a stop that has not started yet reads
  // as a stop that already finished.
  await pollFor(
    () => (observer.getSession(agent.sessionId)?.state === 'working' ? true : null),
    `${description} to start working`,
    120_000,
  );
  return pollFor(
    () => {
      const state = observer.getSession(agent.sessionId)?.state;
      return TURN_OPENING_STATES.has(state) ? state : null;
    },
    description,
    AGENT_STOP_TIMEOUT_MS,
  );
}

async function waitForTurns(client, expected, description, timeoutMs = 30_000) {
  return pollFor(
    async () => {
      const queue = await queueState(client);
      return JSON.stringify(turnIds(queue)) === JSON.stringify(expected) ? queue : null;
    },
    `${description} (expected turns ${JSON.stringify(expected)})`,
    timeoutMs,
  );
}

/**
 * The live agent process for a session, found by its working directory.
 *
 * By cwd rather than by name: a `pkill -f` against the command line would match
 * any sibling agent on this machine, and the run's cwd is unique to it.
 */
function agentPidForCwd(cwd) {
  const rows = execFileSync('ps', ['-axo', 'pid=,command='], { encoding: 'utf8' }).split('\n');
  const candidates = rows
    .map((row) => row.trim().match(/^(\d+)\s+(.*)$/))
    .filter((match) => match && /(^|\/)claude(\s|$)/.test(match[2]))
    .map((match) => Number(match[1]));
  for (const pid of candidates) {
    let out = '';
    try {
      out = execFileSync('lsof', ['-a', '-d', 'cwd', '-p', String(pid), '-Fn'], { encoding: 'utf8' });
    } catch {
      continue; // gone between the listing and the probe
    }
    if (out.split('\n').some((line) => line.startsWith('n') && line.slice(1) === cwd)) return pid;
  }
  return null;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-agent-queue-snooze.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'AGENT-QUEUE-SNOOZE',
    tier: 'tier3-local-agent',
    prefix: 'agent-queue-snooze',
    metadata: {
      focus: 'a snooze closes the turn, suppresses the next one, and wakes to the tail',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const profile = currentHarnessProfile();
  const attnBin = resolveAttnBin();
  const daemonEnv = { ...process.env, ATTN_PROFILE: profile };
  const createdSessionIds = [];

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_sessions', async () => {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
  });
  runner.registerCleanup('restore_queue_mode', () =>
    client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' }).catch(() => {}));

  const snoozeUntil = (sessionId, ms) => {
    const until = new Date(Date.now() + ms).toISOString();
    observer.send({ cmd: 'snooze_turn', session_id: sessionId, until });
    return until;
  };

  let alpha;
  let beta;

  try {
    await runner.step('launch_app_with_queue_mode', async () => {
      process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
      await launchFreshAppAndConnect(client, observer);
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    });

    await runner.step('two_agents_owe_turns', async () => {
      alpha = await createAgent(client, observer, runner, 'alpha', `snooze-alpha-${runner.runId}`);
      createdSessionIds.push(alpha.sessionId);
      await driveToStop(client, observer, alpha, 'SNOOZE_ALPHA', 'alpha to want the user');

      beta = await createAgent(client, observer, runner, 'beta', `snooze-beta-${runner.runId}`);
      createdSessionIds.push(beta.sessionId);
      await driveToStop(client, observer, beta, 'SNOOZE_BETA', 'beta to want the user');

      // Alpha's turn opened first, so it stays ahead of beta throughout — which
      // is what makes "beta came back at the tail" a real assertion rather than
      // a coincidence of a one-row band.
      await waitForTurns(client, [alpha.sessionId, beta.sessionId], 'both turns, oldest first');
    });

    await runner.step('snoozing_from_the_row_menu_parks_the_agent_and_hands_over', async () => {
      // Snooze the agent the user is looking at: closing its turn should move
      // them on, exactly as settling does.
      await client.request('select_session', { sessionId: beta.sessionId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === beta.sessionId ? state : null;
      }, 'beta to be the agent on screen', 15_000);

      await client.request('dom_click', { selector: `[data-testid="queue-snooze-${beta.sessionId}"]` });
      // The click on the choice is itself the assertion that the menu opened:
      // dom_click fails loudly when the selector is not in the DOM.
      const chosen = await pollFor(
        () => client.request('dom_click', { selector: '[data-testid="snooze-choice-30m"]' }).catch(() => null),
        'the snooze duration menu to open with its 30-minute choice',
        10_000,
      );
      runner.log('chose 30 minutes', chosen);

      const queue = await waitForTurns(client, [alpha.sessionId], 'beta out of the turns band');
      runner.assert(
        !(queue.settled || []).some((row) => row.id === beta.sessionId),
        `a deferred agent is not in Settled either: ${JSON.stringify((queue.settled || []).map((r) => r.id))}`,
      );
      runner.assert(queue.snoozed.present, 'the Snoozed section is drawn once something is deferred');
      runner.assert(
        queue.snoozed.header.includes('(1)'),
        `the section counts what is in it: ${queue.snoozed.header}`,
      );
      runner.assert(
        !queue.snoozed.expanded && queue.snoozed.rows.length === 0,
        'the section ships collapsed — a snooze surfaces itself when it wakes',
      );

      await client.request('dom_click', { selector: '[data-testid="snoozed-section-header"]' });
      const expanded = await pollFor(async () => {
        const current = await queueState(client);
        return current.snoozed.expanded ? current : null;
      }, 'the Snoozed section to expand', 10_000);
      const row = expanded.snoozed.rows.find((entry) => entry.id === beta.sessionId);
      runner.assert(row, `beta is the deferred row: ${JSON.stringify(snoozedIds(expanded))}`);
      runner.assert(row.wake, `the row says when it comes back: ${JSON.stringify(row)}`);
      runner.log('deferred row', row);

      // The daemon's own answer, not just the drawing of it.
      const session = await pollFor(
        () => {
          const current = observer.getSession(beta.sessionId);
          // turn_owed is omitted from the broadcast when false, so its absence
          // is the closed turn.
          return current && !current.turn_owed && current.turn_snoozed_until ? current : null;
        },
        'the daemon to broadcast the closed turn and the deadline',
        15_000,
      );
      runner.log('broadcast deadline', {
        turnOwed: session.turn_owed ?? false,
        snoozedUntil: session.turn_snoozed_until,
      });
      const minutes = (Date.parse(session.turn_snoozed_until) - Date.now()) / 60_000;
      runner.assert(
        minutes > 28 && minutes < 31,
        `the 30-minute choice deferred it by about 30 minutes: ${minutes.toFixed(1)}`,
      );

      const moved = await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === alpha.sessionId ? state : null;
      }, 'snoozing the watched agent to hand over the next one that wants the user', 15_000);
      runner.assert(moved.activeSessionId === alpha.sessionId, 'handover landed on alpha');
    });

    await runner.step('the_deferral_holds_while_the_agent_runs_and_stops', async () => {
      // The whole point. The agent works, stops in a state that would open a
      // turn, and nothing opens — this is the suppression, and it is the one
      // claim a unit test of the band cannot make.
      const state = await driveToStop(client, observer, beta, 'SNOOZE_BETA_AGAIN', 'the deferred agent to stop again');
      runner.log('the deferred agent stopped', { state });
      await delay(3_000);

      const queue = await queueState(client);
      runner.assert(
        JSON.stringify(turnIds(queue)) === JSON.stringify([alpha.sessionId]),
        `stopping opened no turn for the deferred agent: ${JSON.stringify(turnIds(queue))}`,
      );
      runner.assert(
        snoozedIds(queue).includes(beta.sessionId),
        `it is still parked, with its wake time: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(
        !observer.getSession(beta.sessionId)?.turn_owed,
        'and the daemon agrees no turn is owed',
      );
    });

    await runner.step('waking_early_returns_it_to_the_tail', async () => {
      await client.request('dom_click', { selector: `[data-testid="queue-wake-${beta.sessionId}"]` });
      const queue = await waitForTurns(
        client,
        [alpha.sessionId, beta.sessionId],
        'the woken agent back in the band, behind the turn owed longer',
      );
      runner.assert(
        !queue.snoozed.present,
        `the section goes away with the last deferral: ${JSON.stringify(queue.snoozed)}`,
      );
      // The tail, not the head: the woken turn is stamped at the wake instant,
      // so alpha — owed since before the snooze — stays ahead of it.
      runner.assert(turnIds(queue)[1] === beta.sessionId, 'it came back at the tail');
    });

    await runner.step('the_timer_wakes_it_on_its_own', async () => {
      const until = snoozeUntil(beta.sessionId, 8_000);
      runner.log('deferred by the daemon command', { until });
      await waitForTurns(client, [alpha.sessionId], 'the short deferral to close the turn');

      const queue = await pollFor(
        async () => {
          const current = await queueState(client);
          return turnIds(current).length === 2 ? current : null;
        },
        'the wake timer to put it back by itself',
        45_000,
      );
      runner.assert(
        JSON.stringify(turnIds(queue)) === JSON.stringify([alpha.sessionId, beta.sessionId]),
        `the timer woke it to the tail: ${JSON.stringify(turnIds(queue))}`,
      );
      runner.assert(
        !queue.snoozed.present,
        `and cleared the deferral: ${JSON.stringify(queue.snoozed)}`,
      );
    });

    await runner.step('a_deferral_survives_a_daemon_restart', async () => {
      snoozeUntil(beta.sessionId, 20 * 60_000);
      await waitForTurns(client, [alpha.sessionId], 'beta deferred again, for long enough to restart under');

      await client.quitApp();
      await observer.close();
      try { execFileSync(attnBin, ['daemon', 'stop'], { env: daemonEnv, encoding: 'utf8' }); } catch { /* already down */ }
      execFileSync(attnBin, ['daemon', 'ensure'], { env: daemonEnv, encoding: 'utf8' });
      await relaunchAppAndConnect(client, observer);

      const queue = await waitForTurns(
        client,
        [alpha.sessionId],
        'the deferral rebuilt from the persisted deadline',
        60_000,
      );
      runner.assert(
        snoozedIds(queue).includes(beta.sessionId) || queue.snoozed.header.includes('(1)'),
        `beta is still parked after the restart: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(
        observer.getSession(beta.sessionId)?.turn_snoozed_until,
        'and the daemon still broadcasts its deadline',
      );
    });

    await runner.step('a_dead_agent_breaks_through_its_own_snooze', async () => {
      // The one thing the user could not have anticipated. Killing the process
      // with a signal rather than a clean exit is deliberate: a clean exit is
      // auto-close's business, and closing the session would take the row away
      // instead of ringing.
      const pid = agentPidForCwd(beta.cwd);
      runner.assert(pid, `found the deferred agent's process: ${pid}`);
      process.kill(pid, 'SIGKILL');

      const queue = await pollFor(
        async () => {
          const current = await queueState(client);
          return turnIds(current).includes(beta.sessionId) ? current : null;
        },
        'the dead agent to break through its deferral and ring',
        60_000,
      );
      runner.log('after the break-through', {
        turns: turnIds(queue),
        state: observer.getSession(beta.sessionId)?.state,
        reason: observer.getSession(beta.sessionId)?.state_reason,
      });
      runner.assert(
        !queue.snoozed.present,
        `the break-through consumed the deferral rather than pausing it: ${JSON.stringify(queue.snoozed)}`,
      );
      runner.assert(
        !observer.getSession(beta.sessionId)?.turn_snoozed_until,
        'and the daemon dropped the deadline',
      );
    });

    const result = runner.finishSuccess({
      alphaSessionId: alpha.sessionId,
      betaSessionId: beta.sessionId,
    });
    console.log('[RealAppHarness] Agent queue snooze passed.');
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    const result = runner.finishFailure(error, {
      queue: await queueState(client).catch(() => null),
      sessions: (await client.request('get_state').catch(() => null))?.sessions?.map((session) => ({
        id: session.id,
        label: session.label,
        state: session.state,
      })) ?? null,
    });
    console.error(result.error);
    process.exitCode = 1;
  } finally {
    await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' }).catch(() => {});
    for (const sessionId of createdSessionIds.reverse()) {
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
