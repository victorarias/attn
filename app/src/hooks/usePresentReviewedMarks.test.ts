// app/src/hooks/usePresentReviewedMarks.test.ts
import { describe, it, expect, beforeEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { usePresentReviewedMarks } from './usePresentReviewedMarks';

describe('usePresentReviewedMarks', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('starts empty when nothing is persisted', () => {
    const { result } = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['a.ts', 'b.ts']));
    expect(result.current.reviewed.size).toBe(0);
  });

  it('toggleReviewed adds then removes a path', () => {
    const { result } = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['a.ts', 'b.ts']));

    act(() => result.current.toggleReviewed('a.ts'));
    expect(result.current.reviewed.has('a.ts')).toBe(true);

    act(() => result.current.toggleReviewed('a.ts'));
    expect(result.current.reviewed.has('a.ts')).toBe(false);
  });

  it('markReviewed is idempotent', () => {
    const { result } = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['a.ts', 'b.ts']));

    act(() => result.current.markReviewed('a.ts'));
    act(() => result.current.markReviewed('a.ts'));
    expect(Array.from(result.current.reviewed)).toEqual(['a.ts']);
  });

  it('persists marks across a remount for the same (presentation, round)', () => {
    const first = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['a.ts', 'b.ts']));
    act(() => first.result.current.markReviewed('a.ts'));
    first.unmount();

    const second = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['a.ts', 'b.ts']));
    expect(second.result.current.reviewed.has('a.ts')).toBe(true);
  });

  it('isolates marks between different rounds of the same presentation', () => {
    const round1 = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['a.ts']));
    act(() => round1.result.current.markReviewed('a.ts'));

    const round2 = renderHook(() => usePresentReviewedMarks('pres-1', 'round-2', ['a.ts']));
    expect(round2.result.current.reviewed.size).toBe(0);
  });

  it('isolates marks between different presentations', () => {
    const presA = renderHook(() => usePresentReviewedMarks('pres-a', 'round-1', ['a.ts']));
    act(() => presA.result.current.markReviewed('a.ts'));

    const presB = renderHook(() => usePresentReviewedMarks('pres-b', 'round-1', ['a.ts']));
    expect(presB.result.current.reviewed.size).toBe(0);
  });

  it('prunes marks for paths no longer in the manifest and writes the pruned set back', () => {
    const first = renderHook(({ paths }) => usePresentReviewedMarks('pres-1', 'round-1', paths), {
      initialProps: { paths: ['a.ts', 'b.ts'] },
    });
    act(() => {
      first.result.current.markReviewed('a.ts');
      first.result.current.markReviewed('b.ts');
    });
    first.unmount();

    // Manifest shrinks to just b.ts (e.g. round was reloaded with a narrower
    // file list) — a.ts's stale mark should be dropped, not resurrected.
    const second = renderHook(() => usePresentReviewedMarks('pres-1', 'round-1', ['b.ts']));
    expect(Array.from(second.result.current.reviewed)).toEqual(['b.ts']);

    const raw = window.localStorage.getItem('attn.present.reviewed.pres-1.round-1');
    expect(JSON.parse(raw!)).toEqual(['b.ts']);
  });

  it('null ids yield an empty set and no-op mutations', () => {
    const { result } = renderHook(() => usePresentReviewedMarks(null, null, ['a.ts']));
    expect(result.current.reviewed.size).toBe(0);

    act(() => result.current.toggleReviewed('a.ts'));
    expect(result.current.reviewed.size).toBe(0);

    act(() => result.current.markReviewed('a.ts'));
    expect(result.current.reviewed.size).toBe(0);
  });
});
