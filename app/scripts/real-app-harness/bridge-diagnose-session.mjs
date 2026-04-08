#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRunContext } from './common.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

function printHelp() {
  console.log(`Usage: pnpm exec node scripts/real-app-harness/bridge-diagnose-session.mjs [options]

Options:
  --app-path <path>          Packaged app path (default: ~/Applications/attn.app)
  --artifacts-dir <path>     Directory for diagnostic output
  --session-root-dir <path>  Unused here, kept for consistent harness output
  --fresh-launch             Quit and relaunch the packaged app before capture
  --session-id <id>          Inspect a specific session id
  --label <label>            Inspect the first session matching this label
  --cwd <path>               Inspect the first session matching this cwd
  --no-select-session        Capture the session as-is without sending select_session
  --settle-frames <n>        Frames to settle before perf capture (default: 2)

Examples:
  pnpm exec node scripts/real-app-harness/bridge-diagnose-session.mjs --label blubs
  pnpm exec node scripts/real-app-harness/bridge-diagnose-session.mjs --session-id 123
`);
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = {
    appPath: process.env.ATTN_REAL_APP_PATH || path.join(os.homedir(), 'Applications', 'attn.app'),
    artifactsDir: process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness'),
    sessionRootDir: process.env.ATTN_REAL_APP_SESSION_ROOT || path.join(os.tmpdir(), 'attn-real-app-sessions'),
    freshLaunch: false,
    sessionId: '',
    label: '',
    cwd: '',
    noSelectSession: false,
    settleFrames: 2,
    help: false,
  };

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--fresh-launch') options.freshLaunch = true;
    else if (arg === '--session-id') options.sessionId = args[++index] || '';
    else if (arg === '--label') options.label = args[++index] || '';
    else if (arg === '--cwd') options.cwd = args[++index] || '';
    else if (arg === '--no-select-session') options.noSelectSession = true;
    else if (arg === '--settle-frames') options.settleFrames = Math.max(0, Number.parseInt(args[++index] || '2', 10) || 0);
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  return options;
}

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

async function tryCaptureNativeWindow(runDir) {
  try {
    return await captureFrontWindowScreenshot(path.join(runDir, 'native-window.png'));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, 'native-window.txt'),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
    return null;
  }
}

async function resolveSession(client, options) {
  if (options.sessionId) {
    const workspace = await client.request('get_workspace', { sessionId: options.sessionId });
    return workspace;
  }

  if (options.label || options.cwd) {
    const session = await client.request('find_session', {
      ...(options.label ? { label: options.label } : {}),
      ...(options.cwd ? { cwd: options.cwd } : {}),
    });
    if (session) {
      return session;
    }
    throw new Error(`No session matched label=${JSON.stringify(options.label)} cwd=${JSON.stringify(options.cwd)}`);
  }

  const state = await client.request('get_state');
  const activeSession = (state.sessions || []).find((session) => session.id === state.activeSessionId);
  if (!activeSession) {
    throw new Error('No active session found');
  }
  return activeSession;
}

