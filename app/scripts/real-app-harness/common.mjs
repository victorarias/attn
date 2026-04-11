import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { waitForPaneVisible } from './scenarioAssertions.mjs';

export function parseCommonArgs(argv) {
  const options = {
    wsUrl: process.env.ATTN_REAL_APP_WS_URL || 'ws://127.0.0.1:9849/ws',
    appPath: process.env.ATTN_REAL_APP_PATH || path.join(os.homedir(), 'Applications', 'attn.app'),
    artifactsDir: process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
    sessionRootDir: process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ws-url') options.wsUrl = argv[++index];
    else if (arg === '--app-path') options.appPath = argv[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = argv[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = argv[++index];
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  return options;
}

export function printCommonHelp(scriptName) {
  console.log(`Usage: pnpm exec node ${scriptName} [options]

Options:
  --ws-url <url>             Daemon websocket URL (default: ws://127.0.0.1:9849/ws)
  --app-path <path>          Packaged app path (default: ~/Applications/attn.app)
  --artifacts-dir <path>     Directory for screenshots and summary output
  --session-root-dir <path>  Directory where harness-created session cwd roots are created
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

export async function createSessionAndWaitForMain({
  client,
  observer,
  cwd,
  label,
  agent,
  endpointId = null,
  sessionWaitMs = 30_000,
  promptReadyFn = null,
  promptReadyTimeoutMs = 45_000,
  waitForMainVisible = true,
  mainWaitMs = 20_000,
}) {
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
  if (waitForMainVisible) {
    await waitForPaneVisible(client, result.sessionId, 'main', mainWaitMs);
  }
  return result.sessionId;
}

export function timestampSlug() {
  return new Date().toISOString().replace(/[:.]/g, '-');
}

export function createRunContext(options, prefix) {
  ensureDir(options.artifactsDir);
  ensureDir(options.sessionRootDir);

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
  await driver.activateApp();
  await captureScreenshot(driver, path.join(runDir, '01-app-launched.png'));

  const deepLink = `attn://spawn?cwd=${encodeURIComponent(sessionDir)}&label=${encodeURIComponent(sessionLabel)}`;
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

  await driver.activateApp();
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

  await driver.activateApp();
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
