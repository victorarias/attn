#!/usr/bin/env node
//
// Delegation reporting on seeds, against the packaged app and its own daemon.
//
// Tickets carry delegation reporting today; the garden has to carry the same
// weight before it can replace them. This scenario is that weight, live:
//
//   - `attn delegate` plants a seed whose body is the brief, tended by the
//     delegate session — the binding is the dispatch record, so it survives
//     into every later read;
//   - what the delegate reports as work state onto its ticket also lands on
//     the seed's log, with the ticket untouched;
//   - attach and detach are typed log entries, and "current artifacts" is the
//     daemon's projection over that log — the panel drill and the docked seed
//     tile render the same set, from the same authority;
//   - a seed id is a steering address: `attn agent msg <seed>` reaches
//     whoever is tending it.
//
// The commands run inside a pane, which is where an agent runs them: the app
// puts its own `attn` first on a session's PATH, so what the pane answers is
// this build talking to this profile's daemon.
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

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

// The brief. One line, so finding it in the seed body is finding the brief
// rather than guessing at where a pane wrapped it.
const BRIEF = 'BRIEF9 carry the delegation on a seed';
const STEER = 'STEER4 the seed id is the address';

const PACE_MS = recordingEnabled() ? 1_400 : 0;

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
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

async function paneText(client, pane) {
  const payload = await client.request('read_pane_text', pane);
  return payload.text || '';
}

// A pane wraps at its own width, so an id or a sentence can arrive split across
// two rows — and a break that lands on a space swallows it. Everything read
// here is matched with the whitespace taken out of both sides.
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

// runInPane types a command with a marker echoed after it and returns only what
// that command printed: the marker appears twice — in the line as typed and
// again as the shell prints it — so the output is what lies between them.
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
      if (saw(out, expected)) {
        await pace();
        return out;
      }
      throw new Error(`${JSON.stringify(command)} did not answer with ${JSON.stringify(expected)}:\n${out}`);
    }
  }
  throw new Error(`pane never finished ${JSON.stringify(command)}:\n${text}`);
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

// The tile is read by naming the seed, never by taking the first one on screen:
// a workspace keeps older seed tiles mounted, and one of those answers too. The
// wait is on the log entry the command just wrote — the document arriving is the
// signal, and the artifact set is what is then asserted.
async function awaitTile(client, seedID, ready, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let state = { present: false, notes: [], artifacts: [] };
  while (Date.now() < deadline) {
    state = await client.request('seed_document_get_state', { seedId: seedID });
    if (state.present && ready(state)) return state;
    await delay(200);
  }
  throw new Error(`the seed tile for ${seedID} never caught up: ${JSON.stringify(state)}`);
}

