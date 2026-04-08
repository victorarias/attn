#!/usr/bin/env node

import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';

import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

async function tryCaptureNativeWindow(runDir, filename) {
  try {
    return await captureFrontWindowScreenshot(path.join(runDir, filename));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${filename}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
    return null;
  }
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

async function runSSH(target, command, timeoutMs = 30_000) {
  const { stdout } = await execFileAsync(
    'ssh',
    [
      '-o', 'BatchMode=yes',
      '-o', 'StrictHostKeyChecking=accept-new',
      '-o', 'ConnectTimeout=10',
      target,
      `bash -lc ${shellQuote(command)}`,
    ],
    {
      timeout: timeoutMs,
      maxBuffer: 1024 * 1024 * 8,
    },
  );
  return stdout;
}

async function getRemoteHome(target) {
  return (await runSSH(target, 'printf %s "$HOME"')).trim();
}

function chooseRemoteWSPort() {
  return 19000 + Math.floor(Math.random() * 2000);
}

async function waitForEndpointConnected(observer, name, timeoutMs = 120_000) {
  const startedAt = Date.now();
  let lastEndpoint = null;
  while (Date.now() - startedAt < timeoutMs) {
    const endpoint = observer.findEndpointByName(name);
    if (endpoint) {
      lastEndpoint = endpoint;
      if (endpoint.status === 'connected') {
        return endpoint;
      }
      if (endpoint.status === 'error') {
        throw new Error(`Endpoint ${name} entered error state: ${endpoint.status_message || 'unknown error'}`);
      }
    }
    await sleep(500);
  }
  throw new Error(
    `Timed out waiting for endpoint ${name} to connect. Last endpoint state:\n${JSON.stringify(lastEndpoint, null, 2)}`
  );
}

async function waitForSessionUiState(client, sessionId, predicate, description, timeoutMs = 60_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_session_ui_state', { sessionId }, { timeoutMs: 20_000 });
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(200);
  }
  throw new Error(
    `Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function removeStaleHarnessEndpoints(observer, timeoutMs = 20_000) {
  const staleEndpoints = [...observer.endpointsById.values()].filter((endpoint) =>
    typeof endpoint?.name === 'string' && endpoint.name.startsWith('harness-')
  );
  for (const endpoint of staleEndpoints) {
    observer.removeEndpoint(endpoint.id);
  }
  if (staleEndpoints.length === 0) {
    return;
  }
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    const remaining = staleEndpoints.filter((endpoint) => observer.getEndpoint(endpoint.id));
    if (remaining.length === 0) {
      return;
    }
    await sleep(250);
  }
}

function summarizeSnapshot(structuredSnapshot, renderHealth, perfSnapshot) {
  const session = structuredSnapshot.sessions?.[0] || null;
  return {
    workspacePanes: (session?.panes || []).map((pane) => ({
      paneId: pane.paneId,
      kind: pane.kind,
      bounds: pane.bounds,
      size: pane.size,
      projectedBounds: pane.layout?.projectedBounds || null,
    })),
    splitWidths: (session?.workspace?.splits || []).map((split) => ({
      splitId: split.splitId,
      path: split.path,
      direction: split.direction,
      ratio: split.ratio,
      width: split.dom?.bounds?.width ?? null,
      firstChildWidth: split.firstChild?.dom?.bounds?.width ?? null,
      secondChildWidth: split.secondChild?.dom?.bounds?.width ?? null,
    })),
    paneWarnings: (renderHealth.sessions?.[0]?.panes || []).map((pane) => ({
      paneId: pane.paneId,
      warnings: pane.warnings || [],
    })),
    runtimeSizes: (perfSnapshot.runtimeTimeline?.runtimes || []).map((runtime) => ({
      runtimeId: runtime.runtimeId,
      paneId: runtime.paneId,
      cols: runtime.terminal?.cols ?? null,
      rows: runtime.terminal?.rows ?? null,
      recentPtyResizes: runtime.geometry?.recentPtyResizes || [],
      ptyResizeBursts: runtime.geometry?.ptyResizeBursts || [],
    })),
  };
}

function parseArgs(argv) {
  const options = {
    sshTarget: process.env.ATTN_REMOTE_SPLIT_GEOMETRY_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_SPLIT_GEOMETRY_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_SPLIT_GEOMETRY_REMOTE_AGENT || 'codex',
    maxWidthDeltaPx: Number.parseInt(process.env.ATTN_REMOTE_SPLIT_GEOMETRY_MAX_WIDTH_DELTA_PX || '48', 10),
  };
  const remaining = [];

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ssh-target') {
      options.sshTarget = argv[++index] || options.sshTarget;
    } else if (arg === '--remote-directory') {
      options.remoteDirectory = argv[++index] || '';
    } else if (arg === '--remote-agent') {
      options.remoteAgent = argv[++index] || options.remoteAgent;
    } else if (arg === '--max-width-delta-px') {
      options.maxWidthDeltaPx = Number.parseInt(argv[++index] || '48', 10);
    } else {
      remaining.push(arg);
    }
  }

  return {
    options: {
      ...parseCommonArgs(remaining),
      sshTarget: options.sshTarget,
      remoteDirectory: options.remoteDirectory,
      remoteAgent: options.remoteAgent,
      maxWidthDeltaPx: Number.isFinite(options.maxWidthDeltaPx) ? options.maxWidthDeltaPx : 48,
    },
    help: remaining.includes('--help') || remaining.includes('-h'),
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/bridge-remote-split-geometry.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>      Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>         Agent for the remote session (default: codex)
  --max-width-delta-px <px>      Fail if pane width deviates from projected width by more than this amount (default: 48)
`);
    return;
  }

  const { runId, runDir } = createRunContext(options, 'bridge-remote-split-geometry');
  const endpointName = `harness-${runId}`;
  const remoteHome = await getRemoteHome(options.sshTarget);
  const remoteDirectory = options.remoteDirectory || remoteHome;
  const remoteHarnessRoot = path.posix.join(remoteHome, '.attn', 'harness', runId);
  const remoteHarnessBinary = path.posix.join(remoteHarnessRoot, 'bin', 'attn');
  const remoteHarnessSocket = path.posix.join(remoteHarnessRoot, 'attn.sock');
  const remoteHarnessDB = path.posix.join(remoteHarnessRoot, 'attn.db');
  const remoteHarnessWSPort = String(chooseRemoteWSPort());

  const client = new UiAutomationClient({
    appPath: options.appPath,
    launchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
      ATTN_REMOTE_ATTN_BIN: remoteHarnessBinary,
      ATTN_REMOTE_SOCKET_PATH: remoteHarnessSocket,
      ATTN_REMOTE_DB_PATH: remoteHarnessDB,
      ATTN_REMOTE_WS_PORT: remoteHarnessWSPort,
    },
  });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let endpoint = null;
  let sessionId = null;

  try {
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();
    await removeStaleHarnessEndpoints(observer, 20_000);

    observer.addEndpoint(endpointName, options.sshTarget);
    endpoint = await waitForEndpointConnected(observer, endpointName, 120_000);
    saveJson(path.join(runDir, 'endpoint.json'), endpoint);

    sessionId = (await client.request('create_session', {
      cwd: remoteDirectory,
      label: `remote-geometry-${runId}`,
      agent: options.remoteAgent,
      endpoint_id: endpoint.id,
    })).sessionId;
    await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
    await observer.waitForWorkspace(
      sessionId,
      (workspace) => (workspace.panes || []).length >= 1,
      `initial workspace for ${sessionId}`,
      30_000,
    );

    await client.request('select_session', { sessionId });
    await waitForSessionUiState(
      client,
      sessionId,
      (state) => Boolean(state?.selected && state?.workspaceBounds),
      `remote workspace ${sessionId}`,
      60_000,
    );

    await client.request('set_pane_debug', { enabled: true });
    await client.request('set_terminal_runtime_trace', { enabled: true });
    await client.request('clear_perf_counters');

    await client.request('focus_pane', { sessionId, paneId: 'main' });
    await client.request('split_pane', {
      sessionId,
      targetPaneId: 'main',
      direction: 'vertical',
    });
    await observer.waitForWorkspace(
      sessionId,
      (workspace) => (workspace.panes || []).length >= 2,
      `two-pane workspace for ${sessionId}`,
      30_000,
    );

    await client.request('focus_pane', { sessionId, paneId: 'main' });
    await client.request('split_pane', {
      sessionId,
      targetPaneId: 'main',
      direction: 'vertical',
    });
    await observer.waitForWorkspace(
      sessionId,
      (workspace) => (workspace.panes || []).length >= 3,
      `three-pane workspace for ${sessionId}`,
      30_000,
    );

    await sleep(1200);

    const structuredSnapshot = await client.request('capture_structured_snapshot', {
      sessionIds: [sessionId],
      includePaneText: false,
    });
    const renderHealth = await client.request('capture_render_health', {
      sessionIds: [sessionId],
    });
    const perfSnapshot = await client.request('capture_perf_snapshot', {
      settleFrames: 2,
      includeMemory: false,
      sessionIds: [sessionId],
    });
    const nativeScreenshot = await tryCaptureNativeWindow(runDir, 'native-window.png');

    saveJson(path.join(runDir, 'structured-snapshot.json'), structuredSnapshot);
    saveJson(path.join(runDir, 'render-health.json'), renderHealth);
    saveJson(path.join(runDir, 'perf-snapshot.json'), perfSnapshot);

    const summary = {
      ok: true,
      runId,
      runDir,
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      sessionId,
      endpointId: endpoint.id,
      diagnostics: {
        remoteHarness: {
          binaryPath: remoteHarnessBinary,
          socketPath: remoteHarnessSocket,
          dbPath: remoteHarnessDB,
          wsPort: remoteHarnessWSPort,
        },
      },
      nativeWindowScreenshot: nativeScreenshot,
      ...summarizeSnapshot(structuredSnapshot, renderHealth, perfSnapshot),
    };
    saveJson(path.join(runDir, 'summary.json'), summary);

    for (const pane of summary.workspacePanes) {
      const actualWidth = pane.bounds?.width ?? null;
      const projectedWidth = pane.projectedBounds?.width ?? null;
      if (
        typeof actualWidth === 'number' &&
        typeof projectedWidth === 'number' &&
        Math.abs(actualWidth - projectedWidth) > options.maxWidthDeltaPx
      ) {
        throw new Error(
          `Pane ${pane.paneId} width ${actualWidth}px differed from projected ${projectedWidth}px by more than ${options.maxWidthDeltaPx}px. Summary:\n${JSON.stringify(summary, null, 2)}`
        );
      }
    }

    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
      try {
        observer.unregisterSession(sessionId);
      } catch {}
    }
    if (endpoint?.id) {
      try {
        observer.removeEndpoint(endpoint.id);
      } catch {}
    }
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
