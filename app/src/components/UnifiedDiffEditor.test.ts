/**
 * UnifiedDiffEditor Unit Tests
 *
 * Tests the diff parsing and document building logic.
 */
import { describe, it, expect } from 'vitest';
import {
  buildUnifiedDocument,
  hashContent,
  createAnchor,
  resolveAnchor,
  getLineNumbers,
  calculateHunks,
  getVisibleLines,
  CommentAnchor,
} from './UnifiedDiffEditor';

describe('buildUnifiedDocument', () => {
  it('handles identical files', () => {
    const content = 'line 1\nline 2\nline 3';
    const { lines, content: doc } = buildUnifiedDocument(content, content);

    expect(doc).toBe(content);
    expect(lines).toHaveLength(3);
    expect(lines.every((l) => l.type === 'unchanged')).toBe(true);
  });

  it('marks deleted lines correctly', () => {
    const original = 'line 1\nline 2\nline 3';
    const modified = 'line 1\nline 3';

    const { lines } = buildUnifiedDocument(original, modified);

    expect(lines).toHaveLength(3);
    expect(lines[0]).toMatchObject({ type: 'unchanged', content: 'line 1' });
    expect(lines[1]).toMatchObject({ type: 'deleted', content: 'line 2' });
    expect(lines[2]).toMatchObject({ type: 'unchanged', content: 'line 3' });
  });

  it('marks added lines correctly', () => {
    const original = 'line 1\nline 3';
    const modified = 'line 1\nline 2\nline 3';

    const { lines } = buildUnifiedDocument(original, modified);

    expect(lines).toHaveLength(3);
    expect(lines[0]).toMatchObject({ type: 'unchanged', content: 'line 1' });
    expect(lines[1]).toMatchObject({ type: 'added', content: 'line 2' });
    expect(lines[2]).toMatchObject({ type: 'unchanged', content: 'line 3' });
  });

  it('tracks original line numbers', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nc'; // b is deleted

    const { lines } = buildUnifiedDocument(original, modified);

    expect(lines[0].originalLine).toBe(1);
    expect(lines[1].originalLine).toBe(2); // deleted line
    expect(lines[2].originalLine).toBe(3);
  });

  it('tracks modified line numbers', () => {
    const original = 'a\nc';
    const modified = 'a\nb\nc'; // b is added

    const { lines } = buildUnifiedDocument(original, modified);

    expect(lines[0].modifiedLine).toBe(1);
    expect(lines[1].modifiedLine).toBe(2); // added line
    expect(lines[2].modifiedLine).toBe(3);
  });

  it('deleted lines have null modifiedLine', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nc';

    const { lines } = buildUnifiedDocument(original, modified);

    const deleted = lines.find((l) => l.type === 'deleted');
    expect(deleted?.modifiedLine).toBeNull();
    expect(deleted?.originalLine).toBe(2);
  });

  it('added lines have null originalLine', () => {
    const original = 'a\nc';
    const modified = 'a\nb\nc';

    const { lines } = buildUnifiedDocument(original, modified);

    const added = lines.find((l) => l.type === 'added');
    expect(added?.originalLine).toBeNull();
    expect(added?.modifiedLine).toBe(2);
  });

  it('handles complex diff with multiple changes', () => {
    const original = `function foo() {
  console.log('old 1');
  console.log('old 2');
  return true;
}`;

    const modified = `function foo() {
  console.log('new 1');
  return false;
}`;

    const { lines } = buildUnifiedDocument(original, modified);

    // Should have: unchanged, deleted, deleted, added, deleted, added, unchanged
    const types = lines.map((l) => l.type);

    // First line unchanged
    expect(lines[0]).toMatchObject({ type: 'unchanged', content: 'function foo() {' });

    // Last line unchanged
    expect(lines[lines.length - 1]).toMatchObject({ type: 'unchanged', content: '}' });

    // Should have some deleted and added
    expect(types).toContain('deleted');
    expect(types).toContain('added');
  });

  it('builds correct document content', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nx\nc';

    const { content, lines } = buildUnifiedDocument(original, modified);

    // Document should contain all lines (including deleted)
    expect(content).toBe('a\nb\nx\nc');
    expect(lines).toHaveLength(4);
  });

  it('handles empty original', () => {
    const original = '';
    const modified = 'line 1\nline 2';

    const { lines } = buildUnifiedDocument(original, modified);

    expect(lines.every((l) => l.type === 'added')).toBe(true);
  });

  it('handles empty modified', () => {
    const original = 'line 1\nline 2';
    const modified = '';

    const { lines } = buildUnifiedDocument(original, modified);

    expect(lines.every((l) => l.type === 'deleted')).toBe(true);
  });
});

