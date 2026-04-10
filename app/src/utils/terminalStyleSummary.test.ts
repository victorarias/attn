import { describe, expect, it } from 'vitest';
import { snapshotVisibleTerminalStyleSummary } from './terminalStyleSummary';

function createCell({
  chars = ' ',
  width = 1,
  bold = false,
  italic = false,
  underline = false,
  inverse = false,
  fgMode = 'default',
  fgColor = -1,
  bgMode = 'default',
  bgColor = -1,
}: {
  chars?: string;
  width?: number;
  bold?: boolean;
  italic?: boolean;
  underline?: boolean;
  inverse?: boolean;
  fgMode?: 'default' | 'palette' | 'rgb';
  fgColor?: number;
  bgMode?: 'default' | 'palette' | 'rgb';
  bgColor?: number;
} = {}) {
  return {
    getChars: () => chars,
    getWidth: () => width,
    isBold: () => bold,
    isItalic: () => italic,
    isUnderline: () => underline,
    isInverse: () => inverse,
    isFgRGB: () => fgMode === 'rgb',
    isFgPalette: () => fgMode === 'palette',
    isBgRGB: () => bgMode === 'rgb',
    isBgPalette: () => bgMode === 'palette',
    getFgColor: () => fgColor,
    getBgColor: () => bgColor,
  };
}

function createLine(
  text: string,
  styledColumns: Record<number, Parameters<typeof createCell>[0]> = {},
) {
  const cells = Array.from(text).map((char, index) => createCell({
    chars: char,
    ...(styledColumns[index] || {}),
  }));
  return {
    translateToString: () => text,
    getCell: (index: number) => cells[index] || null,
  };
}

describe('snapshotVisibleTerminalStyleSummary', () => {
  it('returns an empty snapshot when no terminal is present', () => {
    expect(snapshotVisibleTerminalStyleSummary(null)).toEqual({
      cols: null,
      rows: null,
      viewportY: null,
      lineCount: 0,
      lines: [],
      summary: {
        styledCellCount: 0,
        styledLineCount: 0,
        boldCellCount: 0,
        italicCellCount: 0,
        underlineCellCount: 0,
        inverseCellCount: 0,
        fgPaletteCellCount: 0,
        fgRgbCellCount: 0,
        bgPaletteCellCount: 0,
        bgRgbCellCount: 0,
        uniqueStyleCount: 0,
      },
    });
  });

  it('summarizes visible non-default style cells', () => {
    const terminal = {
      cols: 8,
      rows: 2,
      buffer: {
        active: {
          viewportY: 0,
          length: 2,
          getNullCell: () => createCell(),
          getLine: (index: number) => {
            if (index === 0) {
              return createLine('ABCD    ', {
                0: { bold: true, fgMode: 'palette', fgColor: 1 },
                1: { bold: true, fgMode: 'palette', fgColor: 1 },
                2: { underline: true, fgMode: 'rgb', fgColor: 0x12abef },
                3: { inverse: true, bgMode: 'palette', bgColor: 214 },
              });
            }
            if (index === 1) {
              return createLine('WXYZ    ', {
                0: { italic: true, fgMode: 'rgb', fgColor: 0x445566, bgMode: 'rgb', bgColor: 0x111111 },
              });
            }
            return null;
          },
        },
      },
    };

    const snapshot = snapshotVisibleTerminalStyleSummary(terminal as never);
    expect(snapshot.summary.styledCellCount).toBe(5);
    expect(snapshot.summary.styledLineCount).toBe(2);
    expect(snapshot.summary.boldCellCount).toBe(2);
    expect(snapshot.summary.italicCellCount).toBe(1);
    expect(snapshot.summary.underlineCellCount).toBe(1);
    expect(snapshot.summary.inverseCellCount).toBe(1);
    expect(snapshot.summary.fgPaletteCellCount).toBe(2);
    expect(snapshot.summary.fgRgbCellCount).toBe(2);
    expect(snapshot.summary.bgPaletteCellCount).toBe(1);
    expect(snapshot.summary.bgRgbCellCount).toBe(1);
    expect(snapshot.summary.uniqueStyleCount).toBe(4);
    expect(snapshot.lines[0]?.styledCellCount).toBe(4);
    expect(snapshot.lines[1]?.styledCellCount).toBe(1);
  });
});
