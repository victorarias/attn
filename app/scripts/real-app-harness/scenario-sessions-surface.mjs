#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  pressShortcutKeys,
  restoreHarnessSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessProfile, profileCliEnv } from './harnessProfile.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree, createWindowDriver } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { sleep } from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

// The surface is keyboard-first, so the shortcut opens it here rather than a click.
process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function git(cwd, ...args) {
  return execFileSync('git', args, { cwd, encoding: 'utf8', env: profileCliEnv() }).trim();
}

// Two checkouts of one repository plus a plain one elsewhere, so the repository
// filter has something to separate that the workspace filter cannot.
function buildRepositories(root) {
  const repo = path.join(root, 'ledger-repo');
  fs.mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  git(repo, 'config', 'user.email', 'harness@attn.local');
  git(repo, 'config', 'user.name', 'attn harness');
  fs.writeFileSync(path.join(repo, 'README.md'), '# sessions surface fixture\n');
  git(repo, 'add', 'README.md');
  git(repo, 'commit', '-q', '-m', 'fixture');

  const other = path.join(root, 'other-repo');
  fs.mkdirSync(other, { recursive: true });
  git(other, 'init', '-q', '-b', 'main');
  git(other, 'config', 'user.email', 'harness@attn.local');
  git(other, 'config', 'user.name', 'attn harness');
  fs.writeFileSync(path.join(other, 'README.md'), '# other repository\n');
  git(other, 'add', 'README.md');
  git(other, 'commit', '-q', '-m', 'fixture');

  for (const dir of [repo, other]) writeMockAgentFixture(dir, { name: 'sessions surface mock', turns: [] });
  return { repo, other };
}

