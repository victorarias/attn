// Find-in-scrollback scanning for the Ghostty terminal.
//
// Modeled on Warp's chunked async find: the scan walks the buffer in row
// chunks and yields to the event loop between chunks so a full-scrollback
// search never blocks input or rendering. No row text is cached — scrollback
// is byte-capped and a text mirror would double terminal memory.

export interface FindMatch {
  bufferRow: number;
  startCol: number;
  endCol: number;
}

export interface FindRowAccess {
  totalRows(): number;
  rowText(bufferRow: number): string;
}

export interface FindScanOptions {
  caseSensitive: boolean;
  // Rows scanned per chunk before yielding. Exposed for tests.
  chunkRows?: number;
}

export const FIND_SCAN_CHUNK_ROWS = 2000;

export function matchesInRowText(
  text: string,
  query: string,
  caseSensitive: boolean,
  bufferRow: number,
): FindMatch[] {
  if (!query) return [];
  const haystack = caseSensitive ? text : text.toLowerCase();
  const needle = caseSensitive ? query : query.toLowerCase();
  const matches: FindMatch[] = [];
  let from = 0;
  for (;;) {
    const index = haystack.indexOf(needle, from);
    if (index === -1) break;
    matches.push({ bufferRow, startCol: index, endCol: index + needle.length });
    from = index + Math.max(1, needle.length);
  }
  return matches;
}

export interface FindScanHandle {
  cancel(): void;
}

// Scan all rows ascending, invoking onProgress with the accumulated, sorted
// match list after each chunk and onDone exactly once unless cancelled.
export function startFindScan(
  access: FindRowAccess,
  query: string,
  options: FindScanOptions,
  onProgress: (matches: FindMatch[]) => void,
  onDone: (matches: FindMatch[]) => void,
): FindScanHandle {
  let cancelled = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const chunkRows = options.chunkRows ?? FIND_SCAN_CHUNK_ROWS;
  const matches: FindMatch[] = [];
  let row = 0;

  const step = () => {
    if (cancelled) return;
    const total = access.totalRows();
    const end = Math.min(total, row + chunkRows);
    for (; row < end; row += 1) {
      const rowMatches = matchesInRowText(access.rowText(row), query, options.caseSensitive, row);
      if (rowMatches.length > 0) matches.push(...rowMatches);
    }
    if (row >= access.totalRows()) {
      onDone(matches);
      return;
    }
    onProgress(matches);
    timer = setTimeout(step, 0);
  };

  if (!query) {
    // Empty query resolves immediately with no matches.
    timer = setTimeout(() => onDone(matches), 0);
  } else {
    step();
  }

  return {
    cancel() {
      cancelled = true;
      if (timer) clearTimeout(timer);
    },
  };
}

// Index of the match to focus when a scan completes: the last match at or
// above the bottom of the buffer (terminals search "backwards from the end").
export function initialFocusedMatch(matches: FindMatch[]): number {
  return matches.length - 1;
}

// The subset of matches visible in [firstRow, firstRow + rowCount), located
// with binary search so per-frame overlay work stays O(log n + visible).
export function visibleMatches(matches: FindMatch[], firstRow: number, rowCount: number): FindMatch[] {
  if (matches.length === 0 || rowCount <= 0) return [];
  let low = 0;
  let high = matches.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (matches[mid].bufferRow < firstRow) low = mid + 1;
    else high = mid;
  }
  const visible: FindMatch[] = [];
  for (let i = low; i < matches.length && matches[i].bufferRow < firstRow + rowCount; i += 1) {
    visible.push(matches[i]);
  }
  return visible;
}
