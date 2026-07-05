import { describe, expect, it } from 'vitest';
import { buildBaselineVerdict, evaluateRssBaseline } from './rssBaselineVerdict.mjs';

const fingerprint = {
  key: 'abc123def456',
  arch: 'arm64',
  platform: 'darwin',
  osRelease: '24.5.0',
  hwModel: 'Mac15,6',
  cpuBrand: 'Apple M3 Pro',
  cpuCount: 12,
  totalMemGb: 36,
};

describe('evaluateRssBaseline', () => {
  it('is ok with a fresh baseline to save when there is no existing baseline', () => {
    const result = evaluateRssBaseline({
      totalRssMb: 500,
      fingerprint,
      baseline: null,
      recordedAt: '2026-07-05T00:00:00.000Z',
    });

    expect(result.ok).toBe(true);
    expect(result.comparison.reason).toBe('no-baseline');
    expect(result.baselineToSave).toEqual({
      fingerprint,
      metrics: { totalRssMb: 500 },
      recordedAt: '2026-07-05T00:00:00.000Z',
    });
  });

  it('is ok with nothing to save when within tolerance of an existing baseline', () => {
    const baseline = { fingerprint, metrics: { totalRssMb: 500 }, recordedAt: '2026-07-01T00:00:00.000Z' };

    const result = evaluateRssBaseline({
      totalRssMb: 520,
      fingerprint,
      baseline,
      tolerancePct: 15,
      recordedAt: '2026-07-05T00:00:00.000Z',
    });

    expect(result.ok).toBe(true);
    expect(result.comparison.reason).toBe('within-band');
    expect(result.baselineToSave).toBeNull();
  });

  it('fails with reason regression when beyond tolerance of an existing baseline', () => {
    const baseline = { fingerprint, metrics: { totalRssMb: 500 }, recordedAt: '2026-07-01T00:00:00.000Z' };

    const result = evaluateRssBaseline({
      totalRssMb: 700,
      fingerprint,
      baseline,
      tolerancePct: 15,
      recordedAt: '2026-07-05T00:00:00.000Z',
    });

    expect(result.ok).toBe(false);
    expect(result.comparison.reason).toBe('regression');
    expect(result.baselineToSave).toBeNull();
  });

  it('forces a re-record when record is true even with an existing baseline, and passes by construction', () => {
    // This is the figgyster-reproduced bug: an explicit re-record must never
    // be evaluated against the baseline it is about to replace, even when the
    // new value would have been a regression against the old one.
    const baseline = { fingerprint, metrics: { totalRssMb: 500 }, recordedAt: '2026-07-01T00:00:00.000Z' };

    const result = evaluateRssBaseline({
      totalRssMb: 700,
      fingerprint,
      baseline,
      record: true,
      recordedAt: '2026-07-05T00:00:00.000Z',
    });

    expect(result.baselineToSave).toEqual({
      fingerprint,
      metrics: { totalRssMb: 700 },
      recordedAt: '2026-07-05T00:00:00.000Z',
    });
    expect(result.ok).toBe(true);
    expect(result.comparison.reason).toBe('recorded');
    expect(result.comparison.baseline).toBeNull();
  });

  it('passes recordedAt through into baselineToSave unchanged', () => {
    const result = evaluateRssBaseline({
      totalRssMb: 500,
      fingerprint,
      baseline: null,
      recordedAt: '2099-01-01T12:34:56.000Z',
    });

    expect(result.baselineToSave.recordedAt).toBe('2099-01-01T12:34:56.000Z');
  });
});

describe('buildBaselineVerdict', () => {
  it('builds a passing verdict with firstFailure null and the rss/metrics extensions attached', () => {
    const comparison = { ok: true, value: 500, baseline: null, deltaPct: null, tolerancePct: 15, reason: 'no-baseline' };

    const verdict = buildBaselineVerdict({
      ok: true,
      comparison,
      scenarioId: 'perf-baseline',
      runId: 'perf-baseline-2026-07-05T00-00-00-000Z',
      artifactsDir: '/tmp/attn-real-app-harness/perf-baseline-run',
      summaryPath: '/tmp/attn-real-app-harness/perf-baseline-run/summary.json',
      durationMs: 4321,
    });

    expect(verdict).toEqual({
      ok: true,
      scenarioId: 'perf-baseline',
      runId: 'perf-baseline-2026-07-05T00-00-00-000Z',
      failureCount: 0,
      firstFailure: null,
      artifactsDir: '/tmp/attn-real-app-harness/perf-baseline-run',
      summaryPath: '/tmp/attn-real-app-harness/perf-baseline-run/summary.json',
      durationMs: 4321,
      rss: comparison,
      metrics: { totalRssMb: 500 },
    });
  });

  it('builds a failing verdict with a one-line regression message and failureCount 1', () => {
    const comparison = { ok: false, value: 700, baseline: 500, deltaPct: 40, tolerancePct: 15, reason: 'regression' };

    const verdict = buildBaselineVerdict({
      ok: false,
      comparison,
      scenarioId: 'perf-baseline',
      runId: 'perf-baseline-2026-07-05T00-00-00-000Z',
      artifactsDir: '/tmp/attn-real-app-harness/perf-baseline-run',
      summaryPath: '/tmp/attn-real-app-harness/perf-baseline-run/summary.json',
      durationMs: 4321,
    });

    expect(verdict.failureCount).toBe(1);
    expect(verdict.firstFailure).toBe('RSS regression: 700MB vs baseline 500MB (+40%, tolerance 15%)');
    expect(verdict.firstFailure.split('\n')).toHaveLength(1);
  });

  it('merges extraMetrics into metrics alongside totalRssMb', () => {
    const comparison = { ok: true, value: 500, baseline: 480, deltaPct: 4.2, tolerancePct: 15, reason: 'within-band' };

    const verdict = buildBaselineVerdict({
      ok: true,
      comparison,
      scenarioId: 'perf-baseline',
      runId: 'run-1',
      artifactsDir: '/tmp/run',
      summaryPath: '/tmp/run/summary.json',
      durationMs: 100,
      extraMetrics: { retainedMb: 12.3 },
    });

    expect(verdict.metrics).toEqual({ totalRssMb: 500, retainedMb: 12.3 });
  });
});
