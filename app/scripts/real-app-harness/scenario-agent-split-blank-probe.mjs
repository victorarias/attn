#!/usr/bin/env node

// DIAGNOSTIC probe for the "agent pane goes blank when a shell split opens" bug.
// This is NOT an assertion scenario — it drives the exact user gesture (real
// agent pane, then Cmd+D-equivalent vertical split that creates a shell pane
// beside it) against the packaged dev app, captures native screenshots before
// and after the split, and prints the agent paneId + split timestamp so the
// on-disk render trace (debug/render-trace.jsonl) can be correlated.
//
// Remove together with the render-trace instrumentation once the root cause is
// fixed (or promote into a proper regression scenario asserting the agent pane
// stays painted after the split).

import fs from 'node:fs';
import path from 'node:path';
import {
  createRunContext,
  createSessionAndWaitForInitialPane,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  assertCommonTargetAllowed,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import {
  captureSessionArtifacts,
  waitForFirstWorkspacePane,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeInitialPanePromptReady,
  ensureCodexInitialPanePromptReady,
  preTrustClaudeFolder,
} from './scenarioAgents.mjs';
import { getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = parseCommonArgs([]);
  options.agent = 'claude';
  options.settleMs = 5_000;
  options.idleMs = 6_000;
  options.splitMode = 'shortcut';
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (arg === '--ws-url') options.wsUrl = args[++i];
    else if (arg === '--app-path') options.appPath = args[++i];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++i];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++i];
    else if (arg === '--agent') options.agent = (args[++i] || options.agent).toLowerCase();
    else if (arg === '--settle-ms') options.settleMs = Number(args[++i]) || options.settleMs;
    else if (arg === '--idle-ms') options.idleMs = Number(args[++i]) || options.idleMs;
    else if (arg === '--split-mode') options.splitMode = args[++i] || options.splitMode;
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }
  if (!options.help) assertCommonTargetAllowed(options, args);
  if (options.agent !== 'codex' && options.agent !== 'claude') {
    throw new Error(`Unsupported agent: ${options.agent}`);
  }
  return options;
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// Wait until the agent stops repainting: the blank only persists when the agent
// is IDLE at split time (a continuously-animating agent self-heals on its next
// redraw). We consider it idle once the visible model is byte-stable for idleMs.
async function waitForAgentIdle(client, sessionId, paneId, idleMs, maxWaitMs = 30_000) {
  const startedAt = Date.now();
  let lastSig = null;
  let stableSince = Date.now();
  while (Date.now() - startedAt < maxWaitMs) {
    const v = await readAgentVisible(client, sessionId, paneId);
    const sig = `${v.cols}x${v.rows}:${v.printableLines}:${v.charCount}`;
    if (sig !== lastSig) {
      lastSig = sig;
      stableSince = Date.now();
    } else if (Date.now() - stableSince >= idleMs) {
      return { idle: true, waitedMs: Date.now() - startedAt, sig };
    }
    await sleep(400);
  }
  return { idle: false, waitedMs: Date.now() - startedAt, sig: lastSig };
}

