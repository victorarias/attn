export interface TerminalVisibleStyleLineSnapshot {
  rowOffset: number;
  text: string;
  styledCellCount: number;
  boldCellCount: number;
  italicCellCount: number;
  underlineCellCount: number;
  inverseCellCount: number;
  fgPaletteCellCount: number;
  fgRgbCellCount: number;
  bgPaletteCellCount: number;
  bgRgbCellCount: number;
}

export interface TerminalVisibleStyleSummary {
  styledCellCount: number;
  styledLineCount: number;
  boldCellCount: number;
  italicCellCount: number;
  underlineCellCount: number;
  inverseCellCount: number;
  fgPaletteCellCount: number;
  fgRgbCellCount: number;
  bgPaletteCellCount: number;
  bgRgbCellCount: number;
  uniqueStyleCount: number;
}

export interface TerminalVisibleStyleSnapshot {
  cols: number | null;
  rows: number | null;
  viewportY: number | null;
  lineCount: number;
  lines: TerminalVisibleStyleLineSnapshot[];
  summary: TerminalVisibleStyleSummary;
}

export function emptyTerminalVisibleStyleSnapshot(): TerminalVisibleStyleSnapshot {
  return {
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
  };
}
