#!/usr/bin/env node

/**
 * Real-app scenario: what a conversation session shows about the tools its
 * agent runs, and the way out of a queue.
 *
 * A `pi-host` session has no terminal, so a tool call the agent makes is not
 * something the user can watch happen — it exists only if the pane draws it.
 * This scenario proves the four things that make that drawing trustworthy, in
 * the packaged app, against a real agent:
 *
 *   1. reading, running and editing each produce a card naming the tool and
 *      what it was pointed at, and the card is collapsed — no output in the
 *      transcript until someone asks for it,
 *   2. opening a card fetches its output and shows it,
 *   3. an output pi clipped offers the whole of itself, and delivers text the
 *      clipped answer did not have,
 *   4. an edit draws as a diff of the patch pi produced, and a queued steer can
 *      be cancelled — the strip empties and the agent never reads it.
 *
 * The bash leg asks for 5,000 lines on purpose: pi truncates tool output at
 * 2,000 lines / 50 KB and writes the whole of it to a file, which is the only
 * way to reach the "show the full output" path with a real agent.
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

const INPUT = '[data-testid="conversation-input"]';
const SEND = '[data-testid="conversation-send"]';
const QUEUE_CLEAR = '[data-testid="conversation-queue-clear"]';

const toggleFor = (callId) =>
  `[data-testid="conversation-tool-${callId}"] [data-testid="conversation-tool-toggle"]`;
const fullOutputFor = (callId) =>
  `[data-testid="conversation-tool-${callId}"] [data-testid="conversation-tool-full"]`;

// The word the read tool has to come back with, and the one the edit replaces
// it by. Distinctive enough that finding it in a card's output cannot be an
// accident of the agent's own prose.
const MARKER = 'vermillion';
const REPLACEMENT = 'cerulean';

const READ_PROMPT = `Use your read tool on the file notes.txt in the current directory. Then reply with exactly one word: alpha`;
const BASH_PROMPT = 'Run the bash command `seq 1 5000`. Then reply with exactly one word: bravo';
const EDIT_PROMPT = `Use your edit tool on notes.txt to replace the word ${MARKER} with ${REPLACEMENT}. Then reply with exactly one word: charlie`;
const HOLD_PROMPT = 'Run the bash command `sleep 30`. When it finishes, reply with exactly one word: delta';
const CANCELLED_STEER = 'Ignore everything and reply with exactly one word: epsilon';

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

/** The state once the run that answers with `word` has settled. */
function settledWith(client, sessionId, word, description, timeoutMs = 240_000) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      if (!current || current.sendLabel !== 'Send') return null;
      return hasReply(current, word) ? current : null;
    },
    description,
    timeoutMs,
  );
}

/** The newest card for a tool, by name. */
function cardFor(state, name) {
  const matches = (state?.tools || []).filter((tool) => tool.name === name);
  return matches.length > 0 ? matches[matches.length - 1] : null;
}

