#!/usr/bin/env node

import { execFile, spawn } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { promisify } from 'node:util';
import WebSocket from 'ws';

import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function normalizeSshTarget(value) {
  return String(value || '').trim().toLowerCase();
}

const sshTargetIdentityCache = new Map();

function fallbackSshTargetIdentity(value) {
  const raw = String(value || '').trim();
  const normalizedRaw = normalizeSshTarget(raw);
  if (!normalizedRaw) {
    return {
      raw,
      user: '',
      host: '',
      port: '',
      canonical: '',
    };
  }

  const withoutScheme = normalizedRaw.replace(/^ssh:\/\//, '');
  const atIndex = withoutScheme.lastIndexOf('@');
  const user = atIndex >= 0 ? withoutScheme.slice(0, atIndex) : '';
  const hostPort = atIndex >= 0 ? withoutScheme.slice(atIndex + 1) : withoutScheme;
  const colonIndex = hostPort.lastIndexOf(':');
  const hasPort = colonIndex > -1 && /^[0-9]+$/.test(hostPort.slice(colonIndex + 1));
  const host = hasPort ? hostPort.slice(0, colonIndex) : hostPort;
  const port = hasPort ? hostPort.slice(colonIndex + 1) : '';

  return {
    raw,
    user,
    host,
    port,
    canonical: `${user}@${host}:${port}`.toLowerCase(),
  };
}

async function resolveSshTargetIdentity(value) {
  const raw = String(value || '').trim();
  if (!raw) {
    return fallbackSshTargetIdentity(raw);
  }
  if (sshTargetIdentityCache.has(raw)) {
    return sshTargetIdentityCache.get(raw);
  }

  const pending = (async () => {
    try {
      const { stdout } = await execFileAsync(
        'ssh',
        ['-G', raw],
        {
          timeout: 10_000,
          maxBuffer: 1024 * 1024,
        },
      );
      const resolved = {
        raw,
        user: '',
        host: '',
        port: '',
      };
      for (const line of stdout.split('\n')) {
        const trimmed = line.trim();
        if (!trimmed) {
          continue;
        }
        const spaceIndex = trimmed.indexOf(' ');
        if (spaceIndex <= 0) {
          continue;
        }
        const key = trimmed.slice(0, spaceIndex).toLowerCase();
        const nextValue = trimmed.slice(spaceIndex + 1).trim();
        if (key === 'user') {
          resolved.user = nextValue;
        } else if (key === 'hostname') {
          resolved.host = nextValue.toLowerCase();
        } else if (key === 'port') {
          resolved.port = nextValue;
        }
      }

      if (!resolved.host) {
        return fallbackSshTargetIdentity(raw);
      }

      return {
        raw,
        user: resolved.user.toLowerCase(),
        host: resolved.host,
        port: resolved.port,
        canonical: `${resolved.user}@${resolved.host}:${resolved.port}`.toLowerCase(),
      };
    } catch {
      return fallbackSshTargetIdentity(raw);
    }
  })();

  sshTargetIdentityCache.set(raw, pending);
  return pending;
}

function sshTargetIdentitiesMatch(left, right) {
  if (!left?.host || !right?.host) {
    return normalizeSshTarget(left?.raw) === normalizeSshTarget(right?.raw);
  }
  return left.host === right.host && left.user === right.user && left.port === right.port;
}

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function saveText(filePath, value) {
  fs.writeFileSync(filePath, value, 'utf8');
}

function parseArgs(argv) {
  const remaining = [];
  const options = {
    sshTarget: process.env.ATTN_REMOTE_HUB_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_HUB_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_HUB_REMOTE_AGENT || 'codex',
  };

  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--ssh-target') {
      options.sshTarget = argv[++index];
    } else if (arg === '--remote-directory') {
      options.remoteDirectory = argv[++index];
    } else if (arg === '--remote-agent') {
      options.remoteAgent = argv[++index];
    } else {
      remaining.push(arg);
    }
  }

  return { options, remaining };
}

async function runSSH(target, script, timeoutMs = 30_000) {
  const { stdout, stderr } = await execFileAsync(
    'ssh',
    [
      '-o', 'BatchMode=yes',
      '-o', 'StrictHostKeyChecking=accept-new',
      '-o', 'ConnectTimeout=10',
      target,
      `sh -lc ${shellQuote(script)}`,
    ],
    { timeout: timeoutMs, maxBuffer: 1024 * 1024 * 4 }
  );
  if (stderr && stderr.trim()) {
    return stdout.trim();
  }
  return stdout.trim();
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
    } catch {
      // Ignore races with already-exited daemons.
    }
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
    } catch {
      // Ignore races with already-exited daemons.
    }
  }
}

async function allocateLocalPort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      if (!address || typeof address === 'string') {
        server.close(() => reject(new Error('Failed to allocate local port')));
        return;
      }
      const { port } = address;
      server.close((error) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(port);
      });
    });
  });
}

