#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-smoke.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'bridge-smoke');
  const sessionLabel = `attn-bridge-${runId}`;
  const utilityToken = `__ATTN_BRIDGE_UTILITY_${Date.now()}__`;

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

    const initialState = await client.request('get_workspace', { sessionId });
    const targetPaneId = initialState.activePaneId || 'main';
    await client.request('split_pane', {
      sessionId,
      targetPaneId,
      direction: 'vertical',
    });

    const workspace = await observer.waitForWorkspace(
      sessionId,
      (entry) => (entry.panes || []).filter((pane) => pane.kind === 'shell').length >= 1,
      `utility workspace for session ${sessionId}`,
      20_000
    );
    const utilityPane = (workspace.panes || []).find((pane) => pane.kind === 'shell' && pane.runtime_id);
    if (!utilityPane?.runtime_id) {
      throw new Error('Utility pane runtime not found');
    }

    await client.request('focus_pane', {
      sessionId,
      paneId: utilityPane.pane_id,
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

    const summary = {
      ok: true,
      runId,
      sessionId,
      utilityPane,
      utilityToken,
      artifacts: {
        runDir,
        utilityScrollback: 'utility-scrollback.txt',
      },
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Bridge smoke passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Bridge smoke failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
