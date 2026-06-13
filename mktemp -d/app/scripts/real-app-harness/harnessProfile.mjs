import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));

const DEV_PROFILE = 'dev';
const PROD_BUNDLE_ID = 'com.attn.manager';
const PROD_APP_NAME = 'attn.app';
const PROD_DAEMON_PORT = '9849';

// Profile name grammar — mirrors config.profileNamePattern on the Go side.
const PROFILE_NAME = /^[a-z0-9][a-z0-9-]{0,15}$/;

/**
 * Harness profile resolution.
 *
 * The real-app harness honors the *one knob*, `ATTN_PROFILE`, like every other
 * entrypoint: set it once in a shell and the harness drives that profile's app
 * and daemon. `ATTN_HARNESS_PROFILE` is an explicit override for the rare case
 * where the harness must target a *different* world than the surrounding shell.
 *
 * Resource derivation has a single authority: `attn profile resolve`. The two
 * profiles the harness has always supported — the default/prod install and the
 * `dev` sibling — are kept here as fast-path literals so the common path (and
 * the unit tests) need no built binary; `harnessProfile.test.mjs` asserts those
 * literals equal `attn profile resolve` whenever `./attn` is built, so they can
 * never drift from the Go authority. Every *other* profile is resolved live.
 *
 * @see docs/profiles.md
 */

// Fast-path resources for prod ('') and dev. Verified against the authority by
// the drift guard in harnessProfile.test.mjs.
const BUILTIN_RESOURCES = {
  '': {
    profile: '',
    bundleId: PROD_BUNDLE_ID,
    appName: 'attn',
    appPath: path.join(os.homedir(), 'Applications', PROD_APP_NAME),
    wsPort: 9849,
    socket: path.join(os.homedir(), '.attn', 'attn.sock'),
    dataDir: path.join(os.homedir(), '.attn'),
    deepLinkScheme: 'attn',
  },
  dev: {
    profile: 'dev',
    bundleId: 'com.attn.manager.dev',
    appName: 'attn-dev',
    appPath: path.join(os.homedir(), 'Applications', 'attn-dev.app'),
    wsPort: 29849,
    socket: path.join(os.homedir(), '.attn-dev', 'attn.sock'),
    dataDir: path.join(os.homedir(), '.attn-dev'),
    deepLinkScheme: 'attn-dev',
  },
};

// Normalize a raw profile string the way the Go side does: trim, lower-case,
// and collapse the alias 'default' to the empty (production) profile.
function normalizeProfile(raw) {
  const value = (raw ?? '').trim().toLowerCase();
  return value === 'default' ? '' : value;
}

/**
 * The profile the harness targets, following the one-knob model:
 *
 *   1. `ATTN_HARNESS_PROFILE` set ⇒ explicit override (wins over the shell).
 *      Empty string (or 'default') is the documented production escape hatch —
 *      still gated by `--run-against-prod` downstream.
 *   2. Otherwise follow `ATTN_PROFILE`, but never target production by
 *      omission: an unset/empty/default `ATTN_PROFILE` means the surrounding
 *      shell is in the prod world, so the harness falls back to the safe `dev`
 *      sibling rather than touching the real install.
 */
export function currentHarnessProfile() {
  const override = process.env.ATTN_HARNESS_PROFILE;
  if (override !== undefined) {
    return normalizeProfile(override);
  }
  const base = normalizeProfile(process.env.ATTN_PROFILE);
  return base === '' ? DEV_PROFILE : base;
}

const resourceCache = new Map();

// Resolve the attn binary used for profile derivation. ATTN_HARNESS_BIN wins;
// otherwise the repo-root ./attn built by `make dev` / `go build -o ./attn`.
function resolveAttnBinaryPath() {
  const candidates = [
    process.env.ATTN_HARNESS_BIN,
    path.resolve(HARNESS_DIR, '../../../attn'),
  ].filter(Boolean);
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  throw new Error(
    `attn binary not found for profile resolution. Tried: ${candidates.join(', ')}. `
    + `Build it with 'make dev' (or 'go build -o ./attn ./cmd/attn'), or set ATTN_HARNESS_BIN.`,
  );
}

