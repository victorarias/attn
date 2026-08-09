#!/usr/bin/env node

/**
 * Real-app scenario: long conversations and old ones.
 *
 * Slice 5 of the pi headless host is about depth — a conversation that outgrew
 * one window, and a conversation from days ago. This scenario proves the three
 * things that make those first-class, in the packaged app against a real agent:
 *
 *   1. an existing conversation file can be picked up by a NEW session, and the
 *      agent answers from it — resume of an arbitrary file, not just revive,
 *   2. a transcript longer than the snapshot window draws its newest window and
 *      pages the rest in on demand, with nothing lost and nothing duplicated,
 *   3. another client attaching does NOT shorten what a window that has paged
 *      back is showing — the sharpest edge slice 4 left behind,
 *   4. the model can be switched mid-session, and the host's answer is what the
 *      picker shows.
 *
 * The long conversation is synthesized as a pi session file rather than talked
 * into existence: proving the window needs more items than the window holds,
 * and a thousand real turns is a thousand real API calls. The file is read by
 * pi's own SessionManager, so if pi cannot read it this scenario fails — which
 * is the right failure, and the same one a user resuming an old file would hit.
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
import { currentHarnessProfile, dataDirForProfile } from './harnessProfile.mjs';

// A word only a session that read the earlier conversation can produce.
const SECRET = 'zeppelin';

// How many message entries the synthetic conversation holds. The host's
// snapshot window is 500 items, so this is comfortably past it: enough that a
// page has somewhere to come from and a second page still has more behind it.
const LONG_CONVERSATION_ENTRIES = 1_200;

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const pane = (sessionId) => `[data-testid="conversation-pane-${sessionId}"]`;
const inputOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-input"]`;
const sendOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-send"]`;
const earlierOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-load-earlier"]`;
const modelOf = (sessionId) => `${pane(sessionId)} [data-testid="conversation-model"]`;

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

async function sendPrompt(client, sessionId, text) {
  await client.request('dom_type', { selector: inputOf(sessionId), text });
  await client.request('dom_click', { selector: sendOf(sessionId) });
}

/**
 * The error row this run put in the transcript, if it put one there.
 *
 * `seen` is what was already on screen before the prompt: an earlier refusal
 * stays drawn, and it is not this run's news.
 */
const failureNotice = (state, seen = new Set()) => (state?.notices || [])
  .find((notice) => notice.level === 'error' && notice.done && !seen.has(notice.id));

const noticeIds = (state) => new Set((state?.notices || []).map((notice) => notice.id));

/**
 * Waits for a settled run whose answer contains `word`.
 *
 * A run that the provider refused settles too, and would otherwise burn the
 * whole timeout: the transcript now carries an error row saying why, so this
 * stops on it and reports the provider's own words.
 */
function settledWith(client, sessionId, word, description, { timeoutMs = 180_000, seenNotices = new Set() } = {}) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      if (!current) return null;
      const failed = failureNotice(current, seenNotices);
      if (failed) throw new Error(`the agent could not answer while waiting for: ${description}. ${failed.text}`);
      if (current.sendLabel !== 'Send') return null;
      const reply = (current.messages || []).find(
        (message) => message.role === 'assistant' && !message.streaming
          && message.text.toLowerCase().includes(word),
      );
      return reply ? current : null;
    },
    description,
    timeoutMs,
  );
}

function composerOpen(client, sessionId, description, timeoutMs = 120_000) {
  return pollFor(
    async () => {
      const current = await conversationState(client, sessionId);
      return current && current.inputDisabled === false ? current : null;
    },
    description,
    timeoutMs,
  );
}

