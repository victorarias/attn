// Aligning an agent's message markdown to the terminal rows currently showing it.
//
// Painted rows are re-derived from the live buffer and never stored, so a width
// reflow costs nothing. Both sides reduce to word tokens carrying provenance —
// markdown offsets on one, row/column on the other — so the TUI's rendering
// transforms drop out by construction.
//
// The measurements behind every threshold here are in
// docs/decisions/2026-08-02-terminal-annotations-anchor-to-the-transcript.md.

// One word of the markdown; offsets are UTF-16 code units into the ORIGINAL.
export interface SrcToken {
  norm: string;
  start: number;
  end: number;
}

// One word of a rendered row. `row` is a BUFFER row: viewport rows shift under
// scroll and must never be stored. `col`/`endCol` are code-unit indices, equal
// to cell columns only without wide characters, which skews a wash's horizontal
// bounds but never which rows resolve.
export interface GridToken {
  norm: string;
  row: number;
  col: number;
  endCol: number;
  // The first prose token after an assistant marker. This breaks an otherwise
  // exact tie when the user's prompt quotes the response it requests.
  assistantLead: boolean;
}

// Markdown syntax and TUI chrome, stripped from both sides before comparison.
const CUT_CHARS = new Set([
  ...'*_`~#|>',
  ...'┌─┬┐│├┼┤└┴┘━┃┏┓┗┛',
  ...'⏺✻❯⎿·•▪●○',
]);
const ASSISTANT_MARKERS = new Set(['⏺', '•']);

function isSpace(ch: string): boolean {
  return /\s/.test(ch);
}

// A word that is pure syntax or chrome normalizes to '' and drops out.
function normalizeWord(word: string): string {
  let out = '';
  for (const ch of word) {
    if (!CUT_CHARS.has(ch)) out += ch;
  }
  return out;
}

export function tokenizeMarkdown(markdown: string): SrcToken[] {
  const out: SrcToken[] = [];
  let i = 0;
  while (i < markdown.length) {
    if (isSpace(markdown[i])) {
      i += 1;
      continue;
    }
    let j = i;
    while (j < markdown.length && !isSpace(markdown[j])) j += 1;
    const norm = normalizeWord(markdown.slice(i, j));
    if (norm) out.push({ norm, start: i, end: j });
    i = j;
  }
  return out;
}

// `rowBase` is the buffer row of `rows[0]`, so tokens carry absolute rows.
export function tokenizeRows(rows: readonly string[], rowBase = 0): GridToken[] {
  const out: GridToken[] = [];
  for (let r = 0; r < rows.length; r += 1) {
    const row = rows[r];
    const firstNonSpace = row.search(/\S/);
    const assistantRow = firstNonSpace >= 0 && ASSISTANT_MARKERS.has(row[firstNonSpace]);
    let emitted = 0;
    let i = 0;
    while (i < row.length) {
      if (isSpace(row[i])) {
        i += 1;
        continue;
      }
      let j = i;
      while (j < row.length && !isSpace(row[j])) j += 1;
      const norm = normalizeWord(row.slice(i, j));
      if (norm) {
        out.push({
          norm,
          row: rowBase + r,
          col: i,
          endCol: j,
          assistantLead: assistantRow && emitted === 0,
        });
        emitted += 1;
      }
      i = j;
    }
  }
  return out;
}

// Word-space form: markdown and grid text differ by exactly what is stripped.
export function normalizedWords(text: string): string {
  return tokenizeMarkdown(text)
    .map((t) => t.norm)
    .join(' ');
}

// --- pairing ---------------------------------------------------------------

export interface TokenPair {
  srcIdx: number;
  gridIdx: number;
}

// Look-ahead after a mismatch: a dropped glyph, not a different passage.
const RESYNC_WINDOW = 4;
// How many tokens of the message to score a candidate seed against.
const SEED_LOOKAHEAD = 8;
function resync(
  src: readonly SrcToken[],
  si: number,
  grid: readonly GridToken[],
  gi: number,
): { si: number; gi: number; cost: number } | null {
  for (let d = 1; d <= RESYNC_WINDOW; d += 1) {
    if (si + d < src.length && src[si + d].norm === grid[gi].norm) return { si: si + d, gi, cost: d };
    if (gi + d < grid.length && src[si].norm === grid[gi + d].norm) return { si, gi: gi + d, cost: d };
    if (si + d < src.length && gi + d < grid.length && src[si + d].norm === grid[gi + d].norm) {
      return { si: si + d, gi: gi + d, cost: d };
    }
  }
  return null;
}

function seedScore(src: readonly SrcToken[], grid: readonly GridToken[], gi: number): number {
  let score = 0;
  for (let d = 0; d < SEED_LOOKAHEAD && d < src.length && gi + d < grid.length; d += 1) {
    if (src[d].norm === grid[gi + d].norm) score += 1;
  }
  return score;
}

function positionsByNorm(tokens: readonly { norm: string }[]): Map<string, number[]> {
  const out = new Map<string, number[]>();
  for (let i = 0; i < tokens.length; i += 1) {
    const at = out.get(tokens[i].norm);
    if (at) at.push(i);
    else out.set(tokens[i].norm, [i]);
  }
  return out;
}