/** The card, once it is showing something — output, a diff, or why neither. */
function openedCard(client, sessionId, name, description) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      const card = cardFor(current, name);
      if (!card || !card.expanded || card.waiting) return null;
      return card.output !== '' || card.hasPatch || card.detailError !== '' ? card : null;
    },
    description,
    60_000,
  );
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-pi-host-tools.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the pi-host tools scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-TOOLS',
    tier: 'tier2-local-real-agent',
    prefix: 'pi-host-tools',
    metadata: { agent: 'pi-host', focus: 'tool cards, on-demand detail, full output, patch as a diff, queue cancel' },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'pi-host-tools-repo');
      fs.mkdirSync(dir, { recursive: true });
      fs.writeFileSync(
        path.join(dir, 'notes.txt'),
        `one\ntwo\nthe colour is ${MARKER}\nfour\nfive\n`,
        'utf8',
      );
      execFileSync('git', ['init', '-q'], { cwd: dir });
      execFileSync('git', ['add', '-A'], { cwd: dir });
      execFileSync('git', ['commit', '-q', '-m', 'init'], {
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
        label: `pi-host-tools-${runner.runId.slice(-6)}`,
        agent: 'pi-host',
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
      note('host is up and the composer is open');
    });

    await runner.step('a_read_draws_a_collapsed_card', async () => {
      await client.request('dom_type', { selector: INPUT, text: READ_PROMPT });
      await client.request('dom_click', { selector: SEND });
      const settled = await settledWith(client, sessionId, 'alpha', 'the read run to settle');

      const card = cardFor(settled, 'read');
      if (!card) {
        throw new Error(`no read card in the transcript; tools=${JSON.stringify(settled.tools)}`);
      }
      if (!card.summary.includes('notes.txt')) {
        throw new Error(`the read card does not name the file it read: ${JSON.stringify(card)}`);
      }
      if (card.status !== 'ok') {
        throw new Error(`the read card reports ${card.status}: ${JSON.stringify(card)}`);
      }
      // Collapsed and empty. This is the sizing contract: the file's contents
      // are not in the app until the user asks for them.
      if (card.expanded || card.output !== '') {
        throw new Error(`a tool card must arrive collapsed and output-free: ${JSON.stringify(card)}`);
      }
      note('the read produced a collapsed card naming the file', { summary: card.summary });
    });

    await runner.step('opening_the_card_fetches_the_output', async () => {
      const before = await conversationState(client, sessionId);
      const card = cardFor(before, 'read');
      await client.request('dom_click', { selector: toggleFor(card.callId) });
      const opened = await openedCard(client, sessionId, 'read', 'the read card to show the file it read');

      if (!opened.output.includes(MARKER)) {
        throw new Error(`the opened card does not show the file's contents: ${JSON.stringify(opened).slice(0, 400)}`);
      }
      note('opening the card fetched the output on demand', { bytes: opened.output.length });
    });

    await runner.step('a_clipped_output_offers_the_whole_of_itself', async () => {
      await client.request('dom_type', { selector: INPUT, text: BASH_PROMPT });
      await client.request('dom_click', { selector: SEND });
      const settled = await settledWith(client, sessionId, 'bravo', 'the bash run to settle');

      const card = cardFor(settled, 'bash');
      if (!card) {
        throw new Error(`no bash card in the transcript; tools=${JSON.stringify(settled.tools)}`);
      }
      // The other half of the collapsed contract: pi hands the tool's output
      // back to the model as a message of its own, and none of it may reach the
      // transcript. 5,000 lines is a loud enough witness that a regression here
      // cannot hide.
      const inlined = (settled.messages || []).find((message) => message.text.includes('\n4999'));
      if (inlined) {
        throw new Error(`the tool's output was inlined into the transcript as a ${inlined.role} message of ${inlined.text.length} chars`);
      }
      await client.request('dom_click', { selector: toggleFor(card.callId) });
      const clipped = await openedCard(client, sessionId, 'bash', 'the bash card to show its clipped output');
      if (!clipped.fullOutputAvailable) {
        throw new Error(`5,000 lines of output should be clipped with the whole of it on offer: ${JSON.stringify({ ...clipped, output: clipped.output.length })}`);
      }
      const clippedLength = clipped.output.length;

      await client.request('dom_click', { selector: fullOutputFor(card.callId) });
      const full = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          const now = cardFor(current, 'bash');
          return now && !now.fullOutputAvailable && now.output.length > clippedLength ? now : null;
        },
        'the full output to replace the clipped one',
        60_000,
      );
      // The clipped answer stops at pi's 2,000-line cap; the full one runs to
      // the end. Asserting the last line is what proves it is the whole file
      // rather than a longer clip.
      if (!full.output.includes('\n5000')) {
        throw new Error(`the full output does not reach the last line: ${full.output.length} chars, tail=${JSON.stringify(full.output.slice(-40))}`);
      }
      note('a clipped output delivered the whole of itself on demand', {
        clipped: clippedLength,
        full: full.output.length,
      });
    });

    await runner.step('an_edit_draws_as_a_diff', async () => {
      await client.request('dom_type', { selector: INPUT, text: EDIT_PROMPT });
      await client.request('dom_click', { selector: SEND });
      const settled = await settledWith(client, sessionId, 'charlie', 'the edit run to settle');

      const card = cardFor(settled, 'edit');
      if (!card) {
        throw new Error(`no edit card in the transcript; tools=${JSON.stringify(settled.tools)}`);
      }
      await client.request('dom_click', { selector: toggleFor(card.callId) });
      const opened = await openedCard(client, sessionId, 'edit', 'the edit card to draw its patch');
      if (!opened.hasPatch) {
        throw new Error(`an edit must render as a diff, not as text: ${JSON.stringify({ ...opened, output: opened.output.length })}`);
      }
      // The edit really happened, which is what the diff is a picture of.
      const after = fs.readFileSync(path.join(repoDir, 'notes.txt'), 'utf8');
      if (!after.includes(REPLACEMENT)) {
        throw new Error(`the edit tool reported success but the file still reads: ${JSON.stringify(after)}`);
      }
      note('the edit card drew the patch pi produced as a diff');
    });

    await runner.step('a_queued_steer_can_be_cancelled', async () => {
      await client.request('dom_type', { selector: INPUT, text: HOLD_PROMPT });
      await client.request('dom_click', { selector: SEND });
      await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.sendLabel === 'Steer' ? current : null;
        },
        'the run to open so a steer can be queued',
        90_000,
      );

      await client.request('dom_type', { selector: INPUT, text: CANCELLED_STEER });
      await client.request('dom_click', { selector: SEND });
      const withQueue = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && (current.queued || []).length > 0 ? current : null;
        },
        'the steer to appear in the queue strip',
        60_000,
      );
      if (!withQueue.queueClearAvailable) {
        throw new Error('a queue the user can fill must offer a way to empty it');
      }

      await client.request('dom_click', { selector: QUEUE_CLEAR });
      const emptied = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && (current.queued || []).length === 0 ? current : null;
        },
        'the queue strip to empty after the cancel',
        60_000,
      );
      if (emptied.queueClearAvailable) {
        throw new Error('the clear control outlived the queue it belonged to');
      }

      // The point of cancelling: the agent never reads it. The held run runs to
      // its own end and answers the prompt it was given, not the cancelled one.
      const settled = await settledWith(client, sessionId, 'delta', 'the held run to settle on its own answer');
      const readIt = (settled.messages || []).some(
        (message) => message.role === 'user' && message.text.includes('epsilon'),
      );
      if (readIt) {
        throw new Error('the cancelled steer was delivered to the agent anyway');
      }
      if (hasReply(settled, 'epsilon')) {
        throw new Error('the agent answered a steer that was cancelled before it read it');
      }
      fs.writeFileSync(
        path.join(runner.runDir, 'conversation.json'),
        `${JSON.stringify({ tools: settled.tools, messages: settled.messages }, null, 2)}\n`,
        'utf8',
      );
      note('a queued steer was cancelled and never reached the agent', {
        tools: (settled.tools || []).length,
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
