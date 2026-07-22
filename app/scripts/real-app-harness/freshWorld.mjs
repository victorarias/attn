import { execFileSync, spawnSync } from 'node:child_process';
import {
  bundleIdentifierForProfile,
  currentHarnessProfile,
  defaultAppPathForProfile,
  isProductionHarnessTarget,
} from './harnessProfile.mjs';

// Matrix runs get poisoned by leftover world state from an aborted/failed
// prior run: leaked `pty-worker` processes and stale daemon/app instances
// make the *next* run fail with fake timeouts that look like real
// regressions. `ensureFreshWorld` quits the app, stops the daemon, and kills
// any leaked pty-worker children for the target profile before the matrix
// runs its first scenario.

/**
 * Pure guard: throws when the target is ambiguous or production-shaped.
 * No side effects. Call this before doing anything to the world.
 */
export function assertFreshWorldTargetSafe({ profile, appPath } = {}) {
  if (!profile) {
    throw new Error(`fresh-world preflight refused: profile is empty/falsy (profile=${JSON.stringify(profile)}).`);
  }
  if (!appPath) {
    throw new Error(`fresh-world preflight refused: appPath is empty/falsy (appPath=${JSON.stringify(appPath)}).`);
  }
  // Mirror harnessProfile.mjs's private normalizeProfile: 'default' is the
  // documented alias for the empty/production profile. Without this, a
  // caller passing the raw string 'default' alongside a non-prod-shaped
  // appPath would slip past isProductionHarnessTarget's profile === '' check
  // below and let the preflight scrub a production-aliased target.
  const normalizedProfile = profile.trim().toLowerCase();
  if (normalizedProfile === '' || normalizedProfile === 'default') {
    throw new Error(
      `fresh-world preflight refused: profile ${JSON.stringify(profile)} is the production alias `
      + '(\'default\' collapses to production). Refusing to quit/scrub a production app or daemon.',
    );
  }
  if (isProductionHarnessTarget({ profile, appPath })) {
    throw new Error(
      `fresh-world preflight refused: target looks like production (profile=${JSON.stringify(profile)}, `
      + `appPath=${JSON.stringify(appPath)}). Refusing to quit/scrub a production app or daemon.`,
    );
  }
}

// The worker binary path, e.g. "<appPath>/Contents/MacOS/attn". Matching is
// keyed on this full app path (never a bare "pty-worker" pattern) so a
// fresh-world run for one profile can never touch another profile's — or
// production's — workers, even if they happen to be running side by side.
function attnBinaryPath(appPath) {
  return `${appPath}/Contents/MacOS/attn`;
}

// pgrep -f exits 1 (no matches) which is a normal, expected outcome here —
// treat it as "no pids", not an error.
function pgrepFullCommand(pattern) {
  const result = spawnSync('pgrep', ['-f', pattern], { encoding: 'utf8' });
  if (result.status !== 0 && result.status !== 1) {
    throw new Error(`pgrep -f ${JSON.stringify(pattern)} failed: ${result.stderr || result.status}`);
  }
  return (result.stdout || '')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => Number(line))
    .filter((pid) => Number.isInteger(pid));
}

function commandLineForPid(pid) {
  const result = spawnSync('ps', ['-o', 'command=', '-p', String(pid)], { encoding: 'utf8' });
  return (result.stdout || '').trim();
}

// pids whose full command line contains both the app binary path and
// 'pty-worker' — i.e. leaked pty-worker children of *this profile's* app.
function findLeakedWorkerPids(appPath) {
  const binPath = attnBinaryPath(appPath);
  return pgrepFullCommand(binPath).filter((pid) => commandLineForPid(pid).includes('pty-worker'));
}

// Any process (app, daemon, or worker) whose command line still references
// this profile's app binary path. Used both to detect a pre-existing app
// instance and, after cleanup, to verify nothing survived.
function findAnySurvivingPids(appPath) {
  return pgrepFullCommand(attnBinaryPath(appPath));
}

async function sleep(ms) {
  return new Promise((resolve) => { setTimeout(resolve, ms); });
}

/**
 * Quit the app, stop the daemon, and kill leaked pty-worker processes for
 * `profile`/`appPath`, then verify nothing survives. Always safe-guarded by
 * `assertFreshWorldTargetSafe` first.
 */
export async function ensureFreshWorld({
  profile = currentHarnessProfile(),
  appPath = defaultAppPathForProfile(profile),
  log = (m) => console.log(`[fresh-world] ${m}`),
  timeoutMs = 20_000,
} = {}) {
  assertFreshWorldTargetSafe({ profile, appPath });

  const appWasRunning = findAnySurvivingPids(appPath).length > 0;
  const bundleId = bundleIdentifierForProfile(profile);

  log(`quitting app bundle ${bundleId}${appWasRunning ? ' (was running)' : ' (not running)'}`);
  try {
    execFileSync('osascript', ['-e', `tell application id "${bundleId}" to quit`], { stdio: 'pipe' });
  } catch {
    // The app may not be running — that's fine, not an error for this preflight.
  }

  let daemonStopped = false;
  log(`stopping daemon for profile '${profile}'`);
  const stopResult = spawnSync(`${appPath}/Contents/MacOS/attn`, ['daemon', 'stop'], {
    env: { ...process.env, ATTN_PROFILE: profile },
    encoding: 'utf8',
  });
  if (stopResult.error) {
    log(`daemon stop could not run: ${stopResult.error.message} (continuing — daemon may already be down)`);
  } else if (stopResult.status === 0) {
    daemonStopped = true;
  } else {
    log(`daemon stop exited ${stopResult.status} (no daemon running is expected here)`);
  }

  const leakedPids = findLeakedWorkerPids(appPath);
  if (leakedPids.length > 0) {
    log(`leaked pty-worker pids=[${leakedPids.join(', ')}] from a previous run — killing`);
    for (const pid of leakedPids) {
      try {
        process.kill(pid, 'SIGTERM');
      } catch {
        // Already gone.
      }
    }
    await sleep(2_000);
    for (const pid of leakedPids) {
      try {
        process.kill(pid, 0); // Still alive?
        log(`pty-worker pid=${pid} survived SIGTERM — sending SIGKILL`);
        process.kill(pid, 'SIGKILL');
      } catch {
        // Already gone — expected for the common case.
      }
    }
  } else {
    log('no leaked pty-worker processes found');
  }

  const deadline = Date.now() + timeoutMs;
  let survivors = findAnySurvivingPids(appPath);
  while (survivors.length > 0 && Date.now() < deadline) {
    await sleep(200);
    survivors = findAnySurvivingPids(appPath);
  }
  if (survivors.length > 0) {
    const detail = survivors.map((pid) => `${pid}: ${commandLineForPid(pid)}`).join('; ');
    throw new Error(`fresh-world preflight failed: processes survived cleanup — ${detail}`);
  }

  const summary = {
    appWasRunning,
    daemonStopped,
    leakedWorkersKilled: leakedPids.length,
  };
  log(`fresh world ready: appWasRunning=${summary.appWasRunning} daemonStopped=${summary.daemonStopped} leakedWorkersKilled=${summary.leakedWorkersKilled}`);
  return summary;
}