describe('hashContent', () => {
  it('returns consistent hash for same content', () => {
    expect(hashContent('hello')).toBe(hashContent('hello'));
  });

  it('returns different hash for different content', () => {
    expect(hashContent('hello')).not.toBe(hashContent('world'));
  });

  it('handles empty string', () => {
    expect(hashContent('')).toBe('0');
  });
});

describe('createAnchor', () => {
  it('creates original-side anchor for deleted line', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    // Line 2 in doc is the deleted 'b'
    const anchor = createAnchor(2, lines);

    expect(anchor).toMatchObject({
      side: 'original',
      line: 2,
      anchorContent: 'b',
    });
    expect(anchor?.anchorHash).toBeDefined();
  });

  it('creates modified-side anchor for added line', () => {
    const original = 'a\nc';
    const modified = 'a\nb\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    // Line 2 in doc is the added 'b'
    const anchor = createAnchor(2, lines);

    expect(anchor).toMatchObject({
      side: 'modified',
      line: 2,
      anchorContent: 'b',
    });
  });

  it('creates modified-side anchor for unchanged line', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nb\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    const anchor = createAnchor(2, lines);

    expect(anchor).toMatchObject({
      side: 'modified',
      line: 2,
      anchorContent: 'b',
    });
  });

  it('returns null for invalid line', () => {
    const { lines } = buildUnifiedDocument('a\nb', 'a\nb');
    expect(createAnchor(999, lines)).toBeNull();
    expect(createAnchor(0, lines)).toBeNull();
  });
});

describe('resolveAnchor', () => {
  it('resolves anchor to correct docLine', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    const anchor: CommentAnchor = {
      side: 'original',
      line: 2,
      anchorContent: 'b',
      anchorHash: hashContent('b'),
    };

    const result = resolveAnchor(anchor, lines);

    expect('docLine' in result && result.docLine).toBe(2);
    expect('isOutdated' in result && result.isOutdated).toBe(false);
  });

  it('detects outdated comment when content changed', () => {
    const original = 'a\nold content\nc';
    const modified = 'a\nnew content\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    // Anchor was created when line 2 was 'old content'
    const anchor: CommentAnchor = {
      side: 'modified',
      line: 2,
      anchorContent: 'old content',
      anchorHash: hashContent('old content'),
    };

    const result = resolveAnchor(anchor, lines);

    // Should find line 2 in modified, but mark as outdated
    expect('docLine' in result).toBe(true);
    if ('docLine' in result) {
      expect(result.isOutdated).toBe(true);
    }
  });

  it('detects orphaned comment when line removed', () => {
    const original = 'a\nb';
    const modified = 'a'; // 'b' is removed
    const { lines } = buildUnifiedDocument(original, modified);

    // Anchor was on modified line 2 which no longer exists
    const anchor: CommentAnchor = {
      side: 'modified',
      line: 2,
      anchorContent: 'b',
      anchorHash: hashContent('b'),
    };

    const result = resolveAnchor(anchor, lines);

    expect('isOrphaned' in result && result.isOrphaned).toBe(true);
  });
});

describe('getLineNumbers', () => {
  it('returns both line numbers for unchanged line', () => {
    const { lines } = buildUnifiedDocument('a\nb\nc', 'a\nb\nc');

    const result = getLineNumbers(2, lines);

    expect(result).toEqual({
      original: 2,
      modified: 2,
      type: 'unchanged',
    });
  });

  it('returns null modified for deleted line', () => {
    const original = 'a\nb\nc';
    const modified = 'a\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    const result = getLineNumbers(2, lines);

    expect(result).toEqual({
      original: 2,
      modified: null,
      type: 'deleted',
    });
  });

  it('returns null original for added line', () => {
    const original = 'a\nc';
    const modified = 'a\nb\nc';
    const { lines } = buildUnifiedDocument(original, modified);

    const result = getLineNumbers(2, lines);

    expect(result).toEqual({
      original: null,
      modified: 2,
      type: 'added',
    });
  });

  it('returns null for invalid line', () => {
    const { lines } = buildUnifiedDocument('a', 'a');
    expect(getLineNumbers(999, lines)).toBeNull();
  });
});

