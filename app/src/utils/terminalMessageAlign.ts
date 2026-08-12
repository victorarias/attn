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
  aliases: string[];
  start: number;
  end: number;
}

// One word of a rendered row. `row` is a BUFFER row: viewport rows shift under
// scroll and must never be stored. `col`/`endCol` are code-unit indices, equal
// to cell columns only without wide characters, which skews a wash's horizontal
// bounds but never which rows resolve.
export interface GridToken {
  norm: string;
  aliases: string[];
  row: number;
  col: number;
  endCol: number;
  // The first prose token after an assistant marker. This breaks an otherwise
  // exact tie when the user's prompt quotes the response it requests.
  assistantLead: boolean;
  explicitUser: boolean;
}

interface MatchToken {
  norm: string;
  aliases: readonly string[];
}

// Markdown syntax and TUI chrome, stripped from both sides before comparison.
const CUT_CHARS = new Set([
  ...'*_`~#|>',
  ...'┌─┬┐│├┼┤└┴┘━┃┏┓┗┛',
  ...'⏺✻❯⎿·•▪●○',
]);
const ASSISTANT_MARKERS = new Set(['⏺', '•']);
const USER_MARKERS = new Set(['❯', '›']);

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

const MARKDOWN_LINK_RE = /\[([^\]\n]+)\]\(([^)\n]+)\)/g;
const TRAILING_PATH_NOISE_RE = /[),.;:!?]+$/;
const LINE_COLUMN_SUFFIX_RE = /:\d{1,7}(?::\d{1,7})?$/;

function decodeTarget(value: string): string {
  const trimmed = value.trim().replace(/^<|>$/g, '');
  const withoutFileScheme = trimmed.replace(/^file:\/\/(?:localhost)?/i, '');
  try {
    return decodeURIComponent(withoutFileScheme);
  } catch {
    return withoutFileScheme;
  }
}