async function waitForSessions(client, predicate, description, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  let last = null;
  while (Date.now() < deadline) {
    last = await client.request('sessions_get_state', {});
    if (predicate(last)) return last;
    await sleep(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(last, null, 2)}`);
}

function rowFor(state, sessionId) {
  return state.rows.find((row) => row.id === sessionId) ?? null;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-sessions-surface.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  assertFreshWorldTargetSafe({ profile, appPath: options.appPath });
  const daemonBinary = appDaemonInTree(options.appPath);
  const runner = createScenarioRunner(options, {
    scenarioId: 'SESSIONS-SURFACE',
    tier: 'tier1-local-shell',
    prefix: 'sessions-surface',
    metadata: {
      agent: 'claude',
      focus: 'the Sessions surface lists live and closed sessions, filters them, and updates a row when a session closes',
      profile,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = createWindowDriver();
  const sessions = {};

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const { repo, other } = await runner.step('build_repositories', () => buildRepositories(runner.sessionDir));

    await runner.step('launch_app', async () => {
      await client.quitApp();
      await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: profileCliEnv(profile) });
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_sessions_in_two_repositories', async () => {
      for (const [name, cwd] of [['one', repo], ['two', repo], ['elsewhere', other]]) {
        sessions[name] = await createSessionAndWaitForInitialPane({
          client,
          observer,
          cwd,
          label: `${name}-${runner.runId}`,
          agent: 'claude',
          sessionWaitMs: 30_000,
        });
      }
      const registered = await observer.waitFor(
        () => {
          const session = observer.getSession(sessions.one);
          return session?.repository ? session : null;
        },
        'the daemon to record which repository the session ran in',
        20_000,
      );
      runner.assert(registered.repository === repo,
        'a session in a plain checkout names that checkout as its repository',
        { repository: registered.repository, repo });
      runner.writeJson('registered-session.json', registered);
    });

    const opened = await runner.step('the_shortcut_opens_the_surface', async () => {
      await pressShortcutKeys(client, driver, 'sessions.open');
      const state = await waitForSessions(client, (s) => s.open && s.rows.length >= 3,
        'the surface to open on every live session');
      runner.assert(state.scope === 'All', 'the surface opens on live and closed together', { scope: state.scope });
      for (const id of Object.values(sessions)) {
        runner.assert(!!rowFor(state, id), `session ${id} must be listed`, { rows: state.rows });
      }
      runner.writeJson('surface-opened.json', state);
      return state;
    });

    await runner.step('the_repository_filter_narrows_the_list', async () => {
      const facet = await waitForSessions(client, (s) => s.rows.length >= 3, 'the facets to arrive');
      runner.assert(facet.repository === '', 'the surface starts unfiltered', { repository: facet.repository });
      const filtered = await client.request('sessions_set_filter', { repository: repo });
      const settled = await waitForSessions(client, (s) => s.rows.length === 2,
        'the list to hold only the two sessions from one repository');
      runner.assert(!rowFor(settled, sessions.elsewhere),
        'a session from another repository must drop out of the filtered list',
        { rows: settled.rows });
      runner.writeJson('filtered-by-repository.json', { filtered, settled });
      await client.request('sessions_set_filter', { repository: '' });
      await waitForSessions(client, (s) => s.rows.length >= 3, 'the filter to be lifted');
    });

    await runner.step('a_close_updates_the_row_in_place', async () => {
      const before = await waitForSessions(client, (s) => rowFor(s, sessions.two)?.state !== 'closed',
        'the session to be listed as live before it closes');
      runner.assert(rowFor(before, sessions.two).actions.includes('Focus'),
        'a live row offers Focus', { row: rowFor(before, sessions.two) });

      await client.request('close_session', { sessionId: sessions.two });
      const after = await waitForSessions(client, (s) => rowFor(s, sessions.two)?.state === 'closed',
        'the closed session to read as closed without the list being re-opened', 20_000);
      const row = rowFor(after, sessions.two);
      runner.assert(row.when.includes('closed by you'), 'the row names who closed it', { row });
      runner.assert(!row.actions.includes('Focus'), 'a closed row stops offering Focus', { row });
      runner.assert(after.rows.length === before.rows.length,
        'the close replaces the row rather than adding one', { before: before.rows.length, after: after.rows.length });
      runner.writeJson('row-after-close.json', after);
    });

    await runner.step('the_live_scope_drops_the_closed_row', async () => {
      const live = await client.request('sessions_set_filter', { scope: 'Live' });
      const settled = await waitForSessions(client, (s) => !rowFor(s, sessions.two),
        'the Live scope to drop the session that just closed');
      runner.writeJson('live-scope.json', { live, settled });

      await client.request('sessions_set_filter', { scope: 'Closed' });
      const closed = await waitForSessions(client, (s) => !!rowFor(s, sessions.two),
        'the Closed scope to hold it');
      runner.assert(closed.rows.every((row) => row.state === 'closed'),
        'the Closed scope holds closed rows only', { rows: closed.rows });
      runner.writeJson('closed-scope.json', closed);
    });

    await runner.step('a_date_preset_and_a_custom_range_both_filter', async () => {
      await client.request('sessions_set_filter', { scope: 'All', range: 'today' });
      const today = await waitForSessions(client, (s) => s.rows.length >= 3,
        'today to hold the sessions this run just made');
      runner.writeJson('range-today.json', today);

      const wayBack = new Date(Date.now() - 60 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
      const alsoBack = new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
      await client.request('sessions_set_filter', { range: 'custom', from: wayBack, to: alsoBack });
      const empty = await waitForSessions(client, (s) => s.rows.length === 0,
        'a window before this run started to hold nothing');
      runner.assert(empty.state.length > 0, 'an empty list says why it is empty', { state: empty.state });
      runner.writeJson('range-custom-empty.json', empty);

      await client.request('sessions_set_filter', { range: 'any' });
      await waitForSessions(client, (s) => s.rows.length >= 3, 'the range to be lifted');
    });

    await runner.step('the_cli_and_the_surface_agree', async () => {
      const listed = execFileSync(daemonBinary, ['session', 'list', '--all', '--repository', repo], {
        encoding: 'utf8',
        env: profileCliEnv(profile),
      });
      runner.assert(listed.includes(sessions.one) && listed.includes(sessions.two),
        'the CLI repository filter answers the same rows the surface showed', { listed });
      runner.assert(!listed.includes(sessions.elsewhere),
        'the CLI repository filter excludes the other repository', { listed });
      runner.writeText('session-list-repository.txt', listed);
    });

    const summary = await runner.finishSuccess({ sessions, repo, other, openedRows: opened.rows.length });
    console.log('[RealAppHarness] The Sessions surface listed, filtered and updated in place.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessions });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of Object.values(sessions)) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
    await restoreHarnessSettings();
    await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: profileCliEnv(profile) }).catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
