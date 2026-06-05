import os from 'node:os';
import path from 'node:path';

const DEV_PROFILE = 'dev';
const PROD_BUNDLE_ID = 'com.attn.manager';
const PROD_APP_NAME = 'attn.app';
const PROD_DAEMON_PORT = '9849';

/**
 * Harness profile resolution. A single env var, ATTN_HARNESS_PROFILE,
 * switches the whole real-app harness between the prod install
 * (~/Applications/attn.app) and the dev sibling install
 * (~/Applications/attn-dev.app).
 *
 * Keep bundle identifiers + default port in sync with:
 *   - app/src-tauri/tauri.conf.json         (prod)
 *   - app/src-tauri/tauri.dev.conf.json     (dev)
 *   - app/src-tauri/src/profile.rs          (Rust side)
 *   - internal/config/config.go             (Go side)
 */

export function currentHarnessProfile() {
  return (process.env.ATTN_HARNESS_PROFILE ?? DEV_PROFILE).trim().toLowerCase();
}

export function bundleIdentifierForProfile(profile = currentHarnessProfile()) {
  return profile === DEV_PROFILE ? 'com.attn.manager.dev' : PROD_BUNDLE_ID;
}

export function profileForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  const appName = path.basename(appPath || '');
  if (appName === PROD_APP_NAME) return '';
  if (appName === 'attn-dev.app') return DEV_PROFILE;
  return fallbackProfile;
}

export function bundleIdentifierForAppPath(appPath, fallbackProfile = currentHarnessProfile()) {
  return bundleIdentifierForProfile(profileForAppPath(appPath, fallbackProfile));
}

export function defaultAppPathForProfile(profile = currentHarnessProfile()) {
  const name = profile === DEV_PROFILE ? 'attn-dev.app' : PROD_APP_NAME;
  return path.join(os.homedir(), 'Applications', name);
}

export function defaultDaemonPortForProfile(profile = currentHarnessProfile()) {
  return profile === DEV_PROFILE ? 29849 : 9849;
}

// Unix socket the `attn` CLI talks to for the active profile. Dev keeps its
// state under ~/.attn-dev/; prod under ~/.attn/. Pass this as ATTN_SOCKET_PATH
// when driving the CLI (e.g. `attn open`) against the harness daemon.
export function socketPathForProfile(profile = currentHarnessProfile()) {
  const dir = profile === DEV_PROFILE ? '.attn-dev' : '.attn';
  return path.join(os.homedir(), dir, 'attn.sock');
}

export function defaultWSURLForProfile(profile = currentHarnessProfile()) {
  return `ws://127.0.0.1:${defaultDaemonPortForProfile(profile)}/ws`;
}

export function manifestPathForProfile(profile = currentHarnessProfile()) {
  return path.join(
    os.homedir(),
    'Library',
    'Application Support',
    bundleIdentifierForProfile(profile),
    'debug',
    'ui-automation.json',
  );
}

// Match the deep-link scheme registered for each bundle:
//   - prod: tauri.conf.json declares `attn`
//   - dev:  tauri.dev.conf.json declares `attn-dev`
// Must stay in sync with config.DeepLinkScheme() on the Go side.
export function deepLinkSchemeForProfile(profile = currentHarnessProfile()) {
  return profile === DEV_PROFILE ? 'attn-dev' : 'attn';
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
  return (
    profile !== DEV_PROFILE
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
