#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import {
  createRunContext,
  createSessionAndWaitForMain,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import {
  compactTerminalText,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneState,
  waitForPaneVisible,
} from './scenarioAssertions.mjs';

async function typeAndWaitForEcho(client, sessionId, paneId, token, timeoutMs = 15_000) {
  await client.request('type_pane_via_ui', { sessionId, paneId, text: `echo ${token}` });
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
  const deadline = Date.now() + timeoutMs;
  let lastText = '';
  // Narrow utility panes wrap long tokens across lines, so match on compacted
  // (whitespace-stripped) text — the echo still proves the pane accepted input.
  while (Date.now() < deadline) {
    const payload = await client.request('read_pane_text', { sessionId, paneId });
    lastText = payload?.text || '';
    if (lastText.includes(token) || compactTerminalText(lastText).includes(token)) {
      return lastText;
    }
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
  throw new Error(
    `pane ${paneId} text never contained ${token}; last tail:\n${lastText.slice(-400)}`,
  );
}

async function focusPaneAndAwaitInput(client, sessionId, paneId) {
  await client.request('focus_pane', { sessionId, paneId });
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneInputFocus(client, sessionId, paneId, 20_000, { stableMs: 400 });
  await waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(state?.renderHealth?.flags?.terminalReady),
    `pane ${paneId} terminal ready`,
    20_000,
  );
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
  const client = new UiAutomationClient({ appPath: options.appPath });

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sessionDir=${sessionDir}`);
  console.log(`[RealAppHarness] wsUrl=${options.wsUrl}`);

  try {
    await launchFreshAppAndConnect(client, observer);

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

    const wsBeforeFirstSplit = await client.request('get_workspace', { sessionId });
    const paneIdsBeforeFirstSplit = new Set((wsBeforeFirstSplit.panes || []).map((pane) => pane.paneId));
    await client.request('split_pane', { sessionId, targetPaneId: 'main', direction: 'vertical' });
    const firstUtilityPane = await waitForNewShellPane(
      client,
      sessionId,
      paneIdsBeforeFirstSplit,
      `first utility pane for session ${sessionId}`,
      20_000,
    );
    if (!firstUtilityPane?.runtimeId) {
      throw new Error(`first utility pane missing runtimeId for session ${sessionId}`);
    }
    console.log(
      `[RealAppHarness] firstUtilityPane=${firstUtilityPane.paneId} runtime=${firstUtilityPane.runtimeId}`,
    );
    await focusPaneAndAwaitInput(client, sessionId, firstUtilityPane.paneId);
    const firstScrollback = await typeAndWaitForEcho(client, sessionId, firstUtilityPane.paneId, firstToken);
    fs.writeFileSync(path.join(runDir, 'utility-1-scrollback.txt'), firstScrollback, 'utf8');

    const wsBeforeSecondSplit = await client.request('get_workspace', { sessionId });
    const paneIdsBeforeSecondSplit = new Set((wsBeforeSecondSplit.panes || []).map((pane) => pane.paneId));
    await client.request('split_pane', {
      sessionId,
      targetPaneId: firstUtilityPane.paneId,
      direction: 'vertical',
    });
    const secondUtilityPane = await waitForNewShellPane(
      client,
      sessionId,
      paneIdsBeforeSecondSplit,
      `second utility pane for session ${sessionId}`,
      20_000,
    );
    if (!secondUtilityPane?.runtimeId) {
      throw new Error(`second utility pane missing runtimeId for session ${sessionId}`);
    }
    console.log(
      `[RealAppHarness] secondUtilityPane=${secondUtilityPane.paneId} runtime=${secondUtilityPane.runtimeId}`,
    );
    await focusPaneAndAwaitInput(client, sessionId, secondUtilityPane.paneId);
    const secondScrollback = await typeAndWaitForEcho(client, sessionId, secondUtilityPane.paneId, secondToken);
    fs.writeFileSync(path.join(runDir, 'utility-2-scrollback.txt'), secondScrollback, 'utf8');

    // The actual regression probe: return focus to the older pane and confirm
    // it still accepts typed input (the bug: older pane input was sometimes
    // dropped after a newer split became active). type_pane_via_ui requires
    // the target pane's xterm textarea to be the document's activeElement, so
    // a refocus-routing regression surfaces as a 'Failed to type' error here.
    await focusPaneAndAwaitInput(client, sessionId, firstUtilityPane.paneId);
    const revisitScrollback = await typeAndWaitForEcho(
      client,
      sessionId,
      firstUtilityPane.paneId,
      revisitToken,
    );
    fs.writeFileSync(path.join(runDir, 'utility-1-revisit-scrollback.txt'), revisitScrollback, 'utf8');

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
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Older-pane writability repro failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
