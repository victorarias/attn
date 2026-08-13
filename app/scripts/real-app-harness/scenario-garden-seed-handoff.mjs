#!/usr/bin/env node
//
// Garden slice 4's acceptance, against the packaged app and its own daemon.
//
// Session A tends a seed, leaves a handoff and ends; session B — a fresh
// errand — runs `attn seed tend` and the note is in its face before it does
// any work. Both are real app sessions, because the rule under test is "a
// tender whose session the daemon no longer knows has let go", and only a real
// session can stop being known.
//
// The commands run inside the panes, which is where an agent runs them: the
// app puts its own `attn` first on a session's PATH, so what a pane answers is
// this build talking to this profile's daemon. What the CLI prints — order,
// indentation, the handoff appearing once — is pinned in cmd/attn's unit
// tests; what this scenario is for is the crossing between two live sessions.
import path from 'node:path';
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
import fs from 'node:fs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

// The handoff body. Short enough to survive any pane width without wrapping,
// so finding it is finding the note rather than guessing at a line break.
const HANDOFF = 'PICKUP7 start at freshestHandoff';

function occurrences(haystack, needle) {
  let count = 0;
  let at = haystack.indexOf(needle);
  while (at !== -1) {
    count += 1;
    at = haystack.indexOf(needle, at + needle.length);
  }
  return count;
}

// The whole flow runs in about five seconds, which is unreadable in a clip. A
// recorded run holds each answer on screen long enough to read; an ordinary run
// pauses for nothing.
const PACE_MS = recordingEnabled() ? 1_400 : 0;

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
}

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

// runInPane types a command and waits for a *new* occurrence of what the
// command answers. New, not present: the pane still shows what the earlier
// commands printed, so "contains it" would pass before this command ran.
async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const before = occurrences(await paneText(client, pane), expected);
  await client.request('write_pane', { ...pane, text: command });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = await paneText(client, pane);
    if (occurrences(text, expected) > before) {
      await pace();
      return text;
    }
  }
  throw new Error(`pane never answered ${JSON.stringify(command)} with ${JSON.stringify(expected)}:\n${text}`);
}

async function openPane(client, observer, runner, label) {
  const cwd = path.join(runner.sessionDir, label);
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label, agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  return { sessionId, paneId: pane.paneId };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-handoff');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedHandoff',
    tier: 'local',
    prefix: 'garden-seed-handoff',
  });

  let paneA = null;
  let paneB = null;
  let seedID = null;
  let authorID = null;
  try {
    await launchFreshAppAndConnect(client, observer);

    paneA = await runner.step('open_session_a', () => openPane(client, observer, runner, 'gardenA'));

    seedID = await runner.step('a_plants_and_tends', async () => {
      // A shell pane carries no session identity of its own — attn withholds it
      // so an ordinary command cannot report against a session — so the tender
      // is named. An agent pane has ATTN_SESSION_ID and passes nothing.
      const planted = await runInPane(client, paneA,
        `attn seed plant "carry a seed across two sessions" --session ${paneA.sessionId}`, 's-');
      // plant answers with the id and nothing else, so the id is a line of its own.
      const ids = [...planted.matchAll(/^\s*(s-[a-z0-9]{5,})\s*$/gm)].map((match) => match[1]);
      const id = ids[ids.length - 1];
      runner.assert(Boolean(id), 'plant answered with a seed id', { planted });
      const claimed = await runInPane(client, paneA, `attn seed tend ${id} --session ${paneA.sessionId}`, 'is growing');
      runner.assert(claimed.includes('is growing'), 'A claimed the seed', { claimed });
      return id;
    });

    await runner.step('the_panel_shows_the_seed', async () => {
      await client.request('open_dock_panel', { panelId: 'garden' });
      await delay(500);
      const shot = await client.request('capture_screenshot_data', { selector: '.garden-panel' });
      runner.assert(Boolean(shot?.pngBase64), 'the garden panel is on screen', {});
      fs.writeFileSync(path.join(runner.runDir, 'garden-panel.png'), Buffer.from(shot.pngBase64, 'base64'));
      await pace();
      // The panel is scoped to the active workspace and sits over the pane, so
      // it is closed again before the crossing: what the successor is told is
      // told in the terminal.
      await client.request('dom_click', { selector: '.garden-panel__close' });
    });

    await runner.step('a_leaves_a_handoff_and_ends', async () => {
      const left = await runInPane(client, paneA,
        `attn seed note ${seedID} -m "${HANDOFF}" --handoff --session ${paneA.sessionId}`, 'handoff left on');
      runner.assert(left.includes('handoff left on'), 'the note was recorded as a handoff', { left });
      const ended = paneA.sessionId;
      authorID = ended;
      await client.request('close_session', { sessionId: ended });
      await observer.waitFor(
        () => !observer.sessionsById.has(ended),
        'session A is a session the daemon no longer knows',
        30_000,
      );
      paneA = null;
      await pace();
    });

    paneB = await runner.step('open_session_b', () => openPane(client, observer, runner, 'gardenB'));

    await runner.step('b_tends_and_is_primed', async () => {
      const claimed = await runInPane(client, paneB, `attn seed tend ${seedID} --session ${paneB.sessionId}`, HANDOFF);
      runner.assert(claimed.includes('is growing'), 'B claimed the seed A let go of', { claimed });
      runner.assert(
        claimed.lastIndexOf('is growing') < claimed.lastIndexOf(HANDOFF),
        'the claim is confirmed before the handoff', { claimed });
      // Who wrote it, on the header. The id wraps in a narrow pane, so the
      // assertion takes the part that cannot straddle a line break.
      runner.assert(claimed.includes(`handoff — ${authorID.slice(0, 8)}`),
        'the handoff names the session that left it', { claimed, authorID });
      runner.writeText('tend.txt', claimed + '\n');
    });

    await runner.step('show_puts_the_handoff_first', async () => {
      const shown = await runInPane(client, paneB, `attn seed show ${seedID}`, 'handoff — ');
      const handoffAt = shown.lastIndexOf('handoff — ');
      runner.assert(handoffAt < shown.lastIndexOf('status'), 'the handoff is above the seed', { shown });
      runner.writeText('show.txt', shown + '\n');
    });

    await runner.step('b_harvests', async () => {
      const done = await runInPane(client, paneB,
        `attn seed harvest ${seedID} -m "the handoff reached its successor" --session ${paneB.sessionId}`,
        'is harvested');
      runner.assert(done.includes('is harvested'), 'harvest closed the seed', { done });
    });

    const summary = runner.finishSuccess({ seedID, sessionB: paneB.sessionId });
    console.log('[RealAppHarness] Garden seed handoff passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { seedID });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const pane of [paneA, paneB]) {
      if (pane) await client.request('close_session', { sessionId: pane.sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
