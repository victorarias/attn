#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import {
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, resolveHarnessResources, profileCliEnv as profileEnv } from './harnessProfile.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureScreenshotData } from './nativeWindowCapture.mjs';
import { appDaemonInTree, delay } from './platform.mjs';

const PANEL_APPEAR_TIMEOUT_MS = 120_000;
const REFRESH_VISIBLE_TIMEOUT_MS = 60_000;
const REMOVAL_TIMEOUT_MS = 60_000;
const RESTART_READY_TIMEOUT_MS = 60_000;
const DELEGATE_TIMEOUT_MS = 180_000;

// Measured on this fixture (macOS, APFS): 6.5 s of `git status --untracked-files=all`
// and 1.9 s of tree walking across the worktrees. attn's own 147 cost ~12 s.
const SLOW_REPO_FILES = 40_000;
const FILLER_WORKTREES = 6;

const BRIEF = 'Worktree surface proof: this delegation exists to own a worktree.';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  return { options: parseCommonArgs(args), help: args.includes('--help') || args.includes('-h') };
}

function run(binary, args, env, options = {}) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: options.stdio || ['ignore', 'pipe', 'pipe'],
    timeout: options.timeout || 120_000,
  });
}

function runJSON(binary, args, env, options) {
  return JSON.parse(run(binary, args, env, options));
}

const GIT_ENV = {
  GIT_AUTHOR_NAME: 'harness', GIT_AUTHOR_EMAIL: 'harness@test',
  GIT_COMMITTER_NAME: 'harness', GIT_COMMITTER_EMAIL: 'harness@test',
};

// Quiet and unbuffered: committing 40k files overflows execFileSync's stdout
// buffer, which fails the run with ENOBUFS rather than anything about git.
function git(dir, args) {
  execFileSync('git', args, { cwd: dir, env: { ...process.env, ...GIT_ENV }, stdio: 'ignore' });
}

function gitOut(dir, args) {
  return execFileSync('git', args, { cwd: dir, encoding: 'utf8', env: { ...process.env, ...GIT_ENV } });
}

async function poll(fn, description, timeoutMs, everyMs = 250) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(everyMs);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

async function waitForDaemonReady(binary, daemonEnv) {
  await poll(() => {
    try {
      runJSON(binary, ['worktree', 'list', '--json'], daemonEnv);
      return { ready: true };
    } catch {
      return null;
    }
  }, 'profile daemon', RESTART_READY_TIMEOUT_MS);
}

function fillWithFiles(dir, count) {
  const buckets = 40;
  for (let bucket = 0; bucket < buckets; bucket += 1) {
    const bucketDir = path.join(dir, 'bulk', `b${bucket}`);
    fs.mkdirSync(bucketDir, { recursive: true });
    for (let i = 0; i < count / buckets; i += 1) {
      fs.writeFileSync(path.join(bucketDir, `f${i}.txt`), `${bucket}:${i}\n`);
    }
  }
}

// Every worktree is cut from the pushed tip, so all of them read merged. What
// separates them is the gate each one then trips.
function buildFixtureRepo(root) {
  const origin = path.join(root, 'origin.git');
  const main = path.join(root, 'main');
  fs.mkdirSync(main, { recursive: true });
  git(root, ['init', '--bare', '-q', 'origin.git']);
  git(main, ['init', '-q', '-b', 'main']);
  git(main, ['remote', 'add', 'origin', origin]);
  fs.writeFileSync(path.join(main, 'README.md'), 'fixture\n');
  git(main, ['add', '.']);
  git(main, ['commit', '-q', '-m', 'seed']);
  fillWithFiles(main, SLOW_REPO_FILES);
  git(main, ['add', '.']);
  git(main, ['commit', '-q', '-m', 'bulk']);
  git(main, ['push', '-q', '-u', 'origin', 'main']);
  const base = gitOut(main, ['rev-parse', 'HEAD']).trim();

  const worktrees = {};
  const names = ['merged', 'pinned', 'dirty'];
  for (let i = 0; i < FILLER_WORKTREES; i += 1) names.push(`spare${i}`);
  for (const name of names) {
    const wt = path.join(root, `wt-${name}`);
    git(main, ['worktree', 'add', '-q', '-b', `feat/${name}`, wt, base]);
    worktrees[name] = fs.realpathSync(wt);
  }
  fs.writeFileSync(path.join(worktrees.dirty, 'wip.txt'), 'unfinished\n');
  return { main: fs.realpathSync(main), worktrees, count: names.length };
}

function seedIDs(binary, daemonEnv) {
  const listed = runJSON(binary, ['seed', 'ls', '--flat', '--json'], daemonEnv);
  return new Set((listed.seeds || []).map((seed) => seed.id));
}

function rowFor(state, worktreePath) {
  return (state?.rows || []).find((row) => row.path === worktreePath) || null;
}

