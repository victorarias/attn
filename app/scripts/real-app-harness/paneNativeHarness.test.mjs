import { describe, expect, it } from 'vitest';
import {
  analyzePaneTextCoverage,
  comparePaneNativePaintCoverage,
  comparePaneNativePaintRegression,
  evaluatePaneNativePaintCoverage,
} from './paneNativeAnalysis.mjs';

function paddedLine(cols, text, leftPad = 0) {
  const body = text.padEnd(cols - leftPad, ' ');
  return ' '.repeat(leftPad) + body.slice(0, cols - leftPad);
}

function blankLine(cols) {
  return ' '.repeat(cols);
}

describe('pane native analysis (text coverage)', () => {
  it('passes a healthy full-area synthetic pane', () => {
    const cols = 80;
    const rows = 24;
    const lines = [];
    for (let r = 0; r < rows; r += 1) {
      if (r >= 2 && r <= 18) {
        lines.push(paddedLine(cols, `row${r} some terminal content spanning most columns to mimic a busy pane`));
      } else {
        lines.push(blankLine(cols));
      }
    }

    const analysis = analyzePaneTextCoverage({ cols, lines });
    expect(analysis.busyColumnRatio).toBeGreaterThan(0.75);
    expect(analysis.busyRowRatio).toBeGreaterThan(0.45);
    expect(analysis.bboxWidthRatio).toBeGreaterThan(0.75);
    expect(analysis.bboxHeightRatio).toBeGreaterThan(0.5);

    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio: 0.5,
      minBusyRowRatio: 0.2,
      minBBoxWidthRatio: 0.5,
      minBBoxHeightRatio: 0.2,
    });
    expect(evaluation.ok).toBe(true);

    const comparison = comparePaneNativePaintCoverage(analysis, analysis);
    expect(comparison.ok).toBe(true);
    expect(comparison.deltas).toEqual({
      busyColumnRatioDelta: 0,
      busyRowRatioDelta: 0,
      bboxWidthRatioDelta: 0,
      bboxHeightRatioDelta: 0,
      activePixelRatioDelta: 0,
    });
  });

  it('fails a narrow left-strip render', () => {
    const cols = 80;
    const rows = 24;
    const lines = [];
    for (let r = 0; r < rows; r += 1) {
      if (r >= 2 && r <= 18) {
        lines.push(paddedLine(cols, 'abc'));
      } else {
        lines.push(blankLine(cols));
      }
    }

    const analysis = analyzePaneTextCoverage({ cols, lines });
    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio: 0.45,
      minBusyRowRatio: 0.18,
      minBBoxWidthRatio: 0.45,
      minBBoxHeightRatio: 0.18,
    });
    expect(evaluation.ok).toBe(false);
    expect(evaluation.failures.some((failure) => failure.includes('busyColumnRatio'))).toBe(true);
    expect(evaluation.failures.some((failure) => failure.includes('bboxWidthRatio'))).toBe(true);
  });

  it('fails a footer-band render', () => {
    const cols = 80;
    const rows = 24;
    const lines = [];
    for (let r = 0; r < rows; r += 1) {
      if (r >= 20 && r <= 22) {
        lines.push(paddedLine(cols, 'bottom row content filling most of the available width'));
      } else {
        lines.push(blankLine(cols));
      }
    }

    const analysis = analyzePaneTextCoverage({ cols, lines });
    const evaluation = evaluatePaneNativePaintCoverage(analysis, {
      minBusyColumnRatio: 0.45,
      minBusyRowRatio: 0.18,
      minBBoxWidthRatio: 0.45,
      minBBoxHeightRatio: 0.18,
    });
    expect(evaluation.ok).toBe(false);
    expect(evaluation.failures.some((failure) => failure.includes('busyRowRatio'))).toBe(true);
    expect(evaluation.failures.some((failure) => failure.includes('bboxHeightRatio'))).toBe(true);
  });

  it('treats missing cols or empty lines as zero coverage', () => {
    const empty = analyzePaneTextCoverage({ cols: 0, lines: [] });
    expect(empty.busyColumnRatio).toBe(0);
    expect(empty.busyRowRatio).toBe(0);
    expect(empty.bbox).toBeNull();
  });

  it('allows callers to ignore selected delta metrics', () => {
    const baseline = {
      busyColumnRatio: 0.9,
      busyRowRatio: 0.4,
      bboxWidthRatio: 1,
      bboxHeightRatio: 1,
      activePixelRatio: 0.16,
    };
    const candidate = {
      busyColumnRatio: 0.86,
      busyRowRatio: 0.36,
      bboxWidthRatio: 0.96,
      bboxHeightRatio: 0.96,
      activePixelRatio: 0.07,
    };

    const strict = comparePaneNativePaintCoverage(baseline, candidate, {
      maxBusyColumnRatioDelta: 0.08,
      maxBusyRowRatioDelta: 0.08,
      maxBBoxWidthRatioDelta: 0.08,
      maxBBoxHeightRatioDelta: 0.08,
      maxActivePixelRatioDelta: 0.04,
    });
    expect(strict.ok).toBe(false);
    expect(strict.failures.some((failure) => failure.includes('activePixelRatioDelta'))).toBe(true);

    const reflowAllowed = comparePaneNativePaintCoverage(baseline, candidate, {
      maxBusyColumnRatioDelta: 0.08,
      maxBusyRowRatioDelta: 0.08,
      maxBBoxWidthRatioDelta: 0.08,
      maxBBoxHeightRatioDelta: 0.08,
      maxActivePixelRatioDelta: null,
    });
    expect(reflowAllowed.ok).toBe(true);
  });

  it('allows paint growth while still rejecting native paint regression', () => {
    const baseline = {
      busyColumnRatio: 0.86,
      busyRowRatio: 0.26,
      bboxWidthRatio: 0.96,
      bboxHeightRatio: 0.49,
      activePixelRatio: 0.06,
    };
    const expanded = {
      busyColumnRatio: 0.97,
      busyRowRatio: 0.56,
      bboxWidthRatio: 0.97,
      bboxHeightRatio: 0.71,
      activePixelRatio: 0.39,
    };
    const collapsed = {
      busyColumnRatio: 0.62,
      busyRowRatio: 0.11,
      bboxWidthRatio: 0.58,
      bboxHeightRatio: 0.14,
      activePixelRatio: 0.03,
    };

    const expandedComparison = comparePaneNativePaintRegression(baseline, expanded, {
      maxBusyColumnRatioRegression: 0.1,
      maxBusyRowRatioRegression: 0.1,
      maxBBoxWidthRatioRegression: 0.1,
      maxBBoxHeightRatioRegression: 0.1,
      maxActivePixelRatioRegression: null,
    });
    expect(expandedComparison.ok).toBe(true);
    expect(expandedComparison.regressions).toEqual({
      busyColumnRatioRegression: 0,
      busyRowRatioRegression: 0,
      bboxWidthRatioRegression: 0,
      bboxHeightRatioRegression: 0,
      activePixelRatioRegression: 0,
    });

    const collapsedComparison = comparePaneNativePaintRegression(baseline, collapsed, {
      maxBusyColumnRatioRegression: 0.1,
      maxBusyRowRatioRegression: 0.1,
      maxBBoxWidthRatioRegression: 0.1,
      maxBBoxHeightRatioRegression: 0.1,
      maxActivePixelRatioRegression: null,
    });
    expect(collapsedComparison.ok).toBe(false);
    expect(collapsedComparison.failures.some((failure) => failure.includes('busyRowRatioRegression'))).toBe(true);
    expect(collapsedComparison.failures.some((failure) => failure.includes('bboxHeightRatioRegression'))).toBe(true);
  });
});
