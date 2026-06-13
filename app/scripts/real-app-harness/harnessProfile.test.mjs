import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  bundleIdentifierForProfile,
  currentHarnessProfile,
  daemonPidFilePathForProfile,
  dataDirForProfile,
  defaultAppPathForProfile,
  defaultDaemonPortForProfile,
  defaultWSURLForProfile,
  deepLinkSchemeForProfile,
  hasRunAgainstProdFlag,
  isProductionHarnessTarget,
  profileForAppPath,
  resolveHarnessResources,
  socketPathForProfile,
} from './harnessProfile.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { getFrontWindowBounds } from './nativeWindowCapture.mjs';

const TEST_DIR = path.dirname(fileURLToPath(import.meta.url));

// The drift guard and arbitrary-profile resolution need the real attn binary.
// Locate it the same way harnessProfile.mjs does; skip those tests if absent so
// the unit suite never depends on a built binary.
function attnBinary() {
  const candidates = [process.env.ATTN_HARNESS_BIN, path.resolve(TEST_DIR, '../../../attn')]
    .filter(Boolean);
  return candidates.find((candidate) => fs.existsSync(candidate)) ?? null;
}
const ATTN_BIN = attnBinary();
const describeWithBinary = ATTN_BIN ? describe : describe.skip;

const originalHarnessProfile = process.env.ATTN_HARNESS_PROFILE;
const originalProfile = process.env.ATTN_PROFILE;
const originalArgv = process.argv;

// Start every test from a clean slate so the one-knob precedence is exercised
// deterministically regardless of the surrounding shell's ATTN_PROFILE.
beforeEach(() => {
  delete process.env.ATTN_HARNESS_PROFILE;
  delete process.env.ATTN_PROFILE;
});

afterEach(() => {
  process.argv = originalArgv;
  for (const [name, value] of [
    ['ATTN_HARNESS_PROFILE', originalHarnessProfile],
    ['ATTN_PROFILE', originalProfile],
  ]) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
});

describe('currentHarnessProfile (one-knob precedence)', () => {
  it('defaults to the safe dev sibling when neither knob is set', () => {
    expect(currentHarnessProfile()).toBe('dev');
    expect(defaultAppPathForProfile()).toBe(path.join(os.homedir(), 'Applications', 'attn-dev.app'));
    expect(defaultDaemonPortForProfile()).toBe(29849);
  });

  it('follows ATTN_PROFILE when no harness override is set', () => {
    process.env.ATTN_PROFILE = 'agent7';
    expect(currentHarnessProfile()).toBe('agent7');
  });

  it('never targets production by omission (empty/default ATTN_PROFILE ⇒ dev)', () => {
    process.env.ATTN_PROFILE = '';
    expect(currentHarnessProfile()).toBe('dev');
    process.env.ATTN_PROFILE = 'default';
    expect(currentHarnessProfile()).toBe('dev');
  });

  it('lets ATTN_HARNESS_PROFILE override ATTN_PROFILE', () => {
    process.env.ATTN_PROFILE = 'agent7';
    process.env.ATTN_HARNESS_PROFILE = 'agent9';
    expect(currentHarnessProfile()).toBe('agent9');
  });

  it('treats an explicit empty/default ATTN_HARNESS_PROFILE as the prod escape hatch', () => {
    process.env.ATTN_PROFILE = 'agent7';
    process.env.ATTN_HARNESS_PROFILE = '';
    expect(currentHarnessProfile()).toBe('');
    process.env.ATTN_HARNESS_PROFILE = 'default';
    expect(currentHarnessProfile()).toBe('');
  });

  it('normalizes case and whitespace', () => {
    process.env.ATTN_HARNESS_PROFILE = '  DEV  ';
    expect(currentHarnessProfile()).toBe('dev');
  });
});

