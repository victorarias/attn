#!/usr/bin/env node

/**
 * Packaged-app proof for Slice 5's scheduled-trigger automation: a `scheduled`
 * definition fires at its intended minute, survives a daemon restart per its
 * catch_up policy, does real merged-worktree cleanup in a visible ticket/
 * session, never touches a dirty worktree, and coalesces later occurrences
 * onto the same singleton ticket/session. The scenario quits the profile's app
 * first (ensureFreshWorld) because a running app would respawn the daemon during
 * the simulated downtime and invalidate the restart-catch-up leg.
 *
 * Two definitions are applied against one isolated profile daemon:
 *   - `scheduled-cleanup-<suffix>`: cron `* * * * *`, policy.continuity
 *     singleton, policy.catch_up latest, a REAL `codex` launch (no
 *     executable override) pointed at a local fixture repo with one
 *     fully-merged clean worktree and one dirty worktree. Proves the full
 *     restart/catch-up/cleanup/coalescing story with genuine agent work.
 *   - `scheduled-storm-guard-<suffix>`: cron `* * * * *`, policy.continuity
 *     fresh, policy.catch_up latest, launched against a FAKE codex
 *     executable (argv logger, like the Slice 4 continuity scenario's
 *     probe) so the storm-guard re-assertion in leg 4 is cheap and fast.
 *     It re-proves "one restart catch-up run regardless of missed
 *     instants" under a different continuity policy; skip-vs-latest
 *     discard beyond the 5-minute grace is already covered by
 *     internal/daemon/automations_schedule_test.go and is not re-proven
 *     live here (see leg 4 below for why).
 *
 * Run serially (packaged-app scenarios are single-tenant):
 *   ATTN_HARNESS_PROFILE=<name> node scripts/real-app-harness/scenario-automation-scheduled-cleanup.mjs
 */

import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { currentHarnessProfile, dataDirForProfile, resolveHarnessResources } from './harnessProfile.mjs';
import { ensureFreshWorld } from './freshWorld.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Real-time budgets. The daemon's automation-schedule ticker fires once a
// real minute; these windows are sized around that cadence plus margin, not
// around wall-clock convenience.
const ANCHOR_POLL_TIMEOUT_MS = 90_000; // the anchor tick lands within one 60s ticker interval of apply, plus margin.
const DOWNTIME_MS = 135_000; // >= two whole-minute instants missed while stopped.
const RESTART_RUN_TIMEOUT_MS = 120_000; // daemon start + first post-restart tick + delivery.
const CLEANUP_EVIDENCE_TIMEOUT_MS = 240_000; // real codex agent doing real git work.
const COALESCE_TIMEOUT_MS = 90_000; // one more live tick after cleanup evidence lands.

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs(args);
  return { options, help: args.includes('--help') || args.includes('-h') };
}

function profileEnv(profile, extra = {}) {
  const env = { ...process.env, ATTN_PROFILE: profile, ...extra };
  for (const key of ['ATTN_SOCKET_PATH', 'ATTN_DB_PATH', 'ATTN_CONFIG_PATH', 'ATTN_PLUGIN_DIR']) {
    delete env[key];
  }
  return env;
}

function run(binary, args, env, options = {}) {
  return execFileSync(binary, args, {
    encoding: 'utf8',
    env,
    stdio: options.stdio || ['ignore', 'pipe', 'pipe'],
    timeout: options.timeout || 30_000,
  });
}

function runJSON(binary, args, env) {
  return JSON.parse(run(binary, args, env));
}

// `enable`/`disable` are the only way to move the enabled column post-PR5;
// both print the updated (lowercase) definition summary.
function disableDefinition(binary, id, env) {
  return runJSON(binary, ['automation', 'disable', id], env);
}

async function poll(fn, description, timeoutMs = 30_000) {
  const started = Date.now();
  let last = null;
  while (Date.now() - started < timeoutMs) {
    last = await fn();
    if (last) return last;
    await delay(100);
  }
  throw new Error(`timed out waiting for ${description}; last=${JSON.stringify(last)}`);
}

function sqliteRow(dbPath, sql) {
  const out = execFileSync('sqlite3', ['-cmd', '.timeout 5000', dbPath, sql], { encoding: 'utf8' }).trim();
  return out.length === 0 ? null : out.split('|');
}

function sqlEscape(value) {
  return value.replaceAll("'", "''");
}

// --- fixture repo -----------------------------------------------------

