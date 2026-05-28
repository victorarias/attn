const WHEEL_DELTA_PIXEL = 0;
const WHEEL_DELTA_LINE = 1;
const WHEEL_DELTA_PAGE = 2;

export interface WheelRows {
  lines: number;
  remainderRows: number;
}

export interface TerminalSelectionRange {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
}

export interface ApplicationSelectionAnchor {
  startCol: number;
  endCol: number;
  rowSpan: number;
  markers: Array<{
    text: string;
    rowOffset: number;
    col: number;
  }>;
}

export function applicationWheelInput(
  lines: number,
  col: number,
  row: number,
  mouseTracking: boolean,
): string {
  if (lines === 0) return '';
  const count = Math.min(Math.abs(lines), 5);
  if (!mouseTracking) {
    return (lines < 0 ? '\x1b[A' : '\x1b[B').repeat(count);
  }
  const button = lines < 0 ? 64 : 65;
  const report = `\x1b[<${button};${Math.max(1, col)};${Math.max(1, row)}M`;
  return report.repeat(count);
}

export function createApplicationSelectionAnchor(
  range: TerminalSelectionRange,
  lineAtRow: (row: number) => string,
): ApplicationSelectionAnchor | null {
  const markers = [];
  for (let row = range.startRow; row <= range.endRow; row += 1) {
    const line = lineAtRow(row);
    const start = row === range.startRow ? range.startCol : 0;
    const end = row === range.endRow ? range.endCol : line.length;
    const segment = line.slice(start, end);
    const text = segment.trim();
    if (!text) continue;
    markers.push({
      text,
      rowOffset: row - range.startRow,
      col: start + segment.indexOf(text),
    });
  }
  if (markers.length === 0) return null;
  markers.sort((left, right) => right.text.length - left.text.length);
  return {
    startCol: range.startCol,
    endCol: range.endCol,
    rowSpan: range.endRow - range.startRow,
    markers,
  };
}

export function relocateApplicationSelection(
  anchor: ApplicationSelectionAnchor,
  visibleLines: string[],
  bufferStart: number,
  cols: number,
): TerminalSelectionRange | null {
  const primary = anchor.markers[0];
  for (let row = 0; row < visibleLines.length; row += 1) {
    let markerCol = visibleLines[row].indexOf(primary.text);
    while (markerCol >= 0) {
      const startRow = row - primary.rowOffset;
      const endRow = startRow + anchor.rowSpan;
      const colDelta = markerCol - primary.col;
      const matches = startRow >= 0 && endRow < visibleLines.length
        && anchor.markers.every((marker) => {
          const visibleRow = startRow + marker.rowOffset;
          return visibleLines[visibleRow]?.indexOf(marker.text, Math.max(0, marker.col + colDelta))
            === marker.col + colDelta;
        });
      if (matches) {
        return {
          startRow: bufferStart + startRow,
          startCol: Math.max(0, Math.min(cols, anchor.startCol + colDelta)),
          endRow: bufferStart + endRow,
          endCol: Math.max(0, Math.min(cols, anchor.endCol + colDelta)),
        };
      }
      markerCol = visibleLines[row].indexOf(primary.text, markerCol + 1);
    }
  }
  return null;
}

export function consumeWheelRows(
  deltaY: number,
  deltaMode: number,
  cellHeight: number,
  viewportRows: number,
  remainderRows: number,
): WheelRows {
  let deltaRows: number;
  switch (deltaMode) {
    case WHEEL_DELTA_LINE:
      deltaRows = deltaY;
      break;
    case WHEEL_DELTA_PAGE:
      deltaRows = deltaY * viewportRows;
      break;
    case WHEEL_DELTA_PIXEL:
    default:
      deltaRows = deltaY / Math.max(1, cellHeight);
      break;
  }

  const accumulatedRows = remainderRows + deltaRows;
  const lines = Math.abs(accumulatedRows) < 1
    ? 0
    : accumulatedRows > 0 ? Math.floor(accumulatedRows) : Math.ceil(accumulatedRows);
  return {
    lines,
    remainderRows: accumulatedRows - lines,
  };
}

export function offsetAfterWrite(
  viewportOffset: number,
  scrollbackBefore: number,
  scrollbackAfter: number,
): number {
  if (viewportOffset <= 0) {
    return 0;
  }
  const anchoredOffset = viewportOffset + scrollbackAfter - scrollbackBefore;
  return Math.max(0, Math.min(scrollbackAfter, anchoredOffset));
}

export function cursorRowInViewport(
  liveCursorRow: number,
  viewportOffset: number,
  viewportRows: number,
): number | null {
  const visibleRow = liveCursorRow + viewportOffset;
  return visibleRow >= 0 && visibleRow < viewportRows ? visibleRow : null;
}

export function viewportBufferStart(
  scrollbackLength: number,
  viewportOffset: number,
): number {
  return Math.max(0, scrollbackLength - viewportOffset);
}

export function bufferRowFromViewportRow(
  viewportRow: number,
  scrollbackLength: number,
  viewportOffset: number,
): number {
  return viewportBufferStart(scrollbackLength, viewportOffset) + viewportRow;
}

export function viewportRowFromBufferRow(
  bufferRow: number,
  scrollbackLength: number,
  viewportOffset: number,
): number {
  return bufferRow - viewportBufferStart(scrollbackLength, viewportOffset);
}