// Renderer-independent identities for a path or URL. Codex commonly turns an
// absolute Markdown destination into a shorter visible repository path; the
// basename and final two components survive both forms. These are candidates,
// not proof — uniqueness, monotonicity, row confidence, and the containment
// gate still decide whether a match is usable.
function targetAliases(value: string): string[] {
  let target = decodeTarget(value).replace(TRAILING_PATH_NOISE_RE, '');
  if (!target.includes('/') && !/^[a-z][a-z0-9+.-]*:/i.test(target)) return [];
  target = target.replace(/\\/g, '/').replace(/\/+$/, '');
  if (!target) return [];

  const aliases = new Set<string>([`target:${target}`]);
  const withoutQuery = target.replace(/[?#].*$/, '');
  const parts = withoutQuery.split('/').filter(Boolean);
  const basename = parts[parts.length - 1];
  if (basename) {
    aliases.add(`path:${basename}`);
    aliases.add(`path:${basename.replace(LINE_COLUMN_SUFFIX_RE, '')}`);
  }
  if (parts.length >= 2) aliases.add(`path2:${parts.slice(-2).join('/')}`);
  return [...aliases];
}

function tokenKeys(token: MatchToken): readonly string[] {
  return [token.norm, ...token.aliases];
}

function tokensMatch(left: MatchToken, right: MatchToken): boolean {
  const rightKeys = new Set(tokenKeys(right));
  return tokenKeys(left).some((key) => rightKeys.has(key));
}

function pushWords(out: SrcToken[], text: string, base: number): void {
  let i = 0;
  while (i < text.length) {
    if (isSpace(text[i])) {
      i += 1;
      continue;
    }
    let j = i;
    while (j < text.length && !isSpace(text[j])) j += 1;
    const raw = text.slice(i, j);
    const norm = normalizeWord(raw);
    if (norm) out.push({ norm, aliases: targetAliases(raw), start: base + i, end: base + j });
    i = j;
  }
}

export function tokenizeMarkdown(markdown: string): SrcToken[] {
  const out: SrcToken[] = [];
  let cursor = 0;
  for (const match of markdown.matchAll(MARKDOWN_LINK_RE)) {
    const start = match.index ?? 0;
    pushWords(out, markdown.slice(cursor, start), cursor);

    const label = match[1];
    const target = match[2].trim().split(/\s+['"]/)[0];
    const labelStart = start + 1;
    pushWords(out, label, labelStart);

    const targetInMatch = match[0].indexOf(match[2]);
    const targetStart = start + targetInMatch + match[2].indexOf(target);
    const norm = normalizeWord(target);
    if (norm) {
      out.push({
        norm,
        aliases: targetAliases(target),
        start: targetStart,
        end: targetStart + target.length,
      });
    }
    cursor = start + match[0].length;
  }
  pushWords(out, markdown.slice(cursor), cursor);
  return out;
}

// `rowBase` is the buffer row of `rows[0]`, so tokens carry absolute rows.
export function tokenizeRows(
  rows: readonly string[],
  rowBase = 0,
  hyperlinkUriAt?: (row: number, col: number) => string | null,
): GridToken[] {
  const out: GridToken[] = [];
  for (let r = 0; r < rows.length; r += 1) {
    const row = rows[r];
    const firstNonSpace = row.search(/\S/);
    const marker = firstNonSpace >= 0 ? row[firstNonSpace] : '';
    const assistantRow = ASSISTANT_MARKERS.has(marker);
    const userRow = USER_MARKERS.has(marker);
    let emitted = 0;
    let i = 0;
    while (i < row.length) {
      if (isSpace(row[i])) {
        i += 1;
        continue;
      }
      let j = i;
      while (j < row.length && !isSpace(row[j])) j += 1;
      const raw = row.slice(i, j);
      const norm = normalizeWord(raw);
      if (norm) {
        const aliases = new Set(targetAliases(raw));
        const uri = hyperlinkUriAt?.(rowBase + r, i);
        if (uri) targetAliases(uri).forEach((alias) => aliases.add(alias));
        out.push({
          norm,
          aliases: [...aliases],
          row: rowBase + r,
          col: i,
          endCol: j,
          assistantLead: assistantRow && emitted === 0,
          explicitUser: userRow,
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
    if (si + d < src.length && tokensMatch(src[si + d], grid[gi])) return { si: si + d, gi, cost: d };
    if (gi + d < grid.length && tokensMatch(src[si], grid[gi + d])) return { si, gi: gi + d, cost: d };
    if (si + d < src.length && gi + d < grid.length && tokensMatch(src[si + d], grid[gi + d])) {
      return { si: si + d, gi: gi + d, cost: d };
    }
  }
  return null;
}

function seedScore(src: readonly SrcToken[], grid: readonly GridToken[], gi: number): number {
  let score = 0;
  for (let d = 0; d < SEED_LOOKAHEAD && d < src.length && gi + d < grid.length; d += 1) {
    if (tokensMatch(src[d], grid[gi + d])) score += 1;
  }
  return score;
}

function positionsByKey(tokens: readonly MatchToken[]): Map<string, number[]> {
  const out = new Map<string, number[]>();
  for (let i = 0; i < tokens.length; i += 1) {
    for (const key of new Set(tokenKeys(tokens[i]))) {
      const at = out.get(key);
      if (at) at.push(i);
      else out.set(key, [i]);
    }
  }
  return out;
}

// Unique identities on both sides are hard anchors. Their longest monotone
// chain divides the message into independent alignment islands, so an arbitrary
// renderer substitution (a Markdown link becoming a path, for example) leaves
// a sparse hole rather than cutting off everything after it.
function monotoneAnchors(src: readonly SrcToken[], grid: readonly GridToken[]): TokenPair[] {
  const srcPositions = positionsByKey(src);
  const gridPositions = positionsByKey(grid);
  type Candidate = TokenPair & { strength: number; score: number; previous: number };
  const uniquePairs = new Map<string, Candidate>();
  for (const [key, srcAt] of srcPositions) {
    if (srcAt.length !== 1) continue;
    const gridAt = gridPositions.get(key);
    if (!gridAt || gridAt.length !== 1) continue;
    const pair = { srcIdx: srcAt[0], gridIdx: gridAt[0] };
    const strength = src[pair.srcIdx].norm === grid[pair.gridIdx].norm ? 2 : 1;
    const pairKey = `${pair.srcIdx}:${pair.gridIdx}`;
    const previous = uniquePairs.get(pairKey);
    if (!previous || strength > previous.strength) {
      uniquePairs.set(pairKey, { ...pair, strength, score: 0, previous: -1 });
    }
  }

  const candidates = [...uniquePairs.values()].sort(
    (a, b) => a.srcIdx - b.srcIdx || a.gridIdx - b.gridIdx || b.strength - a.strength,
  );
  if (candidates.length === 0) return [];

  // Fenwick maximum over grid positions: query(gridIdx) sees only positions
  // strictly before gridIdx. Updates are delayed for one source-index batch so
  // a chain cannot use two alternative identities of the same source token.
  const fenwick = Array.from(
    { length: grid.length + 1 },
    () => ({ score: 0, candidate: -1 }),
  );
  const query = (exclusive: number) => {
    let best = { score: 0, candidate: -1 };
    for (let at = exclusive; at > 0; at -= at & -at) {
      if (fenwick[at].score > best.score) best = fenwick[at];
    }
    return best;
  };
  const update = (position: number, value: { score: number; candidate: number }) => {
    for (let at = position; at < fenwick.length; at += at & -at) {
      if (value.score > fenwick[at].score) fenwick[at] = value;
    }
  };

  const pairWeight = 2 * candidates.length + 1;
  let bestCandidate = -1;
  for (let batchStart = 0; batchStart < candidates.length;) {
    let batchEnd = batchStart + 1;
    while (batchEnd < candidates.length && candidates[batchEnd].srcIdx === candidates[batchStart].srcIdx) {
      batchEnd += 1;
    }
    for (let i = batchStart; i < batchEnd; i += 1) {
      const prior = query(candidates[i].gridIdx);
      candidates[i].previous = prior.candidate;
      candidates[i].score = prior.score + pairWeight + candidates[i].strength;
      if (bestCandidate < 0 || candidates[i].score > candidates[bestCandidate].score) bestCandidate = i;
    }
    for (let i = batchStart; i < batchEnd; i += 1) {
      update(candidates[i].gridIdx + 1, { score: candidates[i].score, candidate: i });
    }
    batchStart = batchEnd;
  }

  const chain: TokenPair[] = [];
  for (let at = bestCandidate; at >= 0; at = candidates[at].previous) {
    chain.push({ srcIdx: candidates[at].srcIdx, gridIdx: candidates[at].gridIdx });
  }
  return chain.reverse();
}

// Finds where the message sits in the grid using words unique on BOTH sides:
// each pins one (src, grid) pair, and the true position is the offset most
// agree on. Seeding on opening words fails when the head has scrolled away.
function findSeed(src: readonly SrcToken[], grid: readonly GridToken[]): { si: number; gi: number } | null {
  const srcPositions = positionsByKey(src);
  const gridPositions = positionsByKey(grid);

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
    if (!tokensMatch(src[0], grid[j])) continue;
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

function greedySegment(
  src: readonly SrcToken[],
  grid: readonly GridToken[],
  srcStart: number,
  srcEnd: number,
  gridStart: number,
  gridEnd: number,
): TokenPair[] {
  const pairs: TokenPair[] = [];
  let si = srcStart;
  let gi = gridStart;
  let gaps = 0;
  const gapBudget = Math.max(RESYNC_WINDOW, Math.ceil((srcEnd - srcStart) * 0.5));
  while (si < srcEnd && gi < gridEnd) {
    if (tokensMatch(src[si], grid[gi])) {
      pairs.push({ srcIdx: si, gridIdx: gi });
      si += 1;
      gi += 1;
      continue;
    }
    let next: { si: number; gi: number; cost: number } | null = null;
    for (let d = 1; d <= RESYNC_WINDOW; d += 1) {
      if (si + d < srcEnd && tokensMatch(src[si + d], grid[gi])) {
        next = { si: si + d, gi, cost: d };
        break;
      }
      if (gi + d < gridEnd && tokensMatch(src[si], grid[gi + d])) {
        next = { si, gi: gi + d, cost: d };
        break;
      }
      if (si + d < srcEnd && gi + d < gridEnd && tokensMatch(src[si + d], grid[gi + d])) {
        next = { si: si + d, gi: gi + d, cost: d };
        break;
      }
    }
    if (!next) break;
    gaps += next.cost;
    if (gaps > gapBudget) break;
    si = next.si;
    gi = next.gi;
  }
  return pairs;
}

// Pairs the two token streams by walking both directions in lockstep from the
// seed. Monotone by construction, so a bad seed shows up as a low match ratio
// rather than a wrong span. Greedy O(n+m), not an LCS: repeated words can pair
// wrongly, which is safe only because `quotesAnchor` re-reads the live rows
// before anything is painted.
export function pairTokens(src: readonly SrcToken[], grid: readonly GridToken[]): TokenPair[] {
  if (src.length === 0 || grid.length === 0) return [];
  const anchors = monotoneAnchors(src, grid);
  if (anchors.length > 0) {
    const pairs: TokenPair[] = [];
    let srcStart = 0;
    let gridStart = 0;
    for (const anchor of anchors) {
      pairs.push(...greedySegment(src, grid, srcStart, anchor.srcIdx, gridStart, anchor.gridIdx));
      pairs.push(anchor);
      srcStart = anchor.srcIdx + 1;
      gridStart = anchor.gridIdx + 1;
    }
    pairs.push(...greedySegment(src, grid, srcStart, src.length, gridStart, grid.length));
    return pairs;
  }
  const seed = findSeed(src, grid);
  if (!seed) return [];

  const gapBudget = Math.max(RESYNC_WINDOW, Math.ceil(src.length * 0.5));

  // Backward from the seed: the message may start before the seed word.
  const before: TokenPair[] = [];
  let si = seed.si;
  let gi = seed.gi;
  let gaps = 0;
  while (si >= 0 && gi >= 0) {
    if (tokensMatch(src[si], grid[gi])) {
      before.push({ srcIdx: si, gridIdx: gi });
      si -= 1;
      gi -= 1;
      continue;
    }
    let resynced = false;
    for (let d = 1; d <= RESYNC_WINDOW && !resynced; d += 1) {
      if (si - d >= 0 && tokensMatch(src[si - d], grid[gi])) {
        si -= d; gaps += d; resynced = true;
      } else if (gi - d >= 0 && tokensMatch(src[si], grid[gi - d])) {
        gi -= d; gaps += d; resynced = true;
      } else if (si - d >= 0 && gi - d >= 0 && tokensMatch(src[si - d], grid[gi - d])) {
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
    if (tokensMatch(src[si], grid[gi])) {
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
  explicitUser: boolean;
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
  hyperlinkUriAt?: (row: number, col: number) => string | null,
): MessageAlignment {
  const src = tokenizeMarkdown(markdown);
  const grid = tokenizeRows(rows, rowBase, hyperlinkUriAt);
  const pairs = pairTokens(src, grid);

  const totals = new Map<number, number>();
  for (const token of grid) totals.set(token.row, (totals.get(token.row) ?? 0) + 1);

  const aligned = new Map<number, RowAlignment>();
  for (const pair of pairs) {
    const gridToken = grid[pair.gridIdx];
    const srcToken = src[pair.srcIdx];
    let row = aligned.get(gridToken.row);
    if (!row) {
      row = {
        row: gridToken.row,
        words: [],
        explicitUser: gridToken.explicitUser,
        matched: 0,
        total: totals.get(gridToken.row) ?? 0,
      };
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
    if (entry.explicitUser || rowConfidence(entry) < CONFIDENT_ROW) continue;
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
    if (entry.explicitUser || rowConfidence(entry) < CONFIDENT_ROW) continue;
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
    if (entry.explicitUser || rowConfidence(entry) < CONFIDENT_ROW) continue;
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
  const want = tokenizeMarkdown(anchoredText);
  if (want.length === 0) return false;
  const got = tokenizeRows(paintedText.split('\n'));
  if (got.length === 0) return false;

  const contains = (whole: readonly MatchToken[], part: readonly MatchToken[]): boolean => {
    if (part.length > whole.length) return false;
    for (let start = 0; start <= whole.length - part.length; start += 1) {
      if (part.every((token, index) => tokensMatch(token, whole[start + index]))) return true;
    }
    return false;
  };
  return contains(got, want) || contains(want, got);
}
