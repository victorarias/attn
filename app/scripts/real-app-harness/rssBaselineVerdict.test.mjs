import { describe, expect, it } from 'vitest';
import { buildBaselineVerdict, buildColdWarmVerdict, buildLeakSoakVerdict, evaluateRssBaseline, fitSlope } from './rssBaselineVerdict.mjs';

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

describe('buildColdWarmVerdict', () => {
  const okComparison = (value) => ({ ok: true, value, baseline: value - 10, deltaPct: 1.4, tolerancePct: 15, reason: 'within-band' });
  const regressingComparison = (value) => ({ ok: false, value, baseline: 700, deltaPct: 28.6, tolerancePct: 15, reason: 'regression' });

  it('builds a passing verdict when both phases are within band', () => {
    const cold = okComparison(720);
    const warm = okComparison(910);

    const verdict = buildColdWarmVerdict({
      cold,
      warm,
      scenarioId: 'perf-cold-warm',
      runId: 'perf-cold-warm-2026-07-05T00-00-00-000Z',
      artifactsDir: '/tmp/attn-real-app-harness/perf-cold-warm-run',
      summaryPath: '/tmp/attn-real-app-harness/perf-cold-warm-run/summary.json',
      durationMs: 4321,
    });

    expect(verdict.ok).toBe(true);
    expect(verdict.failureCount).toBe(0);
    expect(verdict.firstFailure).toBeNull();
    expect(verdict.metrics).toEqual({ coldRssMb: 720, warmRssMb: 910 });
    expect(verdict.rss).toEqual({ cold, warm });
    expect(verdict.scenarioId).toBe('perf-cold-warm');
    expect(verdict.runId).toBe('perf-cold-warm-2026-07-05T00-00-00-000Z');
    expect(verdict.artifactsDir).toBe('/tmp/attn-real-app-harness/perf-cold-warm-run');
    expect(verdict.summaryPath).toBe('/tmp/attn-real-app-harness/perf-cold-warm-run/summary.json');
    expect(verdict.durationMs).toBe(4321);
  });

  it('fails with the cold regression line when only cold regresses', () => {
    const cold = regressingComparison(900);
    const warm = okComparison(910);

    const verdict = buildColdWarmVerdict({
      cold,
      warm,
      scenarioId: 'perf-cold-warm',
      runId: 'run-1',
      artifactsDir: '/tmp/run',
      summaryPath: '/tmp/run/summary.json',
      durationMs: 100,
    });

    expect(verdict.ok).toBe(false);
    expect(verdict.failureCount).toBe(1);
    expect(verdict.firstFailure.startsWith('Cold RSS regression:')).toBe(true);
    expect(verdict.firstFailure).toContain('900MB');
    expect(verdict.firstFailure).toContain('700MB');
    expect(verdict.firstFailure).toContain('28.6%');
    expect(verdict.firstFailure).toContain('tolerance 15%');
  });

  it('fails with the warm regression line when only warm regresses', () => {
    const cold = okComparison(720);
    const warm = regressingComparison(900);

    const verdict = buildColdWarmVerdict({
      cold,
      warm,
      scenarioId: 'perf-cold-warm',
      runId: 'run-1',
      artifactsDir: '/tmp/run',
      summaryPath: '/tmp/run/summary.json',
      durationMs: 100,
    });

    expect(verdict.ok).toBe(false);
    expect(verdict.failureCount).toBe(1);
    expect(verdict.firstFailure.startsWith('Warm RSS regression:')).toBe(true);
  });

  it('reports both failures but names cold first when both regress', () => {
    const cold = regressingComparison(900);
    const warm = regressingComparison(950);

    const verdict = buildColdWarmVerdict({
      cold,
      warm,
      scenarioId: 'perf-cold-warm',
      runId: 'run-1',
      artifactsDir: '/tmp/run',
      summaryPath: '/tmp/run/summary.json',
      durationMs: 100,
    });

    expect(verdict.ok).toBe(false);
    expect(verdict.failureCount).toBe(2);
    expect(verdict.firstFailure.startsWith('Cold RSS regression:')).toBe(true);
    expect(verdict.firstFailure).toContain('900MB');
  });
});

