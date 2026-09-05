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
  restoreHarnessSettings,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { assertFreshWorldTargetSafe } from './freshWorld.mjs';
import { currentHarnessProfile, dataDirForProfile, profileCliEnv } from './harnessProfile.mjs';
import { writeMockAgentFixture } from './mockAgent.mjs';
import { appDaemonInTree } from './platform.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { sleep } from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

// The prompt this change removed asked exactly this. Its copy is the only
// evidence that survives a rebuild, so the scenario watches for the words.
const REMOVED_PROMPT_COPY = 'Keep this worktree for later';
// The prompt used to open within a frame of the close; a second of quiet after
// the session leaves the sidebar is past anything an animation could delay.
const PROMPT_WATCH_MS = 1_000;

const execFileAsync = promisify(execFile);

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function git(cwd, ...args) {
  return execFileSync('git', args, { cwd, encoding: 'utf8', env: profileCliEnv() }).trim();
}

function buildWorktree(root, branch) {
  const repo = path.join(root, 'repo');
  fs.mkdirSync(repo, { recursive: true });
  git(repo, 'init', '-q', '-b', 'main');
  git(repo, 'config', 'user.email', 'harness@attn.local');
  git(repo, 'config', 'user.name', 'attn harness');
  fs.writeFileSync(path.join(repo, 'README.md'), '# close ledger fixture\n');
  git(repo, 'add', 'README.md');
  git(repo, 'commit', '-q', '-m', 'fixture');
  const worktree = path.join(root, `repo--${branch}`);
  git(repo, 'worktree', 'add', '-q', '-b', branch, worktree);
  return { repo, worktree };
}

async function waitForSessionGone(client, sessionId, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let ui = null;
  while (Date.now() < deadline) {
    ui = await client.request('get_session_ui_state', { sessionId });
    if (ui.exists === false && ui.sidebarItem == null) return ui;
    await sleep(50);
  }
  throw new Error(`Session ${sessionId} stayed visible for ${timeoutMs}ms: ${JSON.stringify(ui, null, 2)}`);
}

async function watchForRemovedPrompt(client, windowMs) {
  const deadline = Date.now() + windowMs;
  const samples = [];
  while (Date.now() < deadline) {
    const { text } = await client.request('dom_text', { selector: 'body' });
    if (text.includes(REMOVED_PROMPT_COPY)) {
      throw new Error(`The removed worktree cleanup prompt appeared: ${text.slice(0, 400)}`);
    }
    samples.push(Date.now());
    await sleep(100);
  }
  return samples.length;
}

function ledgerEntry(daemonBinary, profile, sessionId) {
  const output = execFileSync(daemonBinary, ['session', 'show', sessionId], {
    encoding: 'utf8',
    env: profileCliEnv(profile),
  });
  return output;
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-session-close-ledger.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  assertFreshWorldTargetSafe({ profile, appPath: options.appPath });
  const daemonBinary = appDaemonInTree(options.appPath);
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'SESSION-CLOSE-LEDGER',
    tier: 'tier1-local-shell',
    prefix: 'session-close-ledger',
    metadata: {
      agent: 'claude',
      focus: 'closing a worktree session keeps the worktree, asks nothing, and stays readable in the ledger',
      profile,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const branch = `close-ledger-${runner.runId}`;
    const { repo, worktree } = await runner.step('build_worktree', () => buildWorktree(runner.sessionDir, branch));
    writeMockAgentFixture(worktree, { name: 'close ledger mock', turns: [] });

    await runner.step('launch_app', async () => {
      await client.quitApp();
      await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: profileCliEnv(profile) });
      await launchFreshAppAndConnect(client, observer);
    });

    await runner.step('create_worktree_session', async () => {
      sessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: worktree,
        label: `close-ledger-${runner.runId}`,
        agent: 'claude',
        sessionWaitMs: 30_000,
      });
      const registered = await observer.waitFor(
        () => {
          const session = observer.getSession(sessionId);
          return session?.is_worktree ? session : null;
        },
        `session ${sessionId} recognised as a worktree`,
        20_000,
      );
      runner.assert(registered.main_repo === repo, 'The session must point back at its main repository', {
        mainRepo: registered.main_repo,
        repo,
      });
      runner.writeJson('worktree-session.json', registered);
    });

    const promptSamples = await runner.step('close_asks_nothing', async () => {
      await client.request('select_session', { sessionId });
      await client.request('close_session', { sessionId });
      const ui = await waitForSessionGone(client, sessionId, 10_000);
      runner.writeJson('session-ui-after-close.json', ui);
      return watchForRemovedPrompt(client, PROMPT_WATCH_MS);
    });

    await runner.step('worktree_survives', async () => {
      runner.assert(fs.existsSync(worktree), 'The worktree directory must survive the close', { worktree });
      const listed = git(repo, 'worktree', 'list', '--porcelain');
      runner.assert(listed.includes(worktree), 'Git must still know the worktree', { listed });
      runner.assert(
        git(repo, 'branch', '--list', branch).length > 0,
        'The worktree branch must survive the close',
        { branch },
      );
      runner.writeText('worktree-list.txt', `${listed}\n`);
    });

    const ledger = await runner.step('ledger_reads_the_close_back', async () => {
      const shown = ledgerEntry(daemonBinary, profile, sessionId);
      runner.assert(/^state\s+closed$/m.test(shown), 'session show must report the session as closed', { shown });
      runner.assert(/^closed\s+.* by user$/m.test(shown), 'session show must name the user as the closer', { shown });
      runner.assert(/^worktree\s+yes, of /m.test(shown), 'session show must keep the worktree it ran in', { shown });
      const closed = execFileSync(daemonBinary, ['session', 'list', '--closed'], {
        encoding: 'utf8',
        env: profileCliEnv(profile),
      });
      runner.assert(closed.includes(sessionId), 'session list --closed must list the closed session', { closed });
      const live = execFileSync(daemonBinary, ['session', 'list'], {
        encoding: 'utf8',
        env: profileCliEnv(profile),
      });
      runner.assert(!live.includes(sessionId), 'session list must not show a closed session', { live });
      runner.writeText('session-show.txt', shown);
      runner.writeText('session-list-closed.txt', closed);
      runner.writeText('session-list-live.txt', live);
      return { shown, closed, live };
    });

    await runner.step('close_survives_a_daemon_restart', async () => {
      await client.quitApp();
      await execFileAsync(daemonBinary, ['daemon', 'stop'], { env: profileCliEnv(profile) });
      await execFileAsync(daemonBinary, ['daemon', 'ensure'], { env: profileCliEnv(profile) });
      const shown = ledgerEntry(daemonBinary, profile, sessionId);
      runner.assert(/^state\s+closed$/m.test(shown), 'A restart must neither resurrect nor reap the closed session', { shown });
      runner.writeText('session-show-after-restart.txt', shown);
    });

    const summary = await runner.finishSuccess({
      sessionId,
      worktree,
      repo,
      branch,
      promptSamples,
      dataDir,
      ledgerLines: ledger.shown.split('\n').length,
    });
    console.log('[RealAppHarness] Closing a worktree session kept it in the ledger and left the worktree alone.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
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