async function readAgentVisible(client, sessionId, paneId) {
  const state = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 }).catch(() => null);
  const vc = state?.pane?.visibleContent || null;
  if (!vc?.lines) return { cols: vc?.cols ?? null, rows: vc?.lines?.length ?? null, printableLines: 0, charCount: 0 };
  let printableLines = 0;
  let charCount = 0;
  for (const line of vc.lines) {
    const text = (line?.text || '').trim();
    if (text.length > 0) { printableLines += 1; charCount += text.length; }
  }
  return { cols: vc.cols, rows: vc.lines.length, printableLines, charCount };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-agent-split-blank-probe.mjs');
    console.log('  --agent <codex|claude>   agent to launch (default codex)');
    console.log('  --settle-ms <n>          ms to wait after split before capture (default 4000)');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, `agent-split-blank-${options.agent}`);
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let sessionId = null;

  console.log(`[probe] runDir=${runDir}`);
  console.log(`[probe] sessionDir=${sessionDir}`);
  console.log(`[probe] agent=${options.agent}`);

  try {
    await launchFreshAppAndConnect(client, observer);

    // Match the user's real geometry: a wide window so the agent pane is ~180
    // cols before the split (~90 after), not the tiny default harness window.
    try {
      const bounds = await getFrontWindowBounds(client.bundleId, { client });
      await setFrontWindowBounds({ ...bounds, x: 40, y: 40, width: 1680, height: 1050 }, { client });
      console.log('[probe] window resized to 1680x1050');
    } catch (error) {
      console.log(`[probe] window resize skipped: ${error?.message || error}`);
    }

    if (options.agent === 'claude') {
      preTrustClaudeFolder(sessionDir);
    }

    sessionId = await createSessionAndWaitForInitialPane({
      client,
      observer,
      cwd: sessionDir,
      label: `split-blank-${options.agent}-${runId}`,
      agent: options.agent,
      waitForInitialPaneVisible: false,
      sessionWaitMs: 30_000,
    });
    console.log(`[probe] sessionId=${sessionId}`);

    await client.request('select_session', { sessionId });
    if (options.agent === 'claude') {
      await ensureClaudeInitialPanePromptReady(client, sessionId, 45_000);
    } else {
      await ensureCodexInitialPanePromptReady(client, sessionId, 45_000);
    }

    const agentPane = await waitForFirstWorkspacePane(client, sessionId, `${options.agent} initial pane`, 20_000);
    const agentPaneId = agentPane.paneId;
    await waitForPaneVisible(client, sessionId, agentPaneId, 45_000);
    console.log(`[probe] agentPaneId=${agentPaneId}`);

    // Let the agent fully settle so it is NOT redrawing when the split opens.
    const idleResult = await waitForAgentIdle(client, sessionId, agentPaneId, options.idleMs);
    console.log(`[probe] idle gate: ${JSON.stringify(idleResult)}`);

    const baselineVisible = await readAgentVisible(client, sessionId, agentPaneId);
    console.log(`[probe] BASELINE agent model: ${JSON.stringify(baselineVisible)}`);
    await captureSessionArtifacts(client, runDir, '01-baseline', sessionId);

    // The decisive gesture: open the shell split beside the agent pane.
    const workspaceBefore = await client.request('get_workspace', { sessionId });
    const existingPaneIds = new Set((workspaceBefore.panes || []).map((p) => p.paneId));
    // Focus the agent pane first so the shortcut targets it, then drive the
    // real Cmd+D path (terminal.splitVertical) — same code path as the user.
    await client.request('focus_pane', { sessionId, paneId: agentPaneId }).catch(() => {});
    const splitAtMs = Date.now();
    console.log(`[probe] SPLIT_AT_MS=${splitAtMs}`);
    if (options.splitMode === 'command') {
      await client.request('split_pane', { sessionId, targetPaneId: agentPaneId, direction: 'vertical' });
    } else {
      await client.request('dispatch_shortcut', { shortcutId: 'terminal.splitVertical' });
    }
    const wsAfter = await waitForSessionWorkspace(
      client,
      sessionId,
      (ws) => (ws.panes || []).some((p) => !existingPaneIds.has(p.paneId) && p.runtimeId),
      'new split pane beside agent',
      30_000,
    );
    const shellPane = (wsAfter.panes || []).find((p) => !existingPaneIds.has(p.paneId));
    console.log(`[probe] shellPaneId=${shellPane?.paneId} kind=${shellPane?.kind} title=${shellPane?.title}`);

    await sleep(options.settleMs);

    const afterVisible = await readAgentVisible(client, sessionId, agentPaneId);
    console.log(`[probe] AFTER-SPLIT agent model: ${JSON.stringify(afterVisible)}`);
    await captureSessionArtifacts(client, runDir, '02-after-split', sessionId);

    // Re-focus the agent pane and capture once more — switching focus is one of
    // the things the user said does NOT fix the blank.
    await client.request('focus_pane', { sessionId, paneId: agentPaneId }).catch(() => {});
    await sleep(1_000);
    await captureSessionArtifacts(client, runDir, '03-agent-focused', sessionId);

    const summary = {
      ok: true,
      runId,
      sessionId,
      agentPaneId,
      shellPaneId: shellPane?.paneId ?? null,
      splitAtMs,
      baselineVisible,
      afterVisible,
      renderTraceFile: '$APPLOCALDATA/debug/render-trace.jsonl',
    };
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log('[probe] DONE');
    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
      await client.request('close_session', { sessionId }).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
