import { describe, expect, it } from 'vitest';
import { TerminalAnnotationStore, type MessageRowAccess } from './terminalAnnotations';

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

// A grid whose contents can be swapped underneath the store, which is how a
// stale mapping is reproduced without waiting for one.
class FakeGrid implements MessageRowAccess {
  rows: string[];
  colCount: number;
  reads = 0;

  constructor(rows: string[], cols = 62) {
    this.rows = rows;
    this.colCount = cols;
  }

  cols(): number {
    return this.colCount;
  }

  totalRows(): number {
    return this.rows.length;
  }

  rowText(bufferRow: number): string {
    this.reads += 1;
    return this.rows[bufferRow] ?? '';
  }

  rowTextRange(bufferRow: number, startCol: number, endCol: number): string {
    return (this.rows[bufferRow] ?? '').slice(startCol, endCol);
  }
}

function storeWithAnchor(markdown = MESSAGE, phrase = ANCHOR) {
  const store = new TerminalAnnotationStore();
  store.setMessages([{ key: 'turn-1', markdown }]);
  const start = markdown.indexOf(phrase);
  expect(start).toBeGreaterThanOrEqual(0);
  const annotation = store.add('turn-1', start, start + phrase.length, '❓', 'why this?');
  expect(annotation).not.toBeNull();
  return { store, annotation: annotation! };
}

describe('projection', () => {
  it('paints the rows currently showing the anchored text', () => {
    const { store, annotation } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));

    const washes = store.project(grid);

    expect(washes).toHaveLength(1);
    expect(washes[0].annotationId).toBe(annotation.id);
    const painted = washes[0].rows
      .map((range) => grid.rowTextRange(range.row, range.startCol, range.endCol))
      .join(' ');
    expect(painted.replace(/\s+/g, ' ').trim()).toBe(ANCHOR);
  });

  it('re-derives onto the new rows after a width reflow', () => {
    const { store } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));
    const before = store.project(grid)[0];

    grid.rows = render(MESSAGE, 34);
    grid.colCount = 34;
    store.noteGeometryChange();
    const after = store.project(grid)[0];

    expect(after).toBeDefined();
    expect(after.rows.map((r) => r.row)).not.toEqual(before.rows.map((r) => r.row));
    const painted = after.rows
      .map((range) => grid.rowTextRange(range.row, range.startCol, range.endCol))
      .join(' ');
    expect(painted.replace(/\s+/g, ' ').trim()).toBe(ANCHOR);
  });

  it('keeps the anchor valid while it is scrolled deep into the buffer', () => {
    const { store } = storeWithAnchor();
    const padding = Array.from({ length: 400 }, (_, i) => `build output line ${i}`);
    const message = render(MESSAGE, 62);
    const grid = new FakeGrid([...padding, ...message, ...padding]);

    const washes = store.project(grid);

    expect(washes).toHaveLength(1);
    const expectedFirst = 400 + message.findIndex((row) => row.includes('visual'));
    expect(washes[0].rows[0].row).toBe(expectedFirst);
  });
});

describe('the containment gate', () => {
  it('refuses to paint when the rows no longer hold the anchored text', () => {
    const { store } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));
    expect(store.project(grid)).toHaveLength(1);

    // The TUI repaints the screen, but nothing tells the store: same row count,
    // same width, no write recorded. The cached mapping is now a lie.
    grid.rows = grid.rows.map(() => '› Use /skills to list available skills');

    expect(store.project(grid)).toHaveLength(0);
  });

  it('is what refuses, not the invalidation — the mapping still resolves rows', () => {
    // Without this, the test above would pass for the wrong reason: the aligner
    // failing to resolve anything rather than the gate rejecting what it found.
    const { store } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));
    const resolved = store.project(grid)[0].rows.map((r) => r.row);

    grid.rows = grid.rows.map(() => '› Use /skills to list available skills');
    const rowsUnderTheWash = resolved
      .map((row) => grid.rowText(row))
      .join(' ');

    expect(rowsUnderTheWash).toContain('/skills');
  });

  it('still paints a wash the viewport has clipped short', () => {
    const { store } = storeWithAnchor();
    // Only the tail of the message is on screen: the anchor's first word is gone.
    const grid = new FakeGrid(render(MESSAGE, 62).slice(1));

    const washes = store.project(grid);

    expect(washes).toHaveLength(1);
    const painted = washes[0].rows
      .map((range) => grid.rowTextRange(range.row, range.startCol, range.endCol))
      .join(' ');
    expect(ANCHOR).toContain(painted.replace(/\s+/g, ' ').trim());
  });
});

