import { describe, expect, it } from 'vitest';
import { analyzeTerminalVisibleLines, snapshotVisibleTerminalContent } from './terminalVisibleContent';

describe('analyzeTerminalVisibleLines', () => {
  it('summarizes non-empty visible lines', () => {
    const summary = analyzeTerminalVisibleLines([
      '',
      '  heading  ',
      'body line with enough density',
      'body line with enough density',
      'tail',
    ], 40);

    expect(summary).toEqual({
      nonEmptyLineCount: 4,
      denseLineCount: 2,
      charCount: 'heading'.length + 'body line with enough density'.length * 2 + 'tail'.length,
      maxLineLength: 'body line with enough density'.length,
      maxOccupiedColumns: 'body line with enough density'.length,
      maxOccupiedWidthRatio: 'body line with enough density'.length / 40,
      medianOccupiedWidthRatio: ('heading'.length / 40 + 'body line with enough density'.length / 40) / 2,
      meanOccupiedWidthRatio:
        ('heading'.length / 40 + 'body line with enough density'.length / 40 + 'body line with enough density'.length / 40 + 'tail'.length / 40) / 4,
      wideLineCount: 2,
      uniqueTrimmedLineCount: 3,
      firstNonEmptyLine: 'heading',
      lastNonEmptyLine: 'tail',
    });
  });
});

describe('snapshotVisibleTerminalContent', () => {
  it('captures only the visible viewport rows', () => {
    const lines = ['zero', 'one', 'two', 'three', 'four', 'five'].map((value) => ({
      translateToString: () => value,
    }));
    const snapshot = snapshotVisibleTerminalContent({
      cols: 10,
      rows: 3,
      buffer: {
        active: {
          viewportY: 2,
          length: lines.length,
          getLine: (index: number) => lines[index] ?? null,
        },
      },
    });

    expect(snapshot.cols).toBe(10);
    expect(snapshot.viewportY).toBe(2);
    expect(snapshot.lineCount).toBe(3);
    expect(snapshot.lines).toEqual(['two', 'three', 'four']);
    expect(snapshot.lineMetrics).toEqual([
      { rowOffset: 0, text: 'two', occupiedColumns: 3, occupiedWidthRatio: 0.3, nonEmpty: true },
      { rowOffset: 1, text: 'three', occupiedColumns: 5, occupiedWidthRatio: 0.5, nonEmpty: true },
      { rowOffset: 2, text: 'four', occupiedColumns: 4, occupiedWidthRatio: 0.4, nonEmpty: true },
    ]);
    expect(snapshot.summary.nonEmptyLineCount).toBe(3);
    expect(snapshot.summary.maxOccupiedColumns).toBe(5);
    expect(snapshot.summary.maxOccupiedWidthRatio).toBe(0.5);
    expect(snapshot.summary.firstNonEmptyLine).toBe('two');
    expect(snapshot.summary.lastNonEmptyLine).toBe('four');
  });
});