function resolveViaAuthority(profile) {
  const attn = resolveAttnBinaryPath();
  let stdout;
  try {
    stdout = execFileSync(attn, ['profile', 'resolve', '--profile', profile, '--json'], {
      encoding: 'utf8',
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`Failed to resolve profile '${profile}' via '${attn} profile resolve': ${message}`);
  }
  const resolved = JSON.parse(stdout);
  return {
    profile: resolved.profile,
    bundleId: resolved.bundleId,
    appName: resolved.appName,
    appPath: resolved.appPath,
    wsPort: Number(resolved.wsPort),
    socket: resolved.socket,
    dataDir: resolved.dataDir,
    deepLinkScheme: resolved.deepLinkScheme,
  };
}

/**
 * The full resource bundle for a profile, from the single authority. dev/prod
 * use the fast-path literals; every other profile resolves from
 * `attn profile resolve` and is memoized by name (resolution is deterministic).
 */
export function resolveHarnessResources(profile = currentHarnessProfile()) {
  const key = normalizeProfile(profile);
  if (Object.prototype.hasOwnProperty.call(BUILTIN_RESOURCES, key)) {
    return BUILTIN_RESOURCES[key];
  }
  if (!PROFILE_NAME.test(key)) {
    throw new Error(`Invalid attn profile name '${profile}' (expected ${PROFILE_NAME}).`);
  }
  if (!resourceCache.has(key)) {
    resourceCache.set(key, resolveViaAuthority(key));
  }
  return resourceCache.get(key);
}

export function bundleIdentifierForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).bundleId;
}

// Map a packaged-app path back to its profile: `attn.app` ⇒ '' (prod),
// `attn-<name>.app` ⇒ '<name>'. Anything else falls back to the active profile.
export function profileForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  const appName = path.basename(appPath || '');
  const match = /^attn(?:-([a-z0-9][a-z0-9-]{0,15}))?\.app$/.exec(appName);
  if (match) return match[1] ?? '';
  return fallbackProfile;
}

export function bundleIdentifierForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  return bundleIdentifierForProfile(profileForAppPath(appPath, fallbackProfile));
}

export function defaultAppPathForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).appPath;
}

export function defaultDaemonPortForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).wsPort;
}

// Data dir for the active profile (~/.attn for prod, ~/.attn-<name> otherwise).
export function dataDirForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).dataDir;
}

// Unix socket the `attn` CLI talks to for the active profile. Pass this as
// ATTN_SOCKET_PATH when driving the CLI (e.g. `attn open`) against the daemon.
export function socketPathForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).socket;
}

// Pid file the daemon writes on startup for the active profile (same state dir
// as the socket). Lets tools resolve the authoritative daemon pid -- and from it
// the pty-worker children -- without the pprof diagnostics endpoint.
export function daemonPidFilePathForProfile(profile = currentHarnessProfile()) {
  return path.join(resolveHarnessResources(profile).dataDir, 'attn.pid');
}

export function defaultWSURLForProfile(profile = currentHarnessProfile()) {
  return `ws://127.0.0.1:${resolveHarnessResources(profile).wsPort}/ws`;
}

export function manifestPathForProfile(profile = currentHarnessProfile()) {
  return path.join(
    os.homedir(),
    'Library',
    'Application Support',
    resolveHarnessResources(profile).bundleId,
    'debug',
    'ui-automation.json',
  );
}

// Deep-link scheme registered for the profile's bundle (prod: `attn`,
// dev: `attn-dev`, named: `attn-<name>`). From config.DeepLinkSchemeForProfile.
export function deepLinkSchemeForProfile(profile = currentHarnessProfile()) {
  return resolveHarnessResources(profile).deepLinkScheme;
}

export function hasRunAgainstProdFlag(argv = process.argv.slice(2)) {
  return argv.includes('--run-against-prod');
}

export function isProductionHarnessTarget({
  appPath,
  bundleId,
  wsUrl,
  profile = currentHarnessProfile(),
} = {}) {
  let wsPort = '';
  try {
    wsPort = new URL(wsUrl).port;
  } catch {
    // Invalid URLs are handled by their consumer; they are not evidence of prod.
  }
  // Production is the default/empty profile. A *named* profile (dev, agent7, …)
  // is an isolated world and is never production. We still flag an explicit
  // prod app path / bundle id / port as prod (defense in depth — e.g. a named
  // profile pointed at `--app-path ~/Applications/attn.app`).
  return (
    profile === ''
    || path.basename(appPath || '') === PROD_APP_NAME
    || bundleId === PROD_BUNDLE_ID
    || wsPort === PROD_DAEMON_PORT
  );
}

export function assertProductionRunAllowed(target = {}, argv = process.argv.slice(2)) {
  if (!isProductionHarnessTarget(target) || hasRunAgainstProdFlag(argv)) {
    return;
  }
  throw new Error(
    'Refusing to run the real-app harness against production. '
    + 'Use the dev install (default; run `make dev` first), or pass '
    + '`--run-against-prod` explicitly to allow production app or daemon operations.',
  );
}
