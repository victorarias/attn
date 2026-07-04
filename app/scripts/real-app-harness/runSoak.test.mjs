import { describe, expect, it } from 'vitest';
import { isRunFailure, parseVerdictFromOutput, summarizeSoak } from './run-soak.mjs';

const okVerdict = {
  ok: true,
  scenarioId: 'demo-scenario',
  runId: 'demo-scenario-2026-01-01T00-00-00-000Z',
  failureCount: 0,
  firstFailure: null,
  artifactsDir: '/tmp/attn-real-app-harness/demo-scenario-run',
  summaryPath: '/tmp/attn-real-app-harness/demo-scenario-run/summary.json',
  durationMs: 1234,
};

const failVerdict = {
  ...okVerdict,
  ok: false,
  failureCount: 1,
  firstFailure: 'assertion failed: pane not visible',
};

describe('parseVerdictFromOutput', () => {
  it('returns null when there is no verdict line', () => {
    expect(parseVerdictFromOutput('some\nordinary\nlog output\n')).toBeNull();
  });

  it('returns null for empty or non-string input', () => {
    expect(parseVerdictFromOutput('')).toBeNull();
    expect(parseVerdictFromOutput(undefined)).toBeNull();
    expect(parseVerdictFromOutput(null)).toBeNull();
  });

  it('parses the JSON payload of a single verdict line', () => {
    const output = `some log line\nATTN_VERDICT ${JSON.stringify(okVerdict)}\n`;

    expect(parseVerdictFromOutput(output)).toEqual(okVerdict);
  });

  it('takes the LAST verdict line when there are multiple', () => {
    const output = [
      `ATTN_VERDICT ${JSON.stringify(okVerdict)}`,
      'some trailing trace output',
      `ATTN_VERDICT ${JSON.stringify(failVerdict)}`,
    ].join('\n');

    expect(parseVerdictFromOutput(output)).toEqual(failVerdict);
  });

  it('returns null when the payload after the prefix is not valid JSON', () => {
    const output = 'ATTN_VERDICT {not valid json';

    expect(parseVerdictFromOutput(output)).toBeNull();
  });

  it('falls back to an earlier valid line if the last one is garbage', () => {
    const output = [`ATTN_VERDICT ${JSON.stringify(okVerdict)}`, 'ATTN_VERDICT {broken'].join('\n');

    // The contract is "last line wins" — if the last ATTN_VERDICT line is
    // garbage, the parse result is null, not a stale earlier verdict.
    expect(parseVerdictFromOutput(output)).toBeNull();
  });
});

describe('isRunFailure', () => {
  it('is false for a clean, non-timed-out run with an ok verdict', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: false, verdict: okVerdict })).toBe(false);
  });

  it('is true when the exit code is non-zero', () => {
    expect(isRunFailure({ exitCode: 1, timedOut: false, verdict: okVerdict })).toBe(true);
  });

  it('is true when the run timed out even with exit code 0', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: true, verdict: okVerdict })).toBe(true);
  });

  it('passes a run that exits 0 with no verdict line (pre-verdict-contract scenario)', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: false, verdict: null })).toBe(false);
  });

  it('fails a run that exits non-zero with no verdict line', () => {
    expect(isRunFailure({ exitCode: 1, timedOut: false, verdict: null })).toBe(true);
  });

  it('is true when the parsed verdict says ok: false', () => {
    expect(isRunFailure({ exitCode: 0, timedOut: false, verdict: failVerdict })).toBe(true);
  });
});

describe('summarizeSoak', () => {
  it('reports ok when every run passed', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 10 },
      { iteration: 2, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 10 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z',
      summaryPath: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z/soak-report.json',
      durationMs: 20,
    });

    expect(summary).toEqual({
      ok: true,
      scenarioId: 'soak:demo-scenario',
      runId: 'soak-demo-scenario-2026-01-01T00-00-00-000Z',
      failureCount: 0,
      firstFailure: null,
      artifactsDir: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z',
      summaryPath: '/tmp/attn-real-app-harness/soak-demo-scenario-2026-01-01T00-00-00-000Z/soak-report.json',
      durationMs: 20,
    });
  });

  it('counts failures and surfaces the first failing run\'s verdict firstFailure', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: false, verdict: okVerdict, durationMs: 10 },
      { iteration: 2, exitCode: 0, timedOut: false, verdict: failVerdict, durationMs: 10 },
      { iteration: 3, exitCode: 1, timedOut: false, verdict: null, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 25,
    });

    expect(summary.ok).toBe(false);
    expect(summary.failureCount).toBe(2);
    expect(summary.firstFailure).toBe('assertion failed: pane not visible');
  });

  it('falls back to iteration/exit-code text when the first failure has no verdict', () => {
    const records = [
      { iteration: 1, exitCode: 1, timedOut: false, verdict: null, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 5,
    });

    expect(summary.firstFailure).toBe('iteration 1 exit 1');
  });

  it('reports ok for exit-0 runs that never emitted a verdict line', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: false, verdict: null, verdictMissing: true, durationMs: 5 },
      { iteration: 2, exitCode: 0, timedOut: false, verdict: null, verdictMissing: true, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 10,
    });

    expect(summary.ok).toBe(true);
    expect(summary.failureCount).toBe(0);
    expect(summary.firstFailure).toBeNull();
  });

  it('treats a timed-out run with exit code 0 as a failure', () => {
    const records = [
      { iteration: 1, exitCode: 0, timedOut: true, verdict: okVerdict, durationMs: 5 },
    ];

    const summary = summarizeSoak(records, {
      scenarioId: 'demo-scenario',
      runDir: '/tmp/run',
      summaryPath: '/tmp/run/soak-report.json',
      durationMs: 5,
    });

    expect(summary.ok).toBe(false);
    expect(summary.failureCount).toBe(1);
  });
});