describe('real-app harness production safety', () => {
  it('detects production from the empty profile, app path, bundle id, or websocket', () => {
    expect(isProductionHarnessTarget({ profile: '' })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    })).toBe(true);
    expect(isProductionHarnessTarget({ profile: 'dev', bundleId: 'com.attn.manager' })).toBe(true);
    expect(isProductionHarnessTarget({ profile: 'dev', wsUrl: 'ws://127.0.0.1:9849/ws' })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'dev',
      appPath: path.join(os.homedir(), 'Applications', 'attn-dev.app'),
      bundleId: 'com.attn.manager.dev',
    })).toBe(false);
  });

  it('treats a named profile as an isolated world, not production', () => {
    // The pre-PR3 guard flagged any non-dev profile as prod; a named profile
    // must NOT require --run-against-prod.
    expect(isProductionHarnessTarget({ profile: 'agent7' })).toBe(false);
    expect(() => assertProductionRunAllowed({ profile: 'agent7' }, [])).not.toThrow();
    // ...but an explicit prod app path / bundle / port still trips the guard.
    expect(isProductionHarnessTarget({ profile: 'agent7', bundleId: 'com.attn.manager' })).toBe(true);
    expect(isProductionHarnessTarget({
      profile: 'agent7',
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    })).toBe(true);
  });

  it('detects the prod app path case-insensitively (macOS filesystems)', () => {
    // APFS/HFS+ are case-insensitive: `Attn.app` launches the real prod app, so
    // the guard and the path→profile mapping must treat it as prod too.
    for (const name of ['Attn.app', 'ATTN.APP', 'attn.App']) {
      const appPath = path.join(os.homedir(), 'Applications', name);
      expect(isProductionHarnessTarget({ profile: 'dev', appPath })).toBe(true);
      expect(profileForAppPath(appPath, 'dev')).toBe('');
    }
  });

  it('derives the matching profile and bundle from explicit packaged app paths', () => {
    const prodAppPath = path.join(os.homedir(), 'Applications', 'attn.app');
    const devAppPath = path.join(os.homedir(), 'Applications', 'attn-dev.app');
    const namedAppPath = path.join(os.homedir(), 'Applications', 'attn-agent7.app');

    expect(profileForAppPath(prodAppPath)).toBe('');
    expect(bundleIdentifierForAppPath(prodAppPath)).toBe('com.attn.manager');
    expect(profileForAppPath(devAppPath, '')).toBe('dev');
    expect(bundleIdentifierForAppPath(devAppPath, '')).toBe('com.attn.manager.dev');
    expect(profileForAppPath(namedAppPath, '')).toBe('agent7');
  });

  it('requires the explicit production acknowledgement flag', () => {
    expect(() => assertProductionRunAllowed({ profile: '' }, [])).toThrow(
      'Refusing to run the real-app harness against production',
    );
    expect(() => assertProductionRunAllowed({ profile: '' }, ['--run-against-prod'])).not.toThrow();
    expect(hasRunAgainstProdFlag(['--run-against-prod'])).toBe(true);
  });

  it('protects low-level macOS lifecycle operations', () => {
    expect(() => new MacOSDriver({
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
      bundleId: 'com.attn.manager',
    })).toThrow('Refusing to run the real-app harness against production');
  });

  it('targets the production bundle for an acknowledged production app path', () => {
    process.argv = [...process.argv, '--run-against-prod'];

    const driver = new MacOSDriver({
      appPath: path.join(os.homedir(), 'Applications', 'attn.app'),
    });

    expect(driver.bundleId).toBe('com.attn.manager');
  });

  it('protects low-level daemon and native-window operations', async () => {
    expect(() => new DaemonObserver({ wsUrl: 'ws://127.0.0.1:9849/ws' })).toThrow(
      'Refusing to run the real-app harness against production',
    );
    await expect(getFrontWindowBounds('com.attn.manager')).rejects.toThrow(
      'Refusing to run the real-app harness against production',
    );
  });
});

describe('daemon pid file resolution', () => {
  it('maps the dev profile to ~/.attn-dev/attn.pid and prod to ~/.attn/attn.pid', () => {
    expect(daemonPidFilePathForProfile('dev')).toBe(path.join(os.homedir(), '.attn-dev', 'attn.pid'));
    // profileForAppPath() returns '' for the prod app; that resolves to ~/.attn.
    expect(daemonPidFilePathForProfile('')).toBe(path.join(os.homedir(), '.attn', 'attn.pid'));
  });
});

// Single-authority guarantees. These require the built attn binary, so they are
// skipped (not failed) when ./attn is absent — the Go-side
// TestProfileDerivation_DefaultAndDev pins the authority's output in Go CI, and
// this guard pins the JS fast-path literals against it during local dev.
describeWithBinary('single authority (attn profile resolve)', () => {
  function resolve(profile) {
    const stdout = execFileSync(ATTN_BIN, ['profile', 'resolve', '--profile', profile, '--json'], {
      encoding: 'utf8',
    });
    return JSON.parse(stdout);
  }

  it('keeps every dev/prod fast-path literal in sync with the authority', () => {
    for (const profile of ['', 'dev']) {
      const r = resolve(profile);
      const resources = resolveHarnessResources(profile);
      expect(resources.bundleId).toBe(r.bundleId);
      expect(resources.appName).toBe(r.appName);
      expect(resources.appPath).toBe(r.appPath);
      expect(resources.wsPort).toBe(Number(r.wsPort));
      expect(resources.socket).toBe(r.socket);
      expect(resources.dataDir).toBe(r.dataDir);
      expect(resources.deepLinkScheme).toBe(r.deepLinkScheme);
      // ...and the public accessors derive from those literals.
      expect(bundleIdentifierForProfile(profile)).toBe(r.bundleId);
      expect(defaultAppPathForProfile(profile)).toBe(r.appPath);
      expect(defaultDaemonPortForProfile(profile)).toBe(Number(r.wsPort));
      expect(defaultWSURLForProfile(profile)).toBe(`ws://127.0.0.1:${r.wsPort}/ws`);
    }
  });

  it('round-trips a named profile: appPath ⇒ profile via the authority naming', () => {
    // Pins the one hand-maintained inverse mapping (profileForAppPath) against
    // the authority's appPath, so a future change to the Go app-name scheme
    // breaks this test instead of silently mis-routing.
    expect(profileForAppPath(defaultAppPathForProfile('agent7'))).toBe('agent7');
    expect(profileForAppPath(defaultAppPathForProfile('dev'))).toBe('dev');
    expect(profileForAppPath(defaultAppPathForProfile(''))).toBe('');
  });

  it('resolves an arbitrary named profile from the authority', () => {
    const r = resolve('agent7');
    expect(bundleIdentifierForProfile('agent7')).toBe('com.attn.manager.agent7');
    expect(defaultAppPathForProfile('agent7')).toBe(path.join(os.homedir(), 'Applications', 'attn-agent7.app'));
    expect(deepLinkSchemeForProfile('agent7')).toBe('attn-agent7');
    expect(dataDirForProfile('agent7')).toBe(path.join(os.homedir(), '.attn-agent7'));
    expect(defaultDaemonPortForProfile('agent7')).toBe(Number(r.wsPort));
    // A named profile's real daemon port never collides with prod/dev.
    expect(defaultDaemonPortForProfile('agent7')).not.toBe(9849);
    expect(defaultDaemonPortForProfile('agent7')).not.toBe(29849);
  });
});
