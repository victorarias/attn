// Markdown is not prefix-stable: the same characters mean different things
// depending on what arrives after them. A half-typed `` `0x1B `` is a literal
// backtick until its partner lands, and a table's header row is a paragraph
// until the delimiter row below it does. Rendering a growing message verbatim
// therefore shows the reader raw syntax that restyles a frame later.
//
// Receipt (2026-08-19, two recorded nisse streams replayed delta by delta
// through this app's own react-markdown stack): a 7,845-char reply over 317
// deltas leaked a bare backtick in 21 prefixes, `**` in 1, and a table header
// in 3; a 27,540-char reply over 1,364 deltas leaked 55, 14 and 4. Everything
// ABOVE the open tail was already stable — the worst prefix rewrote 280 bytes
// of a 33 KB document — so the whole defect lives in the last line.
//
// This closes those constructs speculatively on a copy of the text, so the
// tail renders as what it is about to become and never has to change its mind.
// It runs only while a message streams; a settled message is rendered verbatim,
// which is what makes the streamed-then-closed DOM identical to rendering the
// final text at once.

/** The info string a still-open mermaid fence is rewritten to. */
export const PENDING_DIAGRAM_LANGUAGE = 'attn-pending-diagram';

const FENCE = /^(\s*)(`{3,}|~{3,})(.*)$/;
/** A line that is only block-marker punctuation: the first frame of a heading,
 *  list item, quote or rule, before the character that gives it meaning. */
const BARE_MARKER = /^\s*(#{1,6}|[-*+>]|\d{1,9}[.)]|\|)\s*$/;
const TABLE_ROW = /^\s*\|/;
const TABLE_DELIMITER = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/;
/** The delimiter row mid-arrival: `|`, `|--`, `|---|`, `|---|:-`. */
const PARTIAL_TABLE_DELIMITER = /^\s*\|[\s:|-]*$/;

interface FenceState {
  /** Index of the opening line of a fence that never closed. */
  openedAt: number;
  marker: string;
  language: string;
}

function openFence(lines: string[]): FenceState | null {
  let open: FenceState | null = null;
  for (let i = 0; i < lines.length; i++) {
    const match = FENCE.exec(lines[i]);
    if (!match) continue;
    const [, , marker, info] = match;
    if (open === null) {
      open = { openedAt: i, marker, language: info.trim().split(/\s+/)[0] ?? '' };
    } else if (marker[0] === open.marker[0] && marker.length >= open.marker.length && info.trim() === '') {
      open = null;
    }
  }
  return open;
}

/**
 * Closes the inline constructs the last line left hanging.
 *
 * Only the delimiters measured to leak are handled — a code span, `**`, `__`,
 * `~~`, and a link whose `)` has not arrived. A single `*` or `_` is left
 * alone: it is as often multiplication or a filename as it is emphasis, and
 * guessing wrong shows the reader a word in italics that never was.
 */
function closeInline(line: string): string {
  let out = line;

  // Code spans first: everything inside one is literal, so a `**` there is not
  // an unclosed opener. Walk backtick runs and pair them off.
  const runs: Array<{ index: number; length: number }> = [];
  for (let i = 0; i < out.length; i++) {
    if (out[i] !== '`') continue;
    let j = i;
    while (j < out.length && out[j] === '`') j++;
    runs.push({ index: i, length: j - i });
    i = j - 1;
  }
  const openRun = unpairedRun(runs);
  const codeRanges: Array<[number, number]> = [];
  for (let i = 0; i + 1 < runs.length; i += 1) {
    if (runs[i].length === runs[i + 1].length) {
      codeRanges.push([runs[i].index, runs[i + 1].index + runs[i + 1].length]);
      i += 1;
    }
  }
  if (openRun !== null) {
    // An opener with no content yet would render as an empty code span that
    // pops back open on the next delta; hold it until it has something to hold.
    if (openRun.index + openRun.length === out.length) return out.slice(0, openRun.index);
    return `${out}${'`'.repeat(openRun.length)}`;
  }

  const outside = (index: number) => !codeRanges.some(([start, end]) => index >= start && index < end);
  for (const delimiter of ['**', '__', '~~']) {
    let last = -1;
    let count = 0;
    for (let i = 0; i + delimiter.length <= out.length; i++) {
      if (out.startsWith(delimiter, i) && outside(i)) { count++; last = i; i += delimiter.length - 1; }
    }
    if (count % 2 === 0) continue;
    // Same bargain as an empty code span: an opener with nothing after it would
    // close onto itself and render as literal `****`.
    out = last + delimiter.length === out.length
      ? out.slice(0, last)
      : `${out}${delimiter}`;
  }

  // `[text](url` — literal until the paren closes.
  const linkOpen = out.lastIndexOf('](');
  if (linkOpen !== -1 && outside(linkOpen) && out.indexOf(')', linkOpen) === -1) out = `${out})`;
  return out;
}

function unpairedRun(runs: Array<{ index: number; length: number }>): { index: number; length: number } | null {
  const stack: Array<{ index: number; length: number }> = [];
  for (const run of runs) {
    const match = stack.findIndex((candidate) => candidate.length === run.length);
    if (match === -1) stack.push(run);
    else stack.splice(match, 1);
  }
  return stack.length > 0 ? stack[stack.length - 1] : null;
}

/** Cells in a GFM row, for the delimiter row a streaming header still lacks. */
function cellCount(row: string): number {
  const trimmed = row.trim().replace(/^\|/, '').replace(/\|$/, '');
  return trimmed.split('|').length;
}

