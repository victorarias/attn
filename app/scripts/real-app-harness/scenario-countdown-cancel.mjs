#!/usr/bin/env node

/**
 * Real-app scenario: Cmd+. stops the countdown you can see.
 *
 * attn counts down to two things it does to an agent without being asked —
 * closing a turn the user steered (auto-settle) and doorbelling an agent about
 * pending ticket activity (the ticket nudge). Both indicators now advertise the
 * same key, and one command calls off whichever countdown is on screen.
 *
 * This scenario exists because the delivery of that keystroke is the part no
 * other test can reach. macOS claims Command-period system-wide as
 * `cancelOperation:` and consumes it before WKWebView dispatches any DOM
 * keydown, so attn claims it from a native menu item that re-dispatches into the
 * page. A unit test, a vitest DOM test, and `dispatch_shortcut` all enter below
 * that seam and would pass against a build where the real key does nothing —
 * which is exactly what shipped before this change, with the shortcut printed on
 * the settling indicator the whole time. Only a real CGEvent Cmd+. against the
 * packaged app can tell the difference.
 *
 * The run asserts, in one app lifecycle:
 *
 *   1. a real Cmd+. cancels an armed auto-settle on the visible agent, and the
 *      turn stays owed — the countdown was called off, not completed,
 *   1b. a second real Cmd+., with nothing counting down, undoes the standing
 *      dismissal that cancel left behind and the settle re-arms; this is the
 *      same delivery seam in the case the page used to unregister entirely,
 *   2. a real Cmd+. cancels an armed ticket nudge on a visible-but-unselected
 *      split pane, and the ticket stays unread — it calls off the interruption,
 *      not the message,
 *   3. that cancel survives selecting the session and coming back, which is the
 *      path that used to re-arm it, and
 *   4. genuinely new ticket activity arms it again — the cancel answers what was
 *      pending, not the ticket forever.
 *
 * Each cancel is checked against a countdown whose deadline is far away (60s for
 * auto-settle, the 30s nudge window), so "it expired on its own" is excluded:
 * an expired auto-settle would have settled the turn, and an expired nudge would
 * have doorbelled and cleared unread.
 *
 * Prereqs: `claude` on PATH; a built `./attn` (or ATTN_HARNESS_BIN); a
 * non-production profile install with the automation layer; Accessibility trust
 * for the input driver.
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
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';
import { preTrustClaudeFolder, ensureClaudePromptReadyViaPty } from './scenarioAgents.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

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

// Claude treats a fast multi-line write as a paste, so the submit has to be a
// lone carriage return a beat later.
async function submitPrompt(client, sessionId, paneId, text) {
  await client.request('write_pane', { sessionId, paneId, text, submit: false });
  await delay(600);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
}

// The one thing under test: a real Command-period through the packaged app,
// which only reaches the page if the native menu item is carrying it.
async function pressCancelCountdown(driver) {
  await driver.activateApp();
  await driver.pressKey('.', { command: true });
}

// Splits the workspace into a second (shell) pane and returns its session id.
// The split pane is a session of its own in the same workspace, which is the
// only arrangement where a nudge countdown is both running and on screen: the
// daemon pauses the nudge on the *selected* session.
async function splitIntoShellPane(client, sessionId) {
  const before = await client.request('get_workspace', { sessionId });
  const beforeIds = new Set((before.panes || []).map((pane) => pane.paneId));
  await client.request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
  const after = await waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace?.panes || []).length === beforeIds.size + 1
      && (workspace?.panes || []).every((pane) => pane.runtimeId),
    'the split pane to register a session',
    30_000,
  );
  const created = (after.panes || []).find((pane) => !beforeIds.has(pane.paneId));
  if (!created) {
    throw new Error(`No new pane appeared after the split. Before=${JSON.stringify(before)} After=${JSON.stringify(after)}`);
  }
  await waitForPaneVisible(client, sessionId, created.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, created.paneId, 20_000);
  return created;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-countdown-cancel.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the countdown-cancel scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const attnBin = resolveAttnBin();
  const runAttn = makeAttnRunner(attnBin, profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'COUNTDOWN-CANCEL',
    tier: 'tier3-local-agent',
    prefix: 'countdown-cancel',
    metadata: {
      agent: 'claude',
      focus: 'a real Cmd+. cancels the auto-settle and ticket-nudge countdowns that are on screen',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });
  const note = (message, extra) => runner.log(message, extra);

  let agentId = null;   // the booted claude agent — auto-settle target, then nudge target
  let agentPaneId = null;
  let authorId = null;  // the split shell — authors ticket activity, and holds selection

  runner.log('run context', { runDir: runner.runDir, sessionDir: runner.sessionDir, profile });

  // Cleanups run in reverse registration order, so the observer and app are
  // registered first to close last.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('restore_auto_settle', () =>
    client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' }).catch(() => {}));
  try {
    await runner.step('launch_app', async () => {
      // Pins the cheap launch model for every agent; see "Which Model a
      // Scenario Burns" in this directory's guide.
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
        label: `countdown-cancel-${runner.runId.slice(-6)}`,
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
      // precondition for the first leg rather than a claim of its own.
      const owed = await pollFor(
        () => (observer.getSession(agentId)?.turn_owed === true ? true : null),
        'the booted agent to owe a turn',
        90_000,
      );
      note('agent booted and owes a turn', { agentId, owed });
    });

    await runner.step('cancel_auto_settle_with_a_real_keystroke', async () => {
      // The arm window is at its floor so the countdown starts promptly; the
      // countdown window is deliberately long. A short one would let "it fired
      // on its own" explain the same observation, and this step is about which
      // event cleared it.
      await client.request('set_setting', { key: 'auto_settle_arm_seconds', value: '5' });
      await client.request('set_setting', { key: 'auto_settle_countdown_seconds', value: '60' });
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'true' });

      // Steering is what arms it, and the agent has to still be working when the
      // arm window elapses — leaving `working` cancels the settle by itself, and
      // an agent that stops before then has a classifier verdict pending, which
      // drops the arm too. The count is deliberately long: it has to outlast the
      // arm delay plus both keystrokes plus the second arm, which is ~15s of
      // working. Measured on the pinned haiku: 77 numbers a second (239 -> 1004
      // across ten seconds of one run), so 2000 buys ~26s. 200 ran dry in five.
      // Tool-free on purpose — a tool would ask for approval, and a pending
      // approval leaves `working`.
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
      const armed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the auto-settle countdown to arm on the visible agent',
        90_000,
      );
      const remainingMs = Date.parse(armed) - Date.now();
      note('auto-settle armed', { firesAt: armed, remainingMs });
      runner.assert(
        remainingMs > 20_000,
        `the countdown has far more than the keystroke needs left on it (${remainingMs}ms), so expiry cannot explain a cancel`,
      );

      await pressCancelCountdown(driver);

      await pollFor(
        () => (observer.getSession(agentId)?.auto_settle_fires_at ? null : true),
        'the real Cmd+. to cancel the armed auto-settle',
        15_000,
      );
      const after = observer.getSession(agentId);
      note('auto-settle cancelled by keystroke', { state: after?.state, turn_owed: after?.turn_owed });
      // The other way the deadline could have vanished is the agent leaving
      // `working`, which cancels a pending settle on its own. Still working
      // means the keystroke is the only thing that could have cleared it.
      runner.assert(
        after?.state === 'working',
        `the agent was still working when the countdown went away, so the keystroke cleared it (got state=${JSON.stringify(after?.state)})`,
        after,
      );
      // An auto-settle that completed would have closed the turn. Still owed is
      // what separates "called off" from "ran".
      runner.assert(
        after?.turn_owed === true,
        `the turn the user kept is still owed after the cancel (got turn_owed=${JSON.stringify(after?.turn_owed)})`,
        after,
      );
      // The cancel leaves a standing dismissal behind, and says so: without it
      // the next re-reported `working` re-arms what the user just called off,
      // and without the flag nothing on screen would admit the settle is off.
      runner.assert(
        after?.auto_settle_dismiss_armed === true,
        `the cancel left a standing dismissal on the wire (got auto_settle_dismiss_armed=${JSON.stringify(after?.auto_settle_dismiss_armed)})`,
        after,
      );

      // The second press is the delivery this scenario exists for, in its other
      // form: nothing is counting down now, so the page carries the shortcut only
      // because the standing dismissal is a thing worth pressing at. Before this
      // change App unregistered the handler whenever no countdown was visible,
      // and a real Cmd+. here reached nothing.
      await pressCancelCountdown(driver);

      await pollFor(
        () => (observer.getSession(agentId)?.auto_settle_dismiss_armed ? null : true),
        'the real Cmd+. to undo the standing dismissal',
        15_000,
      );
      // Undoing hands the session back to the ordinary rule with no state change
      // to trigger it — the agent has been working throughout — so a countdown
      // coming back is the whole proof that the disarm re-armed it.
      const rearmed = await pollFor(
        () => observer.getSession(agentId)?.auto_settle_fires_at || null,
        'the settle to re-arm after the dismissal was undone',
        60_000,
      );
      note('dismissal undone, settle re-armed', { firesAt: rearmed });

      // Off before the next leg so nothing can arm a second countdown behind it.
      await client.request('set_setting', { key: 'auto_settle_enabled', value: 'false' });
    });

    const ticketId = await runner.step('arm_a_nudge_on_the_visible_unselected_pane', async () => {
      // The agent creates the ticket, which makes it a participant and so the
      // session ticket activity is delivered to.
      const created = runAttn([
        'ticket', 'new',
        '--title', `Countdown cancel fixture ${runner.runId.slice(-6)}`,
        '--session', agentId,
        '--json',
      ]);
      const id = created.json?.ticket_id;
      runner.assert(typeof id === 'string' && id.length > 0, `ticket new returned an id (got ${JSON.stringify(created.json)})`, created.json);

      // A split gives the workspace a second session, so the agent's tile stays
      // on screen while something else holds the selection — the only shape in
      // which a nudge countdown actually runs where the user can see it.
      const splitPane = await splitIntoShellPane(client, agentId);
      authorId = splitPane.runtimeId;
      await client.request('focus_pane', { sessionId: agentId, paneId: splitPane.paneId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === authorId ? state : null;
      }, 'the split pane to take the selection off the agent', 20_000);
      note('split pane holds the selection; the agent tile is still on screen', { authorId });

      runAttn(['ticket', 'comment', id, '-m', 'Please take a look when you can.', '--session', authorId]);
      const armed = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          return session?.nudge_fires_at ? session : null;
        },
        'the ticket nudge to arm a countdown on the unselected agent',
        30_000,
      );
      note('nudge armed on the visible agent', { firesAt: armed.nudge_fires_at, unread: armed.ticket_unread });
      return id;
    });

    await runner.step('cancel_nudge_with_a_real_keystroke', async () => {
      await pressCancelCountdown(driver);

      await pollFor(
        () => (observer.getSession(agentId)?.nudge_fires_at ? null : true),
        'the real Cmd+. to cancel the armed nudge on the pane the user can see but is not in',
        15_000,
      );
      const after = observer.getSession(agentId);
      note('nudge cancelled by keystroke', { unread: after?.ticket_unread });
      // Stopping a nudge calls off the interruption, not the message: a nudge
      // that had fired instead would have doorbelled and drained unread.
      runner.assert(
        after?.ticket_unread === true,
        `the ticket is still unread after the cancel (got ticket_unread=${JSON.stringify(after?.ticket_unread)})`,
        after,
      );
    });

    await runner.step('the_cancel_survives_looking_at_the_session', async () => {
      // Selecting the agent and coming back is the path that re-armed a
      // cancelled nudge: the daemon re-evaluates the countdown on every
      // selection change. A cancel that a glance undoes is not a cancel.
      await client.request('select_session', { sessionId: agentId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === agentId ? state : null;
      }, 'the agent to take the selection', 20_000);
      await client.request('select_session', { sessionId: authorId });
      await pollFor(async () => {
        const state = await client.request('get_state');
        return state.activeSessionId === authorId ? state : null;
      }, 'the selection to go back to the split pane', 20_000);

      // Given time to re-arm and observed not to. The window it would have used
      // is 30s; a few seconds is enough to catch the immediate re-arm this
      // guards against, and the assertion below is the same field either way.
      await delay(4_000);
      const after = observer.getSession(agentId);
      runner.assert(
        !after?.nudge_fires_at,
        `the cancelled nudge stayed cancelled across a selection round trip (got nudge_fires_at=${JSON.stringify(after?.nudge_fires_at)})`,
        after,
      );
      runner.assert(
        after?.ticket_unread === true,
        `and the ticket is still there to come back to (got ticket_unread=${JSON.stringify(after?.ticket_unread)})`,
        after,
      );
      note('cancel survived a selection round trip');
    });

    await runner.step('new_ticket_activity_asks_again', async () => {
      // The cancel answers what was pending. Something that arrives afterwards
      // is new, and has to be able to reach the user — otherwise the keystroke
      // would be a permanent mute with no way out.
      runAttn(['ticket', 'comment', ticketId, '-m', 'One more thing, after the cancel.', '--session', authorId]);
      const rearmed = await pollFor(
        () => {
          const session = observer.getSession(agentId);
          return session?.nudge_fires_at ? session : null;
        },
        'genuinely new ticket activity to arm the nudge again',
        30_000,
      );
      note('new activity re-armed the nudge', { firesAt: rearmed.nudge_fires_at });
    });

    const summary = runner.finishSuccess({ agentId, authorId, ticketId });
    console.log('[countdown-cancel] PASS — a real Cmd+. cancelled both visible countdowns, and only what was pending.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { agentId, authorId });
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
