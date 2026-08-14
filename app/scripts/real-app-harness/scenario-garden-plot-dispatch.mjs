#!/usr/bin/env node
//
// Garden slice 5's acceptance, against the packaged app and its own daemon.
//
// A plot is planted in one command, a delegation is dispatched at it, and the
// panel walks the garden root to leaf while the plot drains. The three things
// only a live run can answer:
//
//   - the dispatch record is written by the delegation itself, so a flag-free
//     `ready` inside the delegated session answers with its plot;
//   - dispatch is scope and not a fence — `--all` from that same session steps
//     back out to the whole garden;
//   - the panel navigates: a crown row carries its plot's counts, opening it
//     drills into the children, and the trail climbs back out.
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

// The plot under test. Two children are free from the start and the third waits
// on one of them, so "children are parallel by default; only blocks sequences"
// is visible in the counts rather than asserted about the payload.
const PLOT = {
  title: 'Walk a plot end to end',
  body: '# The plan\n\nThe crown body is the plan a delegate is primed with.',
  children: [
    // `blocks` names what this child holds back, so the first step is what the
    // sequenced one waits on: harvesting it is what frees the third.
    { title: 'First parallel step', body: 'Nothing holds this one.', blocks: ['the-sequenced-step'] },
    { title: 'Second parallel step', body: 'Nothing holds this one either.' },
    { title: 'The sequenced step' },
  ],
};

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
// two rows — and a break that lands on a space swallows it. Everything this
// scenario reads is matched with the whitespace taken out of both sides.
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
// again as the shell prints it — so the output is what lies between them. The
// pane is a scrolling buffer, so neither "contains it" nor counting occurrences
// over the whole pane can tell this command's answer from an earlier one's.
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

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-garden-plot-dispatch');
    return;
  }

  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'GardenPlotDispatch',
    tier: 'local',
    prefix: 'garden-plot-dispatch',
  });

  let pane = null;
  let delegated = null;
  let second = null;
  let crown = null;
  let children = [];
  try {
    await launchFreshAppAndConnect(client, observer);
    pane = await runner.step('open_session', () => openPane(client, observer, runner, 'gardener'));

    await runner.step('plant_a_plot_in_one_command', async () => {
      const payload = path.join(runner.sessionDir, 'plot.json');
      fs.writeFileSync(payload, JSON.stringify(PLOT));
      const planted = await runInPane(client, pane,
        `attn seed plot -f ${payload} --session ${pane.sessionId}`, 'the-sequenced-step');
      const ids = seedIDs(planted);
      runner.assert(ids.length >= 4, 'the plot answered with a crown and three children', { planted });
      crown = ids[0];
      children = ids.slice(1, 4);
      runner.writeText('plot.txt', planted + '\n');
    });

    await runner.step('the_crown_wears_its_plot', async () => {
      const listed = await runInPane(client, pane, 'attn seed ls --tree', 'done ·');
      // Two children free, one held back by a sibling: parallel by default.
      runner.assert(saw(listed, '[0/3 done · 0 growing · 2 ready · 1 blocked]'),
        'the crown row carries its plot progress', { listed });
      runner.writeText('ls-tree.txt', listed + '\n');
    });

    delegated = await runner.step('dispatch_a_delegate_at_the_plot', async () => {
      const known = new Set(observer.sessionsById.keys());
      // The delegation is awaited on the daemon, not on the pane: what it
      // prints wraps in a narrow pane, and the session existing is the fact.
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --no-worktree --source-session ${pane.sessionId} ` +
          `--plot ${crown} --name plotdel --brief "Tend the plot you were dispatched at."`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      // The delegation keeps printing after the daemon has the session; drain
      // the pane before the next command types into the middle of it.
      await runInPane(client, pane, 'true', '');
      return spawned;
    });

    await runner.step('the_delegates_ready_is_its_plot', async () => {
      const scoped = await runInPane(client, pane,
        `attn seed ready --session ${delegated}`, 'ready in the plot under');
      runner.assert(saw(scoped, `in the plot under ${crown}`),
        'a flag-free ready inside the delegated session answers with its plot', { scoped });
      runner.assert(!saw(scoped, 'ready in the garden'),
        'the plot answer did not fall back to the garden', { scoped });
      runner.writeText('ready-plot.txt', scoped + '\n');
    });

    await runner.step('dispatch_is_scope_and_not_a_fence', async () => {
      // A seed planted outside the plot: the delegate must still be able to see
      // it, because nothing fences a dispatched session in.
      const outside = await runInPane(client, pane,
        `attn seed plant "Work outside the plot" --session ${pane.sessionId}`, 's-');
      const strayID = seedIDs(outside).pop();
      const all = await runInPane(client, pane,
        `attn seed ready --all --session ${delegated}`, 'ready in the garden');
      runner.assert(saw(all, strayID),
        '--all from the delegated session reaches a seed outside its plot', { all, strayID });
      runner.writeText('ready-all.txt', all + '\n');
    });

    await runner.step('the_panel_walks_the_garden', async () => {
      await client.request('open_dock_panel', { panelId: 'garden' });
      await delay(500);
      const whole = await client.request('garden_get_state', {});
      runner.assert(whole.present, 'the garden panel is on screen', { whole });
      runner.assert(whole.trail.length === 1 && whole.trail[0].here,
        'the panel opens on the whole garden', { trail: whole.trail });
      const crownRow = whole.seeds.find((seed) => seed.id === crown);
      runner.assert(Boolean(crownRow?.plot), 'the crown row carries its plot counts', { crownRow });
      await pace();

      const inside = await client.request('garden_open_plot', { seedId: crown });
      runner.assert(inside.trail.length === 2 && inside.trail[1].here,
        'opening a crown walks into its plot', { trail: inside.trail });
      const listed = inside.seeds.map((seed) => seed.id).sort();
      runner.assert(JSON.stringify(listed) === JSON.stringify([...children].sort()),
        'the plot shows its children and nothing else', { listed, children });
      fs.writeFileSync(path.join(runner.runDir, 'garden-plot.png'),
        Buffer.from((await client.request('capture_screenshot_data', { selector: '.garden-panel' })).pngBase64, 'base64'));
      await pace();

      const back = await client.request('garden_climb_to', { depth: 0 });
      runner.assert(back.trail.length === 1 && back.seeds.length > children.length,
        'the trail climbs back out to the whole garden', { trail: back.trail });
      await pace();
    });

    await runner.step('the_plot_drains_live', async () => {
      const [first, , sequenced] = children;
      await runInPane(client, pane, `attn seed tend ${first} --session ${delegated}`, 'is growing');
      const growing = await client.request('garden_get_state', {});
      const growingRow = growing.seeds.find((seed) => seed.id === crown);
      runner.assert(growingRow.plot.includes('1 growing'),
        'the panel shows the plot moving without anybody refreshing it', { row: growingRow });
      await pace();

      await runInPane(client, pane,
        `attn seed harvest ${first} -m "the first parallel step is done" --session ${delegated}`, 'is harvested');
      const drained = await client.request('garden_get_state', {});
      const drainedRow = drained.seeds.find((seed) => seed.id === crown);
      runner.assert(drainedRow.plot.includes('1/3 done'),
        'harvesting a child drains the plot on screen', { row: drainedRow });
      // Dependency order: the sequenced step was blocked, and harvesting its
      // blocker is what frees it — with nobody clearing anything.
      const freed = await runInPane(client, pane, `attn seed ready --session ${delegated}`, 'ready in the plot under');
      runner.assert(saw(freed, sequenced),
        'harvesting the blocker freed the sequenced step', { freed, sequenced });
      runner.assert(drainedRow.plot.includes('2 ready') && !drainedRow.plot.includes('blocked'),
        'the freed step is what the plot counts as ready, with nothing blocked left', { row: drainedRow });
      runner.writeText('ready-after-harvest.txt', freed + '\n');
      await pace();
    });

    await runner.step('two_delegates_share_one_plot', async () => {
      // Dispatch is not an assignment: a second delegate at the same crown is a
      // second pair of hands, and the per-seed tender is what keeps them apart.
      const known = new Set(observer.sessionsById.keys());
      await client.request('write_pane', {
        ...pane,
        text: `attn delegate --agent shell --no-worktree --source-session ${pane.sessionId} ` +
          `--plot ${crown} --name plotdel2 --brief "Tend the plot you were dispatched at."`,
      });
      await observer.waitFor(() => {
        second = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(second);
      }, 'the second delegated session exists', 60_000);
      await runInPane(client, pane, 'true', '');

      const [, parallel, sequenced] = children;
      // Both delegates see the same two ready children, and each claims one.
      const offered = await runInPane(client, pane, `attn seed ready --session ${second}`, 'ready in the plot under');
      runner.assert(saw(offered, parallel) && saw(offered, sequenced),
        'the second delegate is offered the same plot', { offered });
      await runInPane(client, pane, `attn seed tend ${parallel} --session ${second}`, 'is growing');
      await runInPane(client, pane, `attn seed tend ${sequenced} --session ${delegated}`, 'is growing');

      // The collision that must not happen quietly: claiming a seed somebody
      // else holds is refused by name, not silently taken over.
      const refused = await runInPane(client, pane,
        `attn seed tend ${parallel} --session ${delegated}`, 'one tender at a time');
      runner.assert(saw(refused, `${parallel} is being tended by`),
        'a second claim on one seed is refused and names who holds it', { refused });

      const drained = await runInPane(client, pane, `attn seed ready --session ${delegated}`, 'in the plot under');
      runner.assert(saw(drained, `nothing is ready in the plot under ${crown}`),
        'the two claims emptied the plot’s ready list between them', { drained });
      runner.writeText('two-delegates.txt', drained + '\n');
    });

    await runner.step('a_fresh_session_reorients_from_ready_alone', async () => {
      // Garden state lives in the daemon: a session that watched none of this
      // has to be able to pick the work up from the two read commands alone.
      const fresh = await openPane(client, observer, runner, 'newcomer');
      const seen = await runInPane(client, fresh, 'attn seed ready', 'ready in the garden');
      runner.assert(saw(seen, 'ready in the garden'),
        'a fresh session answers for the whole garden', { seen });
      const tree = await runInPane(client, fresh, `attn seed show ${crown}`, 'plot');
      runner.assert(saw(tree, '1 of 3 done'),
        'the crown tells a newcomer where its plot stands', { tree });
      runner.writeText('fresh-session.txt', seen + '\n' + tree + '\n');
      await client.request('close_session', { sessionId: fresh.sessionId }).catch(() => {});
    });

    const summary = runner.finishSuccess({ crown, children, delegated, second });
    console.log('[RealAppHarness] Garden plot dispatch passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { crown, children, delegated, second });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const id of [delegated, second, pane?.sessionId]) {
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
