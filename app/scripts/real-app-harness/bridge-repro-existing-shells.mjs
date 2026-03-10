#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

function shellPanes(session) {
  return (session?.panes || []).filter((pane) => pane.kind === 'shell');
}

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForPaneText(client, sessionId, paneId, needle, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastText = '';
  const compactNeedle = needle.replace(/\s+/g, '');

  while (Date.now() - startedAt < timeoutMs) {
    const result = await client.request('read_pane_text', { sessionId, paneId });
    lastText = result?.text || '';
    if (lastText.includes(needle) || lastText.replace(/\s+/g, '').includes(compactNeedle)) {
      return result;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for pane text in ${paneId} to contain ${JSON.stringify(needle)}. Last pane text tail:\n${lastText.slice(-400)}`
  );
}

async function main() {
  const options = parseCommonArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-repro-existing-shells.mjs');
    return;
  }

  const runId = `bridge-repro-existing-shells-${new Date().toISOString().replace(/[:.]/g, '-')}`;
  const runDir = path.join(options.artifactsDir || '/tmp/attn-real-app-harness', runId);
  fs.mkdirSync(runDir, { recursive: true });

  const client = new UiAutomationClient({ appPath: options.appPath });
  await client.waitForManifest(5_000);
  await client.waitForReady(5_000);
  await client.request('set_pane_debug', { enabled: true });

  const initialState = await client.request('get_state');
  const session = (initialState.sessions || []).find((entry) => shellPanes(entry).length >= 2);
  if (!session) {
    throw new Error('No existing session with at least two shell panes found');
  }

  const [firstShell, secondShell] = shellPanes(session);
  const secondToken = `__EXISTING_SHELL_TWO_${Date.now()}__`;
  const firstToken = `__EXISTING_SHELL_ONE_REVISIT_${Date.now()}__`;

  await client.request('click_pane', { sessionId: session.id, paneId: secondShell.paneId });
  await client.request('type_pane_via_ui', { sessionId: session.id, paneId: secondShell.paneId, text: secondToken });
  const secondVisible = await waitForPaneText(client, session.id, secondShell.paneId, secondToken, 10_000);

  await client.request('click_pane', { sessionId: session.id, paneId: firstShell.paneId });
  await client.request('type_pane_via_ui', { sessionId: session.id, paneId: firstShell.paneId, text: firstToken });
  const firstVisible = await waitForPaneText(client, session.id, firstShell.paneId, firstToken, 10_000);

  const snapshot = await client.request('capture_structured_snapshot');
  const debugDump = await client.request('dump_pane_debug');
  saveJson(path.join(runDir, 'structured-snapshot.json'), snapshot);
  saveJson(path.join(runDir, 'pane-debug.json'), debugDump);
  fs.writeFileSync(path.join(runDir, 'second-shell-text.txt'), secondVisible.text || '', 'utf8');
  fs.writeFileSync(path.join(runDir, 'first-shell-revisit-text.txt'), firstVisible.text || '', 'utf8');

  const summary = {
    ok: true,
    runId,
    sessionId: session.id,
    firstShell,
    secondShell,
    secondToken,
    firstToken,
    artifacts: {
      runDir,
      files: [
        'structured-snapshot.json',
        'pane-debug.json',
        'second-shell-text.txt',
        'first-shell-revisit-text.txt',
      ],
    },
  };
  saveJson(path.join(runDir, 'summary.json'), summary);
  console.log(JSON.stringify(summary, null, 2));
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