describe('annotationAt', () => {
  it('resolves the annotation covering a cell, and nothing beside it', () => {
    const { store, annotation } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));
    const wash = store.project(grid)[0].rows[0];

    expect(store.annotationAt(grid, wash.row, wash.startCol)).toBe(annotation.id);
    expect(store.annotationAt(grid, wash.row, wash.endCol - 1)).toBe(annotation.id);
    expect(store.annotationAt(grid, wash.row, wash.startCol - 1)).toBeNull();
    expect(store.annotationAt(grid, wash.row, wash.endCol)).toBeNull();
    expect(store.annotationAt(grid, wash.row + 20, wash.startCol)).toBeNull();
  });

  it('does not offer a wash the gate refused', () => {
    // The affordance is derived from the same projection as the paint, so an
    // annotation that is invisible this frame cannot be clicked either.
    // Otherwise the user would reopen an annotation by clicking words that have
    // nothing to do with it.
    const { store } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));
    const wash = store.project(grid)[0].rows[0];

    grid.rows = grid.rows.map(() => '› Use /skills to list available skills');

    expect(store.project(grid)).toHaveLength(0);
    expect(store.annotationAt(grid, wash.row, wash.startCol)).toBeNull();
  });

  it('gives an overlap to the annotation drawn on top', () => {
    const { store, annotation } = storeWithAnchor();
    const start = MESSAGE.indexOf(ANCHOR);
    const later = store.add('turn-1', start, start + ANCHOR.length, '🧪', '');
    const grid = new FakeGrid(render(MESSAGE, 62));
    const wash = store.project(grid)[0].rows[0];

    expect(later!.id).not.toBe(annotation.id);
    expect(store.annotationAt(grid, wash.row, wash.startCol)).toBe(later!.id);
  });
});

describe('the search window', () => {
  it('stops reading the whole buffer once the message has been located', () => {
    const { store } = storeWithAnchor();
    const padding = Array.from({ length: 400 }, (_, i) => `build output line ${i}`);
    const grid = new FakeGrid([...padding, ...render(MESSAGE, 62), ...padding]);

    store.project(grid);
    const firstPass = grid.reads;
    grid.reads = 0;
    store.noteWrite();
    store.project(grid);

    expect(firstPass).toBe(grid.totalRows());
    expect(grid.reads).toBeLessThan(firstPass / 2);
  });

  it('goes back to the whole buffer after a geometry change', () => {
    // A span measured at the old width names rows that hold different text after
    // a reflow. Seeding a bounded search from it finds the message shifted.
    const { store } = storeWithAnchor();
    const padding = Array.from({ length: 400 }, (_, i) => `build output line ${i}`);
    const grid = new FakeGrid([...padding, ...render(MESSAGE, 62), ...padding]);
    store.project(grid);

    grid.rows = [...padding, ...render(MESSAGE, 34), ...padding];
    grid.colCount = 34;
    store.noteGeometryChange();
    grid.reads = 0;
    store.project(grid);

    expect(grid.reads).toBe(grid.totalRows());
  });
});

