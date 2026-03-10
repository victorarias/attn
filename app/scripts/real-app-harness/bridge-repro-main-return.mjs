#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function compact(text) {
  return text.replace(/\s+/g, '');
}

function createTracer(runDir) {
  const tracePath = path.join(runDir, 'trace.log');
  return {
    log(message, details) {
      const line = `[${new Date().toISOString()}] ${message}${details ? ` ${JSON.stringify(details)}` : ''}\n`;
      fs.appendFileSync(tracePath, line, 'utf8');
      process.stdout.write(line);
    },
  };
}

async function waitForPaneText(request, sessionId, paneId, predicate, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastText = '';
  let lastPayload = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastPayload = await request('read_pane_text', { sessionId, paneId });
    lastText = lastPayload?.text || '';
    if (predicate(lastText)) {
      return lastPayload;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for pane text in ${paneId}. Last pane text tail:\n${lastText.slice(-600)}`
  );
}

async function waitForUiSessionVisible(request, sessionId, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastState = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastState = await request('get_pane_state', { sessionId, paneId: 'main' }).catch(() => null);
    if (lastState?.pane?.bounds) {
      return lastState;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for UI session ${sessionId} to be visible. Last pane state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForNewShellPane(request, sessionId, existingPaneIds, timeoutMs = 12_000) {
  const startedAt = Date.now();
  let lastWorkspace = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastWorkspace = await request('get_workspace', { sessionId });
    const newShells = (lastWorkspace.panes || []).filter(
      (pane) => pane.kind === 'shell' && !existingPaneIds.has(pane.paneId)
    );
    if (newShells.length === 1) {
      return newShells[0];
    }
    if (newShells.length > 1) {
      const activeShell = newShells.find((pane) => pane.paneId === lastWorkspace.activePaneId);
      if (activeShell) {
        return activeShell;
      }
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for new shell pane. Existing pane ids=${JSON.stringify([...existingPaneIds])}. Last workspace:\n${JSON.stringify(lastWorkspace, null, 2)}`
  );
}

