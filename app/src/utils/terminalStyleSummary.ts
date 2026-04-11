import type { Terminal } from '@xterm/xterm';

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

function emptySummary(): TerminalVisibleStyleSummary {
  return {
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
  };
}

type StyleCellLike = {
  isBold(): number | boolean;
  isItalic(): number | boolean;
  isUnderline(): number | boolean;
  isInverse(): number | boolean;
  isFgRGB(): number | boolean;
  isFgPalette(): number | boolean;
  isBgRGB(): number | boolean;
  isBgPalette(): number | boolean;
  getFgColor(): number;
  getBgColor(): number;
};

function styleKey(cell: StyleCellLike) {
  return JSON.stringify({
    bold: Boolean(cell.isBold()),
    italic: Boolean(cell.isItalic()),
    underline: Boolean(cell.isUnderline()),
    inverse: Boolean(cell.isInverse()),
    fgMode: Boolean(cell.isFgRGB()) ? 'rgb' : Boolean(cell.isFgPalette()) ? 'palette' : 'default',
    fgColor: cell.getFgColor(),
    bgMode: Boolean(cell.isBgRGB()) ? 'rgb' : Boolean(cell.isBgPalette()) ? 'palette' : 'default',
    bgColor: cell.getBgColor(),
  });
}

function cellHasNonDefaultStyle(cell: StyleCellLike) {
  return (
    Boolean(cell.isBold())
    || Boolean(cell.isItalic())
    || Boolean(cell.isUnderline())
    || Boolean(cell.isInverse())
    || Boolean(cell.isFgRGB())
    || Boolean(cell.isFgPalette())
    || Boolean(cell.isBgRGB())
    || Boolean(cell.isBgPalette())
  );
}

export function snapshotVisibleTerminalStyleSummary(terminal: Terminal | null): TerminalVisibleStyleSnapshot {
  const buffer = terminal?.buffer.active;
  if (!terminal || !buffer || terminal.rows <= 0 || terminal.cols <= 0) {
    return {
      cols: terminal?.cols ?? null,
      rows: terminal?.rows ?? null,
      viewportY: buffer?.viewportY ?? null,
      lineCount: 0,
      lines: [],
      summary: emptySummary(),
    };
  }

  const reusableCell = buffer.getNullCell();
  const lines: TerminalVisibleStyleLineSnapshot[] = [];
  const summary = emptySummary();
  const uniqueStyleKeys = new Set<string>();
  const start = buffer.viewportY;
  const end = Math.min(buffer.length, start + terminal.rows);

  for (let rowIndex = start; rowIndex < end; rowIndex += 1) {
    const line = buffer.getLine(rowIndex);
    const lineSnapshot: TerminalVisibleStyleLineSnapshot = {
      rowOffset: rowIndex - start,
      text: line?.translateToString(true) ?? '',
      styledCellCount: 0,
      boldCellCount: 0,
      italicCellCount: 0,
      underlineCellCount: 0,
      inverseCellCount: 0,
      fgPaletteCellCount: 0,
      fgRgbCellCount: 0,
      bgPaletteCellCount: 0,
      bgRgbCellCount: 0,
    };

    for (let col = 0; col < terminal.cols; col += 1) {
      const cell = line?.getCell(col, reusableCell);
      if (!cell || cell.getWidth() === 0) {
        continue;
      }
      const chars = cell.getChars() || ' ';
      if (chars.trim().length === 0) {
        continue;
      }
      if (!cellHasNonDefaultStyle(cell)) {
        continue;
      }

      lineSnapshot.styledCellCount += 1;
      summary.styledCellCount += 1;
      uniqueStyleKeys.add(styleKey(cell));

      if (Boolean(cell.isBold())) {
        lineSnapshot.boldCellCount += 1;
        summary.boldCellCount += 1;
      }
      if (Boolean(cell.isItalic())) {
        lineSnapshot.italicCellCount += 1;
        summary.italicCellCount += 1;
      }
      if (Boolean(cell.isUnderline())) {
        lineSnapshot.underlineCellCount += 1;
        summary.underlineCellCount += 1;
      }
      if (Boolean(cell.isInverse())) {
        lineSnapshot.inverseCellCount += 1;
        summary.inverseCellCount += 1;
      }
      if (Boolean(cell.isFgPalette())) {
        lineSnapshot.fgPaletteCellCount += 1;
        summary.fgPaletteCellCount += 1;
      }
      if (Boolean(cell.isFgRGB())) {
        lineSnapshot.fgRgbCellCount += 1;
        summary.fgRgbCellCount += 1;
      }
      if (Boolean(cell.isBgPalette())) {
        lineSnapshot.bgPaletteCellCount += 1;
        summary.bgPaletteCellCount += 1;
      }
      if (Boolean(cell.isBgRGB())) {
        lineSnapshot.bgRgbCellCount += 1;
        summary.bgRgbCellCount += 1;
      }
    }

    if (lineSnapshot.styledCellCount > 0) {
      summary.styledLineCount += 1;
    }
    lines.push(lineSnapshot);
  }

  summary.uniqueStyleCount = uniqueStyleKeys.size;

  return {
    cols: terminal.cols,
    rows: terminal.rows,
    viewportY: buffer.viewportY,
    lineCount: lines.length,
    lines,
    summary,
  };
}