/**
 * The text to render for a message that is still arriving.
 *
 * The returned string is only ever the input plus completions at its end, so
 * every character the model has sent is already on screen — nothing is held
 * back except a construct marker that carries no content yet.
 */
export function prepareStreamingMarkdown(text: string): string {
  if (text === '') return text;
  const lines = text.split('\n');
  const fence = openFence(lines);

  if (fence) {
    // An open mermaid fence must not reach the diagram renderer: half a graph
    // is a parse error drawn on screen. Renaming the language is what routes it
    // to the pending placeholder until the closing fence arrives.
    if (fence.language === 'mermaid') {
      lines[fence.openedAt] = lines[fence.openedAt].replace(/mermaid/, PENDING_DIAGRAM_LANGUAGE);
    }
    // Terminating the block keeps the document well formed. CommonMark already
    // runs an unclosed fence to the end of input, so this changes nothing about
    // what is shown — it only spares the parser the recovery path.
    return `${lines.join('\n')}\n${fence.marker}`;
  }

  const lastIndex = lines.length - 1;
  const last = lines[lastIndex];
  if (last === '') return text;

  // A line that is nothing but a marker has no content to show yet, and showing
  // it renders a literal `#` or `-` that becomes a heading or a bullet one
  // delta later. Waiting those ~40ms costs the reader nothing.
  if (BARE_MARKER.test(last)) return lines.slice(0, lastIndex).join('\n');

  // Reaching here with a fence on the last line means it is a CLOSING one —
  // an opening fence would have been the open-fence case above. Its backticks
  // are block syntax, and the inline pass would read them as a code span.
  if (FENCE.test(last)) return text;

  const previousLine = lastIndex > 0 ? lines[lastIndex - 1] : '';
  // Inline first: a half-open code span inside a table cell is still a leak,
  // and closing it cannot change which block the line belongs to.
  lines[lastIndex] = closeInline(last);
  const tail = lines[lastIndex];

  // GFM matches the delimiter row against the header's width, so a delimiter
  // that is complete but too narrow is still not a table.
  if (PARTIAL_TABLE_DELIMITER.test(tail) && TABLE_ROW.test(previousLine)
      && cellCount(tail) < cellCount(previousLine)) {
    lines[lastIndex] = `|${' --- |'.repeat(cellCount(previousLine))}`;
    return lines.join('\n');
  }

  if (TABLE_ROW.test(tail) && cellCount(tail) >= 2
      && !TABLE_ROW.test(previousLine) && !TABLE_DELIMITER.test(previousLine)) {
    // The header row alone is a paragraph; with a delimiter under it, it is
    // the table it is about to be. The real delimiter row replaces this one
    // on the next delta and the DOM does not move.
    return `${lines.join('\n')}\n|${' --- |'.repeat(cellCount(tail))}`;
  }

  return lines.join('\n');
}

// The re-parse bill, and how it is paid.
//
// react-markdown reparses the whole message on every delta. Measured on the
// recorded 27,540-char reply (node, renderToStaticMarkup, M-series): parse
// 8.84 ms, mdast→hast +0.5 ms, React elements +1.1 ms — so the PARSE is the
// bill, and memoizing rendered blocks would buy almost nothing. What buys
// everything is not handing the parser the settled prefix again.
//
// A cut is only safe where no construct can reach across it. Two qualify, and
// both are common in agent output: the blank line after a closed fenced code
// block, and a blank line before a column-0 ATX heading. A loose list's blank
// line does NOT qualify — `- a\n\n- b` is one list — so it is never cut.
// Link reference definitions and footnotes do reach across, so a document
// carrying one is left whole.

const HEADING_AT_MARGIN = /^#{1,6} /;
const CLOSING_FENCE = /^(`{3,}|~{3,})\s*$/;
const OPENING_FENCE = /^(`{3,}|~{3,})/;
const CROSS_REFERENCING = /^\[[^\]]+\]:|\[\^[^\]]+\]/m;

export interface SplitMarkdown {
  /** Blocks that can no longer change. Stable across deltas, so it is memoized. */
  settled: string;
  /** The open end of the document. Only this is reparsed per delta. */
  tail: string;
}

/**
 * Splits a streaming message into a settled head and the open tail.
 *
 * `settled` only ever grows, and `settled + '\n' + tail` is the input, so the
 * two rendered halves concatenate to exactly what rendering the whole would
 * have produced. Returns an empty head when nothing can be cut safely.
 */
export function splitStreamingMarkdown(text: string): SplitMarkdown {
  if (CROSS_REFERENCING.test(text)) return { settled: '', tail: text };
  const lines = text.split('\n');
  let cut = -1;
  let fence: string | null = null;
  for (let i = 0; i < lines.length - 1; i++) {
    const line = lines[i];
    if (fence !== null) {
      if (CLOSING_FENCE.test(line) && line.startsWith(fence)) {
        fence = null;
        // The blank line after a closed fence: nothing joins across it.
        if (lines[i + 1] === '') cut = i + 1;
      }
      continue;
    }
    const opening = OPENING_FENCE.exec(line);
    if (opening) { fence = opening[1]; continue; }
    // A heading at the margin, with a blank line above it, cannot be a lazy
    // continuation or a list item's child — so everything above is finished.
    if (i > 0 && lines[i - 1] === '' && HEADING_AT_MARGIN.test(line)) cut = i - 1;
  }
  if (cut <= 0) return { settled: '', tail: text };
  return { settled: lines.slice(0, cut).join('\n'), tail: lines.slice(cut + 1).join('\n') };
}
