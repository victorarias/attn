#!/usr/bin/env node

/**
 * Real-app scenario: typing to an agent freezes its settling countdown.
 *
 * attn closes a turn for the user once they have steered an agent and it has
 * gone back to work. The countdown that does it used to run regardless of what
 * the user was doing, so a turn could close while they were still at the
 * keyboard writing the next thing. A keystroke now freezes it where it stands
 * and it stays frozen until five quiet seconds have passed.
 *
 * The whole mechanism lives between a real keystroke's route into the daemon and
 * a timer the daemon owns, which is why it is worth a packaged-app run. Two
 * writes reach a pane and only one of them is a person: the terminal's own input
 * path arrives untagged and holds, and an automation write is tagged and must
 * not — otherwise a delegated agent driving a pane would pin every countdown in
 * the app open. A unit test can assert both sides of that filter, but only the
 * real app proves the frontend's typing path is still the untagged one.
 *
 * The run asserts, in one app lifecycle:
 *
 *   1. typing into a running countdown freezes it — `auto_settle_held` rides the
 *      wire, the deadline is withdrawn, and the turn stays owed,
 *   2. the freeze outlives continued typing past the quiet window, so a user who
 *      keeps composing never has the countdown resume under their hands,
 *   3. going quiet hands back a *whole* countdown rather than the sliver that was
 *      left when they started typing, and
 *   4. an automation write to the same pane does not hold anything.
 *
 * The countdown window is deliberately long (60s), so "it expired on its own"
 * cannot explain the freeze, and a resumed deadline more than 50s out cannot be
 * the remainder of the one that was frozen.
 *
 * Prereqs: `claude` on PATH; a built `./attn` (or ATTN_HARNESS_BIN); a
 * non-production profile install with the automation layer.
 */

import fs from 'node:fs';
import path from 'node:path';

import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { preTrustClaudeFolder, ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// The daemon's quiet window. Mirrored here rather than imported because the
// scenario is asserting the behavior a user sees, not reading the constant that
// produces it — a change to one should make this run fail, not follow it.
const QUIET_WINDOW_MS = 5_000;
const COUNTDOWN_SECONDS = 60;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 300) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

// A person typing: the terminal's own input path, which reaches the daemon
// untagged. This is the verb under test — write_pane is tagged `automation` and
// is deliberately a different thing.
async function typeLikeAPerson(client, sessionId, paneId, text) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text });
}