/** The session file pi wrote for this attn session, once it has written one. */
function sessionFileFor(dataDir, sessionId, timeoutMs = 90_000) {
  const dir = path.join(dataDir, 'hosts', 'state', sessionId);
  return pollFor(
    async () => {
      if (!fs.existsSync(dir)) return null;
      const files = fs.readdirSync(dir).filter((name) => name.endsWith('.jsonl'));
      return files.length > 0 ? path.join(dir, files[0]) : null;
    },
    `a pi session file under ${dir}`,
    timeoutMs,
  );
}

/**
 * A pi session file holding a long finished conversation.
 *
 * Shapes are copied from a file pi itself wrote (v3): a session header, then
 * alternating user/assistant message entries chained by parentId. What matters
 * for this scenario is only that pi can read it and the host can rebuild a
 * transcript from it — the words are filler, and say so.
 */
function writeLongConversation(file, entries) {
  const started = Date.UTC(2026, 0, 1);
  const lines = [JSON.stringify({
    type: 'session',
    version: 3,
    id: '019fd879-0000-7000-8000-00000000cafe',
    timestamp: new Date(started).toISOString(),
    cwd: path.dirname(file),
  })];
  let parentId = null;
  for (let index = 0; index < entries; index += 1) {
    const id = `syn${String(index).padStart(6, '0')}`;
    const role = index % 2 === 0 ? 'user' : 'assistant';
    const timestamp = started + index * 1_000;
    const text = role === 'user'
      ? `Filler question ${index / 2 + 1} in a synthesized conversation.`
      : `Filler answer ${Math.ceil(index / 2)}.`;
    lines.push(JSON.stringify({
      type: 'message',
      id,
      parentId,
      timestamp: new Date(timestamp).toISOString(),
      message: {
        role,
        content: [{ type: 'text', text }],
        ...(role === 'assistant' ? { provider: 'synthetic', model: 'synthetic', stopReason: 'stop' } : {}),
        timestamp,
      },
    }));
    parentId = id;
  }
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${lines.join('\n')}\n`);
  return { file, entries, bytes: fs.statSync(file).size };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-nisse-history.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('the nisse history scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE to a named profile');
  }
  const dataDir = dataDirForProfile(profile);

  const runner = createScenarioRunner(options, {
    scenarioId: 'PI-HOST-HISTORY',
    tier: 'tier2-local-real-agent',
    prefix: 'nisse-history',
    metadata: {
      agent: 'nisse',
      focus: 'resume an existing conversation file, page a long transcript, survive another client attaching, switch model mid-session',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  const sessionIds = [];

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repoDir } = await runner.step('create_repo_fixture', async () => {
      const dir = path.join(runner.sessionDir, 'nisse-history-repo');
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

    const { originalFile } = await runner.step('hold_a_conversation_worth_resuming', async () => {
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `nisse-history-${runner.runId.slice(-6)}`,
        agent: 'nisse',
      });
      sessionIds.push(created.sessionId);
      await observer.waitForSession({ id: created.sessionId, timeoutMs: 30_000 });
      await composerOpen(client, created.sessionId, 'the first conversation to open');

      await sendPrompt(client, created.sessionId, `Remember this word: ${SECRET}. Reply with exactly one word: alpha`);
      await settledWith(client, created.sessionId, 'alpha', 'the first conversation to settle');
      const file = await sessionFileFor(dataDir, created.sessionId);
      note('a conversation exists on disk to pick up later', { file });
      return { originalFile: file };
    });

    await runner.step('the_model_switches_mid_session_on_the_host_word', async () => {
      const sessionId = sessionIds[0];
      const before = await conversationState(client, sessionId);
      const alternatives = (before.models || []).filter((name) => name !== before.model);
      if (alternatives.length === 0) {
        // Only one model is authenticated on this machine. That is a real
        // configuration, not a failure — record it and skip rather than assert
        // against a picker with nothing to pick.
        runner.writeJson('model-switch.json', { skipped: 'only one model available', state: before });
        note('model switching not exercised: this machine offers one model', { model: before.model });
        return;
      }
      // Any alternative proves the switch, so prefer a small one — this step
      // runs a real prompt on whatever it picks. `getAvailable` answers from
      // pi's catalog and this machine's credentials, neither of which knows
      // that a provider retired a model: the first candidate came back 404 on
      // 2026-08-09. So candidates are TRIED, and a refusal moves to the next
      // rather than failing the run — what is being proved is that the switch
      // takes effect, not that every model in the catalog still exists.
      //
      // Ordering: the provider currently answering is the one this machine
      // demonstrably works with, so its models come first; a small one within
      // that, since this step runs a real prompt on whatever it picks.
      const providerOf = (name) => name.split('/')[0];
      const working = providerOf(before.model);
      const small = /haiku|mini|nano|flash|small|lite/i;
      const rank = (name) => (providerOf(name) === working ? 0 : 2) + (small.test(name) ? 0 : 1);
      const candidates = [...alternatives].sort((a, b) => rank(a) - rank(b)).slice(0, 4);
      const refused = [];
      let landed = null;
      for (const target of candidates) {
        await client.request('dom_select', { selector: modelOf(sessionId), value: target });
        const switched = await pollFor(
          async () => {
            const current = await conversationState(client, sessionId);
            return current && current.model === target ? current : null;
          },
          `the host to confirm the switch to ${target}`,
          60_000,
        );
        // The run after the switch is what proves it took. An earlier
        // candidate's refusal is still drawn, so this run's news is only what
        // was not on screen when it started.
        const seenNotices = noticeIds(switched);
        await sendPrompt(client, sessionId, 'Reply with exactly one word: bravo');
        try {
          const settled = await settledWith(client, sessionId, 'bravo', `a run on ${target}`, { timeoutMs: 120_000, seenNotices });
          landed = { from: before.model, to: target, refused, models: switched.models.length, state: settled };
          break;
        } catch (error) {
          const failed = failureNotice(await conversationState(client, sessionId), seenNotices);
          if (!failed) throw error;
          // The provider refused, and the pane said so rather than going quiet.
          // That row is itself part of what this slice ships.
          refused.push({ model: target, reason: failed.text });
          note('a model the catalog offers was refused by its provider, and the pane said so', { model: target, reason: failed.text });
        }
      }
      if (!landed) {
        throw new Error(`no offered model would answer: ${JSON.stringify(refused)}`);
      }
      runner.writeJson('model-switch.json', landed);
      note('the model switched mid-session and the next run used it', {
        from: landed.from,
        to: landed.to,
        refusedFirst: refused.length,
        models: landed.models,
      });
    });

    await runner.step('a_new_session_picks_up_the_old_conversation', async () => {
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `nisse-resume-${runner.runId.slice(-6)}`,
        agent: 'nisse',
        resume_conversation_file: originalFile,
      });
      sessionIds.push(created.sessionId);
      await observer.waitForSession({ id: created.sessionId, timeoutMs: 30_000 });
      await client.request('select_session', { sessionId: created.sessionId });
      const opened = await composerOpen(client, created.sessionId, 'the resumed conversation to open');
      if (!(opened.messages || []).some((message) => message.text.includes(SECRET))) {
        throw new Error(`the resumed session drew none of the conversation it forked: ${JSON.stringify(opened.messages)}`);
      }

      // The load-bearing assertion: the AGENT has the history, not just the
      // pane. Only a session that actually forked the file can answer this.
      await sendPrompt(client, created.sessionId, 'What word did I ask you to remember? Reply with only that word.');
      const settled = await settledWith(client, created.sessionId, SECRET, 'the resumed agent to answer from the conversation it picked up');

      // A fork, not an append: the file it came from is untouched, so the
      // session that owns it is still safe to keep talking to.
      const forked = await sessionFileFor(dataDir, created.sessionId);
      if (forked === originalFile) {
        throw new Error('the resumed session is writing to the file it resumed; the source conversation would be corrupted');
      }
      runner.writeJson('resumed-conversation.json', { originalFile, forked, state: settled });
      note('a new session picked up an existing conversation without writing to it', {
        originalFile,
        forked,
        messages: settled.messages.length,
      });
    });

    await runner.step('a_transcript_past_the_window_pages_the_rest_in', async () => {
      const synthetic = writeLongConversation(
        path.join(runner.sessionDir, 'long-conversation', 'synthetic.jsonl'),
        LONG_CONVERSATION_ENTRIES,
      );
      runner.writeJson('synthetic-conversation.json', synthetic);

      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `nisse-long-${runner.runId.slice(-6)}`,
        agent: 'nisse',
        resume_conversation_file: synthetic.file,
      });
      const sessionId = created.sessionId;
      sessionIds.push(sessionId);
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      await client.request('select_session', { sessionId });

      const windowed = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.loadEarlierAvailable ? current : null;
        },
        'a transcript longer than the window, offering the rest',
        120_000,
      );
      const drawnAtFirst = windowed.messages.length;
      if (drawnAtFirst >= LONG_CONVERSATION_ENTRIES) {
        throw new Error(`the window drew the whole ${LONG_CONVERSATION_ENTRIES}-item transcript; nothing is being windowed`);
      }
      const oldestAtFirst = windowed.messages[0].id;

      const askedAt = Date.now();
      await client.request('dom_click', { selector: earlierOf(sessionId) });
      const paged = await pollFor(
        async () => {
          const current = await conversationState(client, sessionId);
          return current && current.messages.length > drawnAtFirst ? current : null;
        },
        'the page of older conversation to arrive',
        60_000,
      );
      const ids = paged.messages.map((message) => message.id);
      if (new Set(ids).size !== ids.length) {
        throw new Error('paging drew items the transcript already had');
      }
      if (ids[ids.length - 1] !== windowed.messages[windowed.messages.length - 1].id) {
        throw new Error('paging older history changed what the newest item is');
      }
      if (!ids.includes(oldestAtFirst) || ids.indexOf(oldestAtFirst) === 0) {
        throw new Error('the page did not land above the item it was anchored to');
      }
      // How long the reader waits between asking for older conversation and
      // seeing it, at this transcript length. It is polled, so it is an upper
      // bound — and an upper bound is what "scrolls smoothly" needs.
      const pagedInMs = Date.now() - askedAt;
      runner.writeJson('paging.json', {
        entries: LONG_CONVERSATION_ENTRIES,
        drawnAtFirst,
        drawnAfterPage: paged.messages.length,
        oldestAtFirst,
        oldestAfterPage: ids[0],
        pagedInMs,
      });
      note('a transcript past the window paged the rest in on demand', {
        entries: LONG_CONVERSATION_ENTRIES,
        drawnAtFirst,
        drawnAfterPage: paged.messages.length,
        pagedInMs,
      });

      // The edge slice 4 left behind: the snapshot is a BROADCAST replace, so
      // another client asking for one used to shorten what this window is
      // showing back to a single window. The observer is that other client.
      observer.send({ cmd: 'agent_attach', id: sessionId });
      await delay(3_000);
      const after = await conversationState(client, sessionId);
      if (after.messages.length < paged.messages.length) {
        throw new Error(
          `another client attaching shortened this window from ${paged.messages.length} to ${after.messages.length} messages`,
        );
      }
      runner.writeJson('multi-client.json', {
        beforeOtherClientAttached: paged.messages.length,
        afterOtherClientAttached: after.messages.length,
      });
      note('another client attaching left the paged-in scroll-back alone', {
        before: paged.messages.length,
        after: after.messages.length,
      });
    });

    await runner.finishSuccess({ sessionIds });
  } catch (error) {
    await runner.finishFailure(error, { sessionIds });
    throw error;
  } finally {
    for (const id of sessionIds) {
      await client.request('close_session', { sessionId: id }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