async function waitForPaneState(request, sessionId, paneId, predicate, timeoutMs = 12_000) {
  const startedAt = Date.now();
  let lastState = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastState = await request('get_pane_state', { sessionId, paneId });
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(150);
  }

  throw new Error(
    `Timed out waiting for pane state ${paneId}. Last state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForPaneVisible(request, sessionId, paneId, timeoutMs = 12_000) {
  return waitForPaneState(
    request,
    sessionId,
    paneId,
    (state) => Boolean(state?.pane?.bounds && state.pane.bounds.width > 0 && state.pane.bounds.height > 0),
    timeoutMs,
  );
}

async function cleanupPreviousRuns(observer, trace) {
  try {
    await observer.connect();
    const removed = await observer.unregisterMatchingSessions(
      (session) => typeof session.label === 'string' && session.label.startsWith('full-flow-'),
      15_000,
    );
    trace.log('daemon:cleanup', {
      removedSessionIds: removed.map((session) => session.id),
      removedLabels: removed.map((session) => session.label),
    });
  } catch (error) {
    trace.log('daemon:cleanup-skipped', {
      error: error instanceof Error ? error.message : String(error),
    });
  } finally {
    await observer.close();
  }
}

async function captureArtifacts(request, runDir, prefix) {
  try {
    const snapshot = await request('capture_structured_snapshot');
    saveJson(path.join(runDir, `${prefix}-structured-snapshot.json`), snapshot);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-structured-snapshot.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }

  try {
    const debugDump = await request('dump_pane_debug');
    saveJson(path.join(runDir, `${prefix}-pane-debug.json`), debugDump);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-pane-debug.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-repro-main-return.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'bridge-repro-main-return');
  const sessionLabel = `full-flow-${Date.now()}`;
  const trustToken = '1';
  const mainToken1 = `__FLOW_MAIN_ONE_${Date.now()}__`;
  const shellToken = `__FLOW_SHELL_${Date.now()}__`;
  const mainToken2 = `__FLOW_MAIN_TWO_${Date.now()}__`;

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const trace = createTracer(runDir);

  trace.log('runDir', { runDir });
  trace.log('sessionDir', { sessionDir });

  const request = async (action, payload = {}, reqOptions = {}) => {
    trace.log('request:start', { action, payload });
    try {
      const result = await client.request(action, payload, reqOptions);
      trace.log('request:ok', { action, result });
      return result;
    } catch (error) {
      trace.log('request:error', {
        action,
        error: error instanceof Error ? error.message : String(error),
      });
      throw error;
    }
  };

  try {
    await cleanupPreviousRuns(observer, trace);

    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await request('set_pane_debug', { enabled: true });

    const created = await request('create_session', {
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
    });
    const sessionId = created.sessionId;
    trace.log('session:created', { sessionId, label: sessionLabel, cwd: sessionDir });

    await waitForUiSessionVisible(request, sessionId, 20_000);
    await request('get_pane_state', { sessionId, paneId: 'main' });
    await request('read_pane_text', { sessionId, paneId: 'main' });

    await waitForPaneVisible(request, sessionId, 'main', 20_000);

    const trustPrompt = await waitForPaneText(
      request,
      sessionId,
      'main',
      (text) => text.includes('Do you trust this folder?') || text.includes('Security guide'),
      30_000
    );
    fs.writeFileSync(path.join(runDir, '02-trust-prompt.txt'), trustPrompt.text || '', 'utf8');

    await request('select_session', { sessionId });
    await waitForPaneState(request, sessionId, 'main', (state) => state.activePaneId === 'main');
    await request('click_pane', { sessionId, paneId: 'main' });
    await waitForPaneState(request, sessionId, 'main', (state) => state.activePaneId === 'main' && state.inputFocused);
    await request('type_pane_via_ui', { sessionId, paneId: 'main', text: trustToken });

    const trustSelection = await waitForPaneText(
      request,
      sessionId,
      'main',
      (text) => compact(text).includes('1yesitrustthisfolder') || compact(text).includes('1.Yes,Itrustthisfolder'),
      10_000
    );
    fs.writeFileSync(path.join(runDir, '03-trust-selected.txt'), trustSelection.text || '', 'utf8');

    await request('write_pane', {
      sessionId,
      paneId: 'main',
      text: '\r',
      submit: false,
    });

    const trustedMain = await waitForPaneText(
      request,
      sessionId,
      'main',
      (text) => !text.includes('Do you trust this folder?') && compact(text).includes('❯'),
      30_000
    );
    fs.writeFileSync(path.join(runDir, '04-main-ready.txt'), trustedMain.text || '', 'utf8');
    await waitForPaneVisible(request, sessionId, 'main', 10_000);

    await request('click_pane', { sessionId, paneId: 'main' });
    await waitForPaneState(request, sessionId, 'main', (state) => state.activePaneId === 'main' && state.inputFocused);
    await request('type_pane_via_ui', { sessionId, paneId: 'main', text: mainToken1 });
    const mainVisible1 = await waitForPaneText(
      request,
      sessionId,
      'main',
      (text) => compact(text).includes(compact(mainToken1)),
      15_000
    );
    fs.writeFileSync(path.join(runDir, '05-main-token-1.txt'), mainVisible1.text || '', 'utf8');

    const workspaceBeforeSplit = await request('get_workspace', { sessionId });
    const existingPaneIds = new Set((workspaceBeforeSplit.panes || []).map((pane) => pane.paneId));

    await request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
    const shellPane = await waitForNewShellPane(request, sessionId, existingPaneIds, 15_000);
    saveJson(path.join(runDir, '06-shell-pane.json'), shellPane);
    await waitForPaneVisible(request, sessionId, shellPane.paneId, 10_000);

    await request('click_pane', { sessionId, paneId: shellPane.paneId });
    await waitForPaneState(request, sessionId, shellPane.paneId, (state) => state.activePaneId === shellPane.paneId && state.inputFocused);
    await request('type_pane_via_ui', { sessionId, paneId: shellPane.paneId, text: shellToken });
    const shellVisible = await waitForPaneText(
      request,
      sessionId,
      shellPane.paneId,
      (text) => compact(text).includes(compact(shellToken)),
      15_000
    );
    fs.writeFileSync(path.join(runDir, '07-shell-token.txt'), shellVisible.text || '', 'utf8');

    await request('click_pane', { sessionId, paneId: 'main' });
    await waitForPaneState(request, sessionId, 'main', (state) => state.activePaneId === 'main' && state.inputFocused);
    await request('type_pane_via_ui', { sessionId, paneId: 'main', text: mainToken2 });

    const mainVisible2 = await waitForPaneText(
      request,
      sessionId,
      'main',
      (text) => compact(text).includes(compact(mainToken2)),
      15_000
    );
    fs.writeFileSync(path.join(runDir, '08-main-token-2.txt'), mainVisible2.text || '', 'utf8');

    await captureArtifacts(request, runDir, '09-success');

    const summary = {
      ok: true,
      runId,
      sessionId,
      shellPane,
      tokens: {
        mainToken1,
        shellToken,
        mainToken2,
      },
      artifacts: {
        runDir,
        files: [
          '02-trust-prompt.txt',
          '03-trust-selected.txt',
          '04-main-ready.txt',
          '05-main-token-1.txt',
          '06-shell-pane.json',
          '07-shell-token.txt',
          '08-main-token-2.txt',
          '09-success-structured-snapshot.json',
          '09-success-pane-debug.json',
        ],
      },
    };
    saveJson(path.join(runDir, 'summary.json'), summary);
    console.log('[RealAppHarness] Bridge main-return repro passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    await captureArtifacts(request, runDir, 'failure');
    throw error;
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Bridge main-return repro failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
