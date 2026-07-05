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

// Pure: builds the single ATTN_VERDICT payload for the cold/warm RSS scenario.
// `cold` and `warm` are `comparison` objects (the shape evaluateRssBaseline /
// compareToBaseline returns: { ok, value, baseline, deltaPct, tolerancePct, reason }).
// ok is the AND of both phases; failureCount counts the regressing phases;
// firstFailure names the first regressor (cold before warm), capped like the
// core contract. Extends the core verdict with `rss: { cold, warm }` and
// `metrics: { coldRssMb, warmRssMb }` (parity with buildBaselineVerdict's
// extension, for the leak-soak consumer).
export function buildColdWarmVerdict({ cold, warm, scenarioId, runId, artifactsDir, summaryPath, durationMs }) {
  const ok = cold.ok && warm.ok;
  const failureCount = (cold.ok ? 0 : 1) + (warm.ok ? 0 : 1);
  const regressionLine = (label, c) =>
    `${label} RSS regression: ${c.value}MB vs baseline ${c.baseline}MB (+${c.deltaPct}%, tolerance ${c.tolerancePct}%)`;
  let firstFailure = null;
  if (!cold.ok) firstFailure = truncateFirstFailure(regressionLine('Cold', cold));
  else if (!warm.ok) firstFailure = truncateFirstFailure(regressionLine('Warm', warm));
  return {
    ok,
    scenarioId,
    runId,
    failureCount,
    firstFailure,
    artifactsDir,
    summaryPath,
    durationMs,
    rss: { cold, warm },
    metrics: { coldRssMb: cold.value, warmRssMb: warm.value },
  };
}

// Pure: least-squares linear regression of `points` (y) against x = 0..n-1.
// Standard closed form: slope = (n*sum(x*y) - sum(x)*sum(y)) / (n*sum(x^2) -
// sum(x)^2). `slope` is rounded to 2 decimals -- it is the leak-soak
// scenario's headline number (MB of retained RSS growth per cycle).
export function fitSlope(points) {
  if (points.length < 2) {
    throw new Error('fitSlope needs at least 2 points');
  }
  const n = points.length;
  let sumX = 0;
  let sumY = 0;
  let sumXY = 0;
  let sumXX = 0;
  for (let x = 0; x < n; x += 1) {
    const y = points[x];
    sumX += x;
    sumY += y;
    sumXY += x * y;
    sumXX += x * x;
  }
  const denominator = n * sumXX - sumX * sumX;
  const slope = (n * sumXY - sumX * sumY) / denominator;
  const intercept = (sumY - slope * sumX) / n;
  return { slope: Number(slope.toFixed(2)), intercept };
}

// Pure: builds the ATTN_VERDICT payload for the leak-soak scenario.
// `retainedByCycle` is the full per-cycle retained-RSS series (including the
// warmup cycles); `slope` is the pre-fitted (fitSlope) trend over the
// post-warmup portion -- this function does not fit it itself so the caller
// can log/record intermediate values (e.g. the machine registry comparison)
// before building the verdict. Extends the core verdict with `rss` and
// `metrics`, in parity with buildBaselineVerdict/buildColdWarmVerdict.
export function buildLeakSoakVerdict({
  retainedByCycle,
  warmupCycles,
  slope,
  slopeThresholdMb,
  scenarioId,
  runId,
  artifactsDir,
  summaryPath,
  durationMs,
}) {
  const ok = slope <= slopeThresholdMb;
  const failureCount = ok ? 0 : 1;
  let firstFailure = null;
  if (!ok) {
    const post = retainedByCycle.slice(warmupCycles);
    const firstPost = post[0];
    const lastPost = post[post.length - 1];
    firstFailure = truncateFirstFailure(
      `Retained-RSS leak: slope ${slope}MB/cycle over ${retainedByCycle.length - warmupCycles} post-warmup cycles `
      + `exceeds ${slopeThresholdMb}MB/cycle (retained ${firstPost}->${lastPost}MB)`,
    );
  }
  return {
    ok,
    scenarioId,
    runId,
    failureCount,
    firstFailure,
    artifactsDir,
    summaryPath,
    durationMs,
    rss: { retainedByCycle, warmupCycles, slope, slopeThresholdMb },
    metrics: {
      retainedRssSlopeMbPerCycle: slope,
      firstRetainedMb: retainedByCycle[0],
      lastRetainedMb: retainedByCycle[retainedByCycle.length - 1],
    },
  };
}
