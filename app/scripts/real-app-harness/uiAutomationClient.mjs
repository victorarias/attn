import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function defaultManifestPath() {
  return path.join(
    os.homedir(),
    'Library',
    'Application Support',
    'com.attn.manager',
    'debug',
    'ui-automation.json'
  );
}

export class UiAutomationClient {
  constructor({
    appPath = path.join(os.homedir(), 'Applications', 'attn.app'),
    manifestPath = defaultManifestPath(),
    launchEnv = null,
  } = {}) {
    this.appPath = appPath;
    this.manifestPath = manifestPath;
    this.launchEnv = launchEnv;
  }

  async launchApp() {
    if (this.launchEnv && Object.keys(this.launchEnv).length > 0) {
      const executablePath = this.appPath.endsWith('.app')
        ? path.join(this.appPath, 'Contents', 'MacOS', 'app')
        : this.appPath;
      const child = spawn(executablePath, [], {
        detached: true,
        stdio: 'ignore',
        env: {
          ...process.env,
          ...this.launchEnv,
        },
      });
      child.unref();
      return;
    }
    await execFileAsync('open', [this.appPath]);
  }

  async quitApp(timeoutMs = 10_000) {
    let existingPid = null;

    try {
      existingPid = this.readManifest()?.pid ?? null;
    } catch {
      existingPid = null;
    }

    try {
      await execFileAsync('osascript', ['-e', 'tell application id "com.attn.manager" to quit']);
    } catch {
      // Fall through to process-based cleanup.
    }

    const startedAt = Date.now();
    while (Date.now() - startedAt < timeoutMs) {
      const appPids = await this.listAppPids();
      if ((!existingPid || !processExists(existingPid)) && appPids.length === 0) {
        return;
      }
      await delay(200);
    }

    const remainingPids = await this.listAppPids();
    for (const pid of new Set([existingPid, ...remainingPids].filter((value) => Number.isInteger(value) && value > 0))) {
      try {
        process.kill(pid, 'SIGTERM');
      } catch {}
    }
    await delay(500);
    for (const pid of await this.listAppPids()) {
      try {
        process.kill(pid, 'SIGKILL');
      } catch {}
    }
  }

  async launchFreshApp() {
    await this.quitApp();
    try {
      fs.unlinkSync(this.manifestPath);
    } catch {}
    await this.launchApp();
  }

  async waitForManifest(timeoutMs = 15_000) {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const manifest = this.readManifest();
        if (manifest?.enabled && manifest.port && manifest.token && processExists(manifest.pid)) {
          return manifest;
        }
      } catch (error) {
        lastError = error;
      }
      await delay(200);
    }

    throw new Error(
      `Timed out waiting for UI automation manifest at ${this.manifestPath}: ${lastError instanceof Error ? lastError.message : lastError || 'manifest unavailable'}`
    );
  }

  readManifest() {
    return JSON.parse(fs.readFileSync(this.manifestPath, 'utf8'));
  }

  async listAppPids() {
    try {
      const { stdout } = await execFileAsync('pgrep', ['-f', `${this.appPath}/Contents/MacOS/app`]);
      return stdout
        .split(/\s+/)
        .map((value) => Number.parseInt(value, 10))
        .filter((value) => Number.isInteger(value) && value > 0);
    } catch {
      return [];
    }
  }

  async request(action, payload = {}, options = {}) {
    const manifest = this.readManifest();
    const requestId = `ui-automation-client-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    const request = {
      id: requestId,
      token: manifest.token,
      action,
      payload,
    };
    const timeoutMs = options.timeoutMs ?? 35_000;

    return new Promise((resolve, reject) => {
      const socket = net.createConnection({
        host: '127.0.0.1',
        port: manifest.port,
      });

      let buffer = '';
      const timeout = setTimeout(() => {
        cleanup();
        reject(new Error(`Automation request timed out: ${action} (${requestId}) after ${timeoutMs}ms`));
      }, timeoutMs);
      const cleanup = () => {
        clearTimeout(timeout);
        socket.removeAllListeners();
        socket.end();
      };

      socket.on('connect', () => {
        socket.write(`${JSON.stringify(request)}\n`);
      });

      socket.on('data', (chunk) => {
        buffer += chunk.toString();
        const newlineIndex = buffer.indexOf('\n');
        if (newlineIndex === -1) {
          return;
        }
        const line = buffer.slice(0, newlineIndex).trim();
        cleanup();
        try {
          const response = JSON.parse(line);
          if (!response.ok) {
            const detail = response.error || 'unknown error';
            reject(new Error(`Automation request failed: ${action} (${requestId}): ${detail}`));
            return;
          }
          resolve(response.result);
        } catch (error) {
          reject(error);
        }
      });

      socket.on('error', (error) => {
        cleanup();
        reject(error);
      });
    });
  }

  async waitForReady(timeoutMs = 20_000) {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const result = await this.request('ping');
        if (result?.frontendReady) {
          return result;
        }
      } catch (error) {
        lastError = error;
      }
      await delay(250);
    }

    throw new Error(
      `Timed out waiting for frontend automation readiness: ${lastError instanceof Error ? lastError.message : lastError || 'bridge not ready'}`
    );
  }

  async waitForFrontendResponsive(timeoutMs = 20_000, action = 'list_sessions') {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const result = await this.request(action, {}, { timeoutMs: Math.min(15_000, timeoutMs) });
        if (
          action === 'get_state' &&
          result &&
          typeof result === 'object' &&
          'daemonReady' in result &&
          result.daemonReady === false
        ) {
          const detail = typeof result.connectionError === 'string' && result.connectionError
            ? `: ${result.connectionError}`
            : '';
          throw new Error(`daemon not ready${detail}`);
        }
        return result;
      } catch (error) {
        lastError = error;
      }
      await delay(250);
    }

    throw new Error(
      `Timed out waiting for frontend automation responsiveness via ${action}: ${lastError instanceof Error ? lastError.message : lastError || 'unknown error'}`
    );
  }
}

function processExists(pid) {
  if (!Number.isInteger(pid) || pid <= 0) {
    return false;
  }

  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}
