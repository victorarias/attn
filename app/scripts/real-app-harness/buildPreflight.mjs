import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');
const SOURCE_FINGERPRINT_SCRIPT = path.join(REPO_ROOT, 'scripts', 'source-fingerprint.sh');
const MINIMAL_SYSTEM_PATH = '/usr/bin:/bin:/usr/sbin:/sbin';

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

function bundledDaemonPathForApp(appPath) {
  if (appPath.endsWith('.app')) {
    return path.join(appPath, 'Contents', 'MacOS', 'attn');
  }
  return path.join(path.dirname(appPath), 'attn');
}

function packagedAppBuildIdentityPath(appPath) {
  if (appPath.endsWith('.app')) {
    return path.join(appPath, 'Contents', 'Resources', 'build-identity.json');
  }
  return path.join(path.dirname(appPath), 'build-identity.json');
}

function getCurrentSourceIdentitySync() {
  const stdout = execFileSync('bash', [SOURCE_FINGERPRINT_SCRIPT, '--json'], {
    cwd: REPO_ROOT,
    encoding: 'utf8',
  });
  return JSON.parse(stdout);
}

function readPackagedAppBuildInfoSync(appPath) {
  const identityPath = packagedAppBuildIdentityPath(appPath);
  let raw;
  try {
    raw = JSON.parse(fs.readFileSync(identityPath, 'utf8'));
  } catch (error) {
    const detail = error instanceof Error ? error.message : String(error || 'unknown error');
    throw new Error(
      `packaged app did not report build identity; rebuild and reinstall attn.app before running real-app scenarios (${identityPath}: ${detail})`
    );
  }
  const buildInfo = normalizeBuildInfo(raw);
  if (!buildInfo.sourceFingerprint) {
    throw new Error(
      `packaged app did not report build identity; rebuild and reinstall attn.app before running real-app scenarios (${identityPath})`
    );
  }
  return buildInfo;
}

function resolveDaemonBinaryPathSync({ appPath, launchEnv = null }) {
  const overridePath = launchEnv && typeof launchEnv === 'object'
    ? launchEnv.ATTN_DAEMON_BINARY
    : undefined;
  const bundledPath = bundledDaemonPathForApp(appPath);
  const explicitPath = typeof overridePath === 'string' && overridePath.trim() !== ''
    ? overridePath.trim()
    : (typeof process.env.ATTN_DAEMON_BINARY === 'string' && process.env.ATTN_DAEMON_BINARY.trim() !== ''
      ? process.env.ATTN_DAEMON_BINARY.trim()
      : '');

  if (explicitPath) {
    if (fs.existsSync(explicitPath)) {
      return explicitPath;
    }
    throw new Error(`Explicit daemon binary override not found: ${explicitPath}`);
  }
  if (fs.existsSync(bundledPath)) {
    return bundledPath;
  }
  throw new Error(
    `No bundled daemon binary found for packaged app checks (looked for ${bundledPath})`
  );
}

function readBinaryBuildInfoSync(binaryPath) {
  try {
    const stdout = execFileSync(binaryPath, ['--build-info-json'], {
      encoding: 'utf8',
      timeout: 1_500,
      env: {
        ...process.env,
        ATTN_INSIDE_APP: '1',
        ATTN_DAEMON_MANAGED: '1',
        ATTN_AGENT: 'codex',
        PATH: MINIMAL_SYSTEM_PATH,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const buildInfo = normalizeBuildInfo(JSON.parse(stdout));
    if (!buildInfo.sourceFingerprint) {
      throw new Error(`binary ${binaryPath} did not report a source fingerprint`);
    }
    return buildInfo;
  } catch (error) {
    const detail = readNonEmptyString(error?.stderr?.toString?.())
      || readNonEmptyString(error?.stdout?.toString?.())
      || (error instanceof Error ? error.message : String(error || 'unknown error'));
    throw new Error(`failed to read build info from ${binaryPath}: ${detail}`);
  }
}

export function assertPackagedAppBuildMatchesCurrentSource({
  appPath = path.join(os.homedir(), 'Applications', 'attn.app'),
  launchEnv = null,
} = {}) {
  const currentSource = getCurrentSourceIdentitySync();
  const currentFingerprint = readNonEmptyString(currentSource?.fingerprint);
  if (!currentFingerprint) {
    throw new Error('current source fingerprint is unavailable');
  }

  const appBuild = readPackagedAppBuildInfoSync(appPath);
  if (appBuild.sourceFingerprint !== currentFingerprint) {
    throw new Error(
      `packaged app source mismatch: app reports ${appBuild.sourceFingerprint}, current source is ${currentFingerprint}; rebuild and reinstall attn.app`
    );
  }

  const daemonPath = resolveDaemonBinaryPathSync({ appPath, launchEnv });
  const daemonBuild = readBinaryBuildInfoSync(daemonPath);
  if (daemonBuild.sourceFingerprint !== currentFingerprint) {
    throw new Error(
      `daemon source mismatch: ${daemonPath} reports ${daemonBuild.sourceFingerprint}, current source is ${currentFingerprint}; rebuild the resolved daemon binary before running real-app scenarios`
    );
  }

  return {
    currentSource,
    appBuild,
    daemonPath,
    daemonBuild,
  };
}
