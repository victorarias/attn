// Filtered view of a command block's output (Warp's "filter block output").
//
// The PTY grid is authoritative and rows cannot be hidden in place, so the
// filter renders as a DOM panel listing the matching output lines. Lines come
// from extractBlock (re-anchored, correct-or-absent); this module only
// filters and segments the extracted text.

import { matchesInRowText } from './terminalFind';

export interface FilteredBlockLine {
  // 0-based line offset within the block's output (maps to a buffer row via
  // the block's outputStartRow + re-anchor delta).
  lineOffset: number;
  text: string;
  ranges: Array<{ startCol: number; endCol: number }>;
}

export function filterBlockOutputLines(
  output: string,
  query: string,
  caseSensitive: boolean,
): FilteredBlockLine[] {
  if (!query || !output) return [];
  const filtered: FilteredBlockLine[] = [];
  const lines = output.split('\n');
  for (let lineOffset = 0; lineOffset < lines.length; lineOffset += 1) {
    const matches = matchesInRowText(lines[lineOffset], query, caseSensitive, lineOffset);
    if (matches.length === 0) continue;
    filtered.push({
      lineOffset,
      text: lines[lineOffset],
      ranges: matches.map((match) => ({ startCol: match.startCol, endCol: match.endCol })),
    });
  }
  return filtered;
}

// Split a matching line into plain/highlighted segments for rendering.
export function lineSegments(line: FilteredBlockLine): Array<{ text: string; match: boolean }> {
  const segments: Array<{ text: string; match: boolean }> = [];
  let cursor = 0;
  for (const range of line.ranges) {
    if (range.startCol > cursor) segments.push({ text: line.text.slice(cursor, range.startCol), match: false });
    segments.push({ text: line.text.slice(range.startCol, range.endCol), match: true });
    cursor = range.endCol;
  }
  if (cursor < line.text.length) segments.push({ text: line.text.slice(cursor), match: false });
  return segments;
}
