import { describe, expect, it } from 'vitest';
import {
  alignMessage,
  CONFIDENT_ROW,
  offsetsForSelection,
  quotesAnchor,
  rowConfidence,
  rowsForOffsets,
  tokenizeMarkdown,
} from './terminalMessageAlign';

// Renders markdown the way an agent TUI does: a marker glyph on the first row,
// a two-space hanging indent on continuations, soft-wrapped at `cols`. Grids are
// generated rather than hand-typed so that a reflow test actually reflows.
function render(markdown: string, cols: number, marker = '⏺ '): string[] {
  const indent = ' '.repeat(marker.length);
  const rows: string[] = [];
  for (const paragraph of markdown.split('\n')) {
    let line = rows.length === 0 ? marker : indent;
    let empty = true;
    for (const word of paragraph.split(/\s+/).filter(Boolean)) {
      if (!empty && line.length + 1 + word.length > cols) {
        rows.push(line);
        line = indent;
        empty = true;
      }
      line += empty ? word : ` ${word}`;
      empty = false;
    }
    rows.push(line);
  }
  return rows;
}

const MESSAGE = 'A soft line wrap moves overflowing text onto the next visual line '
  + 'without inserting a real line break. The wrap can shift when the available '
  + 'width changes.';

const ANCHOR = 'visual line without inserting a real';

const LINKED_MESSAGE = [
  'The paths below are genuine rejection evidence from the agent.',
  '',
  'Examples: [pipeline routing](/Users/tester/src/services-pilot/audiobook-ingestion/ais-pipeline-1/FileProcessingService.java:118), '
    + '[audio validation](/Users/tester/src/services-pilot/audiobook-ingestion/ais-audio-choreography/AudioServiceImpl.java:227), '
    + '[image validation](/Users/tester/src/services-pilot/audiobook-ingestion/ais-image-choreography/ImageServiceImpl.java:88), '
    + '[ZIP handling](/Users/tester/src/services-pilot/audiobook-ingestion/ais-zip-choreography/UnpackerImpl.java:176).',
  '',
  'The conclusion after those rendered links must remain independently annotatable.',
].join('\n');

const LINKED_ROWS = [
  '• The paths below are genuine rejection evidence from the agent.',
  '',
  '  Examples: audiobook-ingestion/ais-pipeline-1/FileProcessingService.java:118,',
  '  audiobook-ingestion/ais-audio-choreography/AudioServiceImpl.java:227,',
  '  audiobook-ingestion/ais-image-choreography/ImageServiceImpl.java:88,',
  '  audiobook-ingestion/ais-zip-choreography/UnpackerImpl.java:176.',
  '',
  '  The conclusion after those rendered links must remain independently annotatable.',
];

// A URL link, which Codex renders as the label followed by the target in
// parentheses — two visible words where the Markdown holds one. Trimmed from a
// real turn whose numbered list stopped being annotatable at exactly this point.
const URL_MESSAGE = [
  'My recommendation:',
  '',
  '1. Close [ASTERISK-1797](https://spotify.atlassian.net/browse/ASTERISK-1797) as obsolete / won’t do.',
  '2. Explain that the RFC deliberately preserves the Findaway compatibility format and that migration',
  '   routing already uses an explicit feed ID.',
].join('\n');

const URL_ROWS = [
  '• My recommendation:',
  '',
  '  1. Close ASTERISK-1797 (https://spotify.atlassian.net/browse/ASTERISK-1797) as obsolete / won’t do.',
  '  2. Explain that the RFC deliberately preserves the Findaway compatibility format and that migration',
  '     routing already uses an explicit feed ID.',
];

// The message with its head scrolled off, under a user turn whose tail repeats
// the words immediately preceding the visible head.
const DECOY_ABOVE = [
  '❯ can you please rewrite that onto the next visual',
  ...render(MESSAGE, 62).slice(1),
];

function anchorOffsets(markdown: string, phrase: string) {
  const start = markdown.indexOf(phrase);
  expect(start).toBeGreaterThanOrEqual(0);
  return { start, end: start + phrase.length };
}

