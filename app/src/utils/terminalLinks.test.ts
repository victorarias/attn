import { describe, expect, it } from 'vitest';
import {
  fragmentAtColumn,
  hyperlinkRangeAt,
  logicalIndexForCell,
  logicalLineAt,
  MAX_WRAP_JOIN_ROWS,
  pathCandidatesForFragment,
  resolveDetectedPath,
  spanFromLogicalRange,
  urlAtColumn,
} from './terminalLinks';

describe('urlAtColumn', () => {
  it('returns the uri and its column range', () => {
    const line = 'see https://example.test/page. then more';
    const hit = urlAtColumn(line, 10);
    expect(hit).toEqual({
      uri: 'https://example.test/page',
      startCol: 4,
      endCol: 29,
    });
  });

  it('misses columns outside the uri', () => {
    const line = 'see https://example.test/page';
    expect(urlAtColumn(line, 2)).toBeNull();
  });
});

describe('hyperlinkRangeAt', () => {
  // A row of uris (or null) at each index, closed over by uriAtIndex — the
  // fake IS the test input, standing in for what ghostty's OSC 8 lookup returns.
  function uriAtIndexOver(uris: (string | null)[]) {
    return (index: number) => uris[index] ?? null;
  }

  it('returns the full range for a label containing spaces', () => {
    // "Learn more" behind one hidden uri, surrounded by plain text.
    const uris = [null, null, null, null, 'https://x.test', 'https://x.test', 'https://x.test',
      'https://x.test', 'https://x.test', 'https://x.test', 'https://x.test', 'https://x.test',
      'https://x.test', 'https://x.test', null, null];
    const hit = hyperlinkRangeAt(uriAtIndexOver(uris), 8, uris.length);
    expect(hit).toEqual({ uri: 'https://x.test', startCol: 4, endCol: 14 });
  });

  it('does not merge two adjacent links with different uris', () => {
    const uris = ['https://a.test', 'https://a.test', 'https://b.test', 'https://b.test'];
    expect(hyperlinkRangeAt(uriAtIndexOver(uris), 1, uris.length)).toEqual({
      uri: 'https://a.test',
      startCol: 0,
      endCol: 2,
    });
    expect(hyperlinkRangeAt(uriAtIndexOver(uris), 2, uris.length)).toEqual({
      uri: 'https://b.test',
      startCol: 2,
      endCol: 4,
    });
  });

  it('bounds the range exactly at the start and end of the line', () => {
    const uris = ['https://x.test', 'https://x.test', 'https://x.test'];
    expect(hyperlinkRangeAt(uriAtIndexOver(uris), 0, uris.length)).toEqual({
      uri: 'https://x.test',
      startCol: 0,
      endCol: 3,
    });
    expect(hyperlinkRangeAt(uriAtIndexOver(uris), uris.length - 1, uris.length)).toEqual({
      uri: 'https://x.test',
      startCol: 0,
      endCol: 3,
    });
  });

  it('returns null when the hovered index has no uri', () => {
    const uris = [null, 'https://x.test', null];
    expect(hyperlinkRangeAt(uriAtIndexOver(uris), 0, uris.length)).toBeNull();
    expect(hyperlinkRangeAt(uriAtIndexOver(uris), 2, uris.length)).toBeNull();
  });
});

describe('fragmentAtColumn', () => {
  it('finds the run of non-whitespace around the column', () => {
    expect(fragmentAtColumn('a (src/x.go:1) b', 5)).toEqual({ startCol: 2, endCol: 14 });
  });

  it('returns null on whitespace and past end of line', () => {
    expect(fragmentAtColumn('a b', 1)).toBeNull();
    expect(fragmentAtColumn('ab', 7)).toBeNull();
  });
});

describe('pathCandidatesForFragment', () => {
  it('parses line:col suffixes and prefers the stripped path', () => {
    const candidates = pathCandidatesForFragment('src/main.go:12:3', 4);
    expect(candidates[0]).toEqual({
      path: 'src/main.go',
      line: 12,
      column: 3,
      startCol: 4,
      endCol: 20,
    });
  });

  it('unwraps brackets and trailing punctuation', () => {
    const candidates = pathCandidatesForFragment('(internal/store/db.go:88),', 0);
    expect(candidates[0]?.path).toBe('internal/store/db.go');
    expect(candidates[0]?.line).toBe(88);
    expect(candidates[0]?.startCol).toBe(1);
    expect(candidates[0]?.endCol).toBe(1 + 'internal/store/db.go:88'.length);
  });

  it('accepts bare filenames with extensions and tilde paths', () => {
    expect(pathCandidatesForFragment('Makefile.am', 0)[0]?.path).toBe('Makefile.am');
    expect(pathCandidatesForFragment('~/notes.md', 0)[0]?.path).toBe('~/notes.md');
  });

  it('rejects prose words and urls', () => {
    expect(pathCandidatesForFragment('error:', 0)).toEqual([]);
    expect(pathCandidatesForFragment('https://example.test/x.go', 0)).toEqual([]);
    expect(pathCandidatesForFragment('word', 0)).toEqual([]);
  });

  it('keeps a colon-bearing path as a fallback candidate', () => {
    const candidates = pathCandidatesForFragment('a/b:9', 0);
    expect(candidates.map((candidate) => candidate.path)).toEqual(['a/b', 'a/b:9']);
  });

  it('finds a path starting mid-fragment after a non-path prefix (agent TUI tool lines)', () => {
    // Claude Code prints tool calls as `Read(/abs/path` — the fragment under
    // the pointer carries the call-name prefix.
    const candidates = pathCandidatesForFragment('Read(/Users/victor/projects/attn/AGENTS.md', 2);
    expect(candidates.map((candidate) => candidate.path)).toContain('/Users/victor/projects/attn/AGENTS.md');
    const mid = candidates.find((candidate) => candidate.path.startsWith('/Users'));
    expect(mid?.startCol).toBe(2 + 'Read('.length);
  });

  it('finds a tilde path after an equals sign', () => {
    const candidates = pathCandidatesForFragment('--file=~/notes/todo.md', 0);
    expect(candidates.map((candidate) => candidate.path)).toContain('~/notes/todo.md');
  });

  it('does not split ordinary relative paths at interior slashes', () => {
    const candidates = pathCandidatesForFragment('src/main.go', 0);
    expect(candidates).toHaveLength(1);
    expect(candidates[0].path).toBe('src/main.go');
  });
});

