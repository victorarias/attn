#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  restoreHarnessSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';
import { appDaemonInTree, delay } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  waitForFirstWorkspacePane,
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { recordingEnabled } from './windowRecording.mjs';

const BRIEF = 'BRIEF7 hold this seed until the dispatcher closes you';
const REASON = 'REASON3 its report landed, nothing left to drive';
const SELF_REASON = 'REASON4 I am done and nobody is waiting on me';

const PACE_MS = recordingEnabled() ? 1_400 : 0;

async function pace() {
  if (PACE_MS > 0) await delay(PACE_MS);
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function flat(text) {
  return text.replace(/\n/g, '');
}

function saw(haystack, needle) {
  return haystack.replace(/\s+/g, '').includes(needle.replace(/\s+/g, ''));
}

function occurrences(haystack, needle) {
  return haystack.split(needle).length - 1;
}

let marks = 0;

// The marker lands twice, as typed and as the shell prints it; the output is between.
async function runInPane(client, pane, command, expected, timeoutMs = 30_000) {
  await client.request('click_pane', pane);
  await waitForPaneVisible(client, pane.sessionId, pane.paneId);
  await waitForPaneAttached(client, pane.sessionId, pane.paneId);
  const mark = `mark${++marks}x`;
  await client.request('write_pane', { ...pane, text: `${command}; echo ${mark}` });
  const deadline = Date.now() + timeoutMs;
  let text = '';
  while (Date.now() < deadline) {
    await delay(250);
    text = flat(await (async () => (await client.request('read_pane_text', pane)).text || '')());
    if (occurrences(text, mark) >= 2) {
      const out = text.slice(text.indexOf(mark) + mark.length, text.lastIndexOf(mark));
      if (saw(out, expected)) return out;
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

async function waitForSessionGone(client, sessionId, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let ui = null;
  while (Date.now() < deadline) {
    ui = await client.request('get_session_ui_state', { sessionId });
    if (ui.exists === false && ui.sidebarItem == null) return ui;
    await delay(50);
  }
  throw new Error(`Session ${sessionId} stayed visible for ${timeoutMs}ms: ${JSON.stringify(ui, null, 2)}`);
}

function cli(daemonBinary, profile, ...args) {
  return execFileSync(daemonBinary, args, { encoding: 'utf8', env: profileCliEnv(profile) });
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scenario-agent-close');
    return;
  }

  const profile = currentHarnessProfile();
  const daemonBinary = appDaemonInTree(options.appPath);
  const client = new UiAutomationClient(options);
  const observer = new DaemonObserver(options);
  const runner = createScenarioRunner(options, {
    scenarioId: 'AgentClose',
    tier: 'local',
    prefix: 'agent-close',
    metadata: { agent: 'shell', focus: 'an agent closes the session it dispatched', profile },
  });

  let dispatcher = null;
  let sibling = null;
  let delegate = null;
  let seed = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    await launchFreshAppAndConnect(client, observer);
    dispatcher = await runner.step('open_dispatcher', () => openPane(client, observer, runner, 'dispatcher'));
    sibling = await runner.step('open_sibling', () => openPane(client, observer, runner, 'sibling'));

    delegate = await runner.step('dispatch_a_delegate', async () => {
      const known = new Set(observer.sessionsById.keys());
      await client.request('write_pane', {
        ...dispatcher,
        text: `attn delegate --agent shell --model none --no-worktree --source-session ${dispatcher.sessionId} ` +
          `--name closeme --brief "${BRIEF}"`,
      });
      let spawned = null;
      await observer.waitFor(() => {
        spawned = [...observer.sessionsById.keys()].find((id) => !known.has(id)) ?? null;
        return Boolean(spawned);
      }, 'the delegated session exists', 60_000);
      await waitForFirstWorkspacePane(client, spawned, 'the delegate’s pane', 20_000);
      // The spawn takes the screen; a close typed into a pane still running never runs.
      await client.request('select_session', { sessionId: dispatcher.sessionId });
      await waitForPaneShellReady(client, dispatcher.sessionId, dispatcher.paneId);
      await runInPane(client, dispatcher, 'true', '');
      return spawned;
    });

    seed = await runner.step('the_delegate_tends_a_seed', async () => {
      const listed = cli(daemonBinary, profile, 'seed', 'ls', '--json');
      const rows = JSON.parse(listed).seeds || [];
      const mine = rows.filter((row) => row.tender_session === delegate);
      runner.assert(mine.length === 1, 'the delegation planted exactly one seed the delegate tends',
        { delegate, tenders: rows.map((row) => ({ id: row.id, tender: row.tender_session })) });
      runner.writeText('seed-ls.json', `${listed}\n`);
      return mine[0].id;
    });

    await pace();
    await runner.step('a_sibling_is_refused_and_told_the_rule', async () => {
      const refused = await runInPane(client, sibling,
        `attn agent close ${delegate} -m "looks done to me" --source-session ${sibling.sessionId}`,
        'close itself');
      runner.assert(saw(refused, 'chief of staff'), 'the refusal names every rule', { refused });
      runner.assert(saw(refused, dispatcher.sessionId.slice(0, 8)),
        'the refusal names who did dispatch the target', { refused });
      const ui = await client.request('get_session_ui_state', { sessionId: delegate });
      runner.assert(ui.exists !== false, 'the refused session is still running', { ui });
      runner.writeText('refusal.txt', `${refused}\n`);
    });

    await pace();
    await runner.step('the_dispatcher_closes_its_delegate', async () => {
      const closed = await runInPane(client, dispatcher,
        `attn agent close ${delegate} -m "${REASON}" --source-session ${dispatcher.sessionId}`,
        'closed session');
      runner.assert(saw(closed, seed), 'the close says which seed it noted', { closed, seed });
      const ui = await waitForSessionGone(client, delegate, 15_000);
      runner.writeJson('session-ui-after-close.json', ui);
      runner.writeText('close.txt', `${closed}\n`);
    });

    await pace();
    await runner.step('the_ledger_names_the_closer_and_the_reason', async () => {
      const shown = cli(daemonBinary, profile, 'session', 'show', delegate);
      runner.assert(/^state\s+closed$/m.test(shown), 'session show must report the session as closed', { shown });
      runner.assert(shown.includes(dispatcher.sessionId),
        'session show must name the dispatcher as the closer', { shown });
      runner.assert(shown.includes(REASON), 'session show must carry the reason', { shown });
      const closedList = cli(daemonBinary, profile, 'session', 'list', '--closed');
      runner.assert(closedList.includes(delegate.slice(0, 8)),
        'session list --closed must list the closed session', { closedList });
      const live = cli(daemonBinary, profile, 'session', 'list');
      runner.assert(!live.includes(delegate.slice(0, 8)),
        'session list must not show a closed session', { live });
      runner.writeText('session-show.txt', shown);
      runner.writeText('session-list-closed.txt', closedList);
    });

    await pace();
    await runner.step('the_seed_keeps_its_tender_and_gains_a_note', async () => {
      const notes = cli(daemonBinary, profile, 'seed', 'notes', seed);
      runner.assert(notes.includes(REASON), 'the close reason must land on the seed’s log', { notes });
      const shown = cli(daemonBinary, profile, 'seed', 'show', seed);
      runner.assert(shown.includes('growing'), 'the close must not move the seed', { shown });
      runner.assert(shown.includes(delegate), 'the closed session must still be the seed’s tender', { shown });
      runner.writeText('seed-notes.txt', notes);
      runner.writeText('seed-show.txt', shown);
    });

    await pace();
    await runner.step('a_session_closes_itself', async () => {
      // No marker to wait for: the shell that would echo it is the one being killed.
      await client.request('click_pane', sibling);
      await waitForPaneVisible(client, sibling.sessionId, sibling.paneId);
      await waitForPaneAttached(client, sibling.sessionId, sibling.paneId);
      await client.request('write_pane', {
        ...sibling,
        text: `attn agent close ${sibling.sessionId} -m "${SELF_REASON}" --source-session ${sibling.sessionId}`,
      });
      const ui = await waitForSessionGone(client, sibling.sessionId, 30_000);
      const shown = cli(daemonBinary, profile, 'session', 'show', sibling.sessionId);
      runner.assert(/^state\s+closed$/m.test(shown), 'a session may close itself', { shown });
      runner.assert(shown.includes(SELF_REASON), 'the self close carries its reason', { shown });
      runner.writeJson('sibling-ui-after-self-close.json', ui);
      runner.writeText('self-close-session-show.txt', shown);
    });

    await pace();
    const summary = await runner.finishSuccess({ dispatcher: dispatcher.sessionId, delegate, seed });
    console.log('[RealAppHarness] A dispatcher closed its delegate; the ledger and the seed both carry it.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { delegate, seed });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    await restoreHarnessSettings();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
