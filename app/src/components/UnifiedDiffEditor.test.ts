/**
 * UnifiedDiffEditor Unit Tests
 *
 * Tests the diff parsing and document building logic.
 */
import { describe, it, expect } from 'vitest';
import { buildUnifiedDocument, DiffLine } from './UnifiedDiffEditor';

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
