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

function shellPanes(workspace) {
  return (workspace?.panes || []).filter((pane) => pane.kind === 'shell' && pane.runtime_id);
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/repro-older-pane-writability.mjs');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'repro-older-pane-writability');
  const sessionLabel = `attn-real-${runId}`;
  const firstToken = `__ATTN_OLDER_PANE_FIRST_${Date.now()}__`;
  const secondToken = `__ATTN_OLDER_PANE_SECOND_${Date.now()}__`;
  const revisitToken = `__ATTN_OLDER_PANE_REVISIT_${Date.now()}__`;

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

    const firstUtilityPane = await splitAndFocusUtilityPane({
      driver,
      observer,
      sessionId: session.id,
      runDir,
      screenshotName: '03-after-first-split.png',
      clickX: 0.75,
      clickY: 0.5,
    });
    await typeIntoFocusedPane(driver, `echo ${firstToken}`);
    const firstScrollback = await observer.waitForScrollbackContains(firstUtilityPane.runtime_id, firstToken, 15_000);
    fs.writeFileSync(path.join(runDir, 'utility-1-scrollback.txt'), firstScrollback, 'utf8');

    await driver.pressKey('d', { command: true });
    const workspaceWithThreePanes = await observer.waitForWorkspace(
      session.id,
      (workspace) => shellPanes(workspace).length >= 2,
      `second utility pane for session ${session.id}`,
      20_000
    );
    const secondUtilityPane = shellPanes(workspaceWithThreePanes).find((pane) => pane.pane_id !== firstUtilityPane.pane_id);
    if (!secondUtilityPane?.runtime_id) {
      throw new Error('Second utility pane runtime was not created');
    }
    console.log(`[RealAppHarness] secondUtilityPane=${secondUtilityPane.pane_id} runtime=${secondUtilityPane.runtime_id}`);

    await driver.activateApp();
    await driver.clickWindow(0.875, 0.5);
    await captureScreenshot(driver, path.join(runDir, '04-after-second-split.png'));
    await typeIntoFocusedPane(driver, `echo ${secondToken}`);
    const secondScrollback = await observer.waitForScrollbackContains(secondUtilityPane.runtime_id, secondToken, 15_000);
    fs.writeFileSync(path.join(runDir, 'utility-2-scrollback.txt'), secondScrollback, 'utf8');

    await driver.activateApp();
    await driver.clickWindow(0.625, 0.5);
    await captureScreenshot(driver, path.join(runDir, '05-refocused-older-pane.png'));
    await typeIntoFocusedPane(driver, `echo ${revisitToken}`);
    const revisitScrollback = await observer.waitForScrollbackContains(firstUtilityPane.runtime_id, revisitToken, 15_000);
    fs.writeFileSync(path.join(runDir, 'utility-1-revisit-scrollback.txt'), revisitScrollback, 'utf8');
    await captureScreenshot(driver, path.join(runDir, '06-older-pane-typed.png'));

    const summary = {
      ok: true,
      runId,
      session: {
        id: session.id,
        label: session.label,
        directory: session.directory,
        agent: session.agent,
      },
      panes: {
        firstUtilityPane,
        secondUtilityPane,
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
          '02-session-opened.png',
          '03-after-first-split.png',
          '04-after-second-split.png',
          '05-refocused-older-pane.png',
          '06-older-pane-typed.png',
        ],
        scrollbacks: [
          'utility-1-scrollback.txt',
          'utility-2-scrollback.txt',
          'utility-1-revisit-scrollback.txt',
        ],
      },
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[RealAppHarness] Older-pane writability repro passed.');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Older-pane writability repro failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
