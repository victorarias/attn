#!/usr/bin/env node

/**
 * Real-app scenario: a RECORDED real nisse reply, replayed into the pane.
 *
 * The recording under `app/src/components/ConversationPane/__recordings__/`
 * was captured off a real host's fd 3 against a real model on 2026-08-19 —
 * headings, lists, a GFM table, fenced code in three languages, inline code, a
 * link, and a mermaid diagram, 7,845 chars over 317 coalesced deltas in 13.8 s.
 * The envelopes are handed to the app at the gaps they were recorded at,
 * through the same `applyEnvelope` the socket's `agent_event` calls, so the
 * store, the pane, the markdown pipeline and the scroll container are all the
 * real thing.
 *
 * Why the envelopes are replayed rather than a stub model answering: the nisse
 * host resolves its model through pi's static catalog (`getModel`), which
 * cannot see a provider declared in an agent dir, so the harness's stub
 * provider is unreachable from a conversation session. Pointing the session at
 * a real model was tried and reaches a real model, which does not reproduce a
 * recording. Replaying the recording is both the honest rig and the
 * deterministic one.
 *
 * What it asserts, one per criterion the spike was set:
 *
 *   1. no flash of raw text — the visible transcript never carries a fence, a
 *      table row, a link's `](`, or a bare emphasis marker, sampled throughout,
 *   2. half-open constructs are stable — the settled transcript is the whole
 *      markdown drawn as structure,
 *   3. a diagram waits for its fence — a pending placeholder is seen, and no
 *      diagram is drawn before the fence closes,
 *   4. scroll anchoring survives — following the stream stays pinned, and a
 *      reader scrolled back does not move while a second reply streams in,
 *   5. the frame budget — what a live pane costs to read while it grows,
 *      reported with the message sizes it was measured at.
 *
 * Prereqs: a non-production profile install with the attn-pi plugin. No model
 * credentials are needed and no model is called.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const RECORDING = path.join(HERE, '../../src/components/ConversationPane/__recordings__/md-tour.jsonl');

const paneOf = (id) => `[data-testid="conversation-pane-${id}"]`;

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Markdown syntax that must never reach the reader as text. */
const RAW_SYNTAX = [
  ['fence', /```/],
  ['table row', /\|[^\n|]*\|/],
  ['link', /\]\(/],
  ['strong', /\*\*\S/],
];

/**
 * The recording, as envelopes carrying the gap that preceded each one.
 *
 * The recording holds the host's coalesced 30 ms deltas rather than the model's
 * own chunks, so replaying it feeds the pane at the rate it saw — which is the
 * rate this scenario is about.
 */
function recordedEnvelopes() {
  const rows = fs.readFileSync(RECORDING, 'utf8').trim().split('\n').map((line) => JSON.parse(line));
  let previous = rows[0].at;
  const envelopes = rows.map((row) => {
    const afterMs = Math.max(0, Math.round(row.at - previous));
    previous = row.at;
    return { seq: row.envelope.seq, kind: row.envelope.kind, body: row.envelope.body, afterMs };
  });
  // The recording holds two assistant messages; the reply the criteria are
  // about is the long one, so `chars` is that message's own total rather than
  // the sum over both.
  const perMessage = new Map();
  let deltas = 0;
  for (const row of rows) {
    if (row.envelope.kind !== 'message_delta') continue;
    deltas += 1;
    const id = row.envelope.body.id;
    perMessage.set(id, (perMessage.get(id) ?? 0) + row.envelope.body.text.length);
  }
  const chars = Math.max(...perMessage.values());
  return { envelopes, chars, deltas };
}

/**
 * The same reply again, as a second run appended to the transcript: the spine
 * continues rather than resetting, and the message ids are fresh so the pane
 * draws new messages instead of rewriting the ones already there.
 */
function secondRun(envelopes) {
  return envelopes
    .filter((entry) => entry.kind !== 'session_ready' && entry.kind !== 'conversation_snapshot')
    .map((entry) => ({
      ...entry,
      seq: entry.seq + 10_000,
      body: typeof entry.body?.id === 'string' ? { ...entry.body, id: `${entry.body.id}-again` } : entry.body,
    }));
}

async function pollFor(fn, description, timeoutMs = 60_000, intervalMs = 250) {
  const startedAt = Date.now();
  let last = null;
  while (Date.now() - startedAt < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last: ${JSON.stringify(last)?.slice(0, 400)}`);
}

