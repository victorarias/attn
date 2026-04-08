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

export async function getRemoteHome(target) {
  return (await runSSH(target, 'printf %s "$HOME"')).trim();
}

export function chooseRemoteWSPort() {
  return 19000 + Math.floor(Math.random() * 2000);
}

export async function waitForEndpointConnected(observer, name, timeoutMs = 120_000) {
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

export function buildRemoteHarnessPaths(remoteHome, runId) {
  const remoteHarnessRoot = path.posix.join(remoteHome, '.attn', 'harness', runId);
  return {
    remoteHarnessRoot,
    remoteHarnessBinary: path.posix.join(remoteHarnessRoot, 'bin', 'attn'),
    remoteHarnessSocket: path.posix.join(remoteHarnessRoot, 'attn.sock'),
    remoteHarnessDB: path.posix.join(remoteHarnessRoot, 'attn.db'),
  };
}