function refreshingCount(state) {
  const rows = (state?.rows || []).filter((row) => row.refreshing).length;
  const repositories = (state?.repositories || []).filter((repo) => repo.refreshing).length;
  return rows + repositories;
}

async function captureFailureEvidence(runner, client) {
  try {
    runner.writeJson('failure-worktrees-state.json', await client.request('worktrees_get_state'));
  } catch (error) {
    runner.log('failure_evidence_state_error', { error: error instanceof Error ? error.message : String(error) });
  }
  try {
    await captureScreenshotData(path.join(runner.runDir, 'failure.png'), { client });
  } catch (error) {
    runner.log('failure_evidence_screenshot_error', { error: error instanceof Error ? error.message : String(error) });
  }
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-worktree-surface.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('worktree surface scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = appDaemonInTree(resources.appPath);
  const runner = createScenarioRunner(options, {
    scenarioId: 'WORKTREE-SURFACE',
    allowRealAgents: false,
    tier: 'tier2-local-fake-agent',
    prefix: 'worktree-surface',
    metadata: { profile, panel: 'Worktrees dock panel', slowRepoFiles: SLOW_REPO_FILES },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver(options);

  let daemonEnv = null;
  let fixture = null;
  let ownerSession = null;
  let delegateSession = null;
  let seed = '';

  try {
    daemonEnv = profileEnv(profile);

    await runner.step('restart_isolated_daemon', async () => {
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('build_slow_repository', async () => {
      const started = Date.now();
      fixture = buildFixtureRepo(runner.sessionDir);
      runner.log('fixture_built', {
        main: fixture.main, worktrees: fixture.count, files: SLOW_REPO_FILES, ms: Date.now() - started,
      });
    });

    await runner.step('launch_packaged_app', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    // A session is what makes the daemon track a repository at all, and it is
    // also the source a delegation needs.
    await runner.step('open_a_session_in_the_repository', async () => {
      ownerSession = await createSessionAndWaitForInitialPane({
        client, observer, cwd: fixture.main, label: 'wtmain', agent: 'shell',
      });
    });

    await runner.step('delegate_into_one_worktree', async () => {
      const before = seedIDs(binary, daemonEnv);
      run(binary, [
        'delegate', '--agent', 'shell', '--model', 'none', '--no-worktree',
        '--cwd', fixture.worktrees.merged, '--name', 'wtproof',
        '--source-session', ownerSession, '--brief', BRIEF,
      ], daemonEnv, { timeout: DELEGATE_TIMEOUT_MS });
      const planted = [...seedIDs(binary, daemonEnv)].filter((id) => !before.has(id));
      runner.assert(planted.length === 1, 'the delegation planted exactly one seed', planted);
      seed = planted[0];
      delegateSession = [...observer.sessionsById.values()]
        .find((session) => session.directory === fixture.worktrees.merged)?.id || null;
      runner.assert(Boolean(delegateSession), 'the delegate session runs in the worktree', {
        seed, worktree: fixture.worktrees.merged,
      });
    });

    await runner.step('leg1_the_panel_says_what_it_would_do_and_why', async () => {
      await client.request('worktrees_open_panel');
      await client.request('worktrees_refresh');
      const state = await poll(async () => {
        const current = await client.request('worktrees_get_state');
        return rowFor(current, fixture.worktrees.dirty) && rowFor(current, fixture.worktrees.merged)
          ? current : null;
      }, 'the fixture worktrees to appear in the panel', PANEL_APPEAR_TIMEOUT_MS);

      runner.assert(state.rows.length >= fixture.count,
        'every worktree of the repository has a row, not only the ones attn made', {
          rows: state.rows.length, want: fixture.count,
        });
      const dirty = rowFor(state, fixture.worktrees.dirty);
      runner.assert(dirty.chips.some((chip) => chip.startsWith('dirty')),
        'the worktree with uncommitted work shows its dirty chip', dirty);
      runner.assert(/uncommitted|untracked/.test(dirty.reason),
        'the dirty worktree says in words why it is kept', dirty);

      const merged = await poll(async () => {
        const current = await client.request('worktrees_get_state');
        const row = rowFor(current, fixture.worktrees.merged);
        return row && /running in it/.test(row.reason) ? row : null;
      }, 'the delegated session to be named as the reason its worktree is kept', PANEL_APPEAR_TIMEOUT_MS);
      runner.assert(merged.branch === 'feat/merged', 'the row names its branch', merged);
      runner.writeJson('leg1-state.json', state);
    });

    await runner.step('leg2_a_slow_refresh_stays_visible_and_answering', async () => {
      const started = Date.now();
      await client.request('worktrees_refresh');
      const seen = await poll(async () => {
        const current = await client.request('worktrees_get_state');
        const count = refreshingCount(current);
        // The panel reads the registry, so it must keep answering with every row
        // while git is still walking the repository behind it.
        if (count > 0 && (current.rows || []).length >= fixture.count) {
          return { refreshing: count, rows: current.rows.length, afterMs: Date.now() - started };
        }
        return null;
      }, 'a refresh in flight on the surface', REFRESH_VISIBLE_TIMEOUT_MS, 150);
      runner.assert(true, 'the refresh is visible per row while the slow repository is walked', seen);

      const settled = await poll(async () => {
        const current = await client.request('worktrees_get_state');
        return refreshingCount(current) === 0 ? current : null;
      }, 'the refresh to finish', REFRESH_VISIBLE_TIMEOUT_MS);
      runner.log('refresh_pass', { visibleAfterMs: seen.afterMs, totalMs: Date.now() - started });
      runner.assert(settled.rows.length >= fixture.count,
        'every row survives the pass', { rows: settled.rows.length });
    });

    await runner.step('leg3_the_keep_pin_and_its_way_back', async () => {
      const target = fixture.worktrees.pinned;
      await client.request('worktrees_toggle_pin', { path: target });
      const pinned = await poll(async () => {
        const row = rowFor(await client.request('worktrees_get_state'), target);
        return row?.pinned ? row : null;
      }, 'the row to read pinned', PANEL_APPEAR_TIMEOUT_MS);
      runner.assert(/kept forever/.test(pinned.sweep + pinned.reason),
        'a pinned row says it is kept forever', pinned);

      const listed = runJSON(binary, ['worktree', 'list', '--json'], daemonEnv);
      const stored = (listed.worktrees || []).find((row) => row.path === target);
      runner.assert(stored?.pinned === true, 'the CLI sees the pin the app set', stored);

      await client.request('worktrees_toggle_pin', { path: target });
      const released = await poll(async () => {
        const row = rowFor(await client.request('worktrees_get_state'), target);
        return row && !row.pinned ? row : null;
      }, 'the pin to release', PANEL_APPEAR_TIMEOUT_MS);
      runner.assert(!/kept forever/.test(released.sweep),
        'unpinning hands the decision back to the sweep', released);
    });

    await runner.step('leg4_a_removal_lands_on_the_seed_and_in_the_log', async () => {
      const target = fixture.worktrees.merged;
      // The live session is its own gate; closing it first is what leaves the
      // removal to be about the worktree.
      await observer.unregisterMatchingSessions((session) => session.id === delegateSession, 20_000);
      delegateSession = null;

      await client.request('worktrees_delete', { path: target });
      await poll(async () => {
        const current = await client.request('worktrees_get_state');
        return rowFor(current, target) ? null : current;
      }, 'the removed worktree to leave the panel', REMOVAL_TIMEOUT_MS);
      runner.assert(!fs.existsSync(target), 'the worktree is gone from disk', { target });

      await client.request('worktrees_toggle_log');
      const entry = await poll(async () => {
        const current = await client.request('worktrees_get_state');
        return (current.log || []).find((row) => row.path === target) || null;
      }, 'the removal to appear in the sweep log', REMOVAL_TIMEOUT_MS);
      runner.assert(entry.action === 'deleted', 'the log says the removal was a delete', entry);
      runner.assert(/at your request/.test(entry.reason), 'the log entry says who asked', entry);

      const notes = runJSON(binary, ['seed', 'notes', seed, '--json'], daemonEnv);
      const note = (notes.notes || []).find((row) => /attn deleted the worktree/.test(row.body || ''));
      runner.assert(Boolean(note), 'the removal landed as a note on the seed that worked there', notes);
      for (const wanted of [target, 'feat/merged', fixture.main]) {
        runner.assert(note.body.includes(wanted), `the note names ${wanted}`, note);
      }
      runner.writeJson('seed-note.json', note);
    });

    await runner.step('capture_evidence', async () => {
      await captureScreenshotData(path.join(runner.runDir, 'worktrees-panel.png'), { client });
      runner.writeJson('worktrees-state.json', await client.request('worktrees_get_state'));
      runner.writeJson('worktree-list-cli.json', runJSON(binary, ['worktree', 'list', '--json'], daemonEnv));
      runner.writeJson('worktree-log-cli.json', runJSON(binary, ['worktree', 'log', '--json'], daemonEnv));
    });

    await runner.finishSuccess({ profile, seed, main: fixture?.main });
  } catch (error) {
    await captureFailureEvidence(runner, client).catch(() => {});
    await runner.finishFailure(error, { profile, seed, main: fixture?.main });
    throw error;
  } finally {
    for (const id of [delegateSession, ownerSession]) {
      if (!id) continue;
      try {
        await observer.unregisterMatchingSessions((session) => session.id === id, 20_000);
      } catch {}
    }
    try { await client.quitApp(); } catch {}
    try { await observer.close(); } catch {}
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
