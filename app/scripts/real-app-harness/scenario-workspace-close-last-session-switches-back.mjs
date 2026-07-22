#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import {
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
import { createScenarioRunner } from './scenarioRunner.mjs';

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

async function waitForActiveSession(client, sessionId, description, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_state');
    if (lastState.activeSessionId === sessionId) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForPaneInputFocused(client, sessionId, paneId, description, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId }).catch((error) => ({ error: String(error) }));
    if (lastState?.inputFocused) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last pane state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForSessionGoneFromUi(client, sessionId, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_session_ui_state', { sessionId }).catch((error) => ({ error: String(error) }));
    if (lastState?.exists === false && lastState?.sidebarItem == null) {
      return lastState;
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for ${description}. Last UI state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForSessionAbsentFromDaemon(observer, sessionId, description, timeoutMs = 20_000) {
  await observer.waitFor(
    () => observer.getSession(sessionId) == null ? true : null,
    description,
    timeoutMs,
  );
}

async function assertWorkspaceVisible(runner, client, visibleSessionId, goneSessionId) {
  const visible = await client.request('get_session_ui_state', { sessionId: visibleSessionId });
  const gone = await client.request('get_session_ui_state', { sessionId: goneSessionId });
  runner.assert(
    Boolean(visible.selected),
    `Expected previous workspace session ${visibleSessionId} to be selected: ${JSON.stringify(visible, null, 2)}`,
    visible,
  );
  runner.assert(
    Boolean(visible.workspace?.view?.sessionVisible),
    `Expected previous workspace ${visibleSessionId} to be visible: ${JSON.stringify(visible, null, 2)}`,
    visible,
  );
  runner.assert(
    visible.workspace?.model?.panes?.length === 1,
    `Expected previous workspace to still have one pane: ${JSON.stringify(visible.workspace?.model, null, 2)}`,
    visible,
  );
  runner.assert(
    gone.exists === false && gone.sidebarItem == null,
    `Expected closed workspace session ${goneSessionId} to be absent from sidebar: ${JSON.stringify(gone, null, 2)}`,
    gone,
  );
}

async function closeWorkspacePanes(client, sessionId) {
  for (let attempt = 0; attempt < 10; attempt += 1) {
    const workspace = await client.request('get_workspace', { sessionId }).catch(() => null);
    const pane = workspace?.panes?.[0];
    if (!pane) {
      return;
    }
    await client.request('close_pane', { sessionId, paneId: pane.paneId }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 200));
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
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`Timed out waiting for harness sessions under ${dir} to close: ${JSON.stringify(lastSessions, null, 2)}`);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-workspace-close-last-session-switches-back.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'WORKSPACE-CLOSE-LAST-SESSION-SWITCHES-BACK',
    tier: 'tier1-local-shell',
    prefix: 'workspace-close-last-session-switches-back',
    metadata: {
      agent: 'shell',
      focus: 'closing the last-selected workspace session switches the UI back to the previously selected workspace',
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const createdSessionIds = [];
  const note = (m, extra) => runner.log(m, extra);

  // Cleanup, registered as soon as each resource exists so a signal mid-scenario
  // still tears them down. Runner cleanups run in REVERSE registration order, so
  // register observer/app first (they must close LAST) and the created sessions
  // last (they must close FIRST) to reproduce the effective order below: close
  // created session panes, quitApp, observer.close.
  runner.registerCleanup('close_observer', () => observer.close());
  runner.registerCleanup('quit_app', () => client.quitApp());
  runner.registerCleanup('close_created_sessions', async () => {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, runner.sessionDir).catch(() => {});
  });

  try {
    await runner.step('launch_app', async () => {
      process.env.ATTN_HARNESS_PARK_VISIBLE_PX ??= '0';
      await launchFreshAppAndConnect(client, observer);
      await closeExistingSessions(client, options.sessionRootDir);
    });

    let previous;
    await runner.step('create_previous_workspace', async () => {
      previous = await waitForShellWorkspace(client, observer, path.join(runner.sessionDir, 'previous'), `ws-close-prev-${runner.runId}`);
      createdSessionIds.push(previous.sessionId);
      note(`previous workspace ready`, { sessionId: previous.sessionId });
    });

    let closing;
    await runner.step('create_closing_workspace', async () => {
      closing = await waitForShellWorkspace(client, observer, path.join(runner.sessionDir, 'closing'), `ws-close-target-${runner.runId}`);
      createdSessionIds.push(closing.sessionId);
      note(`closing workspace ready`, { sessionId: closing.sessionId });
    });

    await runner.step('select_and_focus_closing', async () => {
      await client.request('select_session', { sessionId: previous.sessionId });
      await waitForActiveSession(client, previous.sessionId, 'previous workspace selected before close target');
      await client.request('select_session', { sessionId: closing.sessionId });
      await waitForActiveSession(client, closing.sessionId, 'target workspace selected before close shortcut');
      await client.request('focus_pane', { sessionId: closing.sessionId, paneId: closing.pane.paneId });
      await waitForPaneInputFocused(client, closing.sessionId, closing.pane.paneId, 'target pane focused before close shortcut');
      note(`closing workspace selected and focused`, { sessionId: closing.sessionId });
    });

    await runner.step('close_via_shortcut', async () => {
      await client.request('dispatch_shortcut', { shortcutId: 'session.close' });
      await waitForSessionAbsentFromDaemon(observer, closing.sessionId, 'target session unregistered after close shortcut');
      await waitForSessionGoneFromUi(client, closing.sessionId, 'target session gone from UI/sidebar after close shortcut');
      note(`closing workspace session closed`, { sessionId: closing.sessionId });
    });

    await runner.step('verify_switchback', async () => {
      await waitForActiveSession(client, previous.sessionId, 'previous workspace selected after closing target');
      await assertWorkspaceVisible(runner, client, previous.sessionId, closing.sessionId);
      note(`previous workspace selected and visible after switchback`, { sessionId: previous.sessionId });
    });

    const summary = runner.finishSuccess({
      previousSessionId: previous.sessionId,
      closedSessionId: closing.sessionId,
    });
    console.log('[RealAppHarness] Workspace close-last-session switchback passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionIds: createdSessionIds });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    for (const sessionId of [...createdSessionIds].reverse()) {
      await closeWorkspacePanes(client, sessionId).catch(() => {});
    }
    await waitForNoSessionsUnderDir(client, runner.sessionDir).catch(() => {});
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
