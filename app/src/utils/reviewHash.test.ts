import { describe, it, expect } from 'vitest';
import { hashContent, hashDiffContent } from './reviewHash';

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

  it('is sensitive to small changes (single character)', () => {
    expect(hashContent('line 1\nline 2')).not.toBe(hashContent('line 1\nline 3'));
  });
});

describe('hashDiffContent', () => {
  it('is stable for the same pair', () => {
    expect(hashDiffContent('a', 'b')).toBe(hashDiffContent('a', 'b'));
  });

  it('disambiguates pairs that share a concatenation boundary', () => {
    // ("ab","c") and ("a","bc") both concatenate to "abc"; framing must keep
    // their hashes distinct so a real diff change cannot masquerade as no-op.
    expect(hashDiffContent('ab', 'c')).not.toBe(hashDiffContent('a', 'bc'));
  });

  it('changes when either side changes', () => {
    const base = hashDiffContent('original', 'modified');
    expect(hashDiffContent('original!', 'modified')).not.toBe(base);
    expect(hashDiffContent('original', 'modified!')).not.toBe(base);
  });
});
