#!/usr/bin/env node
//
// Seed doorbells through the packaged app and real PTYs:
//
//   - a dispatcher delegates work and is automatically in the audience;
//   - a ringing note from the delegate reaches the dispatcher's pane;
//   - reading the seed resets the one-bell coalescing gate;
//   - the delegate's harvest reaches the dispatcher as a second nudge.
import path from 'node:path';
import fs from 'node:fs';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { waitForFirstWorkspacePane } from './scenarioAssertions.mjs';
import { delay } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { recordingEnabled } from './windowRecording.mjs';
import { preTrustClaudeFolder } from './scenarioAgents.mjs';

const RINGING_NOTE = 'RING7';
const BRIEF = `Live proof only. Read your assigned seed id from this prompt, run attn seed note SEED_ID -m ${RINGING_NOTE} --ring, then wait. Do not harvest until you are told.`;
const PACE_MS = recordingEnabled() ? 1_400 : 0;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function flat(text) {
  return text.replace(/\n/g, '');
}

function squash(text) {
  return text.replace(/\s+/g, '');
}

function saw(haystack, needle) {
  return squash(haystack).includes(squash(needle));
}

function occurrences(haystack, needle) {
  let count = 0;
  let at = haystack.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = haystack.indexOf(needle, at + needle.length);
  }
  return count;
}

let marks = 0;

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = flat(await paneText(client, pane));
    if (occurrences(text, mark) >= 2) {
      const first = text.indexOf(mark) + mark.length;
      const out = text.slice(first, text.lastIndexOf(mark));
      if (saw(out, expected)) return out;
      throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${out}`);
    }
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
}

async function waitForPane(client, pane, expected, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    text = flat(await paneText(client, pane));
    if (saw(text, expected)) return text;
    await delay(250);
  }
  throw new Error(`pane never received ${JSON.stringify(expected)}:\n${text}`);
}

async function openDispatcher(client, observer, runner) {
  const cwd = path.join(runner.sessionDir, 'dispatcher');
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label: 'seed-bell-dispatcher', agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, 'dispatcher pane', 20_000);
  return { sessionId, paneId: pane.paneId };
}

function seedIDs(text) {
  return [...flat(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-nudges');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedNudges',
    tier: 'local',
    prefix: 'garden-seed-nudges',
  });

  let dispatcher = null;
  let delegated = null;
  let seed = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    dispatcher = await runner.step('open_dispatcher', () => openDispatcher(client, observer, runner));

    delegated = await runner.step('dispatch_delegate', async () => {
      const known = new Set(observer.sessionsById.keys());
      preTrustClaudeFolder(path.join(runner.sessionDir, 'dispatcher'));
      await client.request('write_pane', {
        ...dispatcher,
        text: `attn delegate --agent claude --model claude-haiku-4-5 ` +
          `--yolo --no-worktree --source-session ${dispatcher.sessionId} ` +
          `--name seed-bell --brief "${BRIEF}"`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await runInPane(client, dispatcher, 'true', '');
      return spawned;
    });

    seed = await runner.step('resolve_delegated_seed', async () => {
      const listed = await runInPane(client, dispatcher, 'attn seed ls', delegated);
      for (const candidate of new Set(seedIDs(listed))) {
        const shown = await runInPane(client, dispatcher, `attn seed show ${candidate}`, '');
        if (saw(shown, delegated) && saw(shown, BRIEF)) return candidate;
      }
      throw new Error(`no seed belonged to delegate ${delegated}:\n${listed}`);
    });

    await runner.step('ring_note_reaches_dispatcher', async () => {
      const text = await waitForPane(client, dispatcher, `${seed} moved: note`, 60_000);
      const delivered = text.slice(text.lastIndexOf(`🔔 ${seed} moved: note`));
      runner.assert(!saw(delivered, RINGING_NOTE), 'the doorbell carries no note content', { delivered });
      runner.writeText('note-doorbell.txt', text + '\n');
      if (PACE_MS > 0) await delay(PACE_MS);
    });

    await runner.step('read_resets_then_harvest_rings', async () => {
      // Harness shell sessions do not carry ATTN_SESSION_ID, so name the
      // dispatcher explicitly just as the delegation command above does.
      await runInPane(client, dispatcher,
        `attn seed show ${seed} --session ${dispatcher.sessionId}`, RINGING_NOTE);
      await runInPane(client, dispatcher,
        `attn agent msg ${seed} "Now harvest your assigned seed with reason: the live doorbell proof is done" ` +
          `--source-session ${dispatcher.sessionId}`,
        'delivered');
      const text = await waitForPane(client, dispatcher, `${seed} moved: harvested`, 60_000);
      runner.assert(saw(text, `${seed} moved: note`), 'the first doorbell remains visible', { text });
      runner.assert(saw(text, `${seed} moved: harvested`), 'the harvest rings after the read reset', { text });
      runner.writeText('both-doorbells.txt', text + '\n');
      if (PACE_MS > 0) await delay(PACE_MS);
    });

    const summary = runner.finishSuccess({ seed, delegated });
    console.log('[RealAppHarness] Garden seed nudges passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { seed, delegated });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, dispatcher?.sessionId]) {
      if (id) await client.request('close_session', { sessionId: id }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