// Claude treats a fast multi-line write as a paste, so the submit has to be a
// lone carriage return a beat later.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(600);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-settle-typing-hold.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the settle-typing-hold scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'SETTLE-TYPING-HOLD',
    tier: 'tier3-local-agent',
    prefix: 'settle-typing-hold',
    metadata: {
      agent: 'claude',
      focus: 'typing to an agent freezes its settling countdown, and going quiet hands back a whole one',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);

  let agentId = null;
  let agentPaneId = null;

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  try {
    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('boot_agent_owing_a_turn', async () => {
      const cwd = path.join(runner.sessionDir, 'agent-repo');
      fs.mkdirSync(cwd, { recursive: true });
      preTrustClaudeFolder(cwd);
      agentId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd,
        label: `settle-hold-${runner.runId.slice(-6)}`,
        agent: 'claude',
        sessionWaitMs: 60_000,
        promptReadyFn: ensureClaudePromptReadyViaPty,
        promptReadyTimeoutMs: 90_000,
      });
      runner.registerCleanup('close_agent_session', () => client.request('close_session', { sessionId: agentId }));
      const pane = await waitForFirstWorkspacePane(client, agentId, `pane for ${agentId}`, 20_000);
      agentPaneId = pane.paneId;
      await client.request('select_session', { sessionId: agentId });

      // An agent booted to its prompt and not yet spoken to already owes a turn.
      // Auto-settle only arms on a session that owes one, so this is the
      // precondition for everything below rather than a claim of its own.
      await pollFor(
        () => (observer.getSession(agentId)?.turn_owed === true ? true : null),
        'the booted agent to owe a turn',
        90_000,
      );
      note('agent booted and owes a turn', { agentId });
    });

    let frozenDeadline = null;

    await runner.step('typing_freezes_the_running_countdown', async () => {
      // Arm at the floor so the countdown starts promptly; the countdown itself
      // is long, so nothing below can be explained by it running out.
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: String(COUNTDOWN_SECONDS) });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });

      // Steering is what arms it, and the agent has to still be working when the
      // arm window elapses — leaving `working` cancels the settle by itself.
      await submitPrompt(
        client,
        agentId,
        agentPaneId,
        'Count from 1 to 2000, one number per line, nothing else. Do not use any tools.',
      );
      await pollFor(
        () => (observer.getSession(agentId)?.state === 'working' ? true : null),
        'the steered agent to start working',
        90_000,
      );
      frozenDeadline = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the auto-settle countdown to arm',
        90_000,
      );
      const remainingMs = Date.parse(frozenDeadline) - Date.now();
      note('countdown armed', { firesAt: frozenDeadline, remainingMs });
      runner.assert(
        remainingMs > 30_000,
        `the countdown has far more left than this leg needs (${remainingMs}ms), so expiry cannot explain a freeze`,
      );

      const running = (await client.request('get_session_ui_state', { sessionId: agentId })).settling;
      runner.assert(
        running && running.held === false && running.frozenBar === false && running.text.includes('Settling'),
        `the pane header is drawing a running countdown before anyone types (${JSON.stringify(running)})`,
        running,
      );

      await typeLikeAPerson(client, agentId, agentPaneId, 'and then also');

      const held = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          return session?.auto_settle_held === true ? session : null;
        },
        'the keystrokes to freeze the countdown',
        10_000,
      );
      // The two fields are mutually exclusive on purpose: a frozen countdown has
      // no deadline, which is what stops the tile animating toward one.
      runner.assert(
        !held.auto_settle_fires_at,
        `a frozen countdown carries no deadline (got auto_settle_fires_at=${JSON.stringify(held.auto_settle_fires_at)})`,
        held,
      );
      // A settle that ran would have closed the turn, and leaving `working`
      // would have cancelled the countdown outright. Neither happened, so the
      // keystrokes are what froze it.
      runner.assert(
        held.state === 'working' && held.turn_owed === true,
        `the agent is still working and still owes the turn while frozen (state=${JSON.stringify(held.state)}, turn_owed=${JSON.stringify(held.turn_owed)})`,
        held,
      );
      // What the wire says is only half of it. The tile is where the user finds
      // out a turn is about to close, so the freeze has to be drawn, not just
      // broadcast.
      const chip = (await client.request('get_session_ui_state', { sessionId: agentId })).settling;
      runner.assert(
        chip && chip.held === true && chip.frozenBar === true && chip.text.toLowerCase().includes('paused'),
        `the pane header says the countdown is paused and stops animating (${JSON.stringify(chip)})`,
        chip,
      );
      note('countdown frozen by typing', { state: held.state, turn_owed: held.turn_owed, chip });
    });

    await runner.step('the_freeze_outlives_continued_typing', async () => {
      // Past the quiet window twice over, with the gaps a person leaves between
      // words. If the quiet check resumed instead of re-holding, the deadline
      // would be back before this loop ends — under the user's hands, which is
      // the bug this feature exists to prevent.
      const deadline = Date.now() + QUIET_WINDOW_MS * 2.5;
      let keystrokes = 0;
      while (Date.now() < deadline) {
        await delay(2_000);
        await typeLikeAPerson(client, agentId, agentPaneId, ' more');
        keystrokes += 1;
        const session = observer.getSession(agentId);
        runner.assert(
          session?.auto_settle_held === true && !session?.auto_settle_fires_at,
          `the countdown is still frozen while the user keeps typing (held=${JSON.stringify(session?.auto_settle_held)}, firesAt=${JSON.stringify(session?.auto_settle_fires_at)})`,
          session,
        );
      }
      note('freeze survived continued typing', { keystrokes, forMs: QUIET_WINDOW_MS * 2.5 });
    });

    await runner.step('going_quiet_hands_back_a_whole_countdown', async () => {
      // An agent that finished its work while we were typing takes its pending
      // settle with it — leaving `working` cancels one outright, hold or no hold.
      // That is correct product behavior and a useless run, so it is separated
      // from the assertion below rather than folded into a timeout.
      const resumed = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          if (session?.auto_settle_fires_at) return session;
          if (session && session.state !== 'working') {
            throw new Error(`the agent stopped working (state=${session.state}) before the hold could release; its steering task was too short for this run to say anything`);
          }
          return null;
        },
        'the countdown to come back after the user stopped typing',
        QUIET_WINDOW_MS * 4,
      );
      runner.assert(
        !resumed.auto_settle_held,
        `the frozen flag is gone once the countdown is running again (got auto_settle_held=${JSON.stringify(resumed.auto_settle_held)})`,
        resumed,
      );
      const remainingMs = Date.parse(resumed.auto_settle_fires_at) - Date.now();
      // The frozen bar is drawn full, so the countdown it releases into is a
      // whole one. A remainder would drop the bar the instant typing stopped —
      // and by now the original 60s deadline is long past.
      runner.assert(
        remainingMs > (COUNTDOWN_SECONDS - 10) * 1_000,
        `the resumed countdown is a whole one, not the remainder of the frozen one (${remainingMs}ms of ${COUNTDOWN_SECONDS}s)`,
        { remainingMs, frozenDeadline, resumedDeadline: resumed.auto_settle_fires_at },
      );
      const resumedChip = (await client.request('get_session_ui_state', { sessionId: agentId })).settling;
      runner.assert(
        resumedChip && resumedChip.held === false && resumedChip.frozenBar === false,
        `the pane header is animating again once the countdown resumed (${JSON.stringify(resumedChip)})`,
        resumedChip,
      );
      note('countdown resumed whole', { remainingMs, chip: resumedChip });
    });

    await runner.step('automation_writes_do_not_freeze_it', async () => {
      // attn typing on the user's behalf — a nudge, a delegation brief, a
      // harness driving a pane — must not hold a countdown open. Same pane, same
      // daemon command, different source tag.
      const before = observer.getSession(agentId)?.auto_settle_fires_at;
      await client.request('write_pane', { sessionId: agentId, paneId: agentPaneId, text: ' automated', submit: false });
      await delay(2_000);
      const after = observer.getSession(agentId);
      runner.assert(
        after?.auto_settle_held !== true,
        `an automation write did not freeze the countdown (got auto_settle_held=${JSON.stringify(after?.auto_settle_held)})`,
        after,
      );
      runner.assert(
        after?.auto_settle_fires_at === before,
        `the deadline is untouched by an automation write (${JSON.stringify(before)} -> ${JSON.stringify(after?.auto_settle_fires_at)})`,
        after,
      );
      note('automation write left the countdown alone', { firesAt: after?.auto_settle_fires_at });
    });

    const summary = runner.finishSuccess({ agentId });
    console.log('[settle-typing-hold] PASS — typing froze the countdown, kept it frozen, and going quiet handed back a whole one.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { agentId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {});
    if (agentId) await client.request('close_session', { sessionId: agentId }).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