describe('fitSlope', () => {
  it('returns slope 0 for a flat series', () => {
    const { slope, intercept } = fitSlope([100, 100, 100, 100]);

    expect(slope).toBe(0);
    expect(intercept).toBeCloseTo(100, 5);
  });

  it('returns the exact slope for a perfect staircase', () => {
    const { slope, intercept } = fitSlope([100, 110, 120, 130]);

    expect(slope).toBe(10);
    expect(intercept).toBeCloseTo(100, 5);
  });

  it('returns a slope near 0 for a noisy-but-flat series', () => {
    const { slope } = fitSlope([100, 102, 98, 101, 99, 100]);

    expect(Math.abs(slope)).toBeLessThan(1);
  });

  it('throws when given fewer than 2 points', () => {
    expect(() => fitSlope([100])).toThrow('fitSlope needs at least 2 points');
    expect(() => fitSlope([])).toThrow('fitSlope needs at least 2 points');
  });
});

describe('buildLeakSoakVerdict', () => {
  it('builds a passing verdict when the slope is below the threshold', () => {
    const retainedByCycle = [500, 501, 500, 500.5, 500.2, 500.8];

    const verdict = buildLeakSoakVerdict({
      retainedByCycle,
      warmupCycles: 2,
      slope: 0.1,
      slopeThresholdMb: 5,
      scenarioId: 'perf-leak-soak',
      runId: 'perf-leak-soak-2026-07-05T00-00-00-000Z',
      artifactsDir: '/tmp/attn-real-app-harness/perf-leak-soak-run',
      summaryPath: '/tmp/attn-real-app-harness/perf-leak-soak-run/summary.json',
      durationMs: 4321,
    });

    expect(verdict).toEqual({
      ok: true,
      scenarioId: 'perf-leak-soak',
      runId: 'perf-leak-soak-2026-07-05T00-00-00-000Z',
      failureCount: 0,
      firstFailure: null,
      artifactsDir: '/tmp/attn-real-app-harness/perf-leak-soak-run',
      summaryPath: '/tmp/attn-real-app-harness/perf-leak-soak-run/summary.json',
      durationMs: 4321,
      rss: { retainedByCycle, warmupCycles: 2, slope: 0.1, slopeThresholdMb: 5 },
      metrics: { retainedRssSlopeMbPerCycle: 0.1, firstRetainedMb: 500, lastRetainedMb: 500.8 },
    });
  });

  it('builds a failing verdict with a one-line leak message and failureCount 1 when the slope exceeds the threshold', () => {
    const retainedByCycle = [500, 520, 500, 510, 530, 550];

    const verdict = buildLeakSoakVerdict({
      retainedByCycle,
      warmupCycles: 2,
      slope: 15,
      slopeThresholdMb: 5,
      scenarioId: 'perf-leak-soak',
      runId: 'run-1',
      artifactsDir: '/tmp/run',
      summaryPath: '/tmp/run/summary.json',
      durationMs: 100,
    });

    expect(verdict.ok).toBe(false);
    expect(verdict.failureCount).toBe(1);
    expect(verdict.firstFailure).toContain('Retained-RSS leak');
    expect(verdict.firstFailure.length).toBeLessThanOrEqual(300);
    expect(verdict.firstFailure).toContain('15MB/cycle');
    expect(verdict.firstFailure).toContain('500->550MB');
  });

  it('passes when the slope is exactly equal to the threshold (boundary)', () => {
    const retainedByCycle = [500, 505, 510, 515];

    const verdict = buildLeakSoakVerdict({
      retainedByCycle,
      warmupCycles: 1,
      slope: 5,
      slopeThresholdMb: 5,
      scenarioId: 'perf-leak-soak',
      runId: 'run-1',
      artifactsDir: '/tmp/run',
      summaryPath: '/tmp/run/summary.json',
      durationMs: 100,
    });

    expect(verdict.ok).toBe(true);
    expect(verdict.failureCount).toBe(0);
    expect(verdict.firstFailure).toBeNull();
  });
});