// Finds where the message sits in the grid using words unique on BOTH sides:
// each pins one (src, grid) pair, and the true position is the offset most
// agree on. Seeding on opening words fails when the head has scrolled away.
function findSeed(src: readonly SrcToken[], grid: readonly GridToken[]): { si: number; gi: number } | null {
  const srcPositions = positionsByNorm(src);
  const gridPositions = positionsByNorm(grid);

  const anchors: { si: number; gi: number }[] = [];
  const offsetVotes = new Map<number, number>();
  for (const [norm, at] of srcPositions) {
    if (at.length !== 1) continue;
    const gridAt = gridPositions.get(norm);
    if (!gridAt || gridAt.length !== 1) continue;
    const anchor = { si: at[0], gi: gridAt[0] };
    anchors.push(anchor);
    const offset = anchor.gi - anchor.si;
    offsetVotes.set(offset, (offsetVotes.get(offset) ?? 0) + 1);
  }

  if (anchors.length > 0) {
    let bestOffset = 0;
    let bestVotes = 0;
    for (const [offset, votes] of offsetVotes) {
      if (votes > bestVotes) {
        bestVotes = votes;
        bestOffset = offset;
      }
    }
    // The EXACT consensus offset, not merely near it: an echo of the message
    // above it pins anchors off the true diagonal, and admitting those hands the
    // seed to the echo. Drift is what `resync` is for.
    const onDiagonal = anchors
      .filter((anchor) => anchor.gi - anchor.si === bestOffset)
      .sort((a, b) => a.si - b.si);
    if (onDiagonal.length > 0) return onDiagonal[0];
  }

  // No word unique on both sides: fall back to the opening words.
  let gi = -1;
  let bestScore = 0;
  let bestAssistantLead = false;
  for (let j = 0; j < grid.length; j += 1) {
    if (grid[j].norm !== src[0].norm) continue;
    const score = seedScore(src, grid, j);
    const assistantLead = grid[j].assistantLead;
    if (score > bestScore || (score === bestScore && assistantLead && !bestAssistantLead)) {
      bestScore = score;
      bestAssistantLead = assistantLead;
      gi = j;
    }
  }
  return gi >= 0 ? { si: 0, gi } : null;
}

// Pairs the two token streams by walking both directions in lockstep from the
// seed. Monotone by construction, so a bad seed shows up as a low match ratio
// rather than a wrong span. Greedy O(n+m), not an LCS: repeated words can pair
// wrongly, which is safe only because `quotesAnchor` re-reads the live rows
// before anything is painted.
export function pairTokens(src: readonly SrcToken[], grid: readonly GridToken[]): TokenPair[] {
  if (src.length === 0 || grid.length === 0) return [];
  const seed = findSeed(src, grid);
  if (!seed) return [];

  const gapBudget = Math.max(RESYNC_WINDOW, Math.ceil(src.length * 0.5));

  // Backward from the seed: the message may start before the seed word.
  const before: TokenPair[] = [];
  let si = seed.si;
  let gi = seed.gi;
  let gaps = 0;
  while (si >= 0 && gi >= 0) {
    if (src[si].norm === grid[gi].norm) {
      before.push({ srcIdx: si, gridIdx: gi });
      si -= 1;
      gi -= 1;
      continue;
    }
    let resynced = false;
    for (let d = 1; d <= RESYNC_WINDOW && !resynced; d += 1) {
      if (si - d >= 0 && src[si - d].norm === grid[gi].norm) {
        si -= d; gaps += d; resynced = true;
      } else if (gi - d >= 0 && src[si].norm === grid[gi - d].norm) {
        gi -= d; gaps += d; resynced = true;
      } else if (si - d >= 0 && gi - d >= 0 && src[si - d].norm === grid[gi - d].norm) {
        si -= d; gi -= d; gaps += d; resynced = true;
      }
    }
    if (!resynced || gaps > gapBudget) break;
  }
  before.reverse();

  // Forward from just after the seed.
  const after: TokenPair[] = [];
  si = seed.si + 1;
  gi = seed.gi + 1;
  gaps = 0;
  while (si < src.length && gi < grid.length) {
    if (src[si].norm === grid[gi].norm) {
      after.push({ srcIdx: si, gridIdx: gi });
      si += 1;
      gi += 1;
      continue;
    }
    const next = resync(src, si, grid, gi);
    if (!next) break;
    gaps += next.cost;
    if (gaps > gapBudget) break;
    si = next.si;
    gi = next.gi;
  }

  return [...before, ...after];
}

// --- alignment -------------------------------------------------------------

// One word of the message, located on a row.
export interface PlacedWord {
  start: number;
  end: number;
  // Code-unit columns within the row's text.
  col: number;
  endCol: number;
}

export interface RowAlignment {
  row: number;
  words: PlacedWord[];
  // Rows below CONFIDENT_ROW are ignored when bounding and resolving.
  matched: number;
  total: number;
}

