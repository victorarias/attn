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

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function saveText(filePath, value) {
  fs.writeFileSync(filePath, value, 'utf8');
}

function buildScriptedReviewLoopEnv() {
  const config = {
    scenarios: [
      {
        name: 'stop-flow',
        match_prompt_contains: 'ATTN_REMOTE_LOOP_STOP_SCENARIO',
        iterations: [
          {
            delay_ms: 5000,
            outcome: {
              loop_decision: 'continue',
              summary: 'Stop scenario should be interrupted before completion.',
              changes_made: false,
              files_touched: ['tracked.txt'],
              questions_for_user: [],
              blocking_reason: '',
              suggested_next_focus: 'Stop the loop from the UI.',
            },
            assistant_trace: JSON.stringify({
              entries: [
                { kind: 'text', content: 'Scripted stop-flow iteration is running.' },
              ],
            }),
            result_text: 'stop-flow',
          },
        ],
      },
      {
        name: 'await-user-flow',
        match_prompt_contains: 'ATTN_REMOTE_LOOP_QUESTION_SCENARIO',
        iterations: [
          {
            outcome: {
              loop_decision: 'needs_user_input',
              summary: 'Need a human decision before the next pass.',
              changes_made: false,
              files_touched: ['tracked.txt'],
              questions_for_user: ['Should the loop continue with answer yes?'],
              blocking_reason: 'Waiting for explicit approval.',
              suggested_next_focus: 'Await user answer.',
            },
            assistant_trace: JSON.stringify({
              entries: [
                { kind: 'text', content: 'Scripted review loop is waiting for an answer.' },
              ],
            }),
            result_text: 'await-user-flow',
          },
          {
            expect_answer: 'Answer: yes',
            outcome: {
              loop_decision: 'converged',
              summary: 'Loop completed after receiving the scripted answer.',
              changes_made: true,
              files_touched: ['tracked.txt'],
              questions_for_user: [],
              blocking_reason: '',
              suggested_next_focus: 'Done.',
            },
            assistant_trace: JSON.stringify({
              entries: [
                { kind: 'text', content: 'Scripted review loop received the answer and completed.' },
              ],
            }),
            result_text: 'completed-after-answer',
          },
        ],
      },
    ],
  };
  return {
    ATTN_REVIEW_LOOP_SCRIPT_B64: Buffer.from(JSON.stringify(config), 'utf8').toString('base64'),
  };
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
rm -f "$HOME/.attn/attn.sock" "$HOME/.attn/attn.pid" "$HOME/.attn/daemon.log" "$HOME/.local/bin/attn" "$HOME/.local/bin/attn.tmp"
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

async function waitForReviewLoopState(client, sessionId, predicate, description, timeoutMs = 60_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    const remainingMs = timeoutMs - (Date.now() - startedAt);
    lastState = await client.request(
      'review_loop_get_state',
      { sessionId },
      { timeoutMs: Math.max(10_000, Math.min(20_000, remainingMs)) },
    );
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(250);
  }
  throw new Error(`Timed out waiting for ${description}. Last state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForReviewLoopUiState(client, sessionId, predicate, description, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastState = null;
  while (Date.now() - startedAt < timeoutMs) {
    const remainingMs = timeoutMs - (Date.now() - startedAt);
    lastState = await client.request(
      'review_loop_ui_state',
      { sessionId },
      { timeoutMs: Math.max(10_000, Math.min(20_000, remainingMs)) },
    );
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(250);
  }
  throw new Error(`Timed out waiting for ${description}. Last UI state:\n${JSON.stringify(lastState, null, 2)}`);
}

async function waitForReviewPanelSnapshot(client, predicate, description, timeoutMs = 30_000) {
  const startedAt = Date.now();
  let lastSnapshot = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastSnapshot = await client.request('capture_perf_snapshot', {
      settleFrames: 2,
      includeMemory: false,
    }, {
      timeoutMs: 15_000,
    });
    if (predicate(lastSnapshot)) {
      return lastSnapshot;
    }
    await sleep(250);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last snapshot:\n${JSON.stringify(lastSnapshot, null, 2)}`
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
  let remoteSessionLabel = path.basename(extraOptions.remoteDirectory || `remote-hub-${runId}`) || 'session';
  let remoteWorktreeSessionId = null;
  let remoteWorktreePath = null;
  let remoteWorktreeBranch = null;
  const client = new UiAutomationClient({
    appPath: options.appPath,
    launchEnv: buildScriptedReviewLoopEnv(),
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
    const normalizedTarget = normalizeSshTarget(extraOptions.sshTarget);
    let endpoint = [...observer.endpointsById.values()].find(
      (candidate) => normalizeSshTarget(candidate.ssh_target) === normalizedTarget,
    ) || null;
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
    const remoteUtilityPane = await observer.waitForUtilityPane(remoteSessionId, 30_000);
    saveJson(path.join(runDir, 'remote-utility-pane.json'), {
      splitResult: utilityPane,
      pane: remoteUtilityPane,
    });
    await observer.waitForScrollbackReady(remoteUtilityPane.runtime_id, 20_000);

    await client.request('focus_pane', {
      sessionId: remoteSessionId,
      paneId: remoteUtilityPane.pane_id,
    });

    const marker = `__ATTN_REMOTE_OK_${Date.now()}__`;
    const command = `printf '%s\\n' '${marker}'`;
    await client.request('write_pane', {
      sessionId: remoteSessionId,
      paneId: remoteUtilityPane.pane_id,
      text: command,
    });

    const utilityScrollback = await observer.waitForScrollbackContains(
      remoteUtilityPane.runtime_id,
      marker,
      20_000
    );
    saveText(path.join(runDir, 'remote-utility-scrollback.txt'), utilityScrollback);

    const paneTextState = await waitForPaneTextContains(
      client,
      remoteSessionId,
      remoteUtilityPane.pane_id,
      marker,
      20_000,
    );
    saveJson(path.join(runDir, 'remote-pane-text.json'), paneTextState);

    const paneSnapshot = await client.request('capture_structured_snapshot', {
      includePaneText: true,
      sessionIds: [remoteSessionId],
    });
    saveJson(path.join(runDir, 'session-ui-pane-text.json'), paneSnapshot);

    const utilityBridgeSession = await waitForBridgeSession(
      client,
      (session) => session.id === remoteSessionId,
      `id=${remoteSessionId} utility verification`,
      20_000
    );
    saveJson(path.join(runDir, 'remote-session-interactive.json'), {
      marker,
      command,
      pane: remoteUtilityPane,
      bridge: utilityBridgeSession,
    });

    const preReloadMainScrollback = await observer.waitForScrollbackReady(remoteSessionId, 20_000);

    setStep('remote-reload');
    await client.request('reload_session', {
      sessionId: remoteSessionId,
    }, {
      timeoutMs: 20_000,
    });
    await client.request('focus_pane', {
      sessionId: remoteSessionId,
      paneId: 'main',
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

    const mainScrollback = await waitForScrollbackChange(
      observer,
      remoteSessionId,
      preReloadMainScrollback,
      20_000,
    );
    saveText(path.join(runDir, 'remote-main-reload-scrollback.txt'), mainScrollback);

    if (preparedRepo) {
      setStep('remote-review');
      const diffMarker = `phase-d-${Date.now()}`;
      await runSSH(extraOptions.sshTarget, `
cd ${shellQuote(preparedRepo.repoDir)}
printf '%s\\n' ${shellQuote(diffMarker)} >> ${shellQuote(preparedRepo.trackedFile)}
`);

      await client.request('dispatch_shortcut', { shortcutId: 'dock.diffDetail' });
      const reviewSnapshot = await waitForReviewPanelSnapshot(
        client,
        (snapshot) => Boolean(
          snapshot?.document?.diffDetailOpen &&
          snapshot?.review?.panel?.active &&
          snapshot?.review?.panel?.fileCount >= 1 &&
          typeof snapshot?.review?.panel?.selectedFilePath === 'string' &&
          snapshot.review.panel.selectedFilePath.includes(preparedRepo.trackedFile)
        ),
        'remote diff detail panel',
        45_000,
      );
      saveJson(path.join(runDir, 'remote-review-panel.json'), {
        diffMarker,
        reviewSnapshot,
      });

      const reviewState = await client.request('review_get_state', {
        repoPath: remoteDirectory,
        branch: registered.branch || 'main',
      }, {
        timeoutMs: 20_000,
      });
      if (!reviewState?.success || !reviewState.state?.review_id) {
        throw new Error(`Remote review state missing review_id: ${JSON.stringify(reviewState, null, 2)}`);
      }
      saveJson(path.join(runDir, 'remote-review-state.json'), reviewState);

      const commentMarker = `remote-comment-${Date.now()}`;
      const addComment = await client.request('review_add_comment', {
        reviewId: reviewState.state.review_id,
        filepath: preparedRepo.trackedFile,
        lineStart: 1,
        lineEnd: 1,
        content: commentMarker,
      }, {
        timeoutMs: 20_000,
      });
      if (!addComment?.success || !addComment.comment?.id) {
        throw new Error(`Remote add comment failed: ${JSON.stringify(addComment, null, 2)}`);
      }

      const updatedCommentMarker = `${commentMarker}-updated`;
      const updateComment = await client.request('review_update_comment', {
        commentId: addComment.comment.id,
        content: updatedCommentMarker,
      }, {
        timeoutMs: 20_000,
      });
      if (!updateComment?.success) {
        throw new Error(`Remote update comment failed: ${JSON.stringify(updateComment, null, 2)}`);
      }

      const resolveComment = await client.request('review_resolve_comment', {
        commentId: addComment.comment.id,
        resolved: true,
      }, {
        timeoutMs: 20_000,
      });
      if (!resolveComment?.success) {
        throw new Error(`Remote resolve comment failed: ${JSON.stringify(resolveComment, null, 2)}`);
      }

      const wontFixComment = await client.request('review_wont_fix_comment', {
        commentId: addComment.comment.id,
        wontFix: true,
      }, {
        timeoutMs: 20_000,
      });
      if (!wontFixComment?.success) {
        throw new Error(`Remote wont-fix comment failed: ${JSON.stringify(wontFixComment, null, 2)}`);
      }

      const commentsAfterMutation = await client.request('review_get_comments', {
        reviewId: reviewState.state.review_id,
        filepath: preparedRepo.trackedFile,
      }, {
        timeoutMs: 20_000,
      });
      const updatedComment = (commentsAfterMutation?.comments || []).find((comment) => comment.id === addComment.comment.id);
      if (!commentsAfterMutation?.success || !updatedComment || updatedComment.content !== updatedCommentMarker) {
        throw new Error(`Remote comments did not include updated comment: ${JSON.stringify(commentsAfterMutation, null, 2)}`);
      }

      const deleteComment = await client.request('review_delete_comment', {
        commentId: addComment.comment.id,
      }, {
        timeoutMs: 20_000,
      });
      if (!deleteComment?.success) {
        throw new Error(`Remote delete comment failed: ${JSON.stringify(deleteComment, null, 2)}`);
      }

      const commentsAfterDelete = await client.request('review_get_comments', {
        reviewId: reviewState.state.review_id,
        filepath: preparedRepo.trackedFile,
      }, {
        timeoutMs: 20_000,
      });
      if ((commentsAfterDelete?.comments || []).some((comment) => comment.id === addComment.comment.id)) {
        throw new Error(`Remote delete comment did not remove comment: ${JSON.stringify(commentsAfterDelete, null, 2)}`);
      }
      saveJson(path.join(runDir, 'remote-comment-mutations.json'), {
        addComment,
        updateComment,
        resolveComment,
        wontFixComment,
        commentsAfterMutation,
        deleteComment,
        commentsAfterDelete,
      });

      setStep('remote-review-loop');
      const stopLoopPrompt = 'ATTN_REMOTE_LOOP_STOP_SCENARIO stop this loop after it starts.';
      await client.request('review_loop_start', {
        prompt: stopLoopPrompt,
        iterationLimit: 1,
      }, {
        timeoutMs: 20_000,
      });
      const stopLoopRunning = await waitForReviewLoopState(
        client,
        remoteSessionId,
        (state) => state?.success && state.state?.status === 'running' && !!state.state?.loop_id,
        `remote review loop running for ${remoteSessionId}`,
        20_000,
      );
      await client.request('review_loop_stop', {}, { timeoutMs: 20_000 });
      const stopLoopStopped = await waitForReviewLoopState(
        client,
        remoteSessionId,
        (state) => state?.success && state.state?.status === 'stopped',
        `remote review loop stopped for ${remoteSessionId}`,
        20_000,
      );
      saveJson(path.join(runDir, 'remote-review-loop-stop.json'), {
        start: stopLoopRunning,
        stopped: stopLoopStopped,
      });

      const questionLoopPrompt = 'ATTN_REMOTE_LOOP_QUESTION_SCENARIO ask the scripted question and wait for yes.';
      await client.request('review_loop_start', {
        prompt: questionLoopPrompt,
        iterationLimit: 2,
      }, {
        timeoutMs: 20_000,
      });
      const awaitingUserState = await waitForReviewLoopState(
        client,
        remoteSessionId,
        (state) => state?.success && state.state?.status === 'awaiting_user' && !!state.state?.pending_interaction?.id,
        `remote review loop awaiting user for ${remoteSessionId}`,
        30_000,
      );

      await client.request('select_session', { sessionId: remoteSessionId });
      const initialReviewLoopUi = await client.request('review_loop_ui_state', {
        sessionId: remoteSessionId,
      });
      if (!initialReviewLoopUi?.panelBounds) {
        await client.request('dispatch_shortcut', { shortcutId: 'dock.reviewLoop' });
      }
      const awaitingUserUi = await waitForReviewLoopUiState(
        client,
        remoteSessionId,
        (state) => Boolean(state?.panelBounds && state?.questionText?.includes('Should the loop continue with answer yes?')),
        `remote review loop UI question for ${remoteSessionId}`,
        20_000,
      );
      saveJson(path.join(runDir, 'remote-review-loop-awaiting-user.json'), {
        state: awaitingUserState,
        ui: awaitingUserUi,
      });

      const pendingInteraction = awaitingUserState?.state?.pending_interaction;
      const answerResult = await client.request('review_loop_answer', {
        loopId: awaitingUserState.state.loop_id,
        interactionId: pendingInteraction.id,
        answer: 'yes',
      }, {
        timeoutMs: 20_000,
      });
      const completedLoopState = await waitForReviewLoopState(
        client,
        remoteSessionId,
        (state) => state?.success && state.state?.status === 'completed',
        `remote review loop completed for ${remoteSessionId}`,
        30_000,
      );
      const completedLoopUi = await waitForReviewLoopUiState(
        client,
        remoteSessionId,
        (state) => Boolean(
          state?.panelBounds &&
          state?.drawerTestId === `review-loop-drawer-${remoteSessionId}` &&
          state?.statusText?.includes('All Rounds Done')
        ),
        `remote review loop completed UI for ${remoteSessionId}`,
        20_000,
      );
      saveJson(path.join(runDir, 'remote-review-loop-completed.json'), {
        answerResult,
        state: completedLoopState,
        ui: completedLoopUi,
      });
    }

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
        mainReloadScrollback: 'remote-main-reload-scrollback.txt',
        reviewState: preparedRepo ? 'remote-review-state.json' : null,
        reviewPanelSnapshot: preparedRepo ? 'remote-review-panel.json' : null,
        commentMutations: preparedRepo ? 'remote-comment-mutations.json' : null,
        reviewLoopStop: preparedRepo ? 'remote-review-loop-stop.json' : null,
        reviewLoopAwaitingUser: preparedRepo ? 'remote-review-loop-awaiting-user.json' : null,
        reviewLoopCompleted: preparedRepo ? 'remote-review-loop-completed.json' : null,
        utilityScrollback: 'remote-utility-scrollback.txt',
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