describe('calculateHunks', () => {
  it('returns empty for contextLines=0 (full diff mode)', () => {
    const { lines } = buildUnifiedDocument('a\nb', 'a\nc');
    const { hunks, collapsedRegions } = calculateHunks(lines, 0);

    expect(hunks).toHaveLength(0);
    expect(collapsedRegions).toHaveLength(0);
  });

  it('collapses identical files', () => {
    // 10 identical lines
    const content = Array.from({ length: 10 }, (_, i) => `line ${i + 1}`).join('\n');
    const { lines } = buildUnifiedDocument(content, content);

    const { hunks, collapsedRegions } = calculateHunks(lines, 3);

    expect(hunks).toHaveLength(0);
    expect(collapsedRegions).toHaveLength(1);
    expect(collapsedRegions[0].lineCount).toBe(10);
  });

  it('creates single hunk for isolated change', () => {
    // Lines: 1, 2, 3, CHANGE, 5, 6, 7
    const original = '1\n2\n3\n4\n5\n6\n7';
    const modified = '1\n2\n3\nCHANGED\n5\n6\n7';
    const { lines } = buildUnifiedDocument(original, modified);

    const { hunks } = calculateHunks(lines, 2);

    expect(hunks.length).toBeGreaterThanOrEqual(1);
    // The hunk should contain the change and 2 lines of context on each side
  });

  it('merges nearby changes into single hunk', () => {
    // Changes at lines 2 and 4 should merge with context=3
    const original = '1\n2\n3\n4\n5\n6\n7\n8\n9\n10';
    const modified = '1\nA\n3\nB\n5\n6\n7\n8\n9\n10';
    const { lines } = buildUnifiedDocument(original, modified);

    const { hunks } = calculateHunks(lines, 3);

    // Should be a single hunk since changes are close together
    expect(hunks.length).toBe(1);
  });

  it('creates collapsed regions between distant hunks', () => {
    // 20 lines with changes at line 2 and line 18
    const lineArr = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`);
    const original = lineArr.join('\n');
    lineArr[1] = 'CHANGED A';
    lineArr[17] = 'CHANGED B';
    const modified = lineArr.join('\n');
    const { lines } = buildUnifiedDocument(original, modified);

    const { hunks, collapsedRegions } = calculateHunks(lines, 3);

    // Should have 2 hunks (one for each change)
    expect(hunks.length).toBe(2);
    // Should have 1 collapsed region between them
    expect(collapsedRegions.length).toBeGreaterThanOrEqual(1);
  });
});

describe('getVisibleLines', () => {
  it('returns all lines for contextLines=0', () => {
    const { lines } = buildUnifiedDocument('a\nb\nc', 'a\nx\nc');

    const visible = getVisibleLines(lines, 0, new Set());

    expect(visible.size).toBe(lines.length);
  });

  it('hides collapsed regions', () => {
    // 20 lines with change in the middle
    const lineArr = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`);
    const original = lineArr.join('\n');
    lineArr[9] = 'CHANGED';
    const modified = lineArr.join('\n');
    const { lines } = buildUnifiedDocument(original, modified);

    const visible = getVisibleLines(lines, 3, new Set());

    // Should have fewer visible lines than total
    expect(visible.size).toBeLessThan(lines.length);

    // Change area should be visible (around index 9-10)
    expect(visible.has(9) || visible.has(10)).toBe(true);
  });

  it('expands regions when in expandedRegions set', () => {
    // 20 lines, all identical (one big collapsed region)
    const content = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join('\n');
    const { lines } = buildUnifiedDocument(content, content);

    const { collapsedRegions } = calculateHunks(lines, 3);
    expect(collapsedRegions.length).toBe(1);

    // Without expansion
    const visibleCollapsed = getVisibleLines(lines, 3, new Set());
    expect(visibleCollapsed.size).toBe(0);

    // With expansion
    const expanded = new Set([collapsedRegions[0].startDocLine]);
    const visibleExpanded = getVisibleLines(lines, 3, expanded);
    expect(visibleExpanded.size).toBe(lines.length);
  });
});
