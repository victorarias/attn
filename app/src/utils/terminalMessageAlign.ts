// Aligning an agent's message markdown to the terminal rows currently showing it.
//
// The transcript span is the anchor; the grid is a projection of it. Annotations
// live on offsets into the message's markdown, and the rows they paint over are
// re-derived from the live buffer on demand and never stored. That is what makes
// a width reflow cost nothing — the case `TerminalBlockStore` gives up on
// outright, clearing itself on any width change.
//
// Both sides reduce to word tokens carrying provenance: markdown offsets on one
// side, row/column on the other. Every rendering transform the agent's TUI
// applies then becomes irrelevant by construction — a heading loses its `#` on
// both sides, a soft wrap is just a word boundary, a code fence is an unmatched
// token that gets skipped.
//
// The measurements behind every threshold here are in
// docs/decisions/2026-08-02-terminal-annotations-anchor-to-the-transcript.md.

// One word of the markdown, carrying its offsets in the ORIGINAL markdown so a
// resolved span can be sliced back verbatim. Offsets are UTF-16 code-unit
// indices (what `String.prototype.slice` takes).
export interface SrcToken {
  norm: string;
  start: number;
  end: number;
}

// One word of a rendered row. `row` is a BUFFER row (scrollback-absolute), not a
// viewport row — viewport rows shift under scroll and must never be stored.
//
// `col`/`endCol` are code-unit indices into the row's text, which equal cell
// columns only while the row holds no wide (CJK/emoji) characters. That skews
// the horizontal bounds of a wash on such a row; it never affects which rows
// resolve, nor what text is quoted back.
export interface GridToken {
  norm: string;
  row: number;
  col: number;
  endCol: number;
}

// Markdown syntax and TUI chrome that never survives into — or never appears in
// — the other side's text. Stripped from both sides before comparison.
const CUT_CHARS = new Set([
  ...'*_`~#|>',
  ...'┌─┬┐│├┼┤└┴┘━┃┏┓┗┛',
  ...'⏺✻❯⎿·▪●○',
]);

function isSpace(ch: string): boolean {
  return /\s/.test(ch);
}

// Reduces a raw word to its comparable form. A word that is pure syntax or
// chrome normalizes to '' and drops out of the stream entirely.
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

// `rowBase` is the buffer row index of `rows[0]`, so emitted tokens carry
// absolute buffer rows even when only a window of the buffer was read.
export function tokenizeRows(rows: readonly string[], rowBase = 0): GridToken[] {
  const out: GridToken[] = [];
  for (let r = 0; r < rows.length; r += 1) {
    const row = rows[r];
    let i = 0;
    while (i < row.length) {
      if (isSpace(row[i])) {
        i += 1;
        continue;
      }
      let j = i;
      while (j < row.length && !isSpace(row[j])) j += 1;
      const norm = normalizeWord(row.slice(i, j));
      if (norm) out.push({ norm, row: rowBase + r, col: i, endCol: j });
      i = j;
    }
  }
  return out;
}

// Word-space form of an arbitrary string. The containment gate compares the
// agent's markdown against text read off the grid, and those differ by exactly
// the syntax the tokenizer strips (`**bold**` on one side, `bold` on the other).
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

// How far ahead to look for the next agreement after a mismatch, on either side.
// Covers a dropped marker glyph, a hyphenated wrap, or a word the TUI truncated
// — not a genuinely different passage.
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

// Finds where the message sits in the grid using words that occur exactly once
// on BOTH sides. Each such word pins one (src, grid) index pair, and the
// message's true position is the offset most of them agree on.
//
// Seeding on the message's opening words instead is wrong in the case that
// matters most: an agent TUI keeps a bounded screen, so the head of a long
// message is routinely gone while its tail is still visible and worth annotating.
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
    // Seed on the EXACT consensus offset, not merely near it. Text above the
    // message routinely echoes its opening words — the user asking about them,
    // or an earlier turn repeating them — and such an echo pins an anchor a few
    // positions off the true diagonal. Admitting it and then taking the earliest
    // anchor by source index hands the seed to the echo, and the walk starves
    // there instead of finding the message. Drift is what `resync` is for.
    const onDiagonal = anchors
      .filter((anchor) => anchor.gi - anchor.si === bestOffset)
      .sort((a, b) => a.si - b.si);
    if (onDiagonal.length > 0) return onDiagonal[0];
  }

  // No word is unique on both sides (a very short or very repetitive message).
  // Fall back to the opening words, scored by lookahead.
  let gi = -1;
  let bestScore = 0;
  for (let j = 0; j < grid.length; j += 1) {
    if (grid[j].norm !== src[0].norm) continue;
    const score = seedScore(src, grid, j);
    if (score > bestScore) {
      bestScore = score;
      gi = j;
    }
  }
  return gi >= 0 ? { si: 0, gi } : null;
}