function textAt(rows: readonly string[], rowBase: number, ranges: { row: number; startCol: number; endCol: number }[]) {
  return ranges.map((range) => rows[range.row - rowBase].slice(range.startCol, range.endCol)).join('\n');
}

// Word-space form: markdown and grid text differ by exactly what is stripped.
function normalizedWords(text: string): string {
  return tokenizeMarkdown(text).map((token) => token.norm).join(' ');
}

describe('rowsForOffsets', () => {
  it('resolves an anchor onto the rows currently showing it', () => {
    const rows = render(MESSAGE, 62);
    const alignment = alignMessage(MESSAGE, rows);
    const { start, end } = anchorOffsets(MESSAGE, ANCHOR);

    const ranges = rowsForOffsets(alignment, start, end);

    expect(ranges.length).toBeGreaterThan(0);
    expect(normalizedWords(textAt(rows, 0, ranges))).toBe(normalizedWords(ANCHOR));
  });

  it('re-derives onto different rows after a width reflow, still quoting the anchor', () => {
    const { start, end } = anchorOffsets(MESSAGE, ANCHOR);
    const wide = render(MESSAGE, 62);
    const narrow = render(MESSAGE, 34);

    const wideRanges = rowsForOffsets(alignMessage(MESSAGE, wide), start, end);
    const narrowRanges = rowsForOffsets(alignMessage(MESSAGE, narrow), start, end);

    // The reflow has to have actually moved the anchor, or this proves nothing.
    expect(narrow.length).toBeGreaterThan(wide.length);
    expect(narrowRanges.map((r) => r.row)).not.toEqual(wideRanges.map((r) => r.row));

    expect(normalizedWords(textAt(wide, 0, wideRanges))).toBe(normalizedWords(ANCHOR));
    expect(normalizedWords(textAt(narrow, 0, narrowRanges))).toBe(normalizedWords(ANCHOR));
  });

  it('carries buffer rows through, so scrollback offsets do not shift the anchor', () => {
    const rows = render(MESSAGE, 62);
    const { start, end } = anchorOffsets(MESSAGE, ANCHOR);

    const atZero = rowsForOffsets(alignMessage(MESSAGE, rows, 0), start, end);
    const atDepth = rowsForOffsets(alignMessage(MESSAGE, rows, 900), start, end);

    expect(atDepth.map((r) => r.row)).toEqual(atZero.map((r) => r.row + 900));
    expect(normalizedWords(textAt(rows, 900, atDepth))).toBe(normalizedWords(ANCHOR));
  });

  it('resolves nothing when the message is not on the grid', () => {
    const rows = render('Something else entirely, about unrelated matters.', 62);
    const alignment = alignMessage(MESSAGE, rows);
    const { start, end } = anchorOffsets(MESSAGE, ANCHOR);

    expect(rowsForOffsets(alignment, start, end)).toEqual([]);
  });

  it('does not let a neighbouring user turn widen the span by chance', () => {
    // The case that produces a wrong span in practice: the head of the message
    // has scrolled off, so the aligner still has unmatched source tokens when it
    // walks upwards — and the row immediately above ends with the very words
    // that precede the visible head, so the walk flows straight into it. Four of
    // that row's nine words match: real agreement, but well under the floor.
    const alignment = alignMessage(MESSAGE, DECOY_ABOVE);

    expect(alignment.rows.get(0)?.matched).toBe(4);
    expect(rowConfidence(alignment.rows.get(0)!)).toBeLessThan(CONFIDENT_ROW);
    expect(alignment.firstRow).toBe(1);
    expect(alignment.inversions).toBe(0);
  });

  it('never resolves an anchor onto a row it is not confident about', () => {
    const alignment = alignMessage(MESSAGE, DECOY_ABOVE);
    const { start, end } = anchorOffsets(MESSAGE, 'onto the next visual');

    // Those words are only on the user's row now. Resolving them there would
    // paint a wash over the user's own text and attribute it to the agent.
    expect(rowsForOffsets(alignment, start, end).map((r) => r.row)).not.toContain(0);
  });

  it('keeps prose on both sides of a run of rendered Markdown links', () => {
    const alignment = alignMessage(LINKED_MESSAGE, LINKED_ROWS);
    const before = anchorOffsets(LINKED_MESSAGE, 'genuine rejection evidence');
    const after = anchorOffsets(LINKED_MESSAGE, 'conclusion after those rendered links');

    expect(rowsForOffsets(alignment, before.start, before.end)).not.toEqual([]);
    expect(rowsForOffsets(alignment, after.start, after.end)).not.toEqual([]);
  });

  it('keeps the list going after a URL rendered as label plus parenthesised target', () => {
    const alignment = alignMessage(URL_MESSAGE, URL_ROWS);
    const onTheLinkRow = anchorOffsets(URL_MESSAGE, 'as obsolete');
    const below = anchorOffsets(URL_MESSAGE, 'preserves the Findaway compatibility format');

    expect(rowsForOffsets(alignment, onTheLinkRow.start, onTheLinkRow.end)).not.toEqual([]);
    const ranges = rowsForOffsets(alignment, below.start, below.end);
    expect(ranges).not.toEqual([]);
    expect(quotesAnchor(
      URL_MESSAGE.slice(below.start, below.end),
      textAt(URL_ROWS, 0, ranges),
    )).toBe(true);
  });

  it('maps a shortened visible path back to the Markdown link destination', () => {
    const alignment = alignMessage(LINKED_MESSAGE, LINKED_ROWS);
    const target = anchorOffsets(
      LINKED_MESSAGE,
      '/Users/tester/src/services-pilot/audiobook-ingestion/ais-audio-choreography/AudioServiceImpl.java:227',
    );
    const ranges = rowsForOffsets(alignment, target.start, target.end);

    expect(ranges).toHaveLength(1);
    expect(textAt(LINKED_ROWS, 0, ranges)).toContain('AudioServiceImpl.java:227');
    expect(quotesAnchor(
      LINKED_MESSAGE.slice(target.start, target.end),
      textAt(LINKED_ROWS, 0, ranges),
    )).toBe(true);
  });

  it('keeps links with the same basename distinct by their parent directory', () => {
    const markdown = 'Compare [one](/repo/alpha/Shared.java:42) with '
      + '[two](/repo/beta/Shared.java:42), then keep reading.';
    const rows = [
      '• Compare alpha/Shared.java:42 with beta/Shared.java:42, then keep reading.',
    ];
    const alignment = alignMessage(markdown, rows);

    for (const target of ['/repo/alpha/Shared.java:42', '/repo/beta/Shared.java:42']) {
      const offsets = anchorOffsets(markdown, target);
      const ranges = rowsForOffsets(alignment, offsets.start, offsets.end);
      expect(ranges).toHaveLength(1);
      expect(textAt(rows, 0, ranges)).toContain(target.split('/').slice(-2).join('/'));
    }
  });

  it('does not treat a basename match under the wrong directory as a path anchor', () => {
    const markdown = 'Before [source](/repo/right/Shared.java:42), the middle and after stay mapped.';
    const rows = ['• Before wrong/Shared.java:42, the middle and after stay mapped.'];
    const alignment = alignMessage(markdown, rows);
    const target = anchorOffsets(markdown, '/repo/right/Shared.java:42');
    const after = anchorOffsets(markdown, 'middle and after stay mapped');

    expect(rowsForOffsets(alignment, target.start, target.end)).toEqual([]);
    expect(rowsForOffsets(alignment, after.start, after.end)).not.toEqual([]);
    expect(quotesAnchor('/repo/right/Shared.java:42', 'wrong/Shared.java:42')).toBe(false);
  });

  it('prefers an exact visible link label when OSC 8 also exposes its target', () => {
    const markdown = 'See [implementation](/Users/tester/src/Thing.java:44) for the invariant.';
    const rows = ['• See implementation for the invariant.'];
    const alignment = alignMessage(
      markdown,
      rows,
      0,
      (_row, col) => (col >= rows[0].indexOf('implementation') ? 'file:///Users/tester/src/Thing.java:44' : null),
    );
    const startCol = rows[0].indexOf('implementation');
    const span = offsetsForSelection(alignment, {
      startRow: 0,
      startCol,
      endRow: 0,
      endCol: startCol + 'implementation'.length,
    });

    expect(span).not.toBeNull();
    expect(markdown.slice(span!.start, span!.end)).toBe('implementation');
  });

  it('seeds on the message even when the row above echoes its opening words', () => {
    // An echo pins alignment anchors a few positions off the true diagonal.
    // Seeding on one of those strands the walk on the echo, and the message
    // resolves nowhere at all.
    const full = render(MESSAGE, 62);
    const rows = [
      '❯ hmm can you redo that A soft line wrap explanation please thanks',
      ...full.slice(1),
    ];
    const alignment = alignMessage(MESSAGE, rows);

    expect(alignment.firstRow).toBe(1);
    expect(alignment.lastRow).toBe(rows.length - 1);
  });
});

