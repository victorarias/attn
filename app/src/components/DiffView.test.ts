import { describe, expect, it } from 'vitest';
import type { ReviewComment } from '../types/generated';
import { buildCommentContextBlocks, normalizeRange } from './DiffView';

describe('DiffView range normalization', () => {
  it('normalizes same-side selections', () => {
    expect(normalizeRange({ side: 'additions', start: 8, end: 4 })).toEqual({
      side: 'additions',
      start: 4,
      end: 8,
    });
  });

  it('rejects mixed-side selections', () => {
    expect(normalizeRange({ side: 'deletions', start: 3, endSide: 'additions', end: 5 })).toBeNull();
  });
});

describe('DiffView comment context expansion', () => {
  const comment = (line: number): ReviewComment => ({
    id: `comment-${line}`,
    review_id: 'review',
    filepath: 'example.ts',
    line_start: line,
    line_end: line,
    content: 'Read this line.',
    author: 'agent',
    resolved: false,
    created_at: new Date(0).toISOString(),
  });

  it('builds a bounded partial diff around an annotation outside the changed hunks', () => {
    const source = ['l1', 'l2', 'l3', 'l4', 'l5', 'l6', 'l7', 'l8', 'l9', 'l10'].join('\n');
    const [block] = buildCommentContextBlocks('example.ts', source, [comment(7)]);

    expect(block).toMatchObject({
      start: 4,
      end: 10,
      comments: [expect.objectContaining({ line_start: 7 })],
      fileDiff: {
        name: 'example.ts',
        unifiedLineCount: 7,
        additionLines: ['l4\n', 'l5\n', 'l6\n', 'l7\n', 'l8\n', 'l9\n', 'l10\n'],
      },
    });
    expect(block?.fileDiff.hunks[0]).toMatchObject({
      additionStart: 4,
      additionCount: 7,
      collapsedBefore: 0,
    });
  });

  it('merges overlapping annotation context into one partial diff', () => {
    const source = Array.from({ length: 20 }, (_, index) => `line ${index + 1}`).join('\n');
    const blocks = buildCommentContextBlocks('example.ts', source, [comment(7), comment(10)]);

    expect(blocks).toHaveLength(1);
    expect(blocks[0]).toMatchObject({ start: 4, end: 13 });
  });
});