async function waitForTunnelPort(port, processRef, timeoutMs = 10_000) {
  const startedAt = Date.now();
  let lastError = null;
  while (Date.now() - startedAt < timeoutMs) {
    if (processRef.exitCode !== null) {
      throw new Error(`SSH tunnel exited early with code ${processRef.exitCode}`);
    }
    try {
      await new Promise((resolve, reject) => {
        const socket = net.createConnection({ host: '127.0.0.1', port });
        socket.once('connect', () => {
          socket.end();
          resolve();
        });
        socket.once('error', (error) => {
          socket.destroy();
          reject(error);
        });
      });
      return;
    } catch (error) {
      lastError = error;
      await sleep(100);
    }
  }
  throw new Error(`Timed out waiting for SSH tunnel port ${port}: ${lastError instanceof Error ? lastError.message : lastError || 'unknown error'}`);
}

async function openRemoteWebSocketControl(target, authToken = '') {
  const localPort = await allocateLocalPort();
  const tunnel = spawn(
    'ssh',
    [
      '-o', 'BatchMode=yes',
      '-o', 'StrictHostKeyChecking=accept-new',
      '-o', 'ExitOnForwardFailure=yes',
      '-o', 'ConnectTimeout=10',
      '-N',
      '-L', `${localPort}:127.0.0.1:9849`,
      target,
    ],
    { stdio: ['ignore', 'ignore', 'pipe'] },
  );
  let stderr = '';
  tunnel.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });

  await waitForTunnelPort(localPort, tunnel, 15_000);

  const ws = await new Promise((resolve, reject) => {
    const client = new WebSocket(`ws://127.0.0.1:${localPort}/ws`, authToken
      ? { headers: { Authorization: `Bearer ${authToken}` } }
      : {});

    const timeout = setTimeout(() => {
      client.terminate();
      reject(new Error(`Timed out connecting remote websocket tunnel on port ${localPort}`));
    }, 15_000);

    client.once('open', () => {
      clearTimeout(timeout);
      resolve(client);
    });
    client.once('error', (error) => {
      clearTimeout(timeout);
      reject(error);
    });
  });

  const close = async () => {
    await new Promise((resolve) => {
      ws.once('close', resolve);
      ws.close();
      setTimeout(resolve, 500);
    });
    if (tunnel.exitCode === null) {
      tunnel.kill('SIGTERM');
      await new Promise((resolve) => setTimeout(resolve, 300));
      if (tunnel.exitCode === null) {
        tunnel.kill('SIGKILL');
      }
    }
  };

  const sendAndWait = async (payload, predicate, timeoutMs = 20_000, description = payload.cmd || 'remote ws command') => {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        cleanup();
        reject(new Error(`Timed out waiting for ${description}`));
      }, timeoutMs);

      const onMessage = (raw) => {
        try {
          const data = JSON.parse(raw.toString());
          if (!predicate(data)) {
            return;
          }
          cleanup();
          resolve(data);
        } catch {
          // Ignore malformed frames for matching purposes.
        }
      };

      const onClose = () => {
        cleanup();
        reject(new Error(`Remote websocket closed while waiting for ${description}${stderr ? `: ${stderr.trim()}` : ''}`));
      };

      const cleanup = () => {
        clearTimeout(timeout);
        ws.off('message', onMessage);
        ws.off('close', onClose);
      };

      ws.on('message', onMessage);
      ws.once('close', onClose);
      ws.send(JSON.stringify(payload));
    });
  };

  return { localPort, ws, sendAndWait, close };
}

async function getRemoteHome(target) {
  return runSSH(target, 'printf %s "$HOME"');
}

async function prepareRemoteGitRepo(target, runId) {
  const script = `
repo_dir="$HOME/.attn-harness/remote-hub-${runId}"
rm -rf "$repo_dir"
mkdir -p "$repo_dir"
cd "$repo_dir"
git init -q -b main >/dev/null 2>&1 || {
  git init -q
  git checkout -q -b main
}
git config user.name "attn harness"
git config user.email "attn-harness@example.com"
printf 'base\\n' > tracked.txt
git add tracked.txt
git commit -q -m 'init'
printf %s "$repo_dir"
`;
  const repoDir = await runSSH(target, script, 45_000);
  return {
    repoDir,
    trackedFile: 'tracked.txt',
  };
}

async function getRemoteSocketPath(target) {
  const script = `
config_path="\${ATTN_CONFIG_PATH:-$HOME/.attn/config.json}"
socket_path="\${ATTN_SOCKET_PATH:-}"
if [ -z "$socket_path" ] && [ -f "$config_path" ]; then
  socket_path="$(sed -n 's/.*"socket_path"[[:space:]]*:[[:space:]]*"\\([^"]*\\)".*/\\1/p' "$config_path" | head -n 1)"
fi
if [ -z "$socket_path" ]; then
  socket_path="$HOME/.attn/attn.sock"
fi
case "$socket_path" in
  "~/"*) socket_path="$HOME/\${socket_path#~/}" ;;
esac
printf %s "$socket_path"
`;
  return runSSH(target, script);
}

async function sendRemoteSocketMessage(target, socketPath, payload, timeoutMs = 20_000) {
  const encoded = Buffer.from(JSON.stringify(payload), 'utf8').toString('base64');
  const python = [
    'import base64, json, socket, sys',
    'sock_path = sys.argv[1]',
    'payload = base64.b64decode(sys.argv[2])',
    'client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)',
    'client.settimeout(10)',
    'client.connect(sock_path)',
    'client.sendall(payload)',
    'data = client.recv(65536)',
    'client.close()',
    'sys.stdout.write(data.decode())',
  ].join('; ');
  const script = `python3 -c ${shellQuote(python)} ${shellQuote(socketPath)} ${shellQuote(encoded)}`;
  const out = await runSSH(target, script, timeoutMs);
  return out ? JSON.parse(out) : null;
}