describe('offsetsForSelection', () => {
  it('prefers Codex’s assistant row when the prompt quotes the requested response', () => {
    const message = 'A retry wrapper protects idempotent operations from duplicate network effects.';
    const rows = [
      '› Before using a tool, write exactly this sentence as',
      '  ordinary prose: A retry wrapper protects idempotent',
      '  operations from duplicate network effects.',
      '',
      ...render(message, 54, '• '),
    ];
    const alignment = alignMessage(message, rows);
    const assistantRow = rows.findIndex((row) => row.startsWith('• '));
    const startCol = rows[assistantRow].indexOf('retry');

    const span = offsetsForSelection(alignment, {
      startRow: assistantRow,
      startCol,
      endRow: assistantRow,
      endCol: rows[assistantRow].indexOf('idempotent') + 'idempotent'.length,
    });

    expect(span).not.toBeNull();
    expect(message.slice(span!.start, span!.end)).toBe('retry wrapper protects idempotent');
  });

  it('turns a drag over the grid back into the markdown the agent wrote', () => {
    const rows = render(MESSAGE, 62);
    const alignment = alignMessage(MESSAGE, rows);
    const targetRow = rows.findIndex((row) => row.includes('visual'));
    const startCol = rows[targetRow].indexOf('visual');

    const span = offsetsForSelection(alignment, {
      startRow: targetRow,
      startCol,
      endRow: targetRow + 1,
      endCol: rows[targetRow + 1].indexOf('real') + 'real'.length,
    });

    expect(span).not.toBeNull();
    expect(MESSAGE.slice(span!.start, span!.end)).toBe(ANCHOR);
  });

  it('takes whole words when the drag ends mid-word', () => {
    const rows = render(MESSAGE, 62);
    const alignment = alignMessage(MESSAGE, rows);
    const targetRow = rows.findIndex((row) => row.includes('visual'));
    const startCol = rows[targetRow].indexOf('visual');

    const span = offsetsForSelection(alignment, {
      startRow: targetRow,
      startCol: startCol + 2,
      endRow: targetRow,
      endCol: startCol + 'visual'.length - 2,
    });

    // A half-word anchor could not be quoted back, so the word is taken whole.
    expect(MESSAGE.slice(span!.start, span!.end)).toBe('visual');
  });

  it('refuses a selection that covers none of the message', () => {
    const rows = [
      '› Use /skills to list available skills',
      ...render(MESSAGE, 62),
    ];
    const alignment = alignMessage(MESSAGE, rows);

    expect(offsetsForSelection(alignment, { startRow: 0, startCol: 0, endRow: 0, endCol: 40 })).toBeNull();
  });
});

describe('quotesAnchor', () => {
  it('accepts the anchored words read back off the grid', () => {
    expect(quotesAnchor(ANCHOR, '  visual line\n  without inserting a real')).toBe(true);
  });

  it('accepts markdown syntax that the TUI strips on the way to the screen', () => {
    expect(quotesAnchor('a **real** line `break`', 'a real line break')).toBe(true);
  });

  it('accepts a wash clipped short by the viewport, because it is still the agent’s words', () => {
    expect(quotesAnchor(ANCHOR, 'line without inserting a real')).toBe(true);
  });

  it('rejects rows that hold something the agent did not write', () => {
    expect(quotesAnchor(ANCHOR, '› Use /skills to list available skills')).toBe(false);
  });

  it('rejects blank rows, which is what a repainted screen leaves behind', () => {
    expect(quotesAnchor(ANCHOR, '     ')).toBe(false);
  });
});