// Alignment share for a row to count; neighbouring user turns chance-match.
export const CONFIDENT_ROW = 0.6;

export function rowConfidence(row: RowAlignment): number {
  return row.total === 0 ? 0 : row.matched / row.total;
}

export interface MessageAlignment {
  markdown: string;
  // Keyed by BUFFER row, for rows that resolved at least one word.
  rows: Map<number, RowAlignment>;
  // Bounds from confident rows only; -1 when the message did not resolve.
  firstRow: number;
  lastRow: number;
  inversions: number;
  srcTokens: number;
  gridTokens: number;
  pairs: number;
}

export function alignMessage(
  markdown: string,
  rows: readonly string[],
  rowBase = 0,
): MessageAlignment {
  const src = tokenizeMarkdown(markdown);
  const grid = tokenizeRows(rows, rowBase);
  const pairs = pairTokens(src, grid);

  const totals = new Map<number, number>();
  for (const token of grid) totals.set(token.row, (totals.get(token.row) ?? 0) + 1);

  const aligned = new Map<number, RowAlignment>();
  for (const pair of pairs) {
    const gridToken = grid[pair.gridIdx];
    const srcToken = src[pair.srcIdx];
    let row = aligned.get(gridToken.row);
    if (!row) {
      row = { row: gridToken.row, words: [], matched: 0, total: totals.get(gridToken.row) ?? 0 };
      aligned.set(gridToken.row, row);
    }
    row.words.push({
      start: srcToken.start,
      end: srcToken.end,
      col: gridToken.col,
      endCol: gridToken.endCol,
    });
    row.matched += 1;
  }

  let firstRow = -1;
  let lastRow = -1;
  let inversions = 0;
  let previousStart = -1;
  for (const row of [...aligned.keys()].sort((a, b) => a - b)) {
    const entry = aligned.get(row)!;
    if (rowConfidence(entry) < CONFIDENT_ROW) continue;
    const start = entry.words[0].start;
    if (firstRow < 0) firstRow = row;
    lastRow = row;
    if (previousStart >= 0 && start < previousStart) inversions += 1;
    previousStart = start;
  }

  return {
    markdown,
    rows: aligned,
    firstRow,
    lastRow,
    inversions,
    srcTokens: src.length,
    gridTokens: grid.length,
    pairs: pairs.length,
  };
}

// --- forward: markdown offsets → rows ---------------------------------------

// One row's worth of a wash, in buffer-row/code-unit-column space.
export interface RowRange {
  row: number;
  startCol: number;
  endCol: number;
}

// Which rows show the markdown range `[start, end)`, and where on each.
export function rowsForOffsets(
  alignment: MessageAlignment,
  start: number,
  end: number,
): RowRange[] {
  const out: RowRange[] = [];
  for (const [row, entry] of alignment.rows) {
    if (rowConfidence(entry) < CONFIDENT_ROW) continue;
    let startCol = Number.POSITIVE_INFINITY;
    let endCol = -1;
    for (const word of entry.words) {
      if (word.end <= start || word.start >= end) continue;
      if (word.col < startCol) startCol = word.col;
      if (word.endCol > endCol) endCol = word.endCol;
    }
    if (endCol < 0) continue;
    out.push({ row, startCol, endCol });
  }
  return out.sort((a, b) => a.row - b.row);
}

// --- reverse: rows → markdown offsets ---------------------------------------

export interface AnchoredSpan {
  start: number;
  end: number;
}

// The markdown range a selected grid region corresponds to; whatever it returns
// is quoted back to the agent verbatim, so it refuses rather than guesses when
// the selection covers no confidently-aligned words.
export function offsetsForSelection(
  alignment: MessageAlignment,
  selection: { startRow: number; startCol: number; endRow: number; endCol: number },
): AnchoredSpan | null {
  let start = Number.POSITIVE_INFINITY;
  let end = -1;
  for (const [row, entry] of alignment.rows) {
    if (row < selection.startRow || row > selection.endRow) continue;
    if (rowConfidence(entry) < CONFIDENT_ROW) continue;
    const lowCol = row === selection.startRow ? selection.startCol : 0;
    const highCol = row === selection.endRow ? selection.endCol : Number.POSITIVE_INFINITY;
    for (const word of entry.words) {
      // Any overlap selects the whole word: anchors are word-granular, and a
      // half-word anchor could not be quoted.
      if (word.endCol <= lowCol || word.col >= highCol) continue;
      if (word.start < start) start = word.start;
      if (word.end > end) end = word.end;
    }
  }
  if (end < 0) return null;
  return { start, end };
}

// --- the containment gate ---------------------------------------------------

// Whether the text now at the rows about to be painted still quotes the anchor.
// Deliberately does NOT consult the aligner, so a mistaken alignment can only
// make a wash disappear for a frame. Containment holds in either direction: a
// reflow moves words between rows, so the resolved rows can cover more or less.
export function quotesAnchor(anchoredText: string, paintedText: string): boolean {
  const want = normalizedWords(anchoredText);
  if (!want) return false;
  const got = normalizedWords(paintedText);
  if (!got) return false;
  return got.includes(want) || want.includes(got);
}