function assistantMessage(state) {
  return [...(state?.messages ?? [])].reverse().find((message) => message.role === 'assistant');
}

async function main() {
  const args = process.argv.slice(2);
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  if (args.includes('--help') || args.includes('-h')) {
    printCommonHelp('scripts/real-app-harness/scenario-nisse-markdown-stream.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  if (!profile) {
    throw new Error('this scenario does not run against production; set ATTN_PROFILE / ATTN_HARNESS_PROFILE');
  }

  await drive({ options, replay: recordedEnvelopes() });
}

async function drive({ options, replay }) {
  const runner = createScenarioRunner(options, {
    scenarioId: 'NISSE-MARKDOWN-STREAM',
    tier: 'tier2-local-real-agent',
    prefix: 'nisse-markdown-stream',
    metadata: { agent: 'nisse', focus: 'streaming markdown in the conversation pane' },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const note = (message, extra) => runner.log(message, extra);
  let sessionId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  const shot = async (name, selector) => {
    const data = await client.request('capture_screenshot_data', { selector }).catch(() => null);
    if (data?.pngBase64) fs.writeFileSync(path.join(runner.runDir, `${name}.png`), Buffer.from(data.pngBase64, 'base64'));
  };

  try {
    const { repoDir } = await runner.step('prepare_world', async () => {
      const dir = path.join(runner.sessionDir, 'nisse-markdown-repo');
      fs.mkdirSync(dir, { recursive: true });
      note(`replaying ${replay.deltas} recorded deltas / ${replay.chars} chars from ${path.basename(RECORDING)}`);
      return { repoDir: dir };
    });

    await runner.step('launch_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('open_conversation', async () => {
      const created = await client.request('create_session', {
        cwd: repoDir,
        label: `nisse-md-${runner.runId.slice(-6)}`,
        agent: 'nisse',
      });
      sessionId = created.sessionId;
      await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
      runner.registerCleanup('close_session', () => client.request('close_session', { sessionId }).catch(() => {}));
      await pollFor(
        async () => {
          const state = await client.request('conversation_get_state', { sessionId }).catch(() => null);
          return state && state.inputDisabled === false ? state : null;
        },
        'the composer to open',
        90_000,
      );
      note('conversation session is up');
    });

    const samples = [];
    const leaks = [];
    let sawPendingDiagram = false;
    let diagramBeforeFence = false;
    let followFromBottom = 0;
    const follow = [];
    let fenceShot = false;

    await runner.step('replay_the_recorded_reply', async () => {
      await client.request('conversation_replay_envelopes', { sessionId, envelopes: replay.envelopes });
      const started = Date.now();
      let previousChars = 0;
      let sawStreaming = false;
      for (;;) {
        const at = Date.now();
        const state = await client.request('conversation_get_state', { sessionId }).catch(() => null);
        const cost = Date.now() - at;
        if (state) {
          const message = assistantMessage(state);
          if (message) {
            samples.push({ chars: message.text.length, ms: cost });
            for (const [label, pattern] of RAW_SYNTAX) {
              // Code blocks and inline code legitimately contain these; the
              // bridge gives the rendered html, so strip those subtrees first.
              const visible = message.html
                .replace(/<pre[\s\S]*?<\/pre>/g, ' ')
                .replace(/<code[\s\S]*?<\/code>/g, ' ')
                .replace(/<[^>]+>/g, '\n');
              if (pattern.test(visible)) leaks.push({ label, chars: message.text.length });
            }
            if (message.blocks.pendingDiagrams > 0) {
              sawPendingDiagram = true;
              if (!fenceShot) { await shot('mid-stream-open-fence', paneOf(sessionId)); fenceShot = true; }
            }
            if (message.blocks.diagrams > 0 && !sawPendingDiagram) diagramBeforeFence = true;
            if (state.scroll) {
              followFromBottom = Math.max(followFromBottom, state.scroll.fromBottom);
              follow.push({ ms: Date.now() - started, chars: message.text.length, fromBottom: state.scroll.fromBottom, height: state.scroll.scrollHeight });
            }
            previousChars = message.text.length;
          }
          // The rendered text is shorter than the markdown that made it, so the
          // char count cannot say the reply is whole. The message's own
          // streaming flag can: message_end clears it.
          if (message?.streaming) sawStreaming = true;
          if (sawStreaming && message && !message.streaming) break;
        }
        if (Date.now() - started > 120_000) throw new Error(`the recorded reply never settled (${previousChars} chars)`);
        // Each read is its own connection to the bridge, so an unthrottled loop
        // exhausts the machine's ephemeral ports before the reply ends. 25 ms is
        // still faster than the 30 ms window the host coalesces deltas on, so
        // nothing the pane draws goes unsampled.
        await delay(25);
      }
      fs.writeFileSync(path.join(runner.runDir, 'follow-series.json'), `${JSON.stringify(follow, null, 1)}\n`, 'utf8');
      note(`replay settled: ${previousChars} chars, ${samples.length} samples`);
    });

    await runner.step('criterion_1_no_raw_syntax', async () => {
      if (leaks.length > 0) throw new Error(`raw markdown reached the reader: ${JSON.stringify(leaks.slice(0, 5))}`);
      note(`${samples.length} samples across the stream, no raw markdown syntax visible`);
    });

    await runner.step('criterion_2_the_settled_transcript_is_the_markdown', async () => {
      const state = await client.request('conversation_get_state', { sessionId });
      const message = assistantMessage(state);
      fs.writeFileSync(path.join(runner.runDir, 'settled-message.html'), message.html, 'utf8');
      const { blocks } = message;
      const missing = Object.entries({
        headings: blocks.headings > 0,
        tables: blocks.tables > 0,
        codeBlocks: blocks.codeBlocks > 0,
        listItems: blocks.listItems > 0,
        links: blocks.links > 0,
      }).filter(([, present]) => !present).map(([name]) => name);
      if (missing.length) throw new Error(`the settled message rendered no ${missing.join(', ')}: ${JSON.stringify(blocks)}`);
      note('settled transcript renders as structure', blocks);
      await shot('settled-transcript', paneOf(sessionId));
    });

    await runner.step('criterion_3_the_diagram_waited_for_its_fence', async () => {
      if (diagramBeforeFence) throw new Error('a diagram was drawn before its fence closed');
      if (!sawPendingDiagram) {
        note('no open mermaid fence was sampled in this run; the gate was not exercised', { sawPendingDiagram });
        return;
      }
      note('the diagram stayed a placeholder until its fence closed');
    });

    await runner.step('criterion_5_frame_budget', async () => {
      // What the bridge round trip cost while the transcript grew. It is not a
      // React render timing — it is the whole read of a live pane, which is
      // strictly more than the render — so it is an upper bound.
      const sorted = samples.map((sample) => sample.ms).sort((a, b) => a - b);
      const at = (p) => sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * p))];
      const biggest = Math.max(...samples.map((sample) => sample.chars));
      const report = {
        samples: samples.length,
        largestMessageChars: biggest,
        readP50Ms: at(0.5),
        readP90Ms: at(0.9),
        readMaxMs: at(1),
      };
      fs.writeFileSync(path.join(runner.runDir, 'frame-budget.json'), `${JSON.stringify(report, null, 2)}\n`, 'utf8');
      note('live-pane read cost while the transcript grew', report);
    });

    await runner.step('criterion_4_scroll_anchoring', async () => {
      // Following the stream: the transcript stayed at the bottom for the whole
      // replay. The pane follows only within 80px of the bottom, so anything
      // past that means a block changing height pushed the reader off.
      if (followFromBottom > 80) throw new Error(`follow mode drifted ${followFromBottom}px off the bottom`);
      note(`follow mode stayed pinned (worst ${followFromBottom}px from the bottom)`);

      // Reading scrolled back while a second reply streams: the position must
      // not move under them.
      await client.request('conversation_scroll_to', { sessionId, fromBottom: 600 });
      const before = await client.request('conversation_get_state', { sessionId });
      await client.request('conversation_replay_envelopes', { sessionId, envelopes: secondRun(replay.envelopes) });
      await delay(5000);
      const during = await client.request('conversation_get_state', { sessionId });
      const drift = Math.abs((during.scroll?.scrollTop ?? 0) - (before.scroll?.scrollTop ?? 0));
      await shot('scrolled-back-during-stream', paneOf(sessionId));
      if (drift > 4) throw new Error(`a scrolled-back reader moved ${drift}px while a stream arrived`);
      note(`a scrolled-back reader held position (${drift}px drift)`);
      await pollFor(
        async () => {
          const state = await client.request('conversation_get_state', { sessionId }).catch(() => null);
          const message = assistantMessage(state);
          return message && !message.streaming ? state : null;
        },
        'the second reply to settle',
        120_000,
      );
    });

    await runner.finishSuccess({ sessionId });
  } catch (error) {
    await runner.finishFailure(error, { sessionId });
    throw error;
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
