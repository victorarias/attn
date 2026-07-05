import { compareToBaseline } from './machineRegistry.mjs';

// Contract cap shared with scenarioRunner.mjs's summarizeFirstFailure — the
// ATTN_VERDICT line is single-line and length-bounded, so firstFailure here
// follows the same rule even though this scenario builds its own verdict by
// hand instead of going through createScenarioRunner.
const FIRST_FAILURE_MAX_LENGTH = 300;

function truncateFirstFailure(message) {
  return message.length <= FIRST_FAILURE_MAX_LENGTH ? message : message.slice(0, FIRST_FAILURE_MAX_LENGTH);
}

// Pure: decides whether this run's RSS is within tolerance of the per-machine
// baseline, and whether a new baseline should be written. No fs/Date/process
// access so it stays directly unit-testable — the caller resolves the
// baseline (machineRegistry.loadBaseline), performs the write
// (machineRegistry.saveBaseline), and supplies `recordedAt`.
//
// A baseline is (re)recorded when there isn't one yet (first run on a
// machine always self-baselines) or when the caller explicitly asked to via
// `record` (re-baselining after an intentional regression/improvement). An
// explicit re-record is a pass by construction: this run DEFINES the new
// baseline, so it can never be a regression against the value it replaces —
// comparing it against the old baseline (and possibly failing) would defeat
// the point of asking to re-baseline.
export function evaluateRssBaseline({ totalRssMb, fingerprint, baseline, tolerancePct = 15, record = false, recordedAt }) {
  const hasBaseline = baseline?.metrics?.totalRssMb != null;
  const comparison = record === true
    ? { ok: true, value: totalRssMb, baseline: null, deltaPct: null, tolerancePct, reason: 'recorded' }
    : compareToBaseline(totalRssMb, hasBaseline ? baseline.metrics.totalRssMb : null, { tolerancePct });
  const shouldRecord = record === true || !hasBaseline;
  const baselineToSave = shouldRecord
    ? { fingerprint, metrics: { totalRssMb }, recordedAt }
    : null;

  return { ok: comparison.ok, comparison, baselineToSave };
}

// Pure: builds the ATTN_VERDICT payload for the RSS baseline scenario. Extends
// the core verdict contract (see common.mjs) with two scenario-specific
// fields, `rss` and `metrics` — existing verdict consumers only read the core
// fields, and this extension is documented for the leak-soak scenario that
// will read `rss`/`metrics` off this same shape.
export function buildBaselineVerdict({ ok, comparison, scenarioId, runId, artifactsDir, summaryPath, durationMs, extraMetrics = {} }) {
  const firstFailure = ok
    ? null
    : truncateFirstFailure(
        `RSS regression: ${comparison.value}MB vs baseline ${comparison.baseline}MB `
        + `(+${comparison.deltaPct}%, tolerance ${comparison.tolerancePct}%)`,
      );

  return {
    ok,
    scenarioId,
    runId,
    failureCount: ok ? 0 : 1,
    firstFailure,
    artifactsDir,
    summaryPath,
    durationMs,
    rss: comparison,
    metrics: { totalRssMb: comparison.value, ...extraMetrics },
  };
}
