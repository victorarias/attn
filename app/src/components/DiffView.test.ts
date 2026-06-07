import { describe, expect, it } from 'vitest';
import { normalizeRange } from './DiffView';

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