// Pairs the two token streams by walking both directions in lockstep from the
// seed. Monotone by construction, so it can never pair a later source word to an
// earlier row: row-order inversions can only come from a bad seed, and those
// surface as a low match ratio rather than as a wrong span.
//
// This is a greedy O(n+m) walk rather than an LCS, and is therefore wrong where
// an LCS would have disambiguated repeated words. That is a safe trade only
// because `quotesAnchor` re-reads the text actually sitting at the resolved rows
// before anything is painted — a bad pairing can cause a refusal, never a wrong
// paint.
export function pairTokens(src: readonly SrcToken[], grid: readonly GridToken[]): TokenPair[] {
  if (src.length === 0 || grid.length === 0) return [];
  const seed = findSeed(src, grid);
  if (!seed) return [];

  const gapBudget = Math.max(RESYNC_WINDOW, Math.ceil(src.length * 0.5));

  // Backward from the seed: the message may start before the seed word, or
  // before the first token the grid still holds.
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
  // Markdown offsets.
  start: number;
  end: number;
  // Code-unit columns within the row's text.
  col: number;
  endCol: number;
}

export interface RowAlignment {
  row: number;
  words: PlacedWord[];
  // How many of the row's words aligned, out of how many it has. Rows below
  // CONFIDENT_ROW are ignored when bounding the span and when resolving offsets.
  matched: number;
  total: number;
}

// The share of a row's words that must align for the row to count as part of the
// message. The user's own turns sit directly above and below it and share enough
// common English to pick up chance matches; letting those set the boundary would
// light up text the agent never wrote.
export const CONFIDENT_ROW = 0.6;

export function rowConfidence(row: RowAlignment): number {
  return row.total === 0 ? 0 : row.matched / row.total;
}

export interface MessageAlignment {
  markdown: string;
  // Keyed by BUFFER row, for rows that resolved at least one word.
  rows: Map<number, RowAlignment>;
  // Bounds of the message on the grid, from confident rows only. -1 when the
  // message did not resolve at all.
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

// One row's worth of a wash, in buffer-row and code-unit-column space.
export interface RowRange {
  row: number;
  startCol: number;
  endCol: number;
}

// Which rows currently show the markdown range `[start, end)`, and where on each
// of them. This is the operation that makes an anchor survive a reflow: rows are
// re-derived, never stored.
//
// Returns an empty array when nothing confident resolves — the message has
// scrolled away, has been repainted, or was never on this grid.
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

// The markdown range a selected region of the grid corresponds to. This is what
// turns a drag into an anchor, and it is the direction the product depends on
// most: whatever it returns is quoted back to the agent verbatim.
//
// Refuses (returns null) rather than guessing when the selection covers no
// confidently-aligned words. A selection that runs off the end of the message
// resolves to the part that is inside it.
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
      // A word counts as selected when the selection overlaps any of it, so
      // dragging through the middle of a word takes the whole word. Anchors are
      // word-granular by construction; a half-word anchor could not be quoted.
      if (word.endCol <= lowCol || word.col >= highCol) continue;
      if (word.start < start) start = word.start;
      if (word.end > end) end = word.end;
    }
  }
  if (end < 0) return null;
  return { start, end };
}

// --- the containment gate ---------------------------------------------------

// Whether the text now sitting at the rows about to be painted is still the text
// the annotation was anchored to.
//
// This deliberately does NOT consult the aligner: it compares the anchored
// markdown against text read straight off the live grid. A stale or mistaken
// alignment therefore cannot get a wash onto the screen — the worst it can do is
// make one disappear for a frame.
//
// Containment holds in either direction because a reflow moves words between
// rows, so the resolved rows can cover slightly more or slightly less than the
// anchor. Row-for-row equality would refuse constantly and prove nothing extra.
export function quotesAnchor(anchoredText: string, paintedText: string): boolean {
  const want = normalizedWords(anchoredText);
  if (!want) return false;
  const got = normalizedWords(paintedText);
  if (!got) return false;
  return got.includes(want) || want.includes(got);
}
