#!/usr/bin/env node
//
// The garden's app surfaces, against the packaged app and its own daemon.
//
// The ticket board and its detail panel are gone; the garden replaced them.
// Two things the board did every day have to still work, and this is them:
//
//   - a delegated pane says what it reports to — the seed chip in its header,
//     which opens the seed as a tile (the one annotated reading surface);
//   - a seed whose tender session was CLOSED can be reopened from the panel
//     drill. That was the board's Resume button; without it, a delegation
//     whose pane you closed is a dead end.
//
// The reopen gates on the dispatch record (cwd + agent), which outlives the
// session, so the button reads the same before and after the close — and the
// daemon owns the whole composite (register → pane → spawn, with rollback).
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

const BRIEF = 'BRIEF7 the garden surfaces answer for a delegation';

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

let marks = 0;

// Types a command with a marker echoed after it and returns only what that
// command printed: the marker appears twice — in the line as typed and again as
// the shell prints it — so the output is what lies between them.
async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = flat((await client.request('read_pane_text', pane)).text || '');
    if (occurrences(text, mark) >= 2) {
      const first = text.indexOf(mark) + mark.length;
      const out = text.slice(first, text.lastIndexOf(mark));
      if (saw(out, expected)) return out;
      throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${out}`);
    }
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
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

async function openPane(client, observer, runner, label) {
  const cwd = path.join(runner.sessionDir, label);
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client, observer, cwd, label, agent: 'shell',
  });
  const pane = await waitForFirstWorkspacePane(client, sessionId, `pane for ${label}`, 20_000);
  return { sessionId, paneId: pane.paneId };
}

function seedIDs(text) {
  return [...flat(text).matchAll(/(s-[a-z0-9]{6})/g)].map((match) => match[1]);
}

async function pollFor(fn, description, timeoutMs = 20_000, intervalMs = 250) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await fn();
    if (last) return last;
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for: ${description}. Last value: ${JSON.stringify(last)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-seed-reopen');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenSeedReopen',
    tier: 'local',
    prefix: 'garden-seed-reopen',
  });

  let pane = null;
  let delegated = null;
  let seed = null;
  let reopened = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    pane = await runner.step('open_session', () => openPane(client, observer, runner, 'dispatcher'));

    delegated = await runner.step('dispatch_a_delegation', async () => {
      const known = new Set(observer.sessionsById.keys());
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --no-worktree --source-session ${pane.sessionId} ` +
          `--name gsreopen --brief "${BRIEF}"`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await runInPane(client, pane, 'true', '');
      return spawned;
    });

    seed = await runner.step('the_delegate_pane_names_its_seed', async () => {
      const listed = await runInPane(client, pane, 'attn seed ls', delegated);
      const planted = seedIDs(listed)[0];
      runner.assert(Boolean(planted), 'the delegation planted a seed', { listed });

      // The chip is decorated at broadcast from the dispatch record, so it
      // arrives with the session rather than after a garden read.
      const chip = await pollFor(
        async () => {
          const state = await client.request('session_seed_chip_get_state', { sessionId: delegated });
          return state.present ? state : null;
        },
        'the delegated pane to carry its seed chip',
      );
      runner.assert(chip.hint.includes(planted),
        'the chip names the seed the session reports to', { chip, planted });
      runner.writeText('seed-chip.json', JSON.stringify(chip, null, 2) + '\n');
      return planted;
    });

    await runner.step('the_chip_opens_the_seed_as_a_tile', async () => {
      await client.request('dom_click', { selector: `[data-testid="seed-chip-${delegated}"]` });
      const tile = await pollFor(
        async () => {
          const state = await client.request('seed_document_get_state', { seedId: seed });
          return state.present ? state : null;
        },
        'the seed tile the chip opens',
      );
      runner.assert(saw(tile.body, BRIEF), 'the tile reads the brief as the seed body', { tile });
    });

    reopened = await runner.step('a_closed_tender_is_reopened_from_the_drill', async () => {
      await client.request('close_session', { sessionId: delegated });
      await observer.waitFor(() => !observer.sessionsById.has(delegated),
        'the delegated session to be unregistered', 20_000);

      await client.request('open_dock_panel', { panelId: 'garden' });
      await pollFor(
        async () => {
          const state = await client.request('garden_expand_seed', { seedId: seed, reopen: true });
          return state.present ? state : null;
        },
        'the panel drill for the seed',
      );

      const known = new Set(observer.sessionsById.keys());
      await client.request('garden_resume_seed', { seedId: seed });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the tender to be reopened as a session', 60_000);
      return spawned;
    });

    await runner.step('the_reopened_session_reports_to_the_same_seed', async () => {
      const chip = await pollFor(
        async () => {
          const state = await client.request('session_seed_chip_get_state', { sessionId: reopened });
          return state.present ? state : null;
        },
        'the reopened pane to carry the same seed chip',
      );
      runner.assert(chip.hint.includes(seed),
        'the reopened session reports to the seed it was reopened from', { chip, seed });
    });

    const summary = runner.finishSuccess({ seed, delegated, reopened });
    console.log('[RealAppHarness] Garden seed reopen passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { seed, delegated, reopened });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [reopened, delegated, pane?.sessionId]) {
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
