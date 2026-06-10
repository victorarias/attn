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
  if (text.includes('/')) return true;
  if (text.startsWith('~')) return true;
  return /\.[A-Za-z0-9_]{1,8}$/.test(text);
}

// Candidate paths inside a hovered fragment, most-specific first, capped.
// "(src/a.go:12:3)" yields src/a.go with line 12 / column 3; the column range
// covers the visible path text (including the :line:col suffix) for underlining.
export function pathCandidatesForFragment(fragment: string, fragmentStartCol: number): PathCandidate[] {
  let lead = 0;
  while (lead < fragment.length - 1 && LEADING_WRAPPERS.includes(fragment[lead])) lead += 1;
  let core = fragment.slice(lead);
  const noise = core.match(TRAILING_NOISE_RE);
  if (noise) core = core.slice(0, core.length - noise[0].length);
  // A trailing period is far more often sentence punctuation than a path char.
  while (core.endsWith('.')) core = core.slice(0, -1);
  if (!core) return [];

  const candidates: PathCandidate[] = [];
  const startCol = fragmentStartCol + lead;
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
