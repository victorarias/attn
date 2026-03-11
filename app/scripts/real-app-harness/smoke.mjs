#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import {
  bootstrapPackagedAppSession,
  captureScreenshot,
  createRunContext,
  parseCommonArgs,
  printCommonHelp,
  splitAndFocusUtilityPane,
  typeIntoFocusedPane,
} from './common.mjs';

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
  const driver = new MacOSDriver({ appPath: options.appPath });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    const session = await bootstrapPackagedAppSession({
      driver,
      observer,
      runDir,
      sessionDir,
      sessionLabel,
    });
    const utilityPane = await splitAndFocusUtilityPane({
      driver,
      observer,
      sessionId: session.id,
      runDir,
      screenshotName: '03-after-split.png',
      clickX: 0.75,
      clickY: 0.5,
    });

    await typeIntoFocusedPane(driver, `echo ${utilityToken}`);

    const utilityScrollback = await observer.waitForScrollbackContains(
      utilityPane.runtime_id,
      utilityToken,
      15_000
    );
    fs.writeFileSync(path.join(runDir, 'utility-scrollback.txt'), utilityScrollback, 'utf8');
    await captureScreenshot(driver, path.join(runDir, '04-utility-output.png'));

    const workspace = observer.getWorkspace(session.id);
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
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Smoke flow failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
