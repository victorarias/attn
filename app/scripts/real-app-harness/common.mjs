import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { waitForFirstWorkspacePane, waitForPaneVisible } from './scenarioAssertions.mjs';
import {
  assertProductionRunAllowed,
  currentHarnessProfile,
  defaultAppPathForProfile,
  defaultWSURLForProfile,
  deepLinkSchemeForProfile,
  profileForAppPath,
} from './harnessProfile.mjs';

export function parseCommonArgs(argv) {
  // Default to the active profile's install (the safe dev sibling unless
  // ATTN_PROFILE/ATTN_HARNESS_PROFILE says otherwise). Production requires both
  // an explicit prod target and the --run-against-prod acknowledgement.
  const options = {
    wsUrl: process.env.ATTN_REAL_APP_WS_URL || null,
    appPath: process.env.ATTN_REAL_APP_PATH || null,
    artifactsDir: process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
    sessionRootDir: process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
    runAgainstProd: false,
  };
  let wsUrlExplicit = Boolean(process.env.ATTN_REAL_APP_WS_URL);
  let appPathExplicit = Boolean(process.env.ATTN_REAL_APP_PATH);

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ws-url') { options.wsUrl = argv[++index]; wsUrlExplicit = true; }
    else if (arg === '--app-path') { options.appPath = argv[++index]; appPathExplicit = true; }
    else if (arg === '--artifacts-dir') options.artifactsDir = argv[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = argv[++index];
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  const safetyArgv = argv.length > 0 ? argv : process.argv.slice(2);
  const isHelp = options.help || safetyArgv.includes('--help') || safetyArgv.includes('-h');
  // For --help we do not resolve the active profile's resources: a named
  // profile resolves via `attn profile resolve`, which needs ./attn built, and
  // help should never require a build. printCommonHelp resolves defensively.
  if (isHelp) return options;

  // Resolve the active profile's defaults for anything not set explicitly. The
  // ws URL follows the (possibly explicit) app path's profile, so an explicit
  // prod --app-path also routes to the prod daemon.
  if (!appPathExplicit) options.appPath = defaultAppPathForProfile();
  if (!wsUrlExplicit) options.wsUrl = defaultWSURLForProfile(profileForAppPath(options.appPath));

  assertCommonTargetAllowed(options, safetyArgv);
  return options;
}

export function assertCommonTargetAllowed(options, argv = process.argv.slice(2)) {
  const safetyArgv = options.runAgainstProd ? ['--run-against-prod'] : argv;
  assertProductionRunAllowed({ appPath: options.appPath, wsUrl: options.wsUrl }, safetyArgv);
}

export function printCommonHelp(scriptName) {
  // Show the active profile and its resolved defaults so it is always obvious
  // which world a run targets. currentHarnessProfile() needs no binary, so the
  // label is always accurate; the per-profile resources need ./attn for a named
  // profile, so show an honest placeholder (never a mislabeled dev fallback) if
  // it is not built yet.
  const profile = currentHarnessProfile();
  const label = profile === '' ? 'production' : profile;
  let wsUrl;
  let appPath;
  try {
    wsUrl = defaultWSURLForProfile(profile);
    appPath = defaultAppPathForProfile(profile);
  } catch {
    wsUrl = '(unresolved — build ./attn with `make dev`)';
    appPath = '(unresolved — build ./attn with `make dev`)';
  }

  console.log(`Usage: pnpm exec node ${scriptName} [options]

Active profile: ${label}  (set ATTN_PROFILE, or ATTN_HARNESS_PROFILE to override; see docs/profiles.md)

Options:
  --ws-url <url>             Daemon websocket URL (default: ${wsUrl})
  --app-path <path>          Packaged app path (default: ${appPath})
  --artifacts-dir <path>     Directory for screenshots and summary output
  --session-root-dir <path>  Directory where harness-created session cwd roots are created
  --run-against-prod         Explicitly allow targeting the production app
`);
}

// Contract: a driving agent greps stdout for the LAST line starting with the
// `ATTN_VERDICT ` prefix and JSON.parses the remainder. Keep the payload on a
// single line (compact JSON; no pretty-print) and free of embedded newlines so
// naive line-oriented parsing never splits it.
export const ATTN_VERDICT_PREFIX = 'ATTN_VERDICT ';

// Contract: verdict.firstFailure is capped at this length (see ATTN_VERDICT_PREFIX
// above) so it can never contain a newline or otherwise break the one-line
// contract. Shared by every producer of a firstFailure field (scenarioRunner.mjs,
// rssBaselineVerdict.mjs).
export const FIRST_FAILURE_MAX_LENGTH = 300;

export function formatVerdictLine(verdict) {
  return `${ATTN_VERDICT_PREFIX}${JSON.stringify(verdict)}`;
}

export function emitVerdict(verdict) {
  console.log(formatVerdictLine(verdict));
}

export function ensureDir(dirPath) {
  fs.mkdirSync(dirPath, { recursive: true });
}

export async function captureScreenshot(driver, outputPath) {
  try {
    await driver.screenshot(outputPath);
    return true;
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    console.warn(`[RealAppHarness] Screenshot skipped: ${message}`);
    return false;
  }
}

// A harness session ROW can outlive the fresh-world process sweep: the
// daemon persists sessions in its store, so a process-level cleanup (killing
// pty-workers, restarting the daemon) does not remove a session a prior run
// left behind. Any session whose cwd sits under the shared harness sessions
// root is stale by definition at a *fresh* launch — this run has not created
// one yet — so sweep them before scenarios start, surfacing what was found
// rather than silently scrubbing it (a leaked session is diagnostic signal:
// it means a prior run's own cleanup, e.g. a scenario's finally block, failed).
//
// Cleanup goes through the daemon's `unregister` command (via the observer),
// not the bridge's `close_session`: handleCloseSession (App.tsx) refuses to
// close a chief-of-staff session by design (isChiefOfStaffSession guard), and
// a leaked stale session is very often exactly that — a scenario's chief.
// That guard exists to protect a live user's chief from an accidental close,
// not to protect a dead run's leftovers, so it's the wrong gate for this
// path; `unregister` is the same underlying op the app itself uses via
// sendUnregisterSession, minus that interactive guard.
// Path-component-aware containment: '/a/b-old/x' is NOT under '/a/b'. The
// sweep may tear down chief-of-staff sessions, so this boundary must hold.
export function isDirectoryUnderRoot(directory, roots) {
  if (!directory) return false;
  return roots.some((root) => directory === root || directory.startsWith(root + path.sep));
}

export async function sweepStaleHarnessSessions(observer, {
  sessionRootDir = process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
  log = (m) => console.log(`[harness] ${m}`),
  timeoutMs = 15_000,
} = {}) {
  // Ground truth is the DAEMON, via the observer's WebSocket snapshot — not
  // the bridge's get_state. Right after launchFreshAppAndConnect's connect(),
  // the frontend session store may not have loaded the daemon's persisted
  // sessions yet, so a bridge get_state here can race and see zero stale
  // sessions while the daemon still holds one. observer.sessionsById is
  // populated as part of observer.connect() (already awaited by the caller),
  // so it reflects the daemon's state, not the frontend's catch-up lag.
  //
  // The daemon store resolves symlinks in the cwd it persists (macOS
  // `os.tmpdir()` returns `/var/folders/...`, but the daemon records the
  // realpath `/private/var/folders/...`); the bridge's `cwd` does not. Match
  // both the configured root and its realpath form so this filter isn't
  // silently defeated by the symlink.
  const rootCandidates = [...new Set([sessionRootDir, (() => {
    try {
      return fs.realpathSync(sessionRootDir);
    } catch {
      return sessionRootDir;
    }
  })()])];
  const isUnderSessionRoot = (directory) => isDirectoryUnderRoot(directory, rootCandidates);
  const stale = [...observer.sessionsById.values()].filter((session) => isUnderSessionRoot(session.directory));
  if (stale.length === 0) {
    return { swept: 0 };
  }

  log(`stale harness sessions=${stale.length} from a previous run — sweeping: `
    + stale.map((session) => `${session.id} label=${session.label} state=${session.state}`).join('; '));

  const staleIds = new Set(stale.map((session) => session.id));

  // The daemon refuses to unregister a chief-of-staff session too — by
  // design, at both the app and daemon layers. Demoting first is the
  // sanctioned removal path; demoting a non-chief is a no-op, so it's safe to
  // send for every stale id rather than figuring out which ones are chiefs.
  // No wait between demote and unregister: both go out on the observer's
  // single WebSocket connection, so the daemon processes them in order.
  for (const id of staleIds) {
    try {
      observer.send({ cmd: 'set_chief_of_staff', session_id: id, chief_of_staff: false });
    } catch (error) {
      // A dead socket here still falls through to unregisterMatchingSessions,
      // whose own error reporting will surface the failure below.
      log(`demote (set_chief_of_staff) for ${id} failed: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  // unregisterMatchingSessions's own timeout error already names the
  // surviving session ids and includes a full id/label/state/directory
  // snapshot (daemonObserver.mjs describeState), so it needs no wrapping.
  await observer.unregisterMatchingSessions((session) => staleIds.has(session.id), timeoutMs);

  return { swept: stale.length };
}

export async function launchFreshAppAndConnect(client, observer, { sweepStaleSessions = true } = {}) {
  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);
  // A fresh profile's first launch shows the one-time What's New modal, which
  // swallows native HID clicks; dismiss it so scenarios start on a clean UI.
  await client.request('dismiss_whats_new', {}).catch(() => {});
  await observer.connect();
  if (sweepStaleSessions) {
    await sweepStaleHarnessSessions(observer);
  }
}

export async function relaunchAppAndConnect(client, observer) {
  await client.quitApp();
  // Relaunch scenarios (e.g. tr205) depend on sessions surviving the
  // relaunch, so the stale-session sweep must not run here.
  await launchFreshAppAndConnect(client, observer, { sweepStaleSessions: false });
}

export async function createSessionAndWaitForInitialPane({
  client,
  observer,
  cwd,
  label,
  agent,
  endpointId = null,
  sessionWaitMs = 30_000,
  promptReadyFn = null,
  promptReadyTimeoutMs = 45_000,
  waitForInitialPaneVisible,
  initialPaneWaitMs,
}) {
  const shouldWaitForInitialPane = waitForInitialPaneVisible ?? true;
  const paneWaitMs = initialPaneWaitMs ?? 20_000;
  const result = await client.request('create_session', {
    cwd,
    label,
    agent,
    ...(endpointId ? { endpoint_id: endpointId } : {}),
  });
  await observer.waitForSession({ id: result.sessionId, timeoutMs: sessionWaitMs });
  if (typeof promptReadyFn === 'function') {
    await promptReadyFn(client, result.sessionId, promptReadyTimeoutMs);
  }
  if (shouldWaitForInitialPane) {
    const pane = await waitForFirstWorkspacePane(client, result.sessionId, 'initial workspace pane', paneWaitMs);
    await waitForPaneVisible(client, result.sessionId, pane.paneId, paneWaitMs);
  }
  return result.sessionId;
}

export function timestampSlug() {
  return new Date().toISOString().replace(/[:.]/g, '-');
}

export function createRunContext(options, prefix) {
  ensureDir(options.artifactsDir);
  ensureDir(options.sessionRootDir);
  options.sessionRootDir = fs.realpathSync(options.sessionRootDir);

  const runId = `${prefix}-${timestampSlug()}`;
  const runDir = path.join(options.artifactsDir, runId);
  const sessionDir = path.join(options.sessionRootDir, runId);
  ensureDir(runDir);
  ensureDir(sessionDir);
  return { runId, runDir, sessionDir };
}

export async function bootstrapPackagedAppSession({
  driver,
  observer,
  runDir,
  sessionDir,
  sessionLabel,
}) {
  await driver.launchApp();
  await observer.connect();
  await driver.activateBackground();
  await captureScreenshot(driver, path.join(runDir, '01-app-launched.png'));

  // Scheme follows the selected app path so an explicit prod target opens
  // prod while the default dev target stays isolated.
  const scheme = deepLinkSchemeForProfile(profileForAppPath(driver.appPath));
  const deepLink = `${scheme}://spawn?cwd=${encodeURIComponent(sessionDir)}&label=${encodeURIComponent(sessionLabel)}`;
  console.log(`[RealAppHarness] deepLink=${deepLink}`);
  await driver.openDeepLink(deepLink);

  const session = await observer.waitForSession({
    label: sessionLabel,
    directory: sessionDir,
    timeoutMs: 30_000,
  });

  console.log(`[RealAppHarness] session=${session.id} agent=${session.agent} state=${session.state}`);

  await observer.waitForWorkspace(
    session.id,
    (workspace) => (workspace.panes || []).length >= 1,
    `initial workspace for session ${session.id}`,
    30_000
  );

  await driver.activateBackground();
  await captureScreenshot(driver, path.join(runDir, '02-session-opened.png'));
  return session;
}

export async function splitAndFocusUtilityPane({
  driver,
  observer,
  sessionId,
  runDir,
  screenshotName,
  clickX = 0.75,
  clickY = 0.5,
}) {
  const existingPaneIds = new Set(
    (observer.getWorkspace(sessionId)?.panes || []).map((pane) => pane.pane_id),
  );
  await driver.pressKey('d', { command: true });
  const utilityPane = await observer.waitForUtilityPane(sessionId, 20_000, existingPaneIds);
  if (!utilityPane?.runtime_id) {
    throw new Error(`Utility pane missing runtime_id for session ${sessionId}`);
  }
  console.log(`[RealAppHarness] utilityPane=${utilityPane.pane_id} runtime=${utilityPane.runtime_id}`);

  await driver.activateBackground();
  await driver.clickWindow(clickX, clickY);
  if (runDir && screenshotName) {
    await captureScreenshot(driver, path.join(runDir, screenshotName));
  }

  return utilityPane;
}

export async function typeIntoFocusedPane(driver, text) {
  await driver.typeText(text);
  await driver.pressEnter();
}
