#!/usr/bin/env node

import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';

import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function saveText(filePath, value) {
  fs.writeFileSync(filePath, value, 'utf8');
}

async function localBinaryInfo(binaryPath) {
  const { stdout } = await execFileAsync(
    'bash',
    ['-lc', `
set -eu
binary=${shellQuote(binaryPath)}
if [ ! -x "$binary" ]; then
  printf '{"path":%s,"missing":true}\\n' ${shellQuote(JSON.stringify(binaryPath))}
  exit 0
fi
version="$("$binary" --version 2>/dev/null || true)"
sha="$(shasum -a 256 "$binary" | awk '{print $1}')"
size="$(stat -f '%z' "$binary" 2>/dev/null || stat -c '%s' "$binary")"
mtime="$(stat -f '%Sm' -t '%Y-%m-%dT%H:%M:%SZ' "$binary" 2>/dev/null || date -u -r "$(stat -c '%Y' "$binary")" '+%Y-%m-%dT%H:%M:%SZ')"
python3 - <<'PY' "$binary" "$version" "$sha" "$size" "$mtime"
import json, sys
path, version, sha, size, mtime = sys.argv[1:6]
print(json.dumps({
    "path": path,
    "version": version.strip(),
    "sha256": sha.strip(),
    "size": int(size),
    "mtime": mtime.strip(),
}))
PY
`],
    {
      maxBuffer: 1024 * 1024,
    },
  );
  return JSON.parse(stdout.trim());
}