describe('logicalLineAt', () => {
  const COLS = 10;
  const access = (rows: string[], continuations: boolean[]) => ({
    rowTextAt: (row: number) => rows[row] ?? '',
    isContinuationRow: (row: number) => continuations[row] ?? false,
  });

  it('returns a single row when nothing wraps', () => {
    const { rowTextAt, isContinuationRow } = access(['hello', 'world'], [false, false]);
    const line = logicalLineAt(rowTextAt, isContinuationRow, 1, COLS, 2);
    expect(line).toEqual({ text: 'world', firstRow: 1, rowCount: 1, cols: COLS });
  });

  it('joins a wrapped group around the hovered row, padding interior rows to cols', () => {
    // "Read(/tmp/abc/def.md)" wrapped at 10 cols over 3 rows.
    const rows = ['Read(/tmp/', 'abc/def.md', ')'];
    const { rowTextAt, isContinuationRow } = access(rows, [false, true, true]);
    for (const hovered of [0, 1, 2]) {
      const line = logicalLineAt(rowTextAt, isContinuationRow, hovered, COLS, 3);
      expect(line.text).toBe('Read(/tmp/abc/def.md)');
      expect(line.firstRow).toBe(0);
      expect(line.rowCount).toBe(3);
    }
  });

  it('pads short interior rows so index math stays exact', () => {
    const rows = ['ab', 'cd'];
    const { rowTextAt, isContinuationRow } = access(rows, [false, true]);
    const line = logicalLineAt(rowTextAt, isContinuationRow, 0, COLS, 2);
    expect(line.text).toBe('ab'.padEnd(COLS, ' ') + 'cd');
  });

  it('caps the joined group and keeps rows nearest the line start', () => {
    const rows = Array.from({ length: 12 }, (_, i) => String(i).padEnd(COLS, 'x'));
    const continuations = rows.map((_, i) => i > 0);
    const { rowTextAt, isContinuationRow } = access(rows, continuations);
    const line = logicalLineAt(rowTextAt, isContinuationRow, 11, COLS, 12);
    expect(line.rowCount).toBe(MAX_WRAP_JOIN_ROWS);
    expect(line.firstRow).toBe(11 - (MAX_WRAP_JOIN_ROWS - 1));
  });

  it('never walks outside the viewport', () => {
    const { rowTextAt, isContinuationRow } = access(['a', 'b'], [true, true]);
    const line = logicalLineAt(rowTextAt, isContinuationRow, 0, COLS, 1);
    expect(line.firstRow).toBe(0);
    expect(line.rowCount).toBe(1);
  });
});

describe('logicalIndexForCell / spanFromLogicalRange', () => {
  const line = { text: 'x'.repeat(25), firstRow: 3, rowCount: 3, cols: 10 };

  it('maps cells to logical indexes and rejects cells outside the group', () => {
    expect(logicalIndexForCell(line, 3, 0)).toBe(0);
    expect(logicalIndexForCell(line, 4, 2)).toBe(12);
    expect(logicalIndexForCell(line, 5, 9)).toBe(29);
    expect(logicalIndexForCell(line, 2, 0)).toBeNull();
    expect(logicalIndexForCell(line, 6, 0)).toBeNull();
    expect(logicalIndexForCell(line, 4, 10)).toBeNull();
  });

  it('maps a logical range back to a selection-semantics row span', () => {
    expect(spanFromLogicalRange(line, 5, 25)).toEqual({
      startRow: 3,
      startCol: 5,
      endRow: 5,
      endCol: 5,
    });
    expect(spanFromLogicalRange(line, 12, 18)).toEqual({
      startRow: 4,
      startCol: 2,
      endRow: 4,
      endCol: 8,
    });
  });
});

describe('resolveDetectedPath', () => {
  it('passes through absolute paths and normalizes dot segments', () => {
    expect(resolveDetectedPath('/a/./b/../c')).toBe('/a/c');
  });

  it('resolves relative paths against cwd', () => {
    expect(resolveDetectedPath('src/x.go', '/repo/')).toBe('/repo/src/x.go');
    expect(resolveDetectedPath('./src/x.go', '/repo')).toBe('/repo/src/x.go');
    expect(resolveDetectedPath('../x.go', '/repo/sub')).toBe('/repo/x.go');
  });

  it('expands ~ against home and refuses without it', () => {
    expect(resolveDetectedPath('~/notes.md', '/repo', '/Users/me')).toBe('/Users/me/notes.md');
    expect(resolveDetectedPath('~/notes.md', '/repo')).toBeNull();
    expect(resolveDetectedPath('~other/notes.md', '/repo', '/Users/me')).toBeNull();
  });

  it('refuses relative paths without cwd and escapes above root', () => {
    expect(resolveDetectedPath('src/x.go')).toBeNull();
    expect(resolveDetectedPath('../../../../x', '/a')).toBeNull();
  });
});
