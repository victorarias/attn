import { describe, expect, it } from 'vitest';
import { ATTN_VERDICT_PREFIX, formatVerdictLine } from './common.mjs';
import { summarizeFirstFailure } from './scenarioRunner.mjs';

const baseVerdict = {
  ok: true,
  scenarioId: 'demo-scenario',
  runId: 'demo-scenario-2026-01-01T00-00-00-000Z',
  failureCount: 0,
  firstFailure: null,
  artifactsDir: '/tmp/attn-real-app-harness/demo-scenario-run',
  summaryPath: '/tmp/attn-real-app-harness/demo-scenario-run/summary.json',
  durationMs: 1234,
};

describe('formatVerdictLine', () => {
  it('prefixes the line with ATTN_VERDICT and a space', () => {
    const line = formatVerdictLine(baseVerdict);

    expect(line.startsWith(ATTN_VERDICT_PREFIX)).toBe(true);
  });

  it('emits compact (non-pretty-printed) JSON after the prefix', () => {
    const line = formatVerdictLine(baseVerdict);
    const payload = line.slice(ATTN_VERDICT_PREFIX.length);

    expect(payload).toBe(JSON.stringify(baseVerdict));
    expect(payload).not.toMatch(/\n/);
    expect(JSON.parse(payload)).toEqual(baseVerdict);
  });

  it('preserves every contract key on a failing verdict', () => {
    const failingVerdict = {
      ...baseVerdict,
      ok: false,
      failureCount: 1,
      firstFailure: 'assertion failed: expected pane visible',
    };

    const line = formatVerdictLine(failingVerdict);
    const parsed = JSON.parse(line.slice(ATTN_VERDICT_PREFIX.length));

    expect(parsed).toEqual(failingVerdict);
  });

  it('stays a single line even when firstFailure contains embedded newlines', () => {
    const verdictWithNewlines = {
      ...baseVerdict,
      ok: false,
      failureCount: 1,
      firstFailure: 'first line of error\nsecond line\nthird line',
    };

    const line = formatVerdictLine(verdictWithNewlines);

    // JSON.stringify escapes newlines as the two-character sequence `\n`
    // rather than a literal line break, so the produced line has no raw
    // newline characters in it — verify that invariant directly rather than
    // just re-parsing the JSON.
    expect(line.split('\n')).toHaveLength(1);
    expect(line).toContain('first line of error\\nsecond line\\nthird line');
  });

  it('stays a single line even when firstFailure contains embedded quotes', () => {
    const verdictWithQuotes = {
      ...baseVerdict,
      ok: false,
      failureCount: 1,
      firstFailure: 'expected "visible" but got "hidden"',
    };

    const line = formatVerdictLine(verdictWithQuotes);
    const parsed = JSON.parse(line.slice(ATTN_VERDICT_PREFIX.length));

    expect(line.split('\n')).toHaveLength(1);
    expect(parsed.firstFailure).toBe('expected "visible" but got "hidden"');
  });
});

describe('summarizeFirstFailure', () => {
  it('keeps a short single-line message intact', () => {
    const error = new Error('pane not visible');

    expect(summarizeFirstFailure(error)).toBe('pane not visible');
  });

  it('takes only the first line of a multi-line error message', () => {
    const error = new Error('assertion failed: pane not visible\n    at step (scenario.mjs:42)');

    expect(summarizeFirstFailure(error)).toBe('assertion failed: pane not visible');
  });

  it('truncates a message longer than 300 characters to exactly 300', () => {
    const longMessage = 'x'.repeat(400);
    const error = new Error(longMessage);

    const result = summarizeFirstFailure(error);

    expect(result).toHaveLength(300);
    expect(result).toBe('x'.repeat(300));
  });

  it('handles non-Error values via String()', () => {
    expect(summarizeFirstFailure('plain string failure')).toBe('plain string failure');
  });
});
