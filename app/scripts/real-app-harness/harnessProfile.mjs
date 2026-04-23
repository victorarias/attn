import os from 'node:os';
import path from 'node:path';

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
  return (process.env.ATTN_HARNESS_PROFILE || '').trim().toLowerCase();
}

export function bundleIdentifierForProfile(profile = currentHarnessProfile()) {
  return profile === 'dev' ? 'com.attn.manager.dev' : 'com.attn.manager';
}

export function defaultAppPathForProfile(profile = currentHarnessProfile()) {
  const name = profile === 'dev' ? 'attn-dev.app' : 'attn.app';
  return path.join(os.homedir(), 'Applications', name);
}

export function defaultDaemonPortForProfile(profile = currentHarnessProfile()) {
  return profile === 'dev' ? 29849 : 9849;
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
  return profile === 'dev' ? 'attn-dev' : 'attn';
}