async function remoteBinaryInfo(target, binaryPath) {
  const output = await runSSH(target, `
set -eu
binary=${shellQuote(binaryPath)}
python3 - "$binary" <<'PY'
import json, os, subprocess, sys
path = sys.argv[1]
payload = {"path": path}
if not path or not os.path.exists(path):
    payload["missing"] = True
    print(json.dumps(payload))
    raise SystemExit(0)
try:
    version = subprocess.run([path, "--version"], check=False, capture_output=True, text=True).stdout.strip()
except Exception:
    version = ""
sha = subprocess.run(["sha256sum", path], check=True, capture_output=True, text=True).stdout.split()[0]
stat = os.stat(path)
payload.update({
    "version": version,
    "sha256": sha,
    "size": stat.st_size,
    "mtime": int(stat.st_mtime),
})
print(json.dumps(payload))
PY
`);
  return JSON.parse(output.trim());
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

async function captureLocalDaemonLogSlice(runDir, sessionId, runtimeId) {
  const daemonLogPath = path.join(process.env.HOME || '', '.attn', 'daemon.log');
  if (!daemonLogPath || !fs.existsSync(daemonLogPath)) {
    return null;
  }

  const lines = fs.readFileSync(daemonLogPath, 'utf8')
    .split('\n')
    .filter((line) => line.includes(sessionId) || line.includes(runtimeId));
  const content = lines.slice(-400).join('\n');
  if (!content) {
    return null;
  }

  const outputPath = path.join(runDir, 'local-daemon.log');
  saveText(outputPath, `${content}\n`);
  return outputPath;
}

async function captureRemoteRuntimeLogs(runDir, sshTarget, sessionId, runtimeId) {
  const script = `
set -eu
daemon_log="$HOME/.attn/daemon.log"
worker_log="$(find "$HOME/.attn/workers" -path "*/log/${runtimeId}.log" -print -quit 2>/dev/null || true)"

printf '=== remote-daemon ===\\n'
if [ -f "$daemon_log" ]; then
  python3 - "$daemon_log" "${sessionId}" "${runtimeId}" <<'PY'
import sys
path, session_id, runtime_id = sys.argv[1:4]
with open(path, 'r', encoding='utf-8', errors='replace') as handle:
    lines = [line.rstrip('\\n') for line in handle if session_id in line or runtime_id in line]
for line in lines[-400:]:
    print(line)
PY
else
  echo "<missing>"
fi

printf '\\n=== remote-worker ===\\n'
if [ -n "$worker_log" ] && [ -f "$worker_log" ]; then
  echo "path=$worker_log"
  tail -n 400 "$worker_log"
else
  echo "<missing>"
fi
`;

  const output = await runSSH(sshTarget, script, 45_000);
  const outputPath = path.join(runDir, 'remote-runtime.log');
  saveText(outputPath, output);
  return outputPath;
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

async function listLocalDaemonPids() {
  try {
    const { stdout } = await execFileAsync('pgrep', ['-f', '[a]ttn daemon']);
    return stdout
      .split(/\s+/)
      .map((value) => Number.parseInt(value, 10))
      .filter((value) => Number.isInteger(value) && value > 0);
  } catch {
    return [];
  }
}

async function resetLocalDaemon(timeoutMs = 15_000) {
  const pids = await listLocalDaemonPids();
  for (const pid of pids) {
    try {
      process.kill(pid, 'SIGTERM');
    } catch {}
  }

  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    if ((await listLocalDaemonPids()).length === 0) {
      return;
    }
    await sleep(200);
  }

  for (const pid of await listLocalDaemonPids()) {
    try {
      process.kill(pid, 'SIGKILL');
    } catch {}
  }
}

async function waitForSessionUiState(client, sessionId, predicate, description, timeoutMs = 60_000) {
  const startedAt = Date.now();
  let lastState = null;
  let lastError = null;
  while (Date.now() - startedAt < timeoutMs) {
    const remainingMs = timeoutMs - (Date.now() - startedAt);
    try {
      lastState = await client.request(
        'get_session_ui_state',
        { sessionId },
        { timeoutMs: Math.max(10_000, Math.min(20_000, remainingMs)) },
      );
      if (predicate(lastState)) {
        return lastState;
      }
      lastError = new Error(`ui state not ready for ${description}`);
    } catch (error) {
      lastError = error;
    }
    await sleep(250);
    await client.waitForFrontendResponsive(Math.min(10_000, timeoutMs), 'get_state');
  }

  throw new Error(
    `Timed out waiting for ${description} for ${sessionId}: ${lastError instanceof Error ? lastError.message : String(lastError || 'unknown error')}\nLast state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForPaneState(client, sessionId, paneId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request(
      'get_pane_state',
      { sessionId, paneId },
      { timeoutMs: Math.max(5_000, Math.min(10_000, timeoutMs - (Date.now() - startedAt))) },
    );
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(150);
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForPaneTextChange(client, sessionId, paneId, previousText, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request(
      'read_pane_text',
      { sessionId, paneId },
      { timeoutMs: Math.max(5_000, Math.min(10_000, timeoutMs - (Date.now() - startedAt))) },
    );
    const nextText = typeof lastState?.text === 'string' ? lastState.text : '';
    if (nextText !== previousText) {
      return lastState;
    }
    await sleep(100);
  }
  throw new Error(`Timed out waiting for pane ${paneId} text to change. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForPaneTextContains(client, sessionId, paneId, needle, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request(
      'read_pane_text',
      { sessionId, paneId },
      { timeoutMs: Math.max(5_000, Math.min(10_000, timeoutMs - (Date.now() - startedAt))) },
    );
    if (typeof lastState?.text === 'string' && lastState.text.includes(needle)) {
      return lastState;
    }
    await sleep(100);
  }
  throw new Error(
    `Timed out waiting for pane ${paneId} text to contain ${JSON.stringify(needle)}. Last state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForSessionRemoval(observer, sessionId, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (observer.getSession(sessionId) ? null : true),
    `bridge session ${sessionId} removal`,
    timeoutMs,
  );
}

async function waitForEndpointRemoved(observer, endpointId, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (observer.getEndpoint(endpointId) ? null : true),
    `endpoint ${endpointId} removed`,
    timeoutMs,
  );
}

async function removeStaleHarnessEndpoints(observer, timeoutMs = 20_000) {
  const staleEndpoints = [...observer.endpointsById.values()].filter((endpoint) =>
    typeof endpoint?.name === 'string' && endpoint.name.startsWith('harness-')
  );
  for (const endpoint of staleEndpoints) {
    observer.removeEndpoint(endpoint.id);
  }
  await Promise.all(
    staleEndpoints.map((endpoint) => waitForEndpointRemoved(observer, endpoint.id, timeoutMs))
  );
}

function parseArgs(argv) {
  const remaining = [];
  const options = {
    ...parseCommonArgs([]),
    sshTarget: process.env.ATTN_REMOTE_SPLIT_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_SPLIT_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_SPLIT_REMOTE_AGENT || 'codex',
    echoThresholdMs: Number.parseInt(process.env.ATTN_REMOTE_SPLIT_ECHO_THRESHOLD_MS || '2000', 10),
    outputThresholdMs: Number.parseInt(process.env.ATTN_REMOTE_SPLIT_OUTPUT_THRESHOLD_MS || '4000', 10),
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ssh-target') {
      options.sshTarget = argv[++index];
    } else if (arg === '--remote-directory') {
      options.remoteDirectory = argv[++index] || '';
    } else if (arg === '--remote-agent') {
      options.remoteAgent = argv[++index] || 'codex';
    } else if (arg === '--echo-threshold-ms') {
      options.echoThresholdMs = Number.parseInt(argv[++index] || '2000', 10);
    } else if (arg === '--output-threshold-ms') {
      options.outputThresholdMs = Number.parseInt(argv[++index] || '4000', 10);
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
      echoThresholdMs: Number.isFinite(options.echoThresholdMs) ? options.echoThresholdMs : 2000,
      outputThresholdMs: Number.isFinite(options.outputThresholdMs) ? options.outputThresholdMs : 4000,
    },
    help: remaining.includes('--help') || remaining.includes('-h'),
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/bridge-remote-split-input.mjs');
    console.log(`Additional options:
  --ssh-target <target>        SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>    Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>       Agent for the remote session (default: codex)
  --echo-threshold-ms <ms>     Fail if typed echo takes longer than this (default: 2000)
  --output-threshold-ms <ms>   Fail if command output takes longer than this (default: 4000)
`);
    return;
  }

  const { runId, runDir } = createRunContext(options, 'bridge-remote-split-input');
  const endpointName = `harness-${runId}`;
  const sessionLabel = `remote-split-${runId}`;
  const remoteHome = await getRemoteHome(options.sshTarget);
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
  let utilityPane = null;
  let remoteDirectory = options.remoteDirectory;

  try {
    await resetLocalDaemon();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();
    await removeStaleHarnessEndpoints(observer, 20_000);

    observer.addEndpoint(endpointName, options.sshTarget);
    endpoint = await waitForEndpointConnected(observer, endpointName, 120_000);
    saveJson(path.join(runDir, 'endpoint.json'), endpoint);

    if (!remoteDirectory) {
      remoteDirectory = await getRemoteHome(options.sshTarget);
    }
    saveJson(path.join(runDir, 'remote-target.json'), {
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      remoteHarness: {
        root: remoteHarnessRoot,
        binaryPath: remoteHarnessBinary,
        socketPath: remoteHarnessSocket,
        dbPath: remoteHarnessDB,
        wsPort: remoteHarnessWSPort,
      },
    });

    const created = await client.request('create_session', {
      cwd: remoteDirectory,
      label: sessionLabel,
      agent: options.remoteAgent,
      endpoint_id: endpoint.id,
    });
    sessionId = created.sessionId;
    saveJson(path.join(runDir, 'session-created.json'), created);

    await observer.waitForSession({
      id: sessionId,
      label: sessionLabel,
      directory: remoteDirectory,
      timeoutMs: 30_000,
    });
    await observer.waitForWorkspace(
      sessionId,
      (workspace) => (workspace.panes || []).length >= 1,
      `initial workspace for ${sessionId}`,
      30_000,
    );

    await client.request('select_session', { sessionId });
    const sessionUiState = await waitForSessionUiState(
      client,
      sessionId,
      (state) => Boolean(state?.selected && state?.workspaceBounds && state?.mainPaneBounds),
      `interactive remote workspace for ${sessionId}`,
      60_000,
    );
    saveJson(path.join(runDir, 'session-ui.json'), sessionUiState);

    await client.request('set_pane_debug', { enabled: true });
    await client.request('set_terminal_runtime_trace', { enabled: true });

    const splitResult = await client.request('split_pane', {
      sessionId,
      direction: 'vertical',
    });
    utilityPane = await observer.waitForUtilityPane(sessionId, 30_000);
    saveJson(path.join(runDir, 'split-result.json'), {
      splitResult,
      utilityPane,
    });

    const focusRequestedAt = Date.now();
    await client.request('focus_pane', {
      sessionId,
      paneId: utilityPane.pane_id,
    });

    const readyForInputState = await waitForPaneState(
      client,
      sessionId,
      utilityPane.pane_id,
      (state) => Boolean(
        state?.inputFocused &&
        state?.activePaneId === utilityPane.pane_id &&
        state?.pane?.size?.cols > 0 &&
        state?.pane?.size?.rows > 0
      ),
      `remote utility pane ${utilityPane.pane_id} to become focused and sized`,
      20_000,
    );
    const readyForInputAt = Date.now();
    saveJson(path.join(runDir, 'pane-ready.json'), readyForInputState);

    await client.request('clear_perf_counters');
    const leftOperand = 7000 + Math.floor(Math.random() * 2000);
    const rightOperand = 300 + Math.floor(Math.random() * 600);
    const outputToken = String(leftOperand * rightOperand);
    const typedToken = `attn${outputToken.slice(0, Math.min(6, outputToken.length))}`;
    const command = `echo ${outputToken}`;
    const preTypeState = await client.request('read_pane_text', {
      sessionId,
      paneId: utilityPane.pane_id,
    });
    const preTypeText = typeof preTypeState?.text === 'string' ? preTypeState.text : '';

    const typeStartedAt = Date.now();
    await client.request('type_pane_via_ui', {
      sessionId,
      paneId: utilityPane.pane_id,
      text: typedToken,
    });
    const typedEchoState = await waitForPaneTextChange(
      client,
      sessionId,
      utilityPane.pane_id,
      preTypeText,
      20_000,
    );
    const typedEchoAt = Date.now();
    await client.request('write_pane', {
      sessionId,
      paneId: utilityPane.pane_id,
      text: '\u0003',
      submit: false,
    });
    await sleep(150);

    const submitAt = Date.now();
    await client.request('write_pane', {
      sessionId,
      paneId: utilityPane.pane_id,
      text: command,
      submit: true,
    });
    const outputState = await waitForPaneTextContains(
      client,
      sessionId,
      utilityPane.pane_id,
      outputToken,
      20_000,
    );
    const outputVisibleAt = Date.now();

    const structuredSnapshot = await client.request('capture_structured_snapshot', {
      sessionIds: [sessionId],
      includePaneText: true,
    });
    const renderHealth = await client.request('capture_render_health', {
      sessionIds: [sessionId],
    });
    const perfSnapshot = await client.request('capture_perf_snapshot', {
      settleFrames: 2,
      includeMemory: false,
      sessionIds: [sessionId],
    });
    const paneState = await client.request('get_pane_state', {
      sessionId,
      paneId: utilityPane.pane_id,
    });
    const paneDebug = await client.request('dump_pane_debug', {}, { timeoutMs: 10_000 });
    const terminalRuntimeTrace = await client.request('dump_terminal_runtime_trace', {}, { timeoutMs: 10_000 });

    saveJson(path.join(runDir, 'structured-snapshot.json'), structuredSnapshot);
    saveJson(path.join(runDir, 'render-health.json'), renderHealth);
    saveJson(path.join(runDir, 'perf-snapshot.json'), perfSnapshot);
    saveJson(path.join(runDir, 'pane-state.json'), paneState);
    saveJson(path.join(runDir, 'typed-echo-state.json'), typedEchoState);
    saveJson(path.join(runDir, 'output-state.json'), outputState);
    saveJson(path.join(runDir, 'pane-debug.json'), paneDebug);
    saveJson(path.join(runDir, 'terminal-runtime-trace.json'), terminalRuntimeTrace);
    const localDaemonLogPath = await captureLocalDaemonLogSlice(runDir, sessionId, utilityPane.runtime_id);
    const remoteRuntimeLogPath = await captureRemoteRuntimeLogs(runDir, options.sshTarget, sessionId, utilityPane.runtime_id);
    const localDaemonBinary = await localBinaryInfo(path.join(process.env.HOME || '', '.local', 'bin', 'attn'));
    const remoteDaemonBinary = await remoteBinaryInfo(options.sshTarget, remoteHarnessBinary);
    saveJson(path.join(runDir, 'local-daemon-binary.json'), localDaemonBinary);
    saveJson(path.join(runDir, 'remote-daemon-binary.json'), remoteDaemonBinary);

    const summary = {
      ok: true,
      runId,
      runDir,
      sshTarget: options.sshTarget,
      remoteDirectory,
      sessionId,
      endpointId: endpoint.id,
      paneId: utilityPane.pane_id,
      runtimeId: utilityPane.runtime_id,
      thresholds: {
        echoMs: options.echoThresholdMs,
        outputMs: options.outputThresholdMs,
      },
      timings: {
        focusReadyMs: readyForInputAt - focusRequestedAt,
        typedEchoMs: typedEchoAt - typeStartedAt,
        outputVisibleMs: outputVisibleAt - submitAt,
      },
      typedToken,
      command,
      outputToken,
      typedEchoPreview: typeof typedEchoState?.text === 'string'
        ? typedEchoState.text.slice(Math.max(0, typedEchoState.text.length - 160))
        : '',
      diagnostics: {
        localDaemonLogPath,
        remoteRuntimeLogPath,
        localDaemonBinary,
        remoteDaemonBinary,
        remoteHarness: {
          binaryPath: remoteHarnessBinary,
          socketPath: remoteHarnessSocket,
          dbPath: remoteHarnessDB,
          wsPort: remoteHarnessWSPort,
        },
      },
      ptyFocus: perfSnapshot.ptyFocus || null,
      runtimeTimeline: (perfSnapshot.runtimeTimeline?.runtimes || []).find(
        (runtime) => runtime.runtimeId === utilityPane.runtime_id
      ) || null,
      paneWarnings: (renderHealth.sessions?.[0]?.panes || []).map((pane) => ({
        paneId: pane.paneId,
        warnings: pane.warnings || [],
      })),
    };

    saveJson(path.join(runDir, 'summary.json'), summary);

    if (summary.timings.typedEchoMs > options.echoThresholdMs) {
      throw new Error(
        `Remote split echo latency ${summary.timings.typedEchoMs}ms exceeded threshold ${options.echoThresholdMs}ms. Summary:\n${JSON.stringify(summary, null, 2)}`
      );
    }
    if (summary.timings.outputVisibleMs > options.outputThresholdMs) {
      throw new Error(
        `Remote split output latency ${summary.timings.outputVisibleMs}ms exceeded threshold ${options.outputThresholdMs}ms. Summary:\n${JSON.stringify(summary, null, 2)}`
      );
    }

    console.log(JSON.stringify(summary, null, 2));
  } finally {
    if (sessionId) {
      try {
        observer.unregisterSession(sessionId);
        await waitForSessionRemoval(observer, sessionId, 15_000);
      } catch {}
    }
    if (endpoint?.id) {
      try {
        observer.removeEndpoint(endpoint.id);
        await waitForEndpointRemoved(observer, endpoint.id, 15_000);
      } catch {}
    }
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