function summarizeSnapshot(sessionId, uiState, structuredSnapshot, perfSnapshot, renderHealthSnapshot) {
  const session = (structuredSnapshot.sessions || []).find((entry) => entry.id === sessionId);
  const terminals = (perfSnapshot.terminals || []).filter((terminal) => terminal.sessionId === sessionId);
  const renderHealth = (renderHealthSnapshot.sessions || []).find((entry) => entry.sessionId === sessionId);
  const splitModelById = new Map(
    ((session?.workspace?.model?.layout?.splits) || []).map((split) => [split.splitId, split]),
  );

  return {
    sessionId,
    label: session?.label || uiState?.label || null,
    selected: uiState?.selected ?? null,
    activePaneId: session?.activePaneId || uiState?.activePaneId || null,
    daemonActivePaneId: session?.daemonActivePaneId || uiState?.daemonActivePaneId || null,
    workspace: {
      view: session?.workspace?.view || uiState?.workspace?.view || null,
      dom: session?.workspace?.dom || uiState?.workspace?.dom || null,
      splitCount: session?.workspace?.model?.layout?.splitCount ?? null,
      paneCount: session?.workspace?.model?.layout?.paneCount ?? null,
      splitWidths: (session?.workspace?.splits || []).map((split) => ({
        splitId: split.splitId,
        path: split.path,
        direction: split.direction,
        ratio: split.ratio,
        spanCount: splitModelById.get(split.splitId)?.spanCount ?? null,
        firstChildSpan: splitModelById.get(split.splitId)?.firstChildSpan ?? null,
        secondChildSpan: splitModelById.get(split.splitId)?.secondChildSpan ?? null,
        width: split.dom?.bounds?.width ?? null,
        height: split.dom?.bounds?.height ?? null,
        firstChildWidth: split.firstChild?.dom?.bounds?.width ?? null,
        secondChildWidth: split.secondChild?.dom?.bounds?.width ?? null,
      })),
    },
    panes: (session?.panes || []).map((pane) => ({
      paneId: pane.paneId,
      kind: pane.kind,
      path: pane.path,
      bounds: pane.bounds,
      projectedBounds: pane.layout?.projectedBounds || null,
      size: pane.size,
      renderHealth: renderHealth?.panes?.find((entry) => entry.paneId === pane.paneId) || null,
      helperTextarea: pane.dom?.helperTextarea
        ? {
            focused: pane.dom.helperTextarea.focused,
            disabled: pane.dom.helperTextarea.disabled,
            readOnly: pane.dom.helperTextarea.readOnly,
            width: pane.dom.helperTextarea.bounds?.width ?? null,
            height: pane.dom.helperTextarea.bounds?.height ?? null,
          }
        : null,
    })),
    terminals: terminals.map((terminal) => ({
      paneId: terminal.paneId,
      runtimeId: terminal.runtimeId,
      renderer: terminal.renderer,
      ready: terminal.ready,
      visible: terminal.visible,
      cols: terminal.cols,
      rows: terminal.rows,
      startup: terminal.startup || null,
      renderCount: terminal.renderCount,
      writeParsedCount: terminal.writeParsedCount,
      lastResize: terminal.lastResize
        ? {
            trigger: terminal.lastResize.trigger,
            cols: terminal.lastResize.cols,
            rows: terminal.lastResize.rows,
            containerWidth: terminal.lastResize.diagnostics.containerWidth,
            containerHeight: terminal.lastResize.diagnostics.containerHeight,
          }
        : null,
      dom: terminal.dom,
    })),
    resizeEvents: (perfSnapshot.resizeEvents || []).map((event) => ({
      terminalName: event.terminalName,
      trigger: event.trigger,
      cols: event.cols,
      rows: event.rows,
      prevCols: event.prevCols,
      prevRows: event.prevRows,
      isVisible: event.isVisible,
      containerWidth: event.diagnostics?.containerWidth ?? null,
      containerHeight: event.diagnostics?.containerHeight ?? null,
    })),
    ptyFocus: perfSnapshot.ptyFocus || null,
    runtimeTimeline: perfSnapshot.runtimeTimeline || null,
    renderHealth: renderHealth || null,
    paneWarnings: (renderHealth?.panes || []).map((pane) => ({
      paneId: pane.paneId,
      warnings: pane.warnings || [],
    })),
  };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printHelp();
    return;
  }

  const { runId, runDir } = createRunContext({
    appPath: options.appPath,
    artifactsDir: options.artifactsDir,
    sessionRootDir: options.sessionRootDir,
  }, 'bridge-diagnose-session');

  const client = new UiAutomationClient({
    ...(options.appPath ? { appPath: options.appPath } : {}),
  });

  if (options.freshLaunch) {
    await client.launchFreshApp();
  }
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);

  const session = await resolveSession(client, options);
  const sessionId = session.id;

  if (!options.noSelectSession) {
    await client.request('select_session', { sessionId });
  }
  const nativeScreenshot = await tryCaptureNativeWindow(runDir);

  const uiState = await client.request('get_session_ui_state', { sessionId });
  const structuredSnapshot = await client.request('capture_structured_snapshot', {
    sessionIds: [sessionId],
    includePaneText: false,
  });
  const renderHealthSnapshot = await client.request('capture_render_health', {
    sessionIds: [sessionId],
  });
  const perfSnapshot = await client.request('capture_perf_snapshot', {
    settleFrames: options.settleFrames,
    includeMemory: false,
    sessionIds: [sessionId],
  });

  saveJson(path.join(runDir, 'session-ui-state.json'), uiState);
  saveJson(path.join(runDir, 'structured-snapshot.json'), structuredSnapshot);
  saveJson(path.join(runDir, 'render-health.json'), renderHealthSnapshot);
  saveJson(path.join(runDir, 'perf-snapshot.json'), perfSnapshot);

  const summary = summarizeSnapshot(sessionId, uiState, structuredSnapshot, perfSnapshot, renderHealthSnapshot);
  summary.nativeWindowScreenshot = nativeScreenshot;
  saveJson(path.join(runDir, 'summary.json'), summary);

  console.log(JSON.stringify({
    ok: true,
    runId,
    runDir,
    summary,
  }, null, 2));
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
