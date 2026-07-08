// Pure detection helpers for clickable links and file paths in terminal rows.
//
// Detection is hover-lazy (modeled on Warp's link detection): callers only ever
// analyze the word fragment under the pointer, cache the fragment range, and
// re-detect when the pointer enters a different fragment. Nothing here scans
// scrollback or runs per-write.

export const URL_RE = /\b(?:https?:\/\/|file:\/\/|mailto:|ftp:\/\/|ssh:\/\/|git:\/\/|tel:|magnet:|gemini:\/\/|gopher:\/\/|news:)[^\s<>()]+/g;

export interface ColumnRange {
  startCol: number;
  endCol: number;
}

export interface UrlAtColumn extends ColumnRange {
  uri: string;
}

export function urlAtColumn(line: string, col: number): UrlAtColumn | null {
  for (const match of line.matchAll(URL_RE)) {
    const start = match.index ?? -1;
    const uri = match[0].replace(/[.,;:!?]+$/, '');
    if (col >= start && col < start + uri.length) {
      return { uri, startCol: start, endCol: start + uri.length };
    }
  }
  return null;
}

// An OSC 8 hyperlink's visible label can contain spaces or arbitrary text, so
// its range can't be derived from the line text (unlike urlAtColumn/fragmentAtColumn).
// uriAtIndex resolves the hidden URI at a logical index; the range is the run
// of indices around `index` that resolve to the SAME uri — this stops the
// scan at a boundary between two adjacent but distinct links.
export function hyperlinkRangeAt(
  uriAtIndex: (index: number) => string | null,
  index: number,
  length: number,
): UrlAtColumn | null {
  const uri = uriAtIndex(index);
  if (!uri) return null;
  let startCol = index;
  while (startCol > 0 && uriAtIndex(startCol - 1) === uri) startCol -= 1;
  let endCol = index + 1;
  while (endCol < length && uriAtIndex(endCol) === uri) endCol += 1;
  return { uri, startCol, endCol };
}

// A fragment is the run of non-whitespace characters around a column — the
// unit of hover caching. Pointer movement inside one fragment must cost
// nothing, so the boundary must be derivable from the line text alone.
export function fragmentAtColumn(line: string, col: number): ColumnRange | null {
  const character = line[col];
  if (!character || /\s/.test(character)) return null;
  let startCol = col;
  while (startCol > 0 && !/\s/.test(line[startCol - 1])) startCol -= 1;
  let endCol = col + 1;
  while (endCol < line.length && !/\s/.test(line[endCol])) endCol += 1;
  return { startCol, endCol };
}

export interface PathCandidate extends ColumnRange {
  path: string;
  line?: number;
  column?: number;
}

const LEADING_WRAPPERS = '([{<"\'`';
const TRAILING_NOISE_RE = /['")\]}>,;!?]+$/;
const LINE_COL_RE = /:(\d{1,7})(?:[:.](\d{1,7}))?:?$/;

function looksLikePath(text: string): boolean {
  if (!text || text.includes('://')) return false;
  // `//host/...` is a URL remainder (scheme stripped at a mid-fragment start),
  // not a filesystem path.
  if (text.startsWith('//')) return false;
  if (text.includes('/')) return true;
  if (text.startsWith('~')) return true;
  return /\.[A-Za-z0-9_]{1,8}$/.test(text);
}

// Candidate paths inside a hovered fragment, most-specific first, capped.
// "(src/a.go:12:3)" yields src/a.go with line 12 / column 3; the column range
// covers the visible path text (including the :line:col suffix) for underlining.
export function pathCandidatesForFragment(fragment: string, fragmentStartCol: number): PathCandidate[] {
  const candidates: PathCandidate[] = [];
  const pushFrom = (offset: number) => {
    let core = fragment.slice(offset);
    const noise = core.match(TRAILING_NOISE_RE);
    if (noise) core = core.slice(0, core.length - noise[0].length);
    // A trailing period is far more often sentence punctuation than a path char.
    while (core.endsWith('.')) core = core.slice(0, -1);
    if (!core) return;
    const startCol = fragmentStartCol + offset;
    const endCol = startCol + core.length;
    const lineCol = core.match(LINE_COL_RE);
    if (lineCol && lineCol.index !== undefined && lineCol.index > 0) {
      candidates.push({
        path: core.slice(0, lineCol.index),
        line: Number.parseInt(lineCol[1], 10),
        column: lineCol[2] ? Number.parseInt(lineCol[2], 10) : undefined,
        startCol,
        endCol,
      });
    }
    candidates.push({ path: core, startCol, endCol });
  };

  let lead = 0;
  while (lead < fragment.length - 1 && LEADING_WRAPPERS.includes(fragment[lead])) lead += 1;
  pushFrom(lead);
  // Paths often start mid-fragment after a non-path character — `Read(/abs/x`,
  // `--file=/etc/x`, `path=~/y` — where the prefix is not a strippable wrapper.
  // Add the first such start as a fallback candidate.
  for (let i = lead + 1; i < fragment.length; i += 1) {
    const character = fragment[i];
    if ((character === '/' || character === '~') && !/[A-Za-z0-9._~-]/.test(fragment[i - 1])) {
      pushFrom(i);
      break;
    }
  }
  return candidates.filter((candidate) => looksLikePath(candidate.path)).slice(0, 4);
}

// Resolve a detected path to an absolute path without touching the filesystem.
// Existence checks are the caller's job (they are async and cached).
export function resolveDetectedPath(path: string, cwd?: string, home?: string): string | null {
  let resolved: string;
  if (path.startsWith('/')) {
    resolved = path;
  } else if (path === '~' || path.startsWith('~/')) {
    if (!home) return null;
    resolved = home.replace(/\/$/, '') + path.slice(1);
  } else if (path.startsWith('~')) {
    // ~user expansion is not supported.
    return null;
  } else {
    if (!cwd) return null;
    resolved = `${cwd.replace(/\/$/, '')}/${path}`;
  }
  // Normalize ./ and ../ segments so openPath receives a clean path.
  const segments: string[] = [];
  for (const segment of resolved.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (segments.length === 0) return null;
      segments.pop();
      continue;
    }
    segments.push(segment);
  }
  return `/${segments.join('/')}`;
}

