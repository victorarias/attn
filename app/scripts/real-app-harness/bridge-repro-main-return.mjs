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

async function waitForPaneText(client, sessionId, paneId, needle, timeoutMs = 8_000) {
  const startedAt = Date.now();
  let lastText = '';
  let lastPayload = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastPayload = await client.request('read_pane_text', { sessionId, paneId });
    lastText = lastPayload?.text || '';
    if (lastText.includes(needle)) {
      return lastPayload;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for pane text in ${paneId} to contain ${JSON.stringify(needle)}. Last pane text tail:\n${lastText.slice(-400)}`
  );
}

async function captureBridgeDebugArtifacts(client, runDir, suffix = 'failure') {
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

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-repro-main-return.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'bridge-repro-main-return');
  const sessionLabel = `attn-bridge-${runId}`;
  const utilityToken = `__ATTN_BRIDGE_UTILITY_${Date.now()}__`;
  const mainToken = `__ATTN_BRIDGE_MAIN_${Date.now()}__`;

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await client.launchApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await observer.connect();

    await client.request('set_pane_debug', { enabled: true });
    await client.request('capture_screenshot', {
      path: path.join(runDir, '01-app-launched.png'),
    });

    const createResult = await client.request('create_session', {
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
    });
    const sessionId = createResult.sessionId;
    const session = await observer.waitForSession({
      label: sessionLabel,
      directory: sessionDir,
      timeoutMs: 30_000,
    });
    if (session.id !== sessionId) {
      throw new Error(`Created session mismatch: bridge=${sessionId} daemon=${session.id}`);
    }

    await client.request('capture_screenshot', {
      path: path.join(runDir, '02-session-created.png'),
    });

    const initialWorkspace = await client.request('get_workspace', { sessionId });
    await client.request('split_pane', {
      sessionId,
      targetPaneId: initialWorkspace.activePaneId || 'main',
      direction: 'vertical',
    });

    const splitWorkspace = await observer.waitForWorkspace(
      sessionId,
      (entry) => (entry.panes || []).some((pane) => pane.kind === 'shell' && pane.runtime_id),
      `utility workspace for session ${sessionId}`,
      20_000
    );
    const utilityPane = (splitWorkspace.panes || []).find((pane) => pane.kind === 'shell' && pane.runtime_id);
    if (!utilityPane?.runtime_id) {
      throw new Error('Utility pane runtime not found');
    }

    await client.request('focus_pane', {
      sessionId,
      paneId: utilityPane.pane_id,
    });
    await client.request('capture_screenshot', {
      path: path.join(runDir, '03-utility-focused.png'),
    });
    await client.request('write_pane', {
      sessionId,
      paneId: utilityPane.pane_id,
      text: `echo ${utilityToken}`,
      submit: true,
    });
    const utilityScrollback = await observer.waitForScrollbackContains(
      utilityPane.runtime_id,
      utilityToken,
      15_000
    );
    fs.writeFileSync(path.join(runDir, 'utility-scrollback.txt'), utilityScrollback, 'utf8');

    await client.request('focus_pane', {
      sessionId,
      paneId: 'main',
    });
    await client.request('capture_screenshot', {
      path: path.join(runDir, '04-main-refocused.png'),
    });
    await client.request('write_pane', {
      sessionId,
      paneId: 'main',
      text: mainToken,
      submit: false,
    });

    const [mainScrollback, mainPaneText] = await Promise.all([
      observer.waitForScrollbackContains(sessionId, mainToken, 10_000),
      waitForPaneText(client, sessionId, 'main', mainToken, 10_000),
    ]);

    fs.writeFileSync(path.join(runDir, 'main-scrollback.txt'), mainScrollback, 'utf8');
    fs.writeFileSync(path.join(runDir, 'main-pane-text.txt'), mainPaneText.text || '', 'utf8');
    await client.request('capture_screenshot', {
      path: path.join(runDir, '05-main-token-visible.png'),
    });

    const debugDump = await client.request('dump_pane_debug');
    saveJson(path.join(runDir, 'pane-debug.json'), debugDump);

    const summary = {
      ok: true,
      runId,
      sessionId,
      utilityPane,
      tokens: {
        utilityToken,
        mainToken,
      },
      artifacts: {
        runDir,
        screenshots: [
          '01-app-launched.png',
          '02-session-created.png',
          '03-utility-focused.png',
          '04-main-refocused.png',
          '05-main-token-visible.png',
        ],
        logs: [
          'utility-scrollback.txt',
          'main-scrollback.txt',
          'main-pane-text.txt',
          'pane-debug.json',
        ],
      },
    };
    saveJson(path.join(runDir, 'summary.json'), summary);
    console.log('[RealAppHarness] Bridge main-return repro passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    await captureBridgeDebugArtifacts(client, runDir);
    throw error;
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Bridge main-return repro failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
