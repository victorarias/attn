#!/usr/bin/env node

/**
 * Real-app scenario: the sidebar's agent-queue arrangement.
 *
 * The queue turns the sidebar into a list of turns the user owes. A turn opens
 * when an agent reaches a state that wants the user and closes only when the
 * user settles it — no state change ever removes a row, and a row never moves
 * while the user is working with that agent. That is the whole claim, and it is
 * only observable end to end: the daemon stamps the turn, derives `turn_owed`
 * per broadcast, and the sidebar renders the band from what it is told.
 *
 * The run drives two real Claude agents in two workspaces and asserts, against
 * the rendered DOM (`queue_get_state`), that:
 *
 *   1. both owed turns are in the band, oldest first, and both are still in the
 *      workspace tree below it — the queue is additive, never a filter,
 *   2. clicking a row hands the agent over with the keyboard in its terminal,
 *   3. steering that agent leaves it exactly where it was, showing its live
 *      (working) state rather than dropping out of the queue,
 *   4. settling removes it, and only settling does,
 *   5. a settled agent that wants the user again returns at the bottom, behind
 *      the turn that has been owed longer,
 *   6. a settled agent whose run finishes without asking anything returns too —
 *      a result nobody has read is still the user's — while a shell pane and an
 *      agent sitting at its prompt, which reach the same `idle` state, never do,
 *   7. the chief of staff occupies its own slot and never the band,
 *   8. turning the arrangement off and back on mid-session returns the same
 *      queue with the same agent still selected,
 *   9. a settle survives a daemon restart.
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
import { MacOSDriver } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { preTrustClaudeFolder, ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane, waitForPaneInputFocus } from './scenarioAssertions.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

// States that want the user, and so open a turn. The scenario asserts the queue
// against whichever of them the agent actually reaches: the product claim is
// "a state that wants the user opens a turn", not that a given prompt always
// produces the same one.
const TURN_OPENING_STATES = new Set(['waiting_input', 'pending_approval', 'unknown']);

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

async function queueState(client) {
  return client.request('queue_get_state');
}

function turnIds(queue) {
  return (queue.turns || []).map((row) => row.id);
}

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
  return { sessionId, paneId: pane.paneId, cwd };
}

// A turn that ends on a question is the plainest way to a state that wants the
// user: the classifier reads the agent's own closing text, so no tool
// permission, sandbox, or auto-approve setting is in the path.
function questionPrompt(token) {
  return [
    `I am thinking about ${token} but I have not decided anything yet.`,
    'Ask me exactly one short clarifying question about it and then stop and wait for my answer.',
    'Do not use any tools and do not answer it yourself.',
  ].join(' ');
}

async function driveToOwedTurn(client, observer, agent, token, description) {
  await submitPrompt(client, agent.sessionId, agent.paneId, questionPrompt(token));
  return pollFor(
    () => {
      const state = observer.getSession(agent.sessionId)?.state;
      return TURN_OPENING_STATES.has(state) ? state : null;
    },
    description,
    120_000,
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-agent-queue.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'AGENT-QUEUE',
    tier: 'tier3-local-agent',
    prefix: 'agent-queue',
    metadata: {
      focus: 'a turn opens on a state and closes only when the user settles it',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
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

  let alpha;
  let beta;

  try {
    await runner.step('launch_app_with_queue_mode', async () => {
      process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
      await launchFreshAppAndConnect(client, observer);
      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
    });

    await runner.step('open_first_turn', async () => {
      alpha = await createAgent(client, observer, runner, 'alpha', `queue-alpha-${runner.runId}`);
      createdSessionIds.push(alpha.sessionId);
      // The sidebar only exists once there is something to list, so the band's
      // empty state is checked here rather than against a sessionless app.
      //
      // An agent that has booted to its prompt resolves to `idle`, the same
      // state a finished run resolves to, and `idle` opens a turn. It must not
      // queue anyway: there is no result behind it to read. This is the only
      // place that distinction is observable end to end.
      const empty = await pollFor(async () => {
        const state = await queueState(client);
        return state.present ? state : null;
      }, 'the queue band to render once the arrangement is on', 15_000);
      runner.assert(empty.empty, `a freshly launched agent owes nothing: ${JSON.stringify(empty.turns)}`);

      const state = await driveToOwedTurn(client, observer, alpha, 'QUEUE_ALPHA', 'alpha to want the user');
      runner.log('alpha opened a turn', { state });
      await waitForTurns(client, [alpha.sessionId], 'alpha in the band');
    });

    await runner.step('open_second_turn', async () => {
      beta = await createAgent(client, observer, runner, 'beta', `queue-beta-${runner.runId}`);
      createdSessionIds.push(beta.sessionId);
      const state = await driveToOwedTurn(client, observer, beta, 'QUEUE_BETA', 'beta to want the user');
      runner.log('beta opened a turn', { state });
    });

    await runner.step('band_is_oldest_first_and_additive', async () => {
      const queue = await waitForTurns(client, [alpha.sessionId, beta.sessionId], 'both turns, oldest first');
      runner.assert(
        queue.turns[0].workspace !== queue.turns[1].workspace,
        `the two turns come from different workspaces: ${JSON.stringify(queue.turns.map((row) => row.workspace))}`,
      );
      for (const sessionId of [alpha.sessionId, beta.sessionId]) {
        runner.assert(
          queue.treeSessionIds.includes(sessionId),
          `${sessionId} still has its own row in the workspace tree: ${JSON.stringify(queue.treeSessionIds)}`,
        );
      }
    });

    await runner.step('clicking_a_row_hands_the_agent_over', async () => {
      await client.request('dom_click', { selector: `[data-testid="queue-turn-${alpha.sessionId}"]` });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === alpha.sessionId ? state : null;
      }, 'the clicked row to select its agent', 15_000);
      await waitForPaneInputFocus(client, alpha.sessionId, alpha.paneId, 15_000);
    });

    await runner.step('steering_keeps_the_row_in_place', async () => {
      // Answering the agent's question is steering: it goes back to work, and
      // the row must stay exactly where it was, showing that it is working. A
      // queue that dropped it here would be a queue of stopped agents, not of
      // turns the user still owes.
      const before = await queueState(client);
      const beforeIndex = turnIds(before).indexOf(alpha.sessionId);
      await submitPrompt(client, alpha.sessionId, alpha.paneId, 'Blue. Reply with the single word: noted.');
      const working = await pollFor(async () => {
        const queue = await queueState(client);
        const row = (queue.turns || []).find((entry) => entry.id === alpha.sessionId);
        return row && row.state === 'working' ? { queue, row } : null;
      }, 'alpha to show as working while still queued', 60_000);
      runner.assert(
        turnIds(working.queue).indexOf(alpha.sessionId) === beforeIndex,
        `alpha kept its position while working: ${JSON.stringify(turnIds(working.queue))}`,
      );
    });

    await runner.step('settling_is_the_only_exit', async () => {
      // Let the run end on its own first: the row surviving a completed run is
      // the point — nothing but a settle takes it out.
      await pollFor(() => {
        const state = observer.getSession(alpha.sessionId)?.state;
        return state && state !== 'working' ? state : null;
      }, 'alpha to finish the run it was steered into', 180_000);
      const stillThere = await queueState(client);
      runner.assert(
        turnIds(stillThere).includes(alpha.sessionId),
        `alpha is still owed after its run finished: ${JSON.stringify(turnIds(stillThere))}`,
      );

      await client.request('dom_click', { selector: `[data-testid="queue-settle-${alpha.sessionId}"]` });
      const settled = await waitForTurns(client, [beta.sessionId], 'alpha gone from the band after settling');
      runner.assert(
        settled.treeSessionIds.includes(alpha.sessionId),
        'settling removes the turn, not the session',
      );
    });

    await runner.step('a_new_turn_returns_at_the_bottom', async () => {
      const state = await driveToOwedTurn(client, observer, alpha, 'QUEUE_ALPHA_AGAIN', 'alpha to want the user again');
      runner.log('alpha opened a second turn', { state });
      await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'alpha behind beta, whose turn has been owed longer',
        60_000,
      );
    });

    await runner.step('a_finished_run_returns_the_agent_to_the_queue', async () => {
      // Settle alpha first, so nothing but the finish itself can bring it back.
      await client.request('dom_click', { selector: `[data-testid="queue-settle-${alpha.sessionId}"]` });
      await waitForTurns(client, [beta.sessionId], 'alpha settled again');

      await submitPrompt(
        client,
        alpha.sessionId,
        alpha.paneId,
        'Reply with the single word: done. Do not ask me anything and do not use any tools.',
      );
      await pollFor(
        () => (observer.getSession(alpha.sessionId)?.state === 'working' ? 'working' : null),
        'alpha to start the run',
        60_000,
      );
      const duringRun = await queueState(client);
      runner.assert(
        !turnIds(duringRun).includes(alpha.sessionId),
        `a settled agent stays out of the band while it works: ${JSON.stringify(turnIds(duringRun))}`,
      );

      const finished = await pollFor(
        () => {
          const state = observer.getSession(alpha.sessionId)?.state;
          return state && state !== 'working' ? state : null;
        },
        'alpha to finish the run',
        180_000,
      );
      runner.log('alpha finished', { state: finished });
      runner.assert(
        finished === 'idle',
        `the run ended without a question, so it resolves to idle: got ${finished}`,
      );
      await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'alpha back at the bottom because its run finished',
        60_000,
      );
    });

    await runner.step('a_shell_pane_never_queues', async () => {
      // A shell pane is a real store session, registered idle at birth and left
      // there. Now that idle opens a turn, only the shell exclusion keeps every
      // ⌘` terminal out of a queue nothing could ever settle.
      const before = turnIds(await queueState(client));
      const workspace = await client.request('get_workspace', { sessionId: alpha.sessionId });
      const targetPaneId = workspace.activePaneId || workspace.panes?.[0]?.paneId;
      await client.request('split_pane', { sessionId: alpha.sessionId, targetPaneId, direction: 'vertical' });
      const shell = await pollFor(async () => {
        const state = await client.request('get_state');
        return (state.sessions || []).find((session) => session.agent === 'shell') || null;
      }, 'the shell pane to register as a session', 30_000);
      runner.assert(shell.state === 'idle', `the shell sits in idle, the state that opens a turn: got ${shell.state}`);
      await delay(3000);
      const after = await queueState(client);
      runner.assert(
        !turnIds(after).includes(shell.id),
        `the shell pane is not a turn: ${JSON.stringify(turnIds(after))}`,
      );
      runner.assert(
        JSON.stringify(turnIds(after)) === JSON.stringify(before),
        `opening a terminal changed nothing in the band: ${JSON.stringify(before)} -> ${JSON.stringify(turnIds(after))}`,
      );
      const paneWithShell = await client.request('get_workspace', { sessionId: alpha.sessionId });
      const shellPaneId = (paneWithShell.paneIds || []).find((paneId) => paneId.includes(shell.id));
      await client.request('close_pane', { sessionId: alpha.sessionId, paneId: shellPaneId }).catch(() => {});
    });

    await runner.step('the_chief_never_queues', async () => {
      await client.request('chief_of_staff_open_actions', { sessionId: beta.sessionId });
      await client.request('chief_of_staff_toggle');
      const promoted = await waitForTurns(client, [alpha.sessionId], 'beta out of the band once it is chief', 20_000);
      runner.assert(promoted.chief?.id === beta.sessionId, `beta occupies the chief slot: ${JSON.stringify(promoted.chief)}`);

      await client.request('chief_of_staff_open_actions', { sessionId: beta.sessionId });
      await client.request('chief_of_staff_toggle');
      const demoted = await waitForTurns(
        client,
        [beta.sessionId, alpha.sessionId],
        'beta back in the band, with its original turn age',
        20_000,
      );
      runner.assert(demoted.chief === null, 'the chief slot is empty again');
    });

    await runner.step('toggling_the_arrangement_preserves_the_queue', async () => {
      const activeBefore = (await client.request('get_state')).activeSessionId;
      const expected = turnIds(await queueState(client));

      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'false' });
      const off = await pollFor(async () => {
        const state = await queueState(client);
        return state.present ? null : state;
      }, 'the band to disappear with the arrangement off', 15_000);
      runner.assert(
        off.treeSessionIds.includes(alpha.sessionId) && off.treeSessionIds.includes(beta.sessionId),
        `the workspace tree is untouched by the arrangement: ${JSON.stringify(off.treeSessionIds)}`,
      );

      await client.request('set_setting', { key: 'queue_mode_enabled', value: 'true' });
      await waitForTurns(client, expected, 'the same queue after turning the arrangement back on', 15_000);
      const activeAfter = (await client.request('get_state')).activeSessionId;
      runner.assert(
        activeAfter === activeBefore,
        `the selected agent survived the toggle: ${activeBefore} -> ${activeAfter}`,
      );
    });

    await runner.step('a_settle_survives_a_daemon_restart', async () => {
      // Settle with the real key combo this time: the packaged app's native menu
      // can swallow an accelerator before the DOM ever sees it, which no unit or
      // e2e test can catch.
      await client.request('select_session', { sessionId: beta.sessionId });
      await driver.activateApp();
      await driver.clickWindow(0.5, 0.5);
      await driver.pressKey('e', { command: true, shift: true });
      await waitForTurns(client, [alpha.sessionId], 'beta settled by shortcut before the restart');

      // The app respawns the daemon, so it has to be down for the restart to be
      // a restart.
      await client.quitApp();
      await observer.close();
      try { execFileSync(attnBin, ['daemon', 'stop'], { env: daemonEnv, encoding: 'utf8' }); } catch {}
      execFileSync(attnBin, ['daemon', 'ensure'], { env: daemonEnv, encoding: 'utf8' });

      await relaunchAppAndConnect(client, observer);
      const queue = await waitForTurns(client, [alpha.sessionId], 'the queue rebuilt from persisted stamps', 60_000);
      runner.assert(
        queue.treeSessionIds.includes(beta.sessionId),
        'beta came back as a session, just not as a turn',
      );
    });

    const result = runner.finishSuccess({
      alphaSessionId: alpha.sessionId,
      betaSessionId: beta.sessionId,
    });
    console.log('[RealAppHarness] Agent queue passed.');
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
