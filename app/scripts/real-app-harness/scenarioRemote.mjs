import { execFile } from 'node:child_process';
import path from 'node:path';
import { promisify } from 'node:util';
import { sleep } from './scenarioAssertions.mjs';

const execFileAsync = promisify(execFile);

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

export async function runSSH(target, command, timeoutMs = 30_000) {
  const { stdout } = await execFileAsync(
    'ssh',
    [
      '-S', 'none',
      '-o', 'BatchMode=yes',
      '-o', 'ControlMaster=no',
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

export async function getRemoteHome(target) {
  return (await runSSH(target, 'printf %s "$HOME"')).trim();
}

async function waitForRemoteProcesses(listFn, predicate, description, timeoutMs = 60_000, snapshotLabel = 'remote process snapshot') {
  const startedAt = Date.now();
  let lastSnapshot = [];
  while (Date.now() - startedAt < timeoutMs) {
    lastSnapshot = await listFn(Math.min(30_000, timeoutMs));
    const value = predicate(lastSnapshot);
    if (value) {
      return value;
    }
    await sleep(500);
  }
  throw new Error(
    `Timed out waiting for ${description}. Last ${snapshotLabel}:\n${JSON.stringify(lastSnapshot, null, 2)}`
  );
}

async function inspectRemoteHarnessProcesses(target, harnessRoot, mode = 'list', timeoutMs = 60_000) {
  const output = await runSSH(
    target,
    `python3 - ${shellQuote(harnessRoot)} ${shellQuote(mode)} <<'PY'
import json
import os
import shutil
import signal
import sys
import time

root = os.path.realpath(os.path.expanduser(sys.argv[1]))
mode = sys.argv[2]

def safe_readlink(path):
    try:
        return os.readlink(path)
    except OSError:
        return ''

def safe_cmdline(pid):
    try:
        with open(f'/proc/{pid}/cmdline', 'rb') as handle:
            return handle.read().replace(b'\\0', b' ').decode('utf-8', 'replace').strip()
    except OSError:
        return ''

def safe_ppid(pid):
    try:
        with open(f'/proc/{pid}/status', 'r', encoding='utf-8', errors='replace') as handle:
            for line in handle:
                if line.startswith('PPid:'):
                    return int(line.split(':', 1)[1].strip())
    except OSError:
        return None
    return None

def collect_processes():
    processes = {}
    try:
        entries = os.listdir('/proc')
    except OSError:
        entries = []
    for entry in entries:
        if not entry.isdigit():
            continue
        pid = int(entry)
        cwd = safe_readlink(f'/proc/{pid}/cwd')
        exe = safe_readlink(f'/proc/{pid}/exe')
        processes[pid] = {
            'pid': pid,
            'ppid': safe_ppid(pid),
            'cwd': cwd,
            'exe': exe,
            'cmdline': safe_cmdline(pid),
        }
    return processes

def path_matches_root(value):
    if not value:
        return False
    try:
        return os.path.realpath(value).startswith(root)
    except OSError:
        return False

def direct_match(proc):
    return (
        path_matches_root(proc.get('cwd', '')) or
        path_matches_root(proc.get('exe', '')) or
        root in proc.get('cmdline', '')
    )

def match_processes():
    processes = collect_processes()
    excluded = set()
    cursor = os.getpid()
    while isinstance(cursor, int) and cursor > 1 and cursor not in excluded:
        excluded.add(cursor)
        proc = processes.get(cursor)
        if not proc:
            break
        cursor = proc.get('ppid')
    matched = {pid for pid, proc in processes.items() if pid not in excluded and direct_match(proc)}
    changed = True
    while changed:
        changed = False
        for pid, proc in processes.items():
            ppid = proc.get('ppid')
            if ppid in matched and pid not in matched and pid not in excluded:
                matched.add(pid)
                changed = True
    result = [processes[pid] for pid in sorted(matched)]
    return processes, result

def wait_for_exit(pids, timeout_s):
    deadline = time.time() + timeout_s
    remaining = set(pids)
    while remaining and time.time() < deadline:
        remaining = {pid for pid in remaining if os.path.exists(f'/proc/{pid}')}
        if remaining:
            time.sleep(0.2)
    return sorted(remaining)

def kill_processes(processes, sig):
    delivered = []
    for proc in sorted(processes, key=lambda item: item.get('pid', 0), reverse=True):
        pid = proc.get('pid')
        if not isinstance(pid, int):
            continue
        try:
            os.kill(pid, sig)
            delivered.append(pid)
        except OSError:
            continue
    return delivered

if mode == 'cleanup':
    _, matched_before = match_processes()
    terminated = kill_processes(matched_before, signal.SIGTERM)
    remaining_after_term = wait_for_exit(terminated, 5.0)
    leftover_term_processes = [proc for proc in matched_before if proc.get('pid') in remaining_after_term]
    killed = kill_processes(leftover_term_processes, signal.SIGKILL)
    wait_for_exit(killed, 2.0)
    _, leftover = match_processes()
    root_removed = False
    if os.path.exists(root):
        try:
            shutil.rmtree(root)
            root_removed = not os.path.exists(root)
        except OSError:
            root_removed = False
    print(json.dumps({
        'root': root,
        'matched': matched_before,
        'terminated': terminated,
        'killed': killed,
        'leftover': leftover,
        'rootRemoved': root_removed or (not os.path.exists(root)),
    }))
else:
    _, matched = match_processes()
    print(json.dumps(matched))
PY`,
    timeoutMs,
  );
  return JSON.parse(String(output || '[]').trim() || '[]');
}

export async function listRemoteProcessesByHarnessRoot(target, harnessRoot, timeoutMs = 30_000) {
  return inspectRemoteHarnessProcesses(target, harnessRoot, 'list', timeoutMs);
}

export async function waitForRemoteProcessesByHarnessRoot(target, harnessRoot, predicate, description, timeoutMs = 60_000) {
  return waitForRemoteProcesses(
    (snapshotTimeoutMs) => listRemoteProcessesByHarnessRoot(target, harnessRoot, snapshotTimeoutMs),
    predicate,
    description,
    timeoutMs,
    `remote process snapshot for harness root ${harnessRoot}`,
  );
}

export async function cleanupRemoteHarnessProcesses(target, harnessRoot, timeoutMs = 60_000) {
  return inspectRemoteHarnessProcesses(target, harnessRoot, 'cleanup', timeoutMs);
}

export function chooseRemoteWSPort() {
  return 19000 + Math.floor(Math.random() * 2000);
}

// When the home screen shipped the Sync-button flow (2026-04-12), remote
// endpoints stopped silently auto-bootstrapping on binary or protocol
// mismatches — they park in `binary_mismatch` / `version_mismatch` /
// `version_ahead` waiting for the user to click Sync. The harness doesn't
// have a UI to click, so we send `bootstrap_endpoint` programmatically the
// first time we observe one of those statuses, mirroring what the button does.
const SYNC_REQUIRED_STATUSES = new Set([
  'binary_mismatch',
  'version_mismatch',
  'version_ahead',
]);

export async function waitForEndpointConnected(observer, name, timeoutMs = 180_000) {
  const startedAt = Date.now();
  let lastEndpoint = null;
  const bootstrappedIds = new Set();
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
      if (SYNC_REQUIRED_STATUSES.has(endpoint.status) && !bootstrappedIds.has(endpoint.id)) {
        try {
          observer.send({ cmd: 'bootstrap_endpoint', endpoint_id: endpoint.id });
          bootstrappedIds.add(endpoint.id);
        } catch {
          // Leave endpoint out of the bootstrapped set so the next tick retries.
        }
      }
    }
    await sleep(500);
  }
  throw new Error(
    `Timed out waiting for endpoint ${name} to connect. Last endpoint state:\n${JSON.stringify(lastEndpoint, null, 2)}`
  );
}

export async function removeStaleHarnessEndpoints(observer, timeoutMs = 20_000) {
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

export async function removeStaleHarnessScenarioSessions(observer, timeoutMs = 60_000) {
  const staleSessions = [...observer.sessionsById.values()].filter((session) =>
    typeof session?.label === 'string' && /^tr\d{3}(?:-|$)/.test(session.label)
  );
  if (staleSessions.length === 0) {
    return {
      sessions: [],
      lingeringWorkspaceSessionIds: [],
    };
  }

  const targetIds = new Set(staleSessions.map((session) => session.id));
  for (const session of staleSessions) {
    observer.send({ cmd: 'kill_session', id: session.id });
  }

  await observer.waitFor(() => {
    const remainingWorking = [...observer.sessionsById.values()].filter((session) =>
      targetIds.has(session.id) && session.state === 'working'
    );
    return remainingWorking.length === 0 ? true : null;
  }, `harness scenario session kill (${[...targetIds].join(', ')})`, Math.min(timeoutMs, 20_000));

  for (const sessionId of targetIds) {
    observer.unregisterSession(sessionId);
  }

  await observer.waitFor(() => {
    const remainingSessions = [...observer.sessionsById.values()].filter((session) => targetIds.has(session.id));
    return remainingSessions.length === 0 ? true : null;
  }, `harness scenario session unregister (${[...targetIds].join(', ')})`, timeoutMs);

  return {
    sessions: staleSessions,
    lingeringWorkspaceSessionIds: [...observer.workspacesBySessionId.keys()].filter((sessionId) => targetIds.has(sessionId)),
  };
}

export function buildRemoteHarnessPaths(remoteHome, runId) {
  const remoteHarnessRoot = path.posix.join(remoteHome, '.attn', 'harness', runId);
  return {
    remoteHarnessRoot,
    remoteHarnessBinary: path.posix.join(remoteHarnessRoot, 'bin', 'attn'),
    remoteHarnessSocket: path.posix.join(remoteHarnessRoot, 'attn.sock'),
    remoteHarnessDB: path.posix.join(remoteHarnessRoot, 'attn.db'),
  };
}
