import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { execFile, spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');
const SOURCE_FINGERPRINT_SCRIPT = path.join(REPO_ROOT, 'scripts', 'source-fingerprint.sh');
const MINIMAL_SYSTEM_PATH = '/usr/bin:/bin:/usr/sbin:/sbin';

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

function isTransientManifestReadError(error) {
  if (!error) {
    return false;
  }
  if (error instanceof SyntaxError) {
    return true;
  }
  if (typeof error === 'object' && error && 'code' in error) {
    return error.code === 'ENOENT';
  }
  return false;
}

function isRetryableAutomationAbsence(error) {
  const message = error instanceof Error ? error.message : String(error || '');
  return message.includes('Session not found') || message.includes('Pane not found');
}

const TRANSIENT_SESSION_ACTIONS = new Set([
  'click_pane',
  'close_pane',
  'focus_pane',
  'get_pane_state',
  'get_workspace',
  'read_pane_text',
  'scroll_pane_to_top',
  'select_session',
  'split_pane',
  'type_pane_via_ui',
  'write_pane',
]);

function readNonEmptyString(value) {
  if (typeof value !== 'string') {
    return null;
  }
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

function readTruthyEnv(value) {
  const normalized = readNonEmptyString(value)?.toLowerCase();
  return normalized === '1' || normalized === 'true' || normalized === 'yes';
}

function bundledDaemonPathForApp(appPath) {
  if (appPath.endsWith('.app')) {
    return path.join(appPath, 'Contents', 'MacOS', 'attn');
  }
  return path.join(path.dirname(appPath), 'attn');
}

function normalizeBuildInfo(raw) {
  if (!raw || typeof raw !== 'object') {
    return {
      version: null,
      buildTime: null,
      sourceFingerprint: null,
      gitCommit: null,
    };
  }
  return {
    version: readNonEmptyString(raw.version),
    buildTime: readNonEmptyString(raw.buildTime),
    sourceFingerprint: readNonEmptyString(raw.sourceFingerprint),
    gitCommit: readNonEmptyString(raw.gitCommit),
  };
}

function isFatalFrontendResponsivenessError(error) {
  const message = error instanceof Error ? error.message : String(error || '');
  return (
    message.includes('Version mismatch:') ||
    message.includes('packaged app source mismatch:') ||
    message.includes('daemon source mismatch:') ||
    message.includes('packaged app did not report build identity') ||
    message.includes('failed to read build info from') ||
    message.includes('current source fingerprint is unavailable')
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
    this.currentSourceIdentityPromise = null;
    this.verifiedBuildIdentityKey = null;
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
    const timeoutMs = options.timeoutMs ?? 35_000;
    const retryBudgetMs = Math.min(options.retrySessionAbsenceMs ?? 5_000, timeoutMs);
    const shouldRetrySessionAbsence = options.retrySessionAbsence ?? TRANSIENT_SESSION_ACTIONS.has(action);
    const startedAt = Date.now();

    while (true) {
      try {
        return await this.requestOnce(action, payload, timeoutMs);
      } catch (error) {
        if (!shouldRetrySessionAbsence || !isRetryableAutomationAbsence(error)) {
          throw error;
        }
        if (Date.now() - startedAt >= retryBudgetMs) {
          throw error;
        }
        await delay(200);
      }
    }
  }

  async requestOnce(action, payload = {}, timeoutMs = 35_000) {
    const requestId = `ui-automation-client-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    let manifest;

    try {
      manifest = this.readManifest();
    } catch (error) {
      if (!isTransientManifestReadError(error)) {
        throw error;
      }
      await this.waitForManifest(Math.min(5_000, timeoutMs));
      manifest = this.readManifest();
    }

    const request = {
      id: requestId,
      token: manifest.token,
      action,
      payload,
    };

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

  async getCurrentSourceIdentity() {
    if (!this.currentSourceIdentityPromise) {
      this.currentSourceIdentityPromise = execFileAsync('bash', [SOURCE_FINGERPRINT_SCRIPT, '--json'], {
        cwd: REPO_ROOT,
      }).then(({ stdout }) => JSON.parse(stdout));
    }
    return this.currentSourceIdentityPromise;
  }

  resolvePreferLocalDaemon() {
    const launchValue = this.launchEnv && typeof this.launchEnv === 'object'
      ? this.launchEnv.ATTN_PREFER_LOCAL_DAEMON
      : undefined;
    return readTruthyEnv(launchValue) || readTruthyEnv(process.env.ATTN_PREFER_LOCAL_DAEMON);
  }

  resolveDaemonBinaryPath() {
    const localPath = path.join(os.homedir(), '.local', 'bin', 'attn');
    const bundledPath = bundledDaemonPathForApp(this.appPath);
    const preferLocal = this.resolvePreferLocalDaemon();

    if (preferLocal) {
      if (fs.existsSync(localPath)) {
        return localPath;
      }
      if (fs.existsSync(bundledPath)) {
        return bundledPath;
      }
    } else {
      if (fs.existsSync(bundledPath)) {
        return bundledPath;
      }
      if (fs.existsSync(localPath)) {
        return localPath;
      }
    }

    throw new Error(
      `No daemon binary found for packaged app checks (looked for ${bundledPath} and ${localPath})`
    );
  }

  async readBinaryBuildInfo(binaryPath) {
    try {
      const { stdout, stderr } = await execFileAsync(binaryPath, ['--build-info-json'], {
        env: {
          ...process.env,
          ATTN_INSIDE_APP: '1',
          ATTN_DAEMON_MANAGED: '1',
          ATTN_AGENT: 'codex',
          PATH: MINIMAL_SYSTEM_PATH,
        },
        timeout: 1_500,
      });
      const parsed = JSON.parse(stdout);
      const buildInfo = normalizeBuildInfo(parsed);
      if (!buildInfo.sourceFingerprint) {
        throw new Error(
          `binary ${binaryPath} did not report a source fingerprint${readNonEmptyString(stderr) ? `: ${stderr.trim()}` : ''}`
        );
      }
      return buildInfo;
    } catch (error) {
      const stderr = readNonEmptyString(error?.stderr);
      const stdout = readNonEmptyString(error?.stdout);
      const detail = stderr || stdout || (error instanceof Error ? error.message : String(error || 'unknown error'));
      throw new Error(`failed to read build info from ${binaryPath}: ${detail}`);
    }
  }

  async ensureBuildMatchesCurrentSource(state) {
    const currentSource = await this.getCurrentSourceIdentity();
    const currentFingerprint = readNonEmptyString(currentSource?.fingerprint);
    if (!currentFingerprint) {
      throw new Error('current source fingerprint is unavailable');
    }

    const appBuild = normalizeBuildInfo(state?.appBuild);
    if (!appBuild.sourceFingerprint) {
      throw new Error(
        'packaged app did not report build identity; rebuild and reinstall attn.app before running real-app scenarios'
      );
    }
    if (appBuild.sourceFingerprint !== currentFingerprint) {
      throw new Error(
        `packaged app source mismatch: app reports ${appBuild.sourceFingerprint}, current source is ${currentFingerprint}; rebuild and reinstall attn.app`
      );
    }

    const daemonPath = this.resolveDaemonBinaryPath();
    const cacheKey = `${currentFingerprint}|${appBuild.sourceFingerprint}|${daemonPath}`;
    if (this.verifiedBuildIdentityKey === cacheKey) {
      return;
    }

    const daemonBuild = await this.readBinaryBuildInfo(daemonPath);
    if (daemonBuild.sourceFingerprint !== currentFingerprint) {
      throw new Error(
        `daemon source mismatch: ${daemonPath} reports ${daemonBuild.sourceFingerprint}, current source is ${currentFingerprint}; rebuild the resolved daemon binary before running real-app scenarios`
      );
    }

    this.verifiedBuildIdentityKey = cacheKey;
  }

  async waitForFrontendResponsive(timeoutMs = 20_000, action = 'list_sessions') {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const requestTimeoutMs = Math.min(15_000, timeoutMs);
        const state = await this.request('get_state', {}, { timeoutMs: requestTimeoutMs });
        if (
          state &&
          typeof state === 'object' &&
          'daemonReady' in state &&
          state.daemonReady === false
        ) {
          const detail = typeof state.connectionError === 'string' && state.connectionError
            ? `: ${state.connectionError}`
            : '';
          throw new Error(`daemon not ready${detail}`);
        }
        await this.ensureBuildMatchesCurrentSource(state);
        if (action === 'get_state') {
          return state;
        }
        return await this.request(action, {}, { timeoutMs: requestTimeoutMs });
      } catch (error) {
        lastError = error;
        if (isFatalFrontendResponsivenessError(error)) {
          throw error;
        }
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