export interface DetectedTerminalLink extends ColumnRange {
  kind: 'url' | 'path';
  // url
  uri?: string;
  // path
  absolutePath?: string;
  line?: number;
  column?: number;
}

// --- Cross-wrap logical lines ---
//
// A path or URL can soft-wrap across visual rows. Joining the rows of a
// wrapped group into one logical line lets the single-line detectors above
// work unchanged: every row is padded to the grid width, so logical index i
// maps exactly to (firstRow + floor(i / cols), i % cols) and back.

export interface LogicalLine {
  // Joined text; every row but the last padded with spaces to `cols`.
  text: string;
  // First joined viewport row.
  firstRow: number;
  rowCount: number;
  cols: number;
}

// A path spanning more rows than this would be hundreds of characters long;
// the cap bounds hover work, not correctness for realistic content.
export const MAX_WRAP_JOIN_ROWS = 6;

// Join the soft-wrapped row group containing `row` into a logical line.
// isContinuationRow(r) answers "does row r continue the line started on row
// r-1" (ghostty's isRowWrapped semantics). Rows outside [0, rowCount) are
// never touched; groups larger than the cap keep the rows nearest the start
// of the budget (paths begin above the hovered row more often than below).
export function logicalLineAt(
  rowTextAt: (viewportRow: number) => string,
  isContinuationRow: (viewportRow: number) => boolean,
  row: number,
  cols: number,
  rowCount: number,
): LogicalLine {
  let first = row;
  while (first > 0 && row - first < MAX_WRAP_JOIN_ROWS - 1 && isContinuationRow(first)) first -= 1;
  let last = row;
  while (last + 1 < rowCount && last - first < MAX_WRAP_JOIN_ROWS - 1 && isContinuationRow(last + 1)) last += 1;
  const parts: string[] = [];
  for (let current = first; current <= last; current += 1) {
    const text = rowTextAt(current);
    parts.push(current < last ? text.padEnd(cols, ' ') : text);
  }
  return { text: parts.join(''), firstRow: first, rowCount: last - first + 1, cols };
}

export function logicalIndexForCell(line: LogicalLine, row: number, col: number): number | null {
  if (row < line.firstRow || row >= line.firstRow + line.rowCount) return null;
  if (col < 0 || col >= line.cols) return null;
  return (row - line.firstRow) * line.cols + col;
}

// Selection-semantics span over viewport rows: rows strictly between startRow
// and endRow cover the full grid width (matches WebGlOverlay).
export interface LogicalSpan {
  startRow: number;
  startCol: number;
  endRow: number;
  endCol: number;
}

export function spanFromLogicalRange(line: LogicalLine, startIndex: number, endIndex: number): LogicalSpan {
  const lastIndex = Math.max(startIndex, endIndex - 1);
  return {
    startRow: line.firstRow + Math.floor(startIndex / line.cols),
    startCol: startIndex % line.cols,
    endRow: line.firstRow + Math.floor(lastIndex / line.cols),
    endCol: (lastIndex % line.cols) + 1,
  };
}