// The drill is a row expanded in the panel; re-reading it collapses and reopens,
// because the document is fetched on the way in. Like the tile, it is read on
// the log entry the command just wrote rather than after a fixed pause.
async function readDrill(client, seedID, ready, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let state = { present: false, notes: [], artifacts: [] };
  while (Date.now() < deadline) {
    state = await client.request('garden_expand_seed', { seedId: seedID, reopen: true });
    if (state.present && ready(state)) return state;
    await delay(200);
  }
  throw new Error(`the panel drill for ${seedID} never caught up: ${JSON.stringify(state)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-delegation-reporting');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenDelegationReporting',
    tier: 'local',
    prefix: 'garden-delegation-reporting',
  });

  let pane = null;
  let delegated = null;
  let delegatePane = null;
  let seed = null;
  let artifactPath = null;
  try {
    await launchFreshAppAndConnect(client, observer);
    pane = await runner.step('open_session', () => openPane(client, observer, runner, 'dispatcher'));

    delegated = await runner.step('dispatch_a_delegation', async () => {
      const known = new Set(observer.sessionsById.keys());
      // Awaited on the daemon, not the pane: what the delegation prints wraps
      // in a narrow pane, and the session existing is the fact.
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --no-worktree --source-session ${pane.sessionId} ` +
          `--name delrep --brief "${BRIEF}"`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await runInPane(client, pane, 'true', '');
      return spawned;
    });

    seed = await runner.step('the_brief_is_a_seed_the_delegate_tends', async () => {
      // The listing carries the delegation's name as the title and its session
      // as the tender; the brief is the body, which `show` is what reads.
      const listed = await runInPane(client, pane, 'attn seed ls', delegated);
      const planted = seedIDs(listed)[0];
      runner.assert(Boolean(planted), 'the delegation planted a seed', { listed });
      const shown = await runInPane(client, pane, `attn seed show ${planted}`, BRIEF);
      runner.assert(saw(shown, BRIEF), 'the brief is the seed’s body', { shown });
      runner.assert(saw(shown, delegated),
        'the delegate session is the seed’s tender', { shown, delegated });
      runner.writeText('seed-show.txt', shown + '\n');
      return planted;
    });

    await runner.step('a_status_report_lands_on_the_log', async () => {
      // The ticket is still the reporting channel; the seed's log is what the
      // same report also reaches. The CLI answers with the ticket's new
      // status, which is what says the ticket side is untouched.
      const reported = await runInPane(client, pane,
        `attn ticket status in_progress --comment "digging in" --session ${delegated}`, '→ working');
      runner.writeText('ticket-status.txt', reported + '\n');
      const log = await runInPane(client, pane, `attn seed notes ${seed}`, 'reported in_progress');
      runner.assert(saw(log, 'digging in'),
        'the report’s comment is on the seed’s log', { log });
      runner.writeText('seed-notes.txt', log + '\n');
    });

    await runner.step('artifacts_are_a_projection_over_the_log', async () => {
      artifactPath = path.join(runner.sessionDir, 'evidence.md');
      fs.writeFileSync(artifactPath, '# Evidence\n\nWhat the delegate produced.\n');
      await runInPane(client, pane,
        `attn seed attach ${seed} --path ${artifactPath} -m "the write-up" --session ${delegated}`,
        'attached');

      await client.request('open_dock_panel', { panelId: 'garden' });
      await delay(500);
      const drill = await readDrill(client, seed, (state) => state.notes.some((note) => note.kind === 'attach'));
      runner.assert(drill.present, 'the panel drill shows the seed document', { drill });
      runner.assert(drill.artifacts.some((label) => label.includes('evidence.md')),
        'the drill carries the current artifact', { drill });
      fs.writeFileSync(path.join(runner.runDir, 'drill-attached.png'),
        Buffer.from((await client.request('capture_screenshot_data',
          { selector: '.garden-panel' })).pngBase64, 'base64'));
      await pace();

      // The tile is the second render site of the one read model.
      await runInPane(client, pane, `attn open ${seed} --session ${pane.sessionId}`, '');
      const tile = await awaitTile(client, seed, (state) => state.notes.some((note) => note.kind === 'attach'));
      runner.assert(tile.present, 'the seed opened as a tile', { tile });
      runner.assert(tile.artifacts.some((label) => label.includes('evidence.md')),
        'the tile carries the same artifact', { tile });
      runner.assert(tile.notes.some((note) => note.kind === 'attach'),
        'the attach is on the log as its own kind', { tile });
      fs.writeFileSync(path.join(runner.runDir, 'tile-attached.png'),
        Buffer.from((await client.request('capture_screenshot_data',
          { selector: '.workspace-dock-tile' })).pngBase64, 'base64'));
      await pace();

      await runInPane(client, pane,
        `attn seed detach ${seed} --path ${artifactPath} -m "superseded" --session ${delegated}`,
        'detached');
      const afterTile = await awaitTile(client, seed, (state) => state.notes.some((note) => note.kind === 'detach'));
      runner.assert(afterTile.artifacts.length === 0,
        'detaching takes the artifact out of the set', { afterTile });
      runner.assert(afterTile.notes.some((note) => note.kind === 'detach'),
        'the detach stayed on the log', { afterTile });
      const afterDrill = await readDrill(client, seed, (state) => state.notes.some((note) => note.kind === 'detach'));
      runner.assert(afterDrill.artifacts.length === 0,
        'the drill agrees with the tile', { afterDrill });
      fs.writeFileSync(path.join(runner.runDir, 'drill-detached.png'),
        Buffer.from((await client.request('capture_screenshot_data',
          { selector: '.garden-panel' })).pngBase64, 'base64'));
      runner.writeText('artifacts.json', JSON.stringify({ drill, tile, afterTile, afterDrill }, null, 2) + '\n');
      await pace();
    });

    await runner.step('a_seed_id_steers_its_tender', async () => {
      delegatePane = await waitForFirstWorkspacePane(client, delegated, 'the delegate’s pane', 20_000);
      const sent = await runInPane(client, pane,
        `attn agent msg ${seed} "${STEER}" --source-session ${pane.sessionId}`, 'delivered');
      runner.writeText('agent-msg.txt', sent + '\n');
      const deadline = Date.now() + 20_000;
      let text = '';
      while (Date.now() < deadline) {
        text = flat(await paneText(client, { sessionId: delegated, paneId: delegatePane.paneId }));
        if (saw(text, STEER)) break;
        await delay(250);
      }
      runner.assert(saw(text, STEER),
        'the message addressed to the seed arrived in its tender’s pane', { text });
      runner.writeText('delegate-pane.txt', text + '\n');
      await pace();
    });

    const summary = runner.finishSuccess({ seed, delegated });
    console.log('[RealAppHarness] Garden delegation reporting passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { seed, delegated });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, pane?.sessionId]) {
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