async function waitForBridgeSession(client, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state');
    lastSessions = state.sessions || [];
    const session = lastSessions.find(predicate);
    if (session) {
      return session;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for bridge session ${description}. Sessions seen:\n${JSON.stringify(lastSessions, null, 2)}`
  );
}

async function waitForNewBridgeSession(client, previousSessionIds, predicate, description, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state');
    lastSessions = state.sessions || [];
    const session = lastSessions.find((entry) => !previousSessionIds.has(entry.id) && predicate(entry));
    if (session) {
      return session;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for new bridge session ${description}. Sessions seen:\n${JSON.stringify(lastSessions, null, 2)}`
  );
}

async function waitForSessionRemoved(observer, sessionId, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (!observer.getSession(sessionId) ? true : null),
    `session ${sessionId} removed`,
    timeoutMs
  );
}

async function waitForBridgeSessionRemoved(client, sessionId, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastSessions = [];
  while (Date.now() - startedAt < timeoutMs) {
    const state = await client.request('get_state');
    lastSessions = state.sessions || [];
    if (!lastSessions.some((session) => session.id === sessionId)) {
      return;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for bridge session ${sessionId} removal. Sessions seen:\n${JSON.stringify(lastSessions, null, 2)}`
  );
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

async function waitForEndpointRemoved(observer, endpointId, timeoutMs = 20_000) {
  await observer.waitFor(
    () => (!observer.getEndpoint(endpointId) ? true : null),
    `endpoint ${endpointId} removed`,
    timeoutMs
  );
}

async function waitForLocationPickerState(client, predicate, description, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('location_picker_get_state', {}, {
      timeoutMs: 10_000,
    });
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last picker state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

function normalizePickerPath(value) {
  return String(value || '').replace(/\/+$/, '');
}

function findPickerDirectoryItem(state, targetPath) {
  const targetName = path.basename(normalizePickerPath(targetPath));
  const targetSuffix = `/${targetName}`;
  return (state?.directories || []).find((item) => {
    const itemPath = normalizePickerPath(item.path);
    return item.name === targetName || itemPath === normalizePickerPath(targetPath) || itemPath.endsWith(targetSuffix) || itemPath.endsWith(`~${targetSuffix}`);
  }) || null;
}

async function openRepoOptionsForDirectory(client, endpointId, browseInput, targetDirectory, timeoutMs = 20_000) {
  await client.request('location_picker_open');
  await client.request('location_picker_set_target', { endpointId });
  await client.request('location_picker_set_path', { value: browseInput });
  const pickerSuggestions = await waitForLocationPickerState(
    client,
    (state) => state?.open && state?.mode === 'path-input' && Boolean(findPickerDirectoryItem(state, targetDirectory)),
    `picker suggestions for ${targetDirectory}`,
    timeoutMs,
  );
  const matchingDirectory = findPickerDirectoryItem(pickerSuggestions, targetDirectory);
  if (!matchingDirectory) {
    throw new Error(`Directory suggestion missing for ${targetDirectory}: ${JSON.stringify(pickerSuggestions, null, 2)}`);
  }
  await client.request('location_picker_select_path_item', { index: matchingDirectory.index });
  const repoOptionsState = await waitForLocationPickerState(
    client,
    (state) => state?.open && state?.mode === 'repo-options',
    `repo options for ${targetDirectory}`,
    timeoutMs,
  );
  return { pickerSuggestions, matchingDirectory, repoOptionsState };
}

function expectedWorktreePath(mainRepo, branch) {
  const safeBranch = String(branch).replaceAll('/', '-');
  return path.join(path.dirname(mainRepo), `${path.basename(mainRepo)}--${safeBranch}`);
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

async function resetRemoteTarget(target) {
  await runSSH(
    target,
    `
pkill -x attn >/dev/null 2>&1 || true
rm -f "$HOME/.attn/attn.sock" "$HOME/.attn/attn.pid" "$HOME/.attn/daemon.log"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if ! ss -H -ltn "( sport = :9849 )" 2>/dev/null | grep -q .; then
    break
  fi
  sleep 0.2
done
`,
    30_000
  );
}

async function waitForSessionUiState(client, sessionId, predicate, description, timeoutMs = 60_000) {
  const startedAt = Date.now();
  let lastError = null;
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    const remainingMs = timeoutMs - (Date.now() - startedAt);
    try {
      const sessionState = await client.request(
        'get_session_ui_state',
        {
          sessionId,
        },
        { timeoutMs: Math.max(10_000, Math.min(20_000, remainingMs)) },
      );
      lastState = sessionState;
      if (predicate(sessionState)) {
        return sessionState;
      }
      lastError = new Error(`ui state not ready for ${description}`);
    } catch (error) {
      lastError = error;
    }
    await sleep(500);
    await client.waitForFrontendResponsive(Math.min(10_000, timeoutMs), 'get_state');
  }

  throw new Error(
    `Timed out waiting for ${description} for ${sessionId}: ${lastError instanceof Error ? lastError.message : lastError || 'unknown error'}\nLast state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForPaneTextContains(client, sessionId, paneId, needle, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('read_pane_text', {
      sessionId,
      paneId,
    }, {
      timeoutMs: 15_000,
    });
    if (typeof lastState?.text === 'string' && lastState.text.includes(needle)) {
      return lastState;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for pane ${paneId} text to include ${needle}. Last state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForPaneTextChange(client, sessionId, paneId, previousText, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('read_pane_text', {
      sessionId,
      paneId,
    }, {
      timeoutMs: 15_000,
    });
    if (typeof lastState?.text === 'string' && lastState.text !== previousText) {
      return lastState;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for pane ${paneId} text to change. Last state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForPaneState(client, sessionId, paneId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', {
      sessionId,
      paneId,
    }, {
      timeoutMs: 15_000,
    });
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last pane state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForScrollbackChange(observer, runtimeId, previous, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastScrollback = '';
  while (Date.now() - startedAt < timeoutMs) {
    lastScrollback = await observer.waitForScrollbackReady(runtimeId, Math.min(5_000, timeoutMs));
    if (lastScrollback.length > 0 && lastScrollback !== previous) {
      return lastScrollback;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for scrollback ${runtimeId} to change after reload. Last scrollback tail:\n${lastScrollback.slice(-400)}`
  );
}

async function captureArtifacts(client, runDir, suffix) {
  try {
    const snapshot = await client.request('capture_structured_snapshot', {
      includePaneText: false,
    });
    saveJson(path.join(runDir, `structured-snapshot-${suffix}.json`), snapshot);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `structured-snapshot-${suffix}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }

  try {
    const renderHealth = await client.request('capture_render_health');
    saveJson(path.join(runDir, `render-health-${suffix}.json`), renderHealth);
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `render-health-${suffix}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8'
    );
  }
}

async function main() {
  const { options: extraOptions, remaining } = parseArgs(process.argv.slice(2));
  const options = parseCommonArgs(remaining);
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-remote-hub.mjs');
    console.log(`
Remote hub options:
  --ssh-target <target>        SSH target for remote daemon smoke (default: ai-sandbox)
  --remote-directory <path>    Remote cwd for the spawned remote session
  --remote-agent <agent>       Agent for the spawned remote session (default: codex)
`);
    return;
  }

  const { runId, runDir } = createRunContext(options, 'bridge-remote-hub');
  const endpointName = `harness-${runId}`;
  const createdRemoteSessionIds = new Set();
  let remoteSessionId = null;
  let remoteInitialPaneId = null;
  let remoteSessionLabel = path.basename(extraOptions.remoteDirectory || `remote-hub-${runId}`) || 'session';
  let remoteWorktreeSessionId = null;
  let remoteWorktreePath = null;
  let remoteWorktreeBranch = null;
  const client = new UiAutomationClient({
    appPath: options.appPath,
    launchEnv: {},
  });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  let currentStep = 'init';
  const steps = [];
  const setStep = (step) => {
    currentStep = step;
    steps.push({ step, at: new Date().toISOString() });
    console.log(`[RealAppHarness] step=${step}`);
  };
  let endpointId = null;
  let removeEndpointOnCleanup = false;
  let remoteSocketPath = null;
  let remoteUtilityPane = null;
  let remoteInteraction = null;
  const remoteHome = await getRemoteHome(extraOptions.sshTarget);
  const preparedRepo = extraOptions.remoteDirectory
    ? null
    : await prepareRemoteGitRepo(extraOptions.sshTarget, runId);
  const remoteDirectory = extraOptions.remoteDirectory || preparedRepo?.repoDir || remoteHome;
  remoteSessionLabel = path.basename(remoteDirectory) || remoteSessionLabel;

  console.log(`[RealAppHarness] runDir=${runDir}`);
  console.log(`[RealAppHarness] sshTarget=${extraOptions.sshTarget}`);
  console.log(`[RealAppHarness] remoteDirectory=${remoteDirectory}`);

  try {
    setStep('startup');
    // Reset the remote target before the local daemon starts. Otherwise a
    // previously persisted endpoint can auto-connect during app startup and
    // race with this cleanup step.
    await resetRemoteTarget(extraOptions.sshTarget);
    await resetLocalDaemon();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(45_000, 'get_state');
    await observer.connect();
    await removeStaleHarnessEndpoints(observer);

    setStep('endpoint-bootstrap');
    const targetIdentity = await resolveSshTargetIdentity(extraOptions.sshTarget);
    const endpointCandidates = await Promise.all(
      [...observer.endpointsById.values()].map(async (candidate) => ({
        endpoint: candidate,
        identity: await resolveSshTargetIdentity(candidate.ssh_target),
      })),
    );
    saveJson(path.join(runDir, 'endpoint-candidates.json'), {
      target: targetIdentity,
      candidates: endpointCandidates.map(({ endpoint, identity }) => ({
        id: endpoint.id,
        name: endpoint.name,
        sshTarget: endpoint.ssh_target,
        enabled: endpoint.enabled,
        status: endpoint.status,
        identity,
      })),
    });

    let endpoint = endpointCandidates
      .filter(({ identity }) => sshTargetIdentitiesMatch(identity, targetIdentity))
      .sort((left, right) => {
        const leftScore = left.endpoint.status === 'connected' ? 0 : left.endpoint.enabled === false ? 2 : 1;
        const rightScore = right.endpoint.status === 'connected' ? 0 : right.endpoint.enabled === false ? 2 : 1;
        if (leftScore !== rightScore) {
          return leftScore - rightScore;
        }
        return left.endpoint.name.localeCompare(right.endpoint.name);
      })
      .map(({ endpoint: candidate }) => candidate)[0] || null;
    if (endpoint) {
      if (endpoint.enabled === false) {
        observer.updateEndpoint(endpoint.id, { enabled: true });
      }
      endpoint = await observer.waitForEndpoint({
        id: endpoint.id,
        timeoutMs: 120_000,
      });
      if (endpoint.status !== 'connected') {
        endpoint = await waitForEndpointConnected(observer, endpoint.name, 120_000);
      }
    } else {
      observer.addEndpoint(endpointName, extraOptions.sshTarget);
      endpoint = await waitForEndpointConnected(observer, endpointName, 120_000);
      removeEndpointOnCleanup = true;
    }
    endpointId = endpoint.id;
    saveJson(path.join(runDir, 'endpoint-connected.json'), endpoint);

    remoteSocketPath = await getRemoteSocketPath(extraOptions.sshTarget);
    saveJson(path.join(runDir, 'remote-daemon.json'), {
      sshTarget: extraOptions.sshTarget,
      remoteHome,
      remoteDirectory,
      remoteSocketPath,
    });

    setStep('remote-session-create');
    const pickerOpen = await client.request('location_picker_open');
    saveJson(path.join(runDir, 'picker-open.json'), pickerOpen);

    const pickerTarget = await client.request('location_picker_set_target', {
      endpointId: endpoint.id,
    });
    saveJson(path.join(runDir, 'picker-target.json'), pickerTarget);

    const browseInput = path.join(
      path.dirname(remoteDirectory),
      path.basename(remoteDirectory).slice(0, Math.min(10, path.basename(remoteDirectory).length)),
    );
    const {
      pickerSuggestions,
      matchingDirectory,
      repoOptionsState,
    } = await openRepoOptionsForDirectory(client, endpoint.id, browseInput, remoteDirectory, 20_000);
    saveJson(path.join(runDir, 'picker-suggestions.json'), pickerSuggestions);
    saveJson(path.join(runDir, 'picker-repo-options.json'), repoOptionsState);
    if (!repoOptionsState?.repoOptions?.items?.some((item) => item.kind === 'main-repo')) {
      throw new Error(`Main repo option missing for ${remoteDirectory}: ${JSON.stringify(repoOptionsState, null, 2)}`);
    }

    const previousSessionIds = new Set((await client.request('get_state')).sessions.map((session) => session.id));
    await client.request('location_picker_select_repo_option', { index: 0 });
    const createdBridgeSession = await waitForNewBridgeSession(
      client,
      previousSessionIds,
      (session) => session.cwd === remoteDirectory && session.label === remoteSessionLabel,
      `remote repo session cwd=${remoteDirectory}`,
      45_000,
    );
    remoteSessionId = createdBridgeSession.id;
    createdRemoteSessionIds.add(remoteSessionId);
    saveJson(path.join(runDir, 'remote-session-created.json'), {
      via: 'location-picker',
      browseInput,
      selectedDirectoryIndex: matchingDirectory.index,
      session: createdBridgeSession,
    });
    saveJson(path.join(runDir, 'bridge-session-created.json'), createdBridgeSession);
    await client.request('select_session', { sessionId: remoteSessionId });
    const createdSessionUi = await waitForSessionUiState(
      client,
      remoteSessionId,
      (sessionState) =>
        Boolean(
          sessionState?.selected &&
          sessionState?.workspaceBounds &&
          sessionState?.mainPaneBounds,
        ),
      `new session workspace mount for ${remoteSessionId}`,
      30_000,
    );
    saveJson(path.join(runDir, 'session-ui-created.json'), createdSessionUi);
    const createdWorkspace = await client.request('get_workspace', { sessionId: remoteSessionId });
    remoteInitialPaneId = (createdWorkspace.panes || [])[0]?.paneId || null;
    if (!remoteInitialPaneId) {
      throw new Error(`Remote initial pane not found for ${remoteSessionId}: ${JSON.stringify(createdWorkspace, null, 2)}`);
    }

    const registered = await observer.waitForSession({
      id: remoteSessionId,
      label: remoteSessionLabel,
      directory: remoteDirectory,
      timeoutMs: 30_000,
    });
    const registeredEndpoint = registered.endpoint_id ? observer.getEndpoint(registered.endpoint_id) : null;
    saveJson(path.join(runDir, 'remote-session-registered.json'), {
      session: registered,
      requestedEndpointId: endpoint.id,
      registeredEndpoint,
    });

    const bridgeSession = await waitForBridgeSession(
      client,
      (session) => session.id === remoteSessionId && session.label === remoteSessionLabel,
      `id=${remoteSessionId}`,
      30_000
    );
    saveJson(path.join(runDir, 'bridge-session.json'), bridgeSession);

    setStep('remote-picker-recents');
    await client.request('location_picker_open');
    await client.request('location_picker_set_target', { endpointId: endpoint.id });
    const pickerRecents = await waitForLocationPickerState(
      client,
      (state) => state?.open && state?.mode === 'path-input' && state.recents?.some((item) => item.path === remoteDirectory),
      `picker recents for ${remoteDirectory}`,
      20_000,
    );
    saveJson(path.join(runDir, 'picker-recents.json'), pickerRecents);
    await client.request('location_picker_close');

    setStep('remote-picker-worktree');
    const { repoOptionsState: worktreeRepoOptions } = await openRepoOptionsForDirectory(
      client,
      endpoint.id,
      browseInput,
      remoteDirectory,
      20_000,
    );
    saveJson(path.join(runDir, 'picker-worktree-options.json'), worktreeRepoOptions);
    const newWorktreeOption = (worktreeRepoOptions.repoOptions?.items || []).find((item) => item.kind === 'new-worktree');
    if (!newWorktreeOption) {
      throw new Error(`New worktree option missing from repo options: ${JSON.stringify(worktreeRepoOptions, null, 2)}`);
    }
    await client.request('location_picker_select_repo_option', { index: newWorktreeOption.index });
    const newWorktreeForm = await waitForLocationPickerState(
      client,
      (state) => state?.open && state?.repoOptions?.newWorktree?.visible,
      `new worktree form for ${remoteDirectory}`,
      20_000,
    );
    saveJson(path.join(runDir, 'picker-new-worktree-form.json'), newWorktreeForm);
    remoteWorktreeBranch = `picker-${runId}`;
    remoteWorktreePath = expectedWorktreePath(remoteDirectory, remoteWorktreeBranch);
    await client.request('location_picker_set_new_worktree_name', { value: remoteWorktreeBranch });
    const previousWorktreeSessionIds = new Set((await client.request('get_state')).sessions.map((session) => session.id));
    await client.request('location_picker_submit_new_worktree');
    const createdWorktreeBridgeSession = await waitForNewBridgeSession(
      client,
      previousWorktreeSessionIds,
      (session) => session.cwd === remoteWorktreePath,
      `remote worktree session cwd=${remoteWorktreePath}`,
      60_000,
    );
    remoteWorktreeSessionId = createdWorktreeBridgeSession.id;
    createdRemoteSessionIds.add(remoteWorktreeSessionId);
    const worktreeSession = await observer.waitForSession({
      id: remoteWorktreeSessionId,
      directory: remoteWorktreePath,
      timeoutMs: 30_000,
    });
    saveJson(path.join(runDir, 'picker-worktree-created.json'), {
      branch: remoteWorktreeBranch,
      path: remoteWorktreePath,
      bridge: createdWorktreeBridgeSession,
      session: worktreeSession,
    });

    const { repoOptionsState: repoOptionsWithWorktree } = await openRepoOptionsForDirectory(
      client,
      endpoint.id,
      browseInput,
      remoteDirectory,
      20_000,
    );
    if (!repoOptionsWithWorktree?.repoOptions?.items?.some((item) => item.kind === 'worktree' && item.name === remoteWorktreeBranch)) {
      throw new Error(`Created worktree ${remoteWorktreeBranch} missing from repo options: ${JSON.stringify(repoOptionsWithWorktree, null, 2)}`);
    }
    saveJson(path.join(runDir, 'picker-worktree-visible.json'), repoOptionsWithWorktree);
    await client.request('location_picker_close');

    await client.request('select_session', { sessionId: remoteSessionId });

    const expectedEndpointBadge = registeredEndpoint?.name || endpoint.name;
    const sidebarSession = await waitForSessionUiState(
      client,
      remoteSessionId,
      (sessionState) => Boolean(sessionState?.sidebarItem?.text?.includes(expectedEndpointBadge)),
      `remote sidebar badge for ${expectedEndpointBadge}`,
      60_000,
    );
    if (!sidebarSession?.sidebarItem?.text?.includes(expectedEndpointBadge)) {
      throw new Error(
        `Remote session sidebar row did not include endpoint badge text. Snapshot:\n${JSON.stringify(sidebarSession, null, 2)}`
      );
    }
    saveJson(path.join(runDir, 'session-ui-sidebar.json'), sidebarSession);

    await client.request('select_session', { sessionId: remoteSessionId });
    const selectedSession = await waitForSessionUiState(
      client,
      remoteSessionId,
      (sessionState) =>
        Boolean(
          sessionState?.selected &&
          sessionState?.workspaceBounds &&
          sessionState?.mainPaneBounds,
        ),
      `interactive remote workspace for ${endpointName}`,
      60_000,
    );
    if (!selectedSession?.workspaceBounds || !selectedSession?.mainPaneBounds) {
      throw new Error(
        `Interactive remote workspace not visible for selected session. Snapshot:\n${JSON.stringify(selectedSession, null, 2)}`
      );
    }
    if (!selectedSession?.sidebarItem?.text?.includes(expectedEndpointBadge)) {
      throw new Error(
        `Remote interactive session sidebar row lost endpoint badge text. Snapshot:\n${JSON.stringify(selectedSession, null, 2)}`
      );
    }
    saveJson(path.join(runDir, 'session-ui-selected.json'), selectedSession);

    setStep('remote-pty-interaction');
    const utilityPane = await client.request('split_pane', {
      sessionId: remoteSessionId,
      direction: 'vertical',
    });
    remoteUtilityPane = await observer.waitForUtilityPane(remoteSessionId, 30_000);
    saveJson(path.join(runDir, 'remote-utility-pane.json'), {
      splitResult: utilityPane,
      pane: remoteUtilityPane,
    });

    const focusRequestedAt = Date.now();
    await client.request('focus_pane', {
      sessionId: remoteSessionId,
      paneId: remoteUtilityPane.pane_id,
    });

    const readyForInputState = await waitForPaneState(
      client,
      remoteSessionId,
      remoteUtilityPane.pane_id,
      (state) => Boolean(
        state?.inputFocused &&
        state?.activePaneId === remoteUtilityPane.pane_id &&
        state?.pane?.size?.cols > 0 &&
        state?.pane?.size?.rows > 0
      ),
      `remote utility pane ${remoteUtilityPane.pane_id} to become focused and sized`,
      20_000,
    );
    const readyForInputAt = Date.now();
    saveJson(path.join(runDir, 'remote-utility-pane-ready.json'), readyForInputState);
    saveJson(
      path.join(runDir, 'remote-utility-render-health.json'),
      await client.request('capture_render_health', { sessionIds: [remoteSessionId] }),
    );

    const leftOperand = 7000 + Math.floor(Math.random() * 2000);
    const rightOperand = 300 + Math.floor(Math.random() * 600);
    const outputToken = String(leftOperand * rightOperand);
    const commandEchoNeedle = `python3 -c`;
    const command = `python3 -c "print(${leftOperand}*${rightOperand})"`;
    const preTypePaneTextState = await client.request('read_pane_text', {
      sessionId: remoteSessionId,
      paneId: remoteUtilityPane.pane_id,
    }, {
      timeoutMs: 15_000,
    });
    const preTypePaneText = typeof preTypePaneTextState?.text === 'string' ? preTypePaneTextState.text : '';
    const typeStartedAt = Date.now();
    await client.request('type_pane_via_ui', {
      sessionId: remoteSessionId,
      paneId: remoteUtilityPane.pane_id,
      text: command,
    });
    const typedEchoState = await waitForPaneTextChange(
      client,
      remoteSessionId,
      remoteUtilityPane.pane_id,
      preTypePaneText,
      20_000,
    );
    const typedEchoAt = Date.now();
    if (!typedEchoState?.text?.includes(commandEchoNeedle)) {
      throw new Error(
        `Remote utility pane text changed but did not include typed command ${commandEchoNeedle}. State:\n${JSON.stringify(typedEchoState, null, 2)}`
      );
    }
    const submitAt = Date.now();
    await client.request('write_pane', {
      sessionId: remoteSessionId,
      paneId: remoteUtilityPane.pane_id,
      text: '\r',
      submit: false,
    });

    const paneTextState = await waitForPaneTextContains(
      client,
      remoteSessionId,
      remoteUtilityPane.pane_id,
      outputToken,
      20_000,
    );
    const outputVisibleAt = Date.now();
    saveJson(path.join(runDir, 'remote-pane-text.json'), paneTextState);

    const paneSnapshot = await client.request('capture_structured_snapshot', {
      includePaneText: true,
      sessionIds: [remoteSessionId],
    });
    saveJson(path.join(runDir, 'session-ui-pane-text.json'), paneSnapshot);
    saveJson(
      path.join(runDir, 'remote-pane-render-health.json'),
      await client.request('capture_render_health', { sessionIds: [remoteSessionId] }),
    );

    const utilityBridgeSession = await waitForBridgeSession(
      client,
      (session) => session.id === remoteSessionId,
      `id=${remoteSessionId} utility verification`,
      20_000
    );
    saveJson(path.join(runDir, 'remote-session-interactive.json'), {
      outputToken,
      commandEchoNeedle,
      command,
      pane: remoteUtilityPane,
      bridge: utilityBridgeSession,
      timing: {
        focusReadyMs: readyForInputAt - focusRequestedAt,
        typedEchoMs: typedEchoAt - typeStartedAt,
        outputVisibleMs: outputVisibleAt - submitAt,
      },
    });

    remoteInteraction = {
      outputToken,
      commandEchoNeedle,
      command,
      leftOperand,
      rightOperand,
      focusRequestedAt,
      readyForInputAt,
      typeStartedAt,
      typedEchoAt,
      submitAt,
      outputVisibleAt,
    };

    const preReloadMainTextState = await client.request('read_pane_text', {
      sessionId: remoteSessionId,
      paneId: remoteInitialPaneId,
    }, {
      timeoutMs: 15_000,
    });
    const preReloadMainText = typeof preReloadMainTextState?.text === 'string' ? preReloadMainTextState.text : '';

    setStep('remote-reload');
    await client.request('reload_session', {
      sessionId: remoteSessionId,
    }, {
      timeoutMs: 20_000,
    });
    await client.request('focus_pane', {
      sessionId: remoteSessionId,
      paneId: remoteInitialPaneId,
    });
    await observer.waitForScrollbackReady(remoteSessionId, 20_000);
    const reloadedSessionUi = await waitForSessionUiState(
      client,
      remoteSessionId,
      (sessionState) =>
        Boolean(
          sessionState?.selected &&
          sessionState?.workspaceBounds &&
          sessionState?.mainPaneBounds,
        ),
      `reloaded remote workspace for ${remoteSessionId}`,
      30_000,
    );
    saveJson(path.join(runDir, 'session-ui-reloaded.json'), reloadedSessionUi);

    const mainTextAfterReload = await waitForPaneTextChange(
      client,
      remoteSessionId,
      remoteInitialPaneId,
      preReloadMainText,
      20_000,
    );
    saveJson(path.join(runDir, 'remote-main-reload-pane-text.json'), mainTextAfterReload);

    for (const sessionId of createdRemoteSessionIds) {
      await sendRemoteSocketMessage(extraOptions.sshTarget, remoteSocketPath, {
        cmd: 'unregister',
        id: sessionId,
      });
      await waitForSessionRemoved(observer, sessionId, 20_000);
      await waitForBridgeSessionRemoved(client, sessionId, 20_000);
    }

    if (removeEndpointOnCleanup) {
      setStep('endpoint-cleanup');
      observer.removeEndpoint(endpoint.id);
      await waitForEndpointRemoved(observer, endpoint.id, 20_000);
      endpointId = null;
    }

    setStep('artifact-capture');
    await captureArtifacts(client, runDir, 'final');
    const summary = {
      ok: true,
      runId,
      currentStep,
      steps,
      sshTarget: extraOptions.sshTarget,
      endpointName,
      remoteSessionId,
      remoteSessionLabel,
      remoteWorktreeSessionId,
      remoteWorktreePath,
      remoteWorktreeBranch,
      remoteDirectory,
      remoteSocketPath,
      artifacts: {
        runDir,
        endpoint: 'endpoint-connected.json',
        registered: 'remote-session-registered.json',
        selectedSnapshot: 'session-ui-selected.json',
        reloadedSnapshot: 'session-ui-reloaded.json',
        sidebarSnapshot: 'session-ui-sidebar.json',
        interactiveSnapshot: 'remote-session-interactive.json',
        pickerOpen: 'picker-open.json',
        pickerTarget: 'picker-target.json',
        pickerSuggestions: 'picker-suggestions.json',
        pickerRepoOptions: 'picker-repo-options.json',
        pickerRecents: 'picker-recents.json',
        pickerWorktreeOptions: 'picker-worktree-options.json',
        pickerWorktreeForm: 'picker-new-worktree-form.json',
        pickerWorktreeCreated: remoteWorktreeSessionId ? 'picker-worktree-created.json' : null,
        pickerWorktreeVisible: remoteWorktreeSessionId ? 'picker-worktree-visible.json' : null,
        paneTextState: 'remote-pane-text.json',
        paneTextSnapshot: 'session-ui-pane-text.json',
        mainReloadPaneText: 'remote-main-reload-pane-text.json',
        runtimeDebugConfig: 'runtime-debug-config.json',
        utilityPaneReady: 'remote-utility-pane-ready.json',
        finalSnapshot: 'structured-snapshot-final.json',
      },
    };
    saveJson(path.join(runDir, 'summary.json'), summary);
    console.log('[RealAppHarness] Bridge remote hub smoke passed.');
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = {
      ok: false,
      runId,
      currentStep,
      steps,
      sshTarget: extraOptions.sshTarget,
      endpointName,
      remoteSessionId,
      remoteSessionLabel,
      remoteWorktreeSessionId,
      remoteWorktreePath,
      remoteWorktreeBranch,
      remoteDirectory,
      remoteSocketPath,
      error: error instanceof Error ? error.stack || error.message : String(error),
    };
    try {
      await captureArtifacts(client, runDir, 'failure');
    } catch {
      // Ignore artifact capture failures while recording the primary failure.
    }
    try {
      if (remoteSessionId) {
        const sessionUi = await client.request('get_session_ui_state', { sessionId: remoteSessionId }, { timeoutMs: 10_000 });
        saveJson(path.join(runDir, 'session-ui-failure.json'), sessionUi);
      }
    } catch {
      // Ignore failure artifact errors.
    }
    try {
      if (remoteSessionId && remoteUtilityPane?.pane_id) {
        const paneState = await client.request('get_pane_state', {
          sessionId: remoteSessionId,
          paneId: remoteUtilityPane.pane_id,
        }, { timeoutMs: 10_000 });
        saveJson(path.join(runDir, 'remote-utility-pane-state-failure.json'), paneState);
      }
    } catch {
      // Ignore failure artifact errors.
    }
    try {
      const perfSnapshot = await client.request('capture_perf_snapshot', {
        settleFrames: 2,
        includeMemory: false,
      }, { timeoutMs: 15_000 });
      saveJson(path.join(runDir, 'perf-snapshot-failure.json'), perfSnapshot);
    } catch {
      // Ignore failure artifact errors.
    }
    try {
      if (remoteInteraction) {
        saveJson(path.join(runDir, 'remote-interaction-failure.json'), remoteInteraction);
      }
    } catch {
      // Ignore failure artifact errors.
    }
    saveJson(path.join(runDir, 'summary.json'), summary);
    throw error;
  } finally {
    if (remoteSocketPath) {
      for (const sessionId of createdRemoteSessionIds) {
        try {
          await sendRemoteSocketMessage(extraOptions.sshTarget, remoteSocketPath, {
            cmd: 'unregister',
            id: sessionId,
          });
        } catch {
          // Ignore remote cleanup failures during teardown.
        }
      }
    }
    if (endpointId && removeEndpointOnCleanup) {
      try {
        observer.removeEndpoint(endpointId);
        await waitForEndpointRemoved(observer, endpointId, 10_000);
      } catch {
        // Ignore endpoint cleanup failures during teardown.
      }
    }
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppHarness] Bridge remote hub smoke failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
