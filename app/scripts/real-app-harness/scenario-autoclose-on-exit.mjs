#!/usr/bin/env node

// Verifies auto-close-on-exit: when a session's process exits cleanly (code 0)
// the session closes itself, while a non-zero exit keeps the pane open so the
// error stays readable. Drives the packaged app end-to-end against a real
// shell PTY so the regression is caught in the product, not just unit tests.

import fs from 'node:fs';
import path from 'node:path';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  waitForPaneAttached,
  waitForPaneShellReady,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = parseCommonArgs(args);
  return {
    options,
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function waitForPaneCount(client, sessionId, count, description, timeoutMs = 30_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace?.panes || []).length === count && (workspace?.panes || []).every((pane) => pane.runtimeId),
    description,
    timeoutMs,
  );
}

async function waitForShellWorkspace(client, observer, cwd, label) {
  fs.mkdirSync(cwd, { recursive: true });
  const sessionId = await createSessionAndWaitForInitialPane({
    client,
    observer,
    cwd,
    label,
    agent: 'shell',
    waitForInitialPaneVisible: false,
    sessionWaitMs: 30_000,
  });
  const workspace = await waitForPaneCount(client, sessionId, 1, `initial pane for ${label}`);
  const pane = workspace.panes[0];
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
  await waitForPaneAttached(client, sessionId, pane.paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, pane.paneId, {
    timeoutMs: 20_000,
    description: `shell prompt ready for ${label}`,
  });
  return { sessionId, pane };
}

async function waitForSessionAbsentFromDaemon(observer, sessionId, description, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (observer.getSession(sessionId) == null ? true : null),
    description,
    timeoutMs,
  );
}

async function waitForSessionGoneFromUi(client, sessionId, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_session_ui_state', { sessionId }).catch((error) => ({ error: String(error) }));
    if (lastState?.exists === false && lastState?.sidebarItem == null) {
      return lastState;
    }
    await delay(200);
  }
  throw new Error(`Timed out waiting for ${description}. Last UI state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForPaneTextContains(client, sessionId, paneId, needle, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastText = '';
  while (Date.now() - startedAt < timeoutMs) {
    const result = await client.request('read_pane_text', { sessionId, paneId }).catch((error) => ({ text: '', error: String(error) }));
    lastText = result?.text || '';
    if (lastText.includes(needle)) {
      return lastText;
    }
    await delay(250);
  }
  throw new Error(`Timed out waiting for ${description}. Last pane text tail:\n${lastText.slice(-400)}`);
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await delay(200);
  }
}

async function closeExistingSessions(client, sessionRootDir) {
  const initial = await client.request('get_state');
  const harnessSessions = (initial.sessions || []).filter((session) => session.cwd?.startsWith(sessionRootDir));
  for (const session of harnessSessions) {
    await closeWorkspacePanes(client, session.id).catch(() => {});
  }
}

async function waitForNoSessionsUnderDir(client, dir, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state').catch(() => null);
    lastSessions = (state?.sessions || []).filter((session) => session.cwd?.startsWith(dir));
    if (lastSessions.length === 0) {
      return;
    }
    await delay(200);
  }
  throw new Error(`Timed out waiting for harness sessions under ${dir} to close: ${JSON.stringify(lastSessions, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-autoclose-on-exit.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'autoclose-on-exit');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const createdSessionIds = [];

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
    await launchFreshAppAndConnect(client, observer);
    await closeExistingSessions(client, options.sessionRootDir);

    // --- Clean exit (code 0) auto-closes the session ---
    const clean = await waitForShellWorkspace(client, observer, path.join(sessionDir, 'clean'), `autoclose-clean-${runId}`);
    createdSessionIds.push(clean.sessionId);
    await client.request('write_pane', { sessionId: clean.sessionId, paneId: clean.pane.paneId, text: 'exit', submit: true });
    await waitForSessionAbsentFromDaemon(observer, clean.sessionId, 'clean-exit session unregistered from daemon');
    await waitForSessionGoneFromUi(client, clean.sessionId, 'clean-exit session gone from UI/sidebar');
    console.log('[RealAppHarness] Clean exit auto-closed the session.');

    // --- Non-zero exit (code 1) keeps the session open ---
    const failed = await waitForShellWorkspace(client, observer, path.join(sessionDir, 'failed'), `autoclose-failed-${runId}`);
    createdSessionIds.push(failed.sessionId);
    await client.request('write_pane', { sessionId: failed.sessionId, paneId: failed.pane.paneId, text: 'exit 1', submit: true });
    // The frontend renders the exit banner into the pane model once the process exits.
    await waitForPaneTextContains(
      client,
      failed.sessionId,
      failed.pane.paneId,
      '[Process exited with code 1]',
      'failed-exit pane shows exit banner',
    );
    // Give any (incorrect) auto-close a chance to fire, then assert the session survived.
    await delay(2_000);
    if (observer.getSession(failed.sessionId) == null) {
      throw new Error(`Non-zero exit session ${failed.sessionId} was auto-closed; failed exits must stay open`);
    }
    const failedUi = await client.request('get_session_ui_state', { sessionId: failed.sessionId });
    if (failedUi.exists === false || failedUi.sidebarItem == null) {
      throw new Error(`Non-zero exit session ${failed.sessionId} missing from UI; failed exits must stay open: ${JSON.stringify(failedUi, null, 2)}`);
    }
    console.log('[RealAppHarness] Non-zero exit kept the session open.');

    const summary = {
      ok: true,
      runId,
      cleanSessionId: clean.sessionId,
      failedSessionId: failed.sessionId,
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Auto-close-on-exit passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    for (const sessionId of createdSessionIds.reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, sessionDir).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
