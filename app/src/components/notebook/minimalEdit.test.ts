import { describe, expect, it } from 'vitest';
import { computeMinimalEdit } from './minimalEdit';

// Apply a MinimalEdit the same way CodeMirror would, to prove the range+insert
// reconstructs the target exactly.
function apply(current: string, edit: { from: number; to: number; insert: string }): string {
  return current.slice(0, edit.from) + edit.insert + current.slice(edit.to);
}

describe('computeMinimalEdit', () => {
  it('returns null when the strings are identical (no transaction needed)', () => {
    expect(computeMinimalEdit('# Note\n\nbody', '# Note\n\nbody')).toBeNull();
    expect(computeMinimalEdit('', '')).toBeNull();
  });

  it('narrows an append to a zero-width insert at the end', () => {
    const current = '# Note\n\nfirst paragraph';
    const next = '# Note\n\nfirst paragraph\n\nappended paragraph';
    const edit = computeMinimalEdit(current, next)!;
    expect(edit).toEqual({ from: current.length, to: current.length, insert: '\n\nappended paragraph' });
    expect(apply(current, edit)).toBe(next);
  });

  it('narrows a prepend to a zero-width insert at the start', () => {
    const current = 'body line';
    const next = 'new heading\nbody line';
    const edit = computeMinimalEdit(current, next)!;
    expect(edit).toEqual({ from: 0, to: 0, insert: 'new heading\n' });
    expect(apply(current, edit)).toBe(next);
  });

  it('narrows an in-place edit to just the changed middle', () => {
    const current = 'before XYZ after';
    const next = 'before PQRS after';
    const edit = computeMinimalEdit(current, next)!;
    expect(edit).toEqual({ from: 7, to: 10, insert: 'PQRS' });
    expect(apply(current, edit)).toBe(next);
  });

  it('replaces the whole string when nothing is shared', () => {
    const edit = computeMinimalEdit('alpha', 'OMEGA')!;
    expect(edit).toEqual({ from: 0, to: 5, insert: 'OMEGA' });
    expect(apply('alpha', edit)).toBe('OMEGA');
  });

  it('trims a shared suffix even with no shared prefix', () => {
    // "alpha" and "omega" share only the trailing "a": the edit replaces everything
    // before it, NOT the whole string.
    const edit = computeMinimalEdit('alpha', 'omega')!;
    expect(edit).toEqual({ from: 0, to: 4, insert: 'omeg' });
    expect(apply('alpha', edit)).toBe('omega');
  });

  it('handles a pure deletion of the middle', () => {
    const current = 'keep [drop this] keep';
    const next = 'keep  keep';
    const edit = computeMinimalEdit(current, next)!;
    expect(edit.insert).toBe('');
    expect(apply(current, edit)).toBe(next);
  });

  it('handles a deletion to empty', () => {
    const edit = computeMinimalEdit('something', '')!;
    expect(edit).toEqual({ from: 0, to: 9, insert: '' });
    expect(apply('something', edit)).toBe('');
  });

  it('does not let an overlapping shared prefix/suffix produce a negative range', () => {
    // Shared "aa" prefix and "aa" suffix overlap in "aaaa" -> the suffix trim must stop
    // at the prefix, never crossing it.
    const current = 'aaaa';
    const next = 'aa';
    const edit = computeMinimalEdit(current, next)!;
    expect(edit.from).toBeLessThanOrEqual(edit.to);
    expect(apply(current, edit)).toBe(next);
  });

  it('reconstructs the target across a spread of realistic note edits', () => {
    const cases: Array<[string, string]> = [
      ['# A\n\none\ntwo\nthree', '# A\n\none\ntwo\nthree\nfour'],
      ['- [ ] task', '- [x] task'],
      ['line1\nline2\nline3', 'line1\nlineTWO\nline3'],
      ['intro', '# Title\n\nintro\n\noutro'],
      ['full document here', 'completely different text'],
    ];
    for (const [current, next] of cases) {
      const edit = computeMinimalEdit(current, next);
      expect(edit && apply(current, edit)).toBe(next);
    }
  });
});