describe('the annotatable window', () => {
  it('does not disturb annotations when the same turns come back', () => {
    const { store } = storeWithAnchor();

    expect(store.setMessages([{ key: 'turn-1', markdown: MESSAGE }])).toBe(false);
    expect(store.list()).toHaveLength(1);
  });

  it('keeps annotations on a past turn when a new turn arrives', () => {
    // The bug this exists to prevent: the agent answering again used to wipe
    // everything the user had marked on what it said before.
    const { store } = storeWithAnchor();

    expect(store.setMessages([
      { key: 'turn-1', markdown: MESSAGE },
      { key: 'turn-2', markdown: 'Something the agent said next.' },
    ])).toBe(true);

    expect(store.list()).toHaveLength(1);
    expect(store.list()[0].messageKey).toBe('turn-1');
  });

  it('still paints a past turn once a newer one is on the grid below it', () => {
    const { store } = storeWithAnchor();
    const next = 'Something the agent said next.';
    store.setMessages([
      { key: 'turn-1', markdown: MESSAGE },
      { key: 'turn-2', markdown: next },
    ]);
    const grid = new FakeGrid([...render(MESSAGE, 62), '', ...render(next, 62)]);

    expect(store.project(grid)).toHaveLength(1);
  });

  it('keeps an annotation whose message fell out of the window, unpainted', () => {
    // Its quote is still the user's work and still belongs in the panel; what
    // it cannot do is paint, because its text is not on this grid.
    const { store } = storeWithAnchor();
    const next = 'Something the agent said next.';
    store.setMessages([{ key: 'turn-2', markdown: next }]);
    const grid = new FakeGrid(render(next, 62));

    expect(store.list()).toHaveLength(1);
    expect(store.project(grid)).toHaveLength(0);
  });

  it('keeps the annotations when the terminal is reset, dropping only alignments', () => {
    // A reset means the buffer is gone, not the user's marks: they address
    // markdown, and re-resolve against whatever the buffer becomes.
    const { store } = storeWithAnchor();
    const grid = new FakeGrid(render(MESSAGE, 62));
    expect(store.project(grid)).toHaveLength(1);

    store.reset();

    expect(store.list()).toHaveLength(1);
    expect(store.project(grid)).toHaveLength(1);
  });

  it('refuses an annotation whose offsets fall outside the message', () => {
    const store = new TerminalAnnotationStore();
    store.setMessages([{ key: 'turn-1', markdown: MESSAGE }]);

    expect(store.add('turn-1', -1, 5)).toBeNull();
    expect(store.add('turn-1', 5, MESSAGE.length + 1)).toBeNull();
    expect(store.add('turn-1', 9, 9)).toBeNull();
  });

  it('refuses an annotation on a message it does not know', () => {
    const store = new TerminalAnnotationStore();
    store.setMessages([{ key: 'turn-1', markdown: MESSAGE }]);

    expect(store.add('turn-9', 0, 5)).toBeNull();
  });

  it('gives every annotation its own id', () => {
    const { store } = storeWithAnchor();
    const second = store.add('turn-1', 0, 6, '🧪', '');

    expect(second!.id).not.toBe(store.list()[0].id);
  });
});

describe('anchorForSelection', () => {
  it('turns a drag over the grid into an anchor on the agent’s markdown', () => {
    const store = new TerminalAnnotationStore();
    store.setMessages([{ key: 'turn-1', markdown: MESSAGE }]);
    const rows = render(MESSAGE, 62);
    const grid = new FakeGrid(rows);
    const targetRow = rows.findIndex((row) => row.includes('visual'));

    const anchor = store.anchorForSelection(grid, {
      startRow: targetRow,
      startCol: rows[targetRow].indexOf('visual'),
      endRow: targetRow + 1,
      endCol: rows[targetRow + 1].indexOf('real') + 'real'.length,
    });

    expect(anchor).not.toBeNull();
    expect(anchor!.messageKey).toBe('turn-1');
    expect(anchor!.quote).toBe(ANCHOR);
    expect(MESSAGE.slice(anchor!.start, anchor!.end)).toBe(ANCHOR);
  });

  it('refuses a drag over the TUI’s own chrome', () => {
    const store = new TerminalAnnotationStore();
    store.setMessages([{ key: 'turn-1', markdown: MESSAGE }]);
    const grid = new FakeGrid(['› Use /skills to list available skills', ...render(MESSAGE, 62)]);

    expect(store.anchorForSelection(grid, { startRow: 0, startCol: 0, endRow: 0, endCol: 40 })).toBeNull();
  });

  it('resolves a drag to whichever turn it landed on', () => {
    // Two messages on one grid: the anchor has to name the message the words
    // came from, or the offsets would address the wrong text entirely.
    const store = new TerminalAnnotationStore();
    const older = 'The older answer mentions a retry wrapper around the call.';
    store.setMessages([
      { key: 'turn-1', markdown: older },
      { key: 'turn-2', markdown: MESSAGE },
    ]);
    const olderRows = render(older, 62);
    const rows = [...olderRows, '', ...render(MESSAGE, 62)];
    const grid = new FakeGrid(rows);
    const targetRow = olderRows.findIndex((row) => row.includes('retry'));

    const anchor = store.anchorForSelection(grid, {
      startRow: targetRow,
      startCol: olderRows[targetRow].indexOf('retry'),
      endRow: targetRow,
      endCol: olderRows[targetRow].indexOf('retry') + 'retry wrapper'.length,
    });

    expect(anchor).not.toBeNull();
    expect(anchor!.messageKey).toBe('turn-1');
    expect(older.slice(anchor!.start, anchor!.end)).toContain('retry wrapper');
  });
});
