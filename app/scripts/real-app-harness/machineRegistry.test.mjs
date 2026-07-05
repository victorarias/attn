import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import {
  compareToBaseline,
  fingerprintKey,
  getMachineFingerprint,
  loadBaseline,
  saveBaseline,
} from './machineRegistry.mjs';

const baseIdentity = {
  hwModel: 'Mac15,6',
  cpuBrand: 'Apple M3 Pro',
  cpuCount: 12,
  arch: 'arm64',
  totalMemGb: 36,
  osMajor: 24,
};

describe('fingerprintKey', () => {
  it('is stable for the same identity object', () => {
    expect(fingerprintKey({ ...baseIdentity })).toBe(fingerprintKey({ ...baseIdentity }));
  });

  it('is a 12-character hex string', () => {
    expect(fingerprintKey(baseIdentity)).toMatch(/^[0-9a-f]{12}$/);
  });

  it('changes when any identity field changes', () => {
    const base = fingerprintKey(baseIdentity);

    expect(fingerprintKey({ ...baseIdentity, hwModel: 'Mac14,5' })).not.toBe(base);
    expect(fingerprintKey({ ...baseIdentity, cpuBrand: 'Apple M2' })).not.toBe(base);
    expect(fingerprintKey({ ...baseIdentity, cpuCount: 8 })).not.toBe(base);
    expect(fingerprintKey({ ...baseIdentity, arch: 'x64' })).not.toBe(base);
    expect(fingerprintKey({ ...baseIdentity, totalMemGb: 64 })).not.toBe(base);
    expect(fingerprintKey({ ...baseIdentity, osMajor: 23 })).not.toBe(base);
  });

  it('is unaffected by osRelease patch-level detail collapsed to the same osMajor', () => {
    // Two real osRelease strings (e.g. '24.1.0' and '24.5.0') both parse to
    // osMajor 24 upstream in getMachineFingerprint; here we assert the pure
    // helper treats identical osMajor as identical regardless of what patch
    // version it was derived from.
    const fromPatchOne = fingerprintKey({ ...baseIdentity, osMajor: 24 });
    const fromPatchTwo = fingerprintKey({ ...baseIdentity, osMajor: 24 });

    expect(fromPatchOne).toBe(fromPatchTwo);
  });
});

describe('compareToBaseline', () => {
  it('is always ok with reason no-baseline when there is no baseline yet', () => {
    expect(compareToBaseline(500, null)).toEqual({
      ok: true,
      value: 500,
      baseline: null,
      deltaPct: null,
      tolerancePct: 10,
      reason: 'no-baseline',
    });
  });

  it('treats undefined baseline the same as null', () => {
    expect(compareToBaseline(500, undefined).reason).toBe('no-baseline');
  });

  it('is ok within tolerance', () => {
    const result = compareToBaseline(105, 100, { tolerancePct: 10 });

    expect(result.ok).toBe(true);
    expect(result.reason).toBe('within-band');
    expect(result.deltaPct).toBe(5);
  });

  it('flags a regression above tolerance', () => {
    const result = compareToBaseline(120, 100, { tolerancePct: 10 });

    expect(result.ok).toBe(false);
    expect(result.reason).toBe('regression');
    expect(result.deltaPct).toBe(20);
  });

  it('is ok when the value improves below baseline', () => {
    const result = compareToBaseline(80, 100, { tolerancePct: 10 });

    expect(result.ok).toBe(true);
    expect(result.reason).toBe('within-band');
    expect(result.deltaPct).toBe(-20);
  });

  it('rounds deltaPct to 1 decimal place', () => {
    const result = compareToBaseline(103.333, 100, { tolerancePct: 10 });

    expect(result.deltaPct).toBe(3.3);
  });

  it('defaults tolerancePct to 10 when not provided', () => {
    expect(compareToBaseline(109, 100).ok).toBe(true);
    expect(compareToBaseline(111, 100).ok).toBe(false);
  });
});

describe('loadBaseline / saveBaseline round-trip', () => {
  let registryDir;

  beforeEach(() => {
    registryDir = fs.mkdtempSync(path.join(os.tmpdir(), 'reg-'));
  });

  afterEach(() => {
    fs.rmSync(registryDir, { recursive: true, force: true });
  });

  it('returns null for a key with no saved baseline', () => {
    expect(loadBaseline('nonexistent', { registryDir })).toBeNull();
  });

  it('saves then loads the same baseline object back', () => {
    const baseline = {
      fingerprint: { key: 'abc123def456', hwModel: 'Mac15,6' },
      metrics: { rssMb: 512 },
      recordedAt: '2026-07-05T00:00:00.000Z',
    };

    saveBaseline('abc123def456', baseline, { registryDir });

    expect(loadBaseline('abc123def456', { registryDir })).toEqual(baseline);
  });

  it('creates registryDir if it does not already exist', () => {
    const nestedDir = path.join(registryDir, 'nested', 'dir');
    const baseline = { fingerprint: {}, metrics: {}, recordedAt: '2026-07-05T00:00:00.000Z' };

    saveBaseline('key1', baseline, { registryDir: nestedDir });

    expect(loadBaseline('key1', { registryDir: nestedDir })).toEqual(baseline);
  });

  it('prefers the canonical file over the local cache when both have the key', () => {
    const canonicalPath = path.join(registryDir, 'canonical.json');
    const canonicalBaseline = { fingerprint: {}, metrics: { rssMb: 100 }, recordedAt: '2026-01-01T00:00:00.000Z' };
    const localBaseline = { fingerprint: {}, metrics: { rssMb: 999 }, recordedAt: '2026-02-01T00:00:00.000Z' };

    fs.writeFileSync(canonicalPath, `${JSON.stringify({ sharedkey123: canonicalBaseline }, null, 2)}\n`);
    saveBaseline('sharedkey123', localBaseline, { registryDir });

    expect(loadBaseline('sharedkey123', { registryDir, canonicalPath })).toEqual(canonicalBaseline);
  });

  it('falls back to the local cache when the canonical file lacks the key', () => {
    const canonicalPath = path.join(registryDir, 'canonical.json');
    const localBaseline = { fingerprint: {}, metrics: { rssMb: 999 }, recordedAt: '2026-02-01T00:00:00.000Z' };

    fs.writeFileSync(canonicalPath, `${JSON.stringify({ otherkey: {} }, null, 2)}\n`);
    saveBaseline('sharedkey123', localBaseline, { registryDir });

    expect(loadBaseline('sharedkey123', { registryDir, canonicalPath })).toEqual(localBaseline);
  });

  it('tolerates a missing canonical file entirely', () => {
    const canonicalPath = path.join(registryDir, 'does-not-exist.json');
    const localBaseline = { fingerprint: {}, metrics: { rssMb: 42 }, recordedAt: '2026-03-01T00:00:00.000Z' };

    saveBaseline('key2', localBaseline, { registryDir });

    expect(loadBaseline('key2', { registryDir, canonicalPath })).toEqual(localBaseline);
  });
});

describe('getMachineFingerprint', () => {
  it('returns an object with the expected shape', () => {
    const fingerprint = getMachineFingerprint();

    expect(fingerprint).toMatchObject({
      key: expect.stringMatching(/^[0-9a-f]{12}$/),
      arch: expect.any(String),
      platform: expect.any(String),
      osRelease: expect.any(String),
      hwModel: expect.any(String),
      cpuBrand: expect.any(String),
      cpuCount: expect.any(Number),
      totalMemGb: expect.any(Number),
    });
  });
});
