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
  // Default to the isolated dev install. Production requires both an explicit
  // prod target and the --run-against-prod acknowledgement.
  const options = {
    wsUrl: process.env.ATTN_REAL_APP_WS_URL || defaultWSURLForProfile(),
    appPath: process.env.ATTN_REAL_APP_PATH || defaultAppPathForProfile(),
    artifactsDir: process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
    sessionRootDir: process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
    runAgainstProd: false,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ws-url') options.wsUrl = argv[++index];
    else if (arg === '--app-path') options.appPath = argv[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = argv[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = argv[++index];
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  if (!process.env.ATTN_REAL_APP_WS_URL && !argv.includes('--ws-url')) {
    options.wsUrl = defaultWSURLForProfile(profileForAppPath(options.appPath));
  }

  const safetyArgv = argv.length > 0 ? argv : process.argv.slice(2);
  const isHelp = options.help || safetyArgv.includes('--help') || safetyArgv.includes('-h');
  if (!isHelp) assertCommonTargetAllowed(options, safetyArgv);
  return options;
}

export function assertCommonTargetAllowed(options, argv = process.argv.slice(2)) {
  const safetyArgv = options.runAgainstProd ? ['--run-against-prod'] : argv;
  assertProductionRunAllowed({ appPath: options.appPath, wsUrl: options.wsUrl }, safetyArgv);
}

export function printCommonHelp(scriptName) {
  // Show the active profile and its resolved defaults so it is always obvious
  // which world a run targets. Fall back to the dev sibling text if the attn
  // binary needed to resolve a named profile is not built yet.
  let label = 'dev';
  let wsUrl = 'ws://127.0.0.1:29849/ws';
  let appPath = path.join(os.homedir(), 'Applications', 'attn-dev.app');
  try {
    const profile = currentHarnessProfile();
    label = profile === '' ? 'production' : profile;
    wsUrl = defaultWSURLForProfile(profile);
    appPath = defaultAppPathForProfile(profile);
  } catch {
    // Keep the dev fallback above.
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

export async function launchFreshAppAndConnect(client, observer) {
  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);
  await observer.connect();
}

export async function relaunchAppAndConnect(client, observer) {
  await client.quitApp();
  await launchFreshAppAndConnect(client, observer);
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
  await driver.pressKey('d', { command: true });
  const utilityPane = await observer.waitForUtilityPane(sessionId, 20_000);
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