function gitConfigIdentity(repoDir) {
  execFileSync('git', ['config', 'user.name', 'attn'], { cwd: repoDir });
  execFileSync('git', ['config', 'user.email', 'attn@local'], { cwd: repoDir });
}

function createFixture(root) {
  const repo = path.join(root, 'repo');
  const worktrees = path.join(root, 'worktrees');
  fs.mkdirSync(repo, { recursive: true });
  fs.mkdirSync(worktrees, { recursive: true });

  execFileSync('git', ['init', '-q'], { cwd: repo });
  gitConfigIdentity(repo);
  // Deterministic default-branch name regardless of the host's
  // init.defaultBranch config or git version's -b support.
  execFileSync('git', ['symbolic-ref', 'HEAD', 'refs/heads/main'], { cwd: repo });
  fs.writeFileSync(path.join(repo, 'README.md'), 'Scheduled cleanup fixture.\n');
  execFileSync('git', ['add', 'README.md'], { cwd: repo });
  execFileSync('git', ['commit', '-q', '-m', 'initial'], { cwd: repo });

  // merged-work: fully merged into main, worktree stays clean -> eligible for removal.
  execFileSync('git', ['checkout', '-q', '-b', 'merged-work'], { cwd: repo });
  fs.writeFileSync(path.join(repo, 'merged-work.txt'), 'merged work\n');
  execFileSync('git', ['add', 'merged-work.txt'], { cwd: repo });
  execFileSync('git', ['commit', '-q', '-m', 'merged work'], { cwd: repo });
  execFileSync('git', ['checkout', '-q', 'main'], { cwd: repo });
  execFileSync('git', ['merge', '-q', '--no-ff', 'merged-work', '-m', 'Merge merged-work'], { cwd: repo });

  const mergedClean = path.join(worktrees, 'merged-clean');
  execFileSync('git', ['worktree', 'add', mergedClean, 'merged-work'], { cwd: repo });

  // wip: dirty worktree -> must be preserved regardless of merge status.
  execFileSync('git', ['branch', 'wip', 'main'], { cwd: repo });
  const dirtyWip = path.join(worktrees, 'dirty-wip');
  execFileSync('git', ['worktree', 'add', dirtyWip, 'wip'], { cwd: repo });
  fs.writeFileSync(path.join(dirtyWip, 'scratch.txt'), 'uncommitted work in progress\n');

  return { repo, worktrees, mergedClean, dirtyWip };
}

function worktreeListShows(repo, absolutePath) {
  const out = execFileSync('git', ['worktree', 'list', '--porcelain'], { cwd: repo, encoding: 'utf8' });
  return out.includes(absolutePath);
}

// --- fake codex probe (storm-guard leg only; see file header) ---------

function createCodexProbe(root) {
  const log = path.join(root, 'codex-invocations.jsonl');
  const executable = path.join(root, 'codex-probe.mjs');
  fs.writeFileSync(
    executable,
    `#!/usr/bin/env node\nimport fs from 'node:fs';\nfs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({argv: process.argv.slice(2), at: new Date().toISOString()}) + '\\n');\nsetInterval(() => {}, 1000);\n`,
    { mode: 0o700 },
  );
  return { executable, log };
}

function invocations(log) {
  if (!fs.existsSync(log)) return [];
  return fs.readFileSync(log, 'utf8').trim().split('\n').filter(Boolean).map((line) => JSON.parse(line));
}

// --- automation definition YAML ----------------------------------------

const API_VERSION = 'attn.dev/automations/v1alpha1';

