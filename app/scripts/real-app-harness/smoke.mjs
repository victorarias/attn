#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import {
  captureScreenshot,
  createRunContext,
  createSessionAndWaitForMain,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import {
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneState,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/smoke.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'smoke');
  const sessionLabel = `attn-real-${runId}`;
  const utilityToken = `__ATTN_REAL_UTILITY_${Date.now()}__`;

  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const client = new UiAutomationClient({ appPath: options.appPath });
  const driver = new MacOSDriver({ appPath: options.appPath });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);
    await captureScreenshot(driver, path.join(runDir, '01-app-launched.png'));

    const sessionId = await createSessionAndWaitForMain({
      client,
      observer,
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
      sessionWaitMs: 60_000,
    });
    const session = observer.getSession(sessionId);
    if (!session) {
      throw new Error(`session ${sessionId} missing from observer after creation`);
    }
    console.log(`[RealAppHarness] session=${session.id} agent=${session.agent} state=${session.state}`);
    await captureScreenshot(driver, path.join(runDir, '02-session-opened.png'));

    const workspaceBeforeSplit = await client.request('get_workspace', { sessionId });
    const existingPaneIds = new Set((workspaceBeforeSplit.panes || []).map((pane) => pane.paneId));
    await client.request('split_pane', {
      sessionId,
      targetPaneId: 'main',
      direction: 'vertical',
    });
    const utilityPane = await waitForNewShellPane(
      client,
      sessionId,
      existingPaneIds,
      `utility pane for session ${sessionId}`,
      20_000,
    );
    if (!utilityPane?.runtimeId) {
      throw new Error(`utility pane missing runtimeId for session ${sessionId}`);
    }
    console.log(
      `[RealAppHarness] utilityPane=${utilityPane.paneId} runtime=${utilityPane.runtimeId}`,
    );
    await client.request('focus_pane', { sessionId, paneId: utilityPane.paneId });
    await waitForPaneVisible(client, sessionId, utilityPane.paneId, 20_000);
    await waitForPaneInputFocus(client, sessionId, utilityPane.paneId, 20_000, { stableMs: 400 });
    await waitForPaneState(
      client,
      sessionId,
      utilityPane.paneId,
      (state) => Boolean(state?.renderHealth?.flags?.terminalReady),
      `utility pane ${utilityPane.paneId} terminal ready`,
      20_000,
    );
    await captureScreenshot(driver, path.join(runDir, '03-after-split.png'));

    await client.request('type_pane_via_ui', {
      sessionId,
      paneId: utilityPane.paneId,
      text: `echo ${utilityToken}`,
    });
    await client.request('write_pane', {
      sessionId,
      paneId: utilityPane.paneId,
      text: '\r',
      submit: false,
    });

    // Prefer bridge-side read_pane_text for verification — it walks the same
    // xterm buffer the user sees, without requiring a daemon attach.
    let utilityScrollback = '';
    try {
      const textResult = await (async () => {
        const deadline = Date.now() + 15_000;
        let lastText = '';
        while (Date.now() < deadline) {
          const payload = await client.request('read_pane_text', {
            sessionId,
            paneId: utilityPane.paneId,
          });
          lastText = payload?.text || '';
          if (lastText.includes(utilityToken)) {
            return lastText;
          }
          await new Promise((resolve) => setTimeout(resolve, 300));
        }
        throw new Error(
          `utility pane text never contained ${utilityToken}; last tail:\n${lastText.slice(-400)}`,
        );
      })();
      utilityScrollback = textResult;
    } catch (error) {
      console.warn(
        `[RealAppHarness] read_pane_text path failed; falling back to daemon scrollback: ${error.message}`,
      );
      utilityScrollback = await observer.waitForScrollbackContains(
        utilityPane.runtimeId,
        utilityToken,
        15_000,
      );
    }
    fs.writeFileSync(path.join(runDir, 'utility-scrollback.txt'), utilityScrollback, 'utf8');
    await captureScreenshot(driver, path.join(runDir, '04-utility-output.png'));

    const workspace = observer.getWorkspace(sessionId);
    const summary = {
      ok: true,
      runId,
      session: {
        id: session.id,
        label: session.label,
        directory: session.directory,
        agent: session.agent,
      },
      workspace: workspace
        ? {
            activePaneId: workspace.active_pane_id,
            panes: workspace.panes,
          }
        : null,
      utilityToken,
      artifacts: {
        runDir,
        screenshots: [
          '01-app-launched.png',
          '02-session-opened.png',
          '03-after-split.png',
          '04-utility-output.png',
        ],
        utilityScrollback: 'utility-scrollback.txt',
      },
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');

    console.log('[RealAppHarness] Smoke flow passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Smoke flow failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
