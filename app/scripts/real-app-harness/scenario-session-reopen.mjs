#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile, execFileSync, spawnSync } from 'node:child_process';
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
  fs.writeFileSync(path.join(repo, 'README.md'), '# reopen fixture\n');
  git(repo, 'add', 'README.md');
  git(repo, 'commit', '-q', '-m', 'fixture');
  const worktree = path.join(root, `repo--${branch}`);
  git(repo, 'worktree', 'add', '-q', '-b', branch, worktree);
  return { repo, worktree };
}

function attn(daemonBinary, profile, ...args) {
  return execFileSync(daemonBinary, args, { encoding: 'utf8', env: profileCliEnv(profile) });
}

function attnAllowingFailure(daemonBinary, profile, ...args) {
  const result = spawnSync(daemonBinary, args, { encoding: 'utf8', env: profileCliEnv(profile) });
  return { status: result.status, stdout: result.stdout || '', stderr: result.stderr || '' };
}

async function waitForSessionGone(client, observer, sessionId, timeoutMs) {
  await observer.waitFor(() => !observer.sessionsById.has(sessionId), `session ${sessionId} unregistered`, timeoutMs);
  const deadline = Date.now() + timeoutMs;
  let ui = null;
  while (Date.now() < deadline) {
    ui = await client.request('get_session_ui_state', { sessionId });
    if (ui.exists === false && ui.sidebarItem == null) return ui;
    await sleep(50);
  }
  throw new Error(`Session ${sessionId} stayed visible for ${timeoutMs}ms: ${JSON.stringify(ui, null, 2)}`);
}

async function waitForSessionBack(client, observer, sessionId, timeoutMs) {
  await observer.waitFor(() => observer.sessionsById.has(sessionId), `session ${sessionId} registered again`, timeoutMs);
  const deadline = Date.now() + timeoutMs;
  let ui = null;
  while (Date.now() < deadline) {
    ui = await client.request('get_session_ui_state', { sessionId });
    if (ui.exists === true && ui.sidebarItem != null) return ui;
    await sleep(50);
  }
  throw new Error(`Session ${sessionId} never came back to the app in ${timeoutMs}ms: ${JSON.stringify(ui, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-session-reopen.mjs');
    return;
  }

  const profile = currentHarnessProfile();
  assertFreshWorldTargetSafe({ profile, appPath: options.appPath });
  const daemonBinary = appDaemonInTree(options.appPath);
  const dataDir = dataDirForProfile(profile);
  const runner = createScenarioRunner(options, {
    scenarioId: 'SESSION-REOPEN',
    tier: 'tier1-local-shell',
    prefix: 'session-reopen',
    metadata: {
      agent: 'codex',
      focus: 'a closed worktree session reopens under its own id, and recreates a deleted worktree only when asked',
      profile,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());

  try {
    const branch = `session-reopen-${runner.runId}`;
    const { repo, worktree } = await runner.step('build_worktree', () => buildWorktree(runner.sessionDir, branch));
    writeMockAgentFixture(worktree, {
      // Reopen resumes the saved conversation, so the rollout must sit where the driver looks.
      resumable: true,
      name: 'session reopen mock',
      turns: [],
    });

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
        label: `session-reopen-${runner.runId}`,
        agent: 'codex',
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

    await runner.step('close_the_session', async () => {
      await client.request('select_session', { sessionId });
      await client.request('close_session', { sessionId });
      const ui = await waitForSessionGone(client, observer, sessionId, 15_000);
      runner.writeJson('session-ui-after-close.json', ui);
    });

    await runner.step('the_verdict_says_it_comes_back', async () => {
      const shown = attn(daemonBinary, profile, 'session', 'show', sessionId);
      runner.assert(/^state\s+closed$/m.test(shown), 'session show must report the session as closed', { shown });
      runner.assert(/^reopen\s+yes$/m.test(shown), 'the verdict must offer a plain reopen', { shown });
      runner.assert(/^place\s+directory present/m.test(shown), 'the verdict must read the directory as present', { shown });
      runner.writeText('session-show-closed.txt', shown);
    });

    await runner.step('reopen_from_the_cli', async () => {
      const out = attn(daemonBinary, profile, 'session', 'reopen', sessionId);
      runner.assert(out.includes(sessionId) && out.includes('reopened'), 'reopen must report the session it brought back', { out });
      const ui = await waitForSessionBack(client, observer, sessionId, 60_000);
      const live = attn(daemonBinary, profile, 'session', 'list');
      runner.assert(live.includes(sessionId), 'the reopened session must be live again', { live });
      runner.writeText('session-reopen.txt', out);
      runner.writeJson('session-ui-after-reopen.json', ui);
    });

    const beforeRecreate = await runner.step('close_again_and_delete_the_worktree', async () => {
      await client.request('close_session', { sessionId });
      await waitForSessionGone(client, observer, sessionId, 15_000);
      fs.rmSync(worktree, { recursive: true, force: true });
      runner.assert(!fs.existsSync(worktree), 'the fixture must delete the worktree directory', { worktree });
      return git(repo, 'worktree', 'list', '--porcelain');
    });

    await runner.step('the_verdict_refuses_and_writes_nothing', async () => {
      const shown = attn(daemonBinary, profile, 'session', 'show', sessionId);
      runner.assert(/^reopen\s+no: /m.test(shown), 'the verdict must refuse and say why', { shown });
      runner.assert(/^actions\s+.*recreate_worktree_and_reopen/m.test(shown),
        'the verdict must offer the recreate action', { shown });
      const refused = attnAllowingFailure(daemonBinary, profile, 'session', 'reopen', sessionId);
      runner.assert(refused.status !== 0, 'a plain reopen must be refused', refused);
      runner.assert(refused.stderr.includes('recreate_worktree_and_reopen'),
        'the refusal must name the action offered instead', refused);
      runner.assert(!fs.existsSync(worktree), 'reading a verdict must not put the worktree back', { worktree });
      const after = git(repo, 'worktree', 'list', '--porcelain');
      runner.assert(after === beforeRecreate, 'reading a verdict must not touch the worktree registrations', {
        beforeRecreate,
        after,
      });
      runner.writeText('session-show-refused.txt', shown);
      runner.writeText('reopen-refused.txt', `${refused.stdout}${refused.stderr}`);
    });

    await runner.step('the_recreate_action_brings_it_back', async () => {
      const out = attn(daemonBinary, profile, 'session', 'reopen', sessionId, '--action', 'recreate_worktree_and_reopen');
      runner.assert(out.includes('recreated worktree'), 'the action must report the worktree it recreated', { out });
      runner.assert(fs.existsSync(worktree), 'the recreate action must put the worktree back', { worktree });
      runner.assert(git(worktree, 'rev-parse', '--abbrev-ref', 'HEAD') === branch,
        'the recreated worktree must be on the session branch', { branch });
      const ui = await waitForSessionBack(client, observer, sessionId, 60_000);
      runner.writeText('session-reopen-recreate.txt', out);
      runner.writeJson('session-ui-after-recreate.json', ui);
    });

    const summary = await runner.finishSuccess({ sessionId, worktree, repo, branch, dataDir });
    console.log('[RealAppHarness] A closed worktree session reopened under its own id, worktree recreated on request.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = await runner.finishFailure(error, { sessionId });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) await client.request('close_session', { sessionId }).catch(() => {});
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
