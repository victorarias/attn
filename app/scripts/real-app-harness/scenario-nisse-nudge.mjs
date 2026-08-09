#!/usr/bin/env node

/**
 * Real-app scenario: nudging a conversation session, and the states it moves
 * through.
 *
 * A `nisse` session has no PTY, so every way attn interrupts an agent —
 * the composer's Steer button, a ticket doorbell, a Present notice — becomes a
 * verb down the host's own pipe. This scenario proves the two that matter, in
 * the packaged app, against a real agent:
 *
 *   1. a nudge to a session that is mid-run lands at the agent's next turn
 *      boundary, and the pane shows the whole arc: the message sits in the
 *      queue strip (sent, not yet read), then leaves it as the user message it
 *      delivered appears in the transcript, then the reply answers it,
 *   2. the same verb sent to an idle session starts a run instead of
 *      disappearing — which is what makes a doorbell safe to ring at any time.
 *
 * Alongside both, the session's own state is asserted from the daemon: working
 * while a run is open, idle when it settles, and a turn owed to the user at the
 * end, exactly like a PTY agent that stopped.
 *
 * The idle nudge is sent as a raw `agent_prompt` with `mode: steer` because
 * that is precisely what the daemon's doorbell does — the composer never sends
 * steer while idle, so driving the UI could not reach this path.
 *
 * Prereqs: a non-production profile install with the attn-pi plugin installed
 * (`attn plugin install-bundled attn-pi`) and pi credentials in ~/.pi.
 */
import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

// Composer selectors are pane-scoped. The app mounts a pane per conversation
// session, so a bare [data-testid="conversation-input"] resolves to whichever
// one is first in the DOM — and a run that types into another session's
// composer looks exactly like a host that never got the prompt.
const paneOf = (sessionId) => `[data-testid="conversation-pane-${sessionId}"]`;
const INPUT = (sessionId) => `${paneOf(sessionId)} [data-testid="conversation-input"]`;
const SEND = (sessionId) => `${paneOf(sessionId)} [data-testid="conversation-send"]`;

// Long enough that the steer is provably queued rather than racing the reply,
// short enough that the run settles well inside the scenario timeout.
const HOLD_PROMPT = 'Run the bash command `sleep 25`. When it finishes, reply with exactly one word: alpha';
const STEER_TEXT = 'Change of plan: when you reply, use exactly one word: bravo';
const IDLE_NUDGE_TEXT = 'Reply with exactly one word: charlie';

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

function conversationState(client, sessionId) {
  return client.request('conversation_get_state', { sessionId }, { timeoutMs: 20_000 }).catch(() => null);
}

