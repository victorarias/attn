export interface TerminalVisibleContentSummary {
  nonEmptyLineCount: number;
  denseLineCount: number;
  charCount: number;
  maxLineLength: number;
  maxOccupiedColumns: number;
  maxOccupiedWidthRatio: number;
  medianOccupiedWidthRatio: number;
  meanOccupiedWidthRatio: number;
  wideLineCount: number;
  uniqueTrimmedLineCount: number;
  firstNonEmptyLine: string | null;
  lastNonEmptyLine: string | null;
}

export interface TerminalVisibleLineMetric {
  rowOffset: number;
  text: string;
  occupiedColumns: number;
  occupiedWidthRatio: number;
  nonEmpty: boolean;
}

export interface TerminalVisibleContentSnapshot {
  cols: number | null;
  viewportY: number | null;
  lineCount: number;
  lines: string[];
  lineMetrics: TerminalVisibleLineMetric[];
  summary: TerminalVisibleContentSummary;
}

interface TerminalBufferLineLike {
  translateToString(trimRight?: boolean): string;
}

interface TerminalBufferLike {
  viewportY: number;
  length: number;
  getLine(index: number): TerminalBufferLineLike | null | undefined;
}

interface TerminalLike {
  cols: number;
  rows: number;
  buffer: {
    active: TerminalBufferLike;
  };
}

function safeRatio(numerator: number, denominator: number): number {
  if (!Number.isFinite(numerator) || !Number.isFinite(denominator) || denominator <= 0) {
    return 0;
  }
  return numerator / denominator;
}

function median(values: number[]): number {
  if (values.length === 0) {
    return 0;
  }
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  if (sorted.length % 2 === 0) {
    return (sorted[mid - 1] + sorted[mid]) / 2;
  }
  return sorted[mid];
}

export function analyzeTerminalVisibleLines(lines: string[], cols: number | null = null): TerminalVisibleContentSummary {
  const trimmedLines = lines.map((line) => line.trim()).filter((line) => line.length > 0);
  const nonEmptyLineCount = trimmedLines.length;
  const denseLineCount = trimmedLines.filter((line) => line.length >= 20).length;
  const charCount = trimmedLines.reduce((sum, line) => sum + line.length, 0);
  const maxLineLength = trimmedLines.reduce((max, line) => Math.max(max, line.length), 0);
  const occupiedColumns = trimmedLines.map((line) => line.length);
  const occupiedRatios = occupiedColumns.map((value) => safeRatio(value, cols || 0));

  return {
    nonEmptyLineCount,
    denseLineCount,
    charCount,
    maxLineLength,
    maxOccupiedColumns: occupiedColumns.reduce((max, value) => Math.max(max, value), 0),
    maxOccupiedWidthRatio: occupiedRatios.reduce((max, value) => Math.max(max, value), 0),
    medianOccupiedWidthRatio: median(occupiedRatios),
    meanOccupiedWidthRatio: occupiedRatios.length > 0
      ? occupiedRatios.reduce((sum, value) => sum + value, 0) / occupiedRatios.length
      : 0,
    wideLineCount: occupiedRatios.filter((value) => value >= 0.6).length,
    uniqueTrimmedLineCount: new Set(trimmedLines).size,
    firstNonEmptyLine: trimmedLines[0] ?? null,
    lastNonEmptyLine: trimmedLines[trimmedLines.length - 1] ?? null,
  };
}

export function snapshotVisibleTerminalContent(terminal: TerminalLike | null): TerminalVisibleContentSnapshot {
  const buffer = terminal?.buffer.active;
  if (!buffer || !terminal || terminal.rows <= 0) {
    return {
      cols: terminal?.cols ?? null,
      viewportY: buffer?.viewportY ?? null,
      lineCount: 0,
      lines: [],
      lineMetrics: [],
      summary: analyzeTerminalVisibleLines([], terminal?.cols ?? null),
    };
  }

  const start = Math.max(0, buffer.viewportY);
  const end = Math.min(buffer.length, start + terminal.rows);
  const lines: string[] = [];
  const lineMetrics: TerminalVisibleLineMetric[] = [];

  for (let index = start; index < end; index += 1) {
    const line = buffer.getLine(index);
    const text = line ? line.translateToString(true) : '';
    const trimmed = text.trim();
    const occupiedColumns = text.length;
    lines.push(text);
    lineMetrics.push({
      rowOffset: index - start,
      text,
      occupiedColumns,
      occupiedWidthRatio: safeRatio(occupiedColumns, terminal.cols),
      nonEmpty: trimmed.length > 0,
    });
  }

  return {
    cols: terminal.cols,
    viewportY: buffer.viewportY,
    lineCount: lines.length,
    lines,
    lineMetrics,
    summary: analyzeTerminalVisibleLines(lines, terminal.cols),
  };
}
