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

async function waitForBridgeSession(client, cwd, label, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastSessions = [];

  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state');
    lastSessions = state.sessions || [];
    const session = lastSessions.find((entry) => entry.cwd === cwd && entry.label === label);
    if (session) {
      return session;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for bridge session ${label} at ${cwd}. Sessions seen:\n${JSON.stringify(lastSessions, null, 2)}`
  );
}

function shellPanes(workspace) {
  return (workspace?.panes || []).filter((pane) => pane.kind === 'shell' && pane.runtime_id);
}

async function waitForPaneText(client, sessionId, paneId, needle, timeoutMs = 8_000) {
  const startedAt = Date.now();
  let lastText = '';
  let lastPayload = null;
  const compactNeedle = needle.replace(/\s+/g, '');

  while (Date.now() - startedAt < timeoutMs) {
    lastPayload = await client.request('read_pane_text', { sessionId, paneId });
    lastText = lastPayload?.text || '';
    if (lastText.includes(needle) || lastText.replace(/\s+/g, '').includes(compactNeedle)) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for pane text in ${paneId} to contain ${JSON.stringify(needle)}. Last pane text tail:\n${lastText.slice(-400)}`
  );
}

async function captureBridgeDebugArtifacts(client, runDir, suffix = 'failure') {
  try {
    const structuredSnapshot = await client.request('capture_structured_snapshot');
    saveJson(path.join(runDir, `structured-snapshot-${suffix}.json`), structuredSnapshot);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `structured-snapshot-${suffix}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }

  try {
    const debugDump = await client.request('dump_pane_debug');
    saveJson(path.join(runDir, `pane-debug-${suffix}.json`), debugDump);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `pane-debug-${suffix}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }

  try {
    const screenshot = await client.request('capture_screenshot', {
      path: path.join(runDir, `ui-${suffix}.png`),
    });
    saveJson(path.join(runDir, `screenshot-${suffix}.json`), screenshot);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `screenshot-${suffix}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }
}

async function tryCaptureScreenshot(client, runDir, filename) {
  try {
    await client.request('capture_screenshot', {
      path: path.join(runDir, filename),
    });
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${filename}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-repro-shell-return.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'bridge-repro-shell-return');
  const sessionLabel = `attn-bridge-${runId}`;
  const firstToken = `__ATTN_BRIDGE_SHELL_ONE_${Date.now()}__`;
  const secondToken = `__ATTN_BRIDGE_SHELL_TWO_${Date.now()}__`;
  const revisitToken = `__ATTN_BRIDGE_SHELL_REVISIT_${Date.now()}__`;

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();

    await client.request('set_pane_debug', { enabled: true });
    await tryCaptureScreenshot(client, runDir, '01-app-launched.png');

    const createResult = await client.request('create_session', {
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
    });
    const createdSessionId = createResult.sessionId;
    const uiSession = await waitForBridgeSession(client, sessionDir, sessionLabel, 10_000);
    const uiSessionId = uiSession.id || createdSessionId;
    const session = await observer.waitForSession({
      label: sessionLabel,
      directory: sessionDir,
      timeoutMs: 30_000,
    });
    const daemonSessionId = session.id;
    if (createdSessionId !== uiSessionId) {
      console.warn(`[RealAppHarness] Bridge create_session returned ${createdSessionId} but active UI session resolved to ${uiSessionId}`);
    }
    if (uiSessionId !== daemonSessionId) {
      console.warn(`[RealAppHarness] Session ID mismatch bridge=${uiSessionId} daemon=${daemonSessionId}; using bridge id for UI actions and daemon id for PTY observation`);
    }

    const resolveUiSessionId = async () => {
      const current = await waitForBridgeSession(client, sessionDir, sessionLabel, 10_000);
      return current.id;
    };

    await tryCaptureScreenshot(client, runDir, '02-session-created.png');

    const initialWorkspace = await client.request('get_workspace', { sessionId: await resolveUiSessionId() });
    await client.request('split_pane', {
      sessionId: daemonSessionId,
      targetPaneId: initialWorkspace.activePaneId || 'main',
      direction: 'vertical',
    });

    const workspaceWithOneShell = await observer.waitForWorkspace(
      daemonSessionId,
      (entry) => shellPanes(entry).length >= 1,
      `first utility pane for session ${daemonSessionId}`,
      20_000
    );
    const firstShell = shellPanes(workspaceWithOneShell)[0];
    if (!firstShell?.runtime_id) {
      throw new Error('First shell runtime not found');
    }

    await client.request('focus_pane', {
      sessionId: await resolveUiSessionId(),
      paneId: firstShell.pane_id,
    });
    await tryCaptureScreenshot(client, runDir, '03-first-shell-focused.png');
    await client.request('type_pane_via_ui', {
      sessionId: await resolveUiSessionId(),
      paneId: firstShell.pane_id,
      text: firstToken,
    });
    const firstShellVisible = await waitForPaneText(client, await resolveUiSessionId(), firstShell.pane_id, firstToken, 10_000);
    fs.writeFileSync(path.join(runDir, 'first-shell-text.txt'), firstShellVisible.text || '', 'utf8');

    await client.request('split_pane', {
      sessionId: daemonSessionId,
      targetPaneId: firstShell.pane_id,
      direction: 'vertical',
    });

    const workspaceWithTwoShells = await observer.waitForWorkspace(
      daemonSessionId,
      (entry) => shellPanes(entry).length >= 2,
      `second utility pane for session ${daemonSessionId}`,
      20_000
    );
    const shells = shellPanes(workspaceWithTwoShells);
    const secondShell = shells.find((pane) => pane.pane_id !== firstShell.pane_id);
    if (!secondShell?.runtime_id) {
      throw new Error('Second shell runtime not found');
    }

    await client.request('focus_pane', {
      sessionId: await resolveUiSessionId(),
      paneId: secondShell.pane_id,
    });
    await tryCaptureScreenshot(client, runDir, '04-second-shell-focused.png');
    await client.request('type_pane_via_ui', {
      sessionId: await resolveUiSessionId(),
      paneId: secondShell.pane_id,
      text: secondToken,
    });
    const secondShellVisible = await waitForPaneText(client, await resolveUiSessionId(), secondShell.pane_id, secondToken, 10_000);
    fs.writeFileSync(path.join(runDir, 'second-shell-text.txt'), secondShellVisible.text || '', 'utf8');

    await client.request('focus_pane', {
      sessionId: await resolveUiSessionId(),
      paneId: firstShell.pane_id,
    });
    await tryCaptureScreenshot(client, runDir, '05-first-shell-refocused.png');
    await client.request('type_pane_via_ui', {
      sessionId: await resolveUiSessionId(),
      paneId: firstShell.pane_id,
      text: revisitToken,
    });
    const firstShellRevisit = await waitForPaneText(client, await resolveUiSessionId(), firstShell.pane_id, revisitToken, 10_000);
    fs.writeFileSync(path.join(runDir, 'first-shell-revisit-text.txt'), firstShellRevisit.text || '', 'utf8');

    await tryCaptureScreenshot(client, runDir, '06-first-shell-after-revisit.png');

    const debugDump = await client.request('dump_pane_debug');
    saveJson(path.join(runDir, 'pane-debug.json'), debugDump);
    const structuredSnapshot = await client.request('capture_structured_snapshot');
    saveJson(path.join(runDir, 'structured-snapshot.json'), structuredSnapshot);

    const summary = {
      ok: true,
      runId,
      uiSessionId,
      daemonSessionId,
      panes: {
        firstShell,
        secondShell,
      },
      tokens: {
        firstToken,
        secondToken,
        revisitToken,
      },
      artifacts: {
        runDir,
        screenshots: [
          '01-app-launched.png',
          '02-session-created.png',
          '03-first-shell-focused.png',
          '04-second-shell-focused.png',
          '05-first-shell-refocused.png',
          '06-first-shell-after-revisit.png',
        ],
        texts: [
          'first-shell-text.txt',
          'second-shell-text.txt',
          'first-shell-revisit-text.txt',
          'pane-debug.json',
          'structured-snapshot.json',
        ],
      },
    };
    saveJson(path.join(runDir, 'summary.json'), summary);
    console.log('[RealAppHarness] Bridge shell-return repro passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    await captureBridgeDebugArtifacts(client, runDir);
    throw error;
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Bridge shell-return repro failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