function hasReply(state, word) {
  return (state?.messages || []).some(
    (message) => message.role === 'assistant' && !message.streaming && message.text.toLowerCase().includes(word),
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-nisse-nudge.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the nisse nudge scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-NUDGE',
    tier: 'tier2-local-real-agent',
    prefix: 'nisse-nudge',
    metadata: { agent: 'nisse', focus: 'steer mid-run, nudge an idle session, session state and turn' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'nisse-nudge-repo');
      fs.mkdirSync(dir, { recursive: true });
      execFileSync('git', ['init', '-q'], { cwd: dir });
      execFileSync('git', ['commit', '-q', '--allow-empty', '-m', 'init'], {
        cwd: dir,
        env: {
          ...process.env,
          GIT_AUTHOR_NAME: 'attn',
          GIT_AUTHOR_EMAIL: 'attn@local',
          GIT_COMMITTER_NAME: 'attn',
          GIT_COMMITTER_EMAIL: 'attn@local',
        },
      });
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_conversation_session', async () => {
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `nisse-nudge-${runner.runId.slice(-6)}`,
        agent: 'nisse',
      });
      sessionId = created.sessionId;
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      runner.registerCleanup('close_session', () => client.request('close_session', { sessionId }).catch(() => {}));

      await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.inputDisabled === false ? current : null;
        },
        'the conversation composer to open (session_ready)',
        90_000,
      );
      // A host that has said it is ready but has never run declares itself idle,
      // and an idle session is one the user is owed a turn on.
      const session = await observer.waitFor(
        () => {
          const current = observer.getSession(sessionId);
          return current && current.state === 'idle' ? current : null;
        },
        'the session to reach idle on session_ready',
        30_000,
      );
      if (session.turn_owed !== true) {
        throw new Error(`a session sitting at its prompt owes a turn; turn_owed=${JSON.stringify(session.turn_owed)}`);
      }
      note('host is up, session is idle and owes a turn', { state: session.state });
    });

    await runner.step('run_opens_and_declares_working', async () => {
      await client.request('dom_type', { selector: INPUT(sessionId), text: HOLD_PROMPT });
      await client.request('dom_click', { selector: SEND(sessionId) });
      const session = await observer.waitFor(
        () => {
          const current = observer.getSession(sessionId);
          return current && current.state === 'working' ? current : null;
        },
        'the session to declare working when the run opens',
        60_000,
      );
      // Enter is a steer for as long as the run is open — the composer stays
      // usable, which is the whole point of the state living on the wire.
      const pane = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.inputDisabled === false && current.sendLabel === 'Steer' ? current : null;
        },
        'the composer to reopen as a steer while the run is live',
        60_000,
      );
      note('run is open and the composer offers a steer', { state: session.state, followUp: pane.followUpAvailable });
    });

    const { queued } = await runner.step('steer_lands_at_the_turn_boundary', async () => {
      await client.request('dom_type', { selector: INPUT(sessionId), text: STEER_TEXT });
      await client.request('dom_click', { selector: SEND(sessionId) });

      // Queued: pi has it, the agent has not read it. It sits here for as long
      // as the agent stays inside its tool call.
      const withQueue = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && (current.queued || []).length > 0 ? current : null;
        },
        'the steer to appear in the pane\'s queue strip',
        60_000,
      );
      const entry = withQueue.queued[0];
      if (!entry.text.includes('bravo')) {
        throw new Error(`the queue strip shows ${JSON.stringify(entry)}, not the steer that was sent`);
      }

      // Seen: pi drains the queue at the agent's next turn boundary and the
      // message becomes part of the transcript.
      const delivered = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          if (!current || (current.queued || []).length > 0) return null;
          const read = current.messages.some(
            (message) => message.role === 'user' && message.text.includes('bravo'),
          );
          return read ? current : null;
        },
        'the queue strip to drain into a delivered user message',
        180_000,
      );
      note('steer was queued, then read at the turn boundary', {
        label: entry.kind,
        messages: delivered.messages.length,
      });
      return { queued: entry };
    });

    await runner.step('run_settles_idle_and_owes_a_turn', async () => {
      const settled = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          if (!current || current.sendLabel !== 'Send') return null;
          return hasReply(current, 'bravo') ? current : null;
        },
        'the run to settle with a reply that obeyed the steer',
        180_000,
      );
      const session = await observer.waitFor(
        () => {
          const current = observer.getSession(sessionId);
          return current && current.state === 'idle' ? current : null;
        },
        'the session to declare idle when the run settles',
        60_000,
      );
      if (session.turn_owed !== true) {
        throw new Error(`a settled run owes the user a turn; turn_owed=${JSON.stringify(session.turn_owed)}`);
      }
      if (settled.followUpAvailable) {
        throw new Error('the follow-up button outlived the run it belonged to');
      }
      fs.writeFileSync(
        path.join(runner.runDir, 'conversation.json'),
        `${JSON.stringify({ queued, messages: settled.messages, session }, null, 2)}\n`,
        'utf8',
      );
      note('run settled idle with a turn owed', { state: session.state, messages: settled.messages.length });
    });

    await runner.step('nudge_on_idle_starts_a_run', async () => {
      // Exactly what typeDoorbell sends: the steer verb at a session with no
      // run open. The host resolves it into a fresh run rather than dropping it.
      observer.send({ cmd: 'agent_prompt', id: sessionId, text: IDLE_NUDGE_TEXT, mode: 'steer' });
      const session = await observer.waitFor(
        () => {
          const current = observer.getSession(sessionId);
          return current && current.state === 'working' ? current : null;
        },
        'a steer at an idle session to open a run',
        60_000,
      );
      const answered = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && hasReply(current, 'charlie') ? current : null;
        },
        'the nudged run to answer',
        180_000,
      );
      note('a nudge at an idle session started a run and it answered', {
        state: session.state,
        messages: answered.messages.length,
      });
    });

    await runner.finishSuccess({ sessionId });
  } catch (error) {
    await runner.finishFailure(error, { sessionId });
    throw error;
  } finally {
    // The app and the observer socket outlive the assertions, and an open
    // socket holds node's event loop open.
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
