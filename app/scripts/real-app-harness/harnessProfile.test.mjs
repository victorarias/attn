import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  assertProductionRunAllowed,
  bundleIdentifierForAppPath,
  currentHarnessProfile,
  defaultAppPathForProfile,
  defaultDaemonPortForProfile,
  hasRunAgainstProdFlag,
  isProductionHarnessTarget,
  profileForAppPath,
} from './harnessProfile.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { getFrontWindowBounds } from './nativeWindowCapture.mjs';

const originalProfile = process.env.ATTN_HARNESS_PROFILE;
const originalArgv = process.argv;

afterEach(() => {
  process.argv = originalArgv;
  if (originalProfile === undefined) {
    delete process.env.ATTN_HARNESS_PROFILE;
  } else {
    process.env.ATTN_HARNESS_PROFILE = originalProfile;
  }
});

describe('real-app harness production safety', () => {
  it('defaults to the isolated dev app and daemon', () => {
    delete process.env.ATTN_HARNESS_PROFILE;

    expect(currentHarnessProfile()).toBe('dev');
    expect(defaultAppPathForProfile()).toBe(path.join(os.homedir(), 'Applications', 'attn-dev.app'));
    expect(defaultDaemonPortForProfile()).toBe(29849);
  });

  it('detects production from profile, app path, bundle id, or websocket', () => {
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

  it('derives the matching profile and bundle from explicit packaged app paths', () => {
    const prodAppPath = path.join(os.homedir(), 'Applications', 'attn.app');
    const devAppPath = path.join(os.homedir(), 'Applications', 'attn-dev.app');

    expect(profileForAppPath(prodAppPath)).toBe('');
    expect(bundleIdentifierForAppPath(prodAppPath)).toBe('com.attn.manager');
    expect(profileForAppPath(devAppPath, '')).toBe('dev');
    expect(bundleIdentifierForAppPath(devAppPath, '')).toBe('com.attn.manager.dev');
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