// `enabled` is not a spec field post-PR5 (column-only; a YAML carrying
// `enabled:` is rejected outright — errEnabledManagedOutsideSpec in
// internal/automation/automation.go), so neither template below emits it.
// Every apply of a brand-new id is inserted enabled regardless
// (store.UpsertAutomationDefinition); teardown/leg-end disabling now goes
// through `automation disable <id>` (disableDefinition above) instead of a
// reapply with `enabled: false`.
function cleanupDefinitionYAML({ id, locationPath }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 5 packaged scheduled cleanup proof
trigger:
  type: scheduled
  schedule:
    cron: "* * * * *"
    time_zone: UTC
  continuity: singleton
  catch_up: latest
prompt: |
  Review git worktrees of \`repo/\`; remove with \`git worktree remove\` (never --force) each linked worktree whose branch is fully merged into main AND whose tree is completely clean, then delete that fully-merged branch with \`git branch -d\`. NEVER remove a worktree with staged, unstaged, or untracked changes — list preserved worktrees with reasons. Summarize actions in the ticket.
launch:
  driver: codex
  model: gpt-5.5
  effort: medium
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

function stormGuardDefinitionYAML({ id, locationPath, executable }) {
  return `api_version: ${API_VERSION}
id: ${id}
name: Slice 5 scheduler storm-guard probe
trigger:
  type: scheduled
  schedule:
    cron: "* * * * *"
    time_zone: UTC
  continuity: fresh
  catch_up: latest
prompt: |
  Scheduler storm-guard probe. Do nothing; this executable is a test double.
launch:
  driver: codex
  executable: ${JSON.stringify(executable)}
  model: slice5-storm-probe
  effort: high
location:
  type: directory
  path: ${JSON.stringify(locationPath)}
`;
}

// The tick AFTER the anchor tick legitimately fires a run (one minute boundary
// falls between any two ticks of a `* * * * *` schedule), so asserting "no run
// yet" after a fixed sleep races that second tick. Poll for the cursor row the
// anchor tick writes instead, and let the caller assert zero runs immediately
// — the caller then has most of a minute to act (e.g. stop the daemon) before
// a run can legally fire.
async function waitForScheduleAnchor(dbPath, definitionID) {
  await poll(
    () => sqliteRow(dbPath, `SELECT observed_at FROM automation_provider_cursors WHERE definition_id='${sqlEscape(definitionID)}' AND provider='schedule' AND scope='*';`),
    `schedule cursor anchor for ${definitionID}`,
    ANCHOR_POLL_TIMEOUT_MS,
  );
}

async function waitForDaemonReady(binary, daemonEnv) {
  await poll(() => {
    try {
      runJSON(binary, ['automation', 'list'], daemonEnv);
      return { ready: true };
    } catch {
      return null;
    }
  }, 'profile daemon');
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-automation-scheduled-cleanup.mjs');
    return;
  }
  const profile = currentHarnessProfile();
  if (!profile) throw new Error('automation scheduled-cleanup scenario requires a named non-production profile');
  const resources = resolveHarnessResources(profile);
  const binary = path.join(resources.appPath, 'Contents', 'MacOS', 'attn');
  const dbPath = path.join(dataDirForProfile(profile), 'attn.db');
  const runner = createScenarioRunner(options, {
    scenarioId: 'AUTOMATION-SCHEDULED-CLEANUP',
    tier: 'tier2-local-real-agent',
    prefix: 'automation-scheduled-cleanup',
    metadata: { profile, provider: 'local fixture repo', legFour: 'storm-guard re-assertion (fresh continuity); skip-discard-beyond-grace covered by unit tests' },
  });

  const suffix = Date.now().toString(36);
  const cleanupID = `scheduled-cleanup-${suffix}`;
  const stormGuardID = `scheduled-storm-guard-${suffix}`;
  const fixtureRoot = fs.realpathSync(fs.mkdtempSync(path.join(runner.sessionDir, 'scheduled-cleanup-')));
  const cleanupDefinitionFile = path.join(runner.sessionDir, 'scheduled-cleanup.yml');
  const stormGuardDefinitionFile = path.join(runner.sessionDir, 'scheduled-storm-guard.yml');

  let daemonEnv = null;
  let fixture = null;
  let probe = null;
  let cleanupTicketID = '';
  let cleanupSessionID = '';
  let cleanupApplied = false;
  let stormGuardApplied = false;

  try {
    daemonEnv = profileEnv(profile);
    fixture = createFixture(fixtureRoot);
    probe = createCodexProbe(runner.sessionDir);

    await runner.step('restart_isolated_daemon', async () => {
      await ensureFreshWorld({ profile, appPath: resources.appPath });
      try { run(binary, ['daemon', 'stop'], daemonEnv); } catch {}
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
    });

    await runner.step('leg1_apply_and_anchor', async () => {
      fs.writeFileSync(cleanupDefinitionFile, cleanupDefinitionYAML({ id: cleanupID, locationPath: fixtureRoot }));
      runJSON(binary, ['automation', 'apply', '--file', cleanupDefinitionFile], daemonEnv);
      cleanupApplied = true;
      // First observation after apply only anchors the cursor; it must not fire.
      await waitForScheduleAnchor(dbPath, cleanupID);
      const rows = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
      runner.assert(rows.length === 0, 'no run fires on the anchor-only tick', { rows });
    });

    await runner.step('leg1_restart_catchup', async () => {
      run(binary, ['daemon', 'stop'], daemonEnv);
      await delay(DOWNTIME_MS);
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);
      const rows = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
        return list.length >= 1 ? list : null;
      }, 'restart catch-up run', RESTART_RUN_TIMEOUT_MS);
      runner.assert(rows.length === 1, 'exactly one catch-up run fires despite multiple missed instants (latest policy)', { rows });
      const runRow = rows[0];
      cleanupTicketID = runRow.ticket_id;
      cleanupSessionID = runRow.session_id;
      runner.assert(Boolean(cleanupTicketID) && Boolean(cleanupSessionID), 'catch-up run reserves a ticket and session', runRow);

      const occurrence = sqliteRow(
        dbPath,
        `SELECT o.occurrence_key FROM automation_occurrences o JOIN automation_runs r ON r.occurrence_id=o.id WHERE r.id='${sqlEscape(runRow.id)}';`,
      );
      runner.assert(occurrence !== null, 'catch-up run has a resolvable occurrence row', { runID: runRow.id });
      const occurrenceKey = occurrence[0];
      runner.assert(
        /^scheduled:\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:00Z$/.test(occurrenceKey),
        'occurrence key carries the scheduled prefix and is minute-aligned',
        { occurrenceKey },
      );
    });

    await runner.step('leg2_real_cleanup_evidence', async () => {
      await poll(() => {
        const removedFromDisk = !fs.existsSync(fixture.mergedClean);
        const removedFromGit = !worktreeListShows(fixture.repo, fixture.mergedClean);
        return removedFromDisk && removedFromGit ? true : null;
      }, 'merged-clean worktree removed by the agent', CLEANUP_EVIDENCE_TIMEOUT_MS);
      runner.assert(!fs.existsSync(fixture.mergedClean), 'merged-clean worktree directory is gone from disk');
      runner.assert(!worktreeListShows(fixture.repo, fixture.mergedClean), 'merged-clean worktree is untracked by git worktree list');
      runner.assert(fs.existsSync(fixture.dirtyWip), 'dirty-wip worktree directory is preserved');
      runner.assert(fs.existsSync(path.join(fixture.dirtyWip, 'scratch.txt')), 'dirty-wip uncommitted file is untouched');
      runner.assert(worktreeListShows(fixture.repo, fixture.dirtyWip), 'dirty-wip worktree is still tracked by git worktree list');

      // Deliberately weak on agent prose (per brief): filesystem outcome above is
      // the primary evidence. This only checks the ticket did not fail outright
      // and has at least the delivery activity row.
      const ticket = sqliteRow(
        dbPath,
        `SELECT status, (SELECT COUNT(*) FROM ticket_activity WHERE ticket_id=tickets.id) FROM tickets WHERE id='${sqlEscape(cleanupTicketID)}';`,
      );
      runner.assert(ticket !== null, 'cleanup ticket row exists', { cleanupTicketID });
      const [ticketStatus, activityCountRaw] = ticket;
      runner.assert(ticketStatus !== 'failed', 'cleanup ticket did not fail', { ticketStatus });
      runner.assert(Number(activityCountRaw) >= 1, 'cleanup ticket has recorded activity', { activityCount: activityCountRaw });
    });

    await runner.step('leg3_singleton_coalescing', async () => {
      // The definition is still enabled and cron fires every minute, so by the
      // time leg 2's (possibly multi-minute) cleanup wait completes, later
      // occurrences have typically already coalesced onto the same ticket.
      // Poll rather than a fixed ~75s sleep so a fast leg 2 still gets one more
      // real tick before this asserts.
      const rows = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', cleanupID], daemonEnv) || [];
        return list.length >= 2 ? list : null;
      }, 'a second coalesced occurrence', COALESCE_TIMEOUT_MS);
      runner.assert(rows.length >= 2, 'at least a second occurrence fired while enabled', { count: rows.length });
      const tickets = new Set(rows.map((row) => row.ticket_id));
      const sessions = new Set(rows.map((row) => row.session_id));
      runner.assert(
        tickets.size === 1 && tickets.has(cleanupTicketID),
        'every occurrence for this definition coalesces onto the same singleton ticket',
        { tickets: [...tickets] },
      );
      runner.assert(
        sessions.size === 1 && sessions.has(cleanupSessionID),
        'every occurrence reuses the same live session; no duplicate session is spawned',
        { sessions: [...sessions] },
      );

      // Later occurrences coalescing onto the same singleton agent must never
      // touch the dirty worktree either, re-asserting leg 2's evidence after
      // at least one more real tick has nudged the same live session.
      runner.assert(fs.existsSync(fixture.dirtyWip), 'dirty-wip worktree directory is still preserved after coalescing');
      runner.assert(fs.existsSync(path.join(fixture.dirtyWip, 'scratch.txt')), 'dirty-wip uncommitted file is still untouched after coalescing');
      runner.assert(worktreeListShows(fixture.repo, fixture.dirtyWip), 'dirty-wip worktree is still tracked by git worktree list after coalescing');

      disableDefinition(binary, cleanupID, daemonEnv);
    });

    // Leg 4: storm-guard re-assertion. A true skip-vs-latest discard proof needs
    // downtime past the 5-minute grace (internal/daemon/automations_schedule.go
    // scheduleSkipGrace); that would push this scenario's wall-clock budget well
    // past ~15 minutes. Per the brief's option (b), skip-discard-beyond-grace is
    // left to internal/daemon/automations_schedule_test.go (asserts skip fires
    // nothing past the grace window) and this leg instead re-proves "restart
    // catch-up fires exactly one run despite missed instants" under a SEPARATE
    // continuity policy (fresh, not singleton) and a fresh definition ID, using a
    // fake codex executable so the re-assertion is fast and cheap.
    await runner.step('leg4_storm_guard_restart', async () => {
      fs.writeFileSync(
        stormGuardDefinitionFile,
        stormGuardDefinitionYAML({ id: stormGuardID, locationPath: fixtureRoot, executable: probe.executable }),
      );
      runJSON(binary, ['automation', 'apply', '--file', stormGuardDefinitionFile], daemonEnv);
      stormGuardApplied = true;
      await waitForScheduleAnchor(dbPath, stormGuardID);
      const anchoredRows = runJSON(binary, ['automation', 'runs', stormGuardID], daemonEnv) || [];
      runner.assert(anchoredRows.length === 0, 'storm-guard probe does not fire on its anchor-only tick', { anchoredRows });

      run(binary, ['daemon', 'stop'], daemonEnv);
      await delay(DOWNTIME_MS);
      run(binary, ['daemon', 'ensure'], daemonEnv);
      await waitForDaemonReady(binary, daemonEnv);

      // Poll until the run row exists AND has actually been delivered (its
      // spawn already happened), not merely created: waiting on existence
      // alone can race the NEXT real minute tick under fresh continuity (a
      // second, distinct run+spawn) if delivery is slow. Disable the
      // definition immediately once delivered — stopping further fires
      // before polling for the probe invocation — so that race window can't
      // widen while this step keeps running.
      const rows = await poll(() => {
        const list = runJSON(binary, ['automation', 'runs', stormGuardID], daemonEnv) || [];
        const delivered = list.filter((row) => row.state === 'delivered');
        return delivered.length >= 1 ? delivered : null;
      }, 'storm-guard restart catch-up run delivered', RESTART_RUN_TIMEOUT_MS);
      runner.assert(rows.length === 1, 'storm-guard: exactly one catch-up run under fresh continuity too', { rows });

      disableDefinition(binary, stormGuardID, daemonEnv);

      await poll(() => (invocations(probe.log).length >= 1 ? invocations(probe.log) : null), 'storm-guard probe launch');
      runner.assert(invocations(probe.log).length === 1, 'exactly one process spawn backs the single catch-up run (no replay storm)', {
        invocations: invocations(probe.log),
      });
    });

    runner.finishSuccess({ profile, cleanupID, stormGuardID, cleanupTicketID, cleanupSessionID, fixtureRoot });
  } catch (error) {
    runner.finishFailure(error, { profile, cleanupID, stormGuardID, cleanupTicketID, cleanupSessionID, fixtureRoot });
    throw error;
  } finally {
    // Disable both definitions before the fixture directory disappears: an
    // enabled `directory`-location scheduled definition re-validates its path
    // (os.Stat) on every future tick, so leaving one enabled against a deleted
    // temp root would spam schedule-observation errors on this profile forever.
    if (daemonEnv) {
      if (cleanupApplied) { try { disableDefinition(binary, cleanupID, daemonEnv); } catch {} }
      if (stormGuardApplied) { try { disableDefinition(binary, stormGuardID, daemonEnv); } catch {} }
    }
    try { fs.rmSync(fixtureRoot, { recursive: true, force: true }); } catch {}
    // daemonEnv never diverged from a plain profile daemon (no mock provider,
    // no CODEX_HOME override) here, so a single idempotent `ensure` is enough
    // to leave a healthy daemon behind — no stop/swap cycle needed.
    try { run(binary, ['daemon', 'ensure'], profileEnv(profile)); } catch {}
    runner.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
