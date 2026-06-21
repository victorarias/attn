import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { NotebookEntry } from './useDaemonSocket';
import { FILE_INDEX_REFETCH_DEBOUNCE_MS, useNotebookFileIndex } from './useNotebookFileIndex';

function entry(path: string): NotebookEntry {
  return { path, size: 0 };
}

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('useNotebookFileIndex', () => {
  it('holds an empty index and never calls the daemon when disabled', () => {
    const listFiles = vi.fn(async () => [entry('a.md')]);
    const { result } = renderHook(() => useNotebookFileIndex(listFiles, 0, false));
    expect(result.current.files).toEqual([]);
    expect(listFiles).not.toHaveBeenCalled();
  });

  it('walks the vault once on mount when enabled', async () => {
    const listFiles = vi.fn(async () => [entry('knowledge/index.md'), entry('notes.md')]);
    const { result } = renderHook(() => useNotebookFileIndex(listFiles, 0, true));
    await waitFor(() => expect(result.current.files).toHaveLength(2));
    expect(listFiles).toHaveBeenCalledTimes(1);
    expect(result.current.loading).toBe(false);
  });

  it('debounces a refetch on a change-signal bump', async () => {
    vi.useFakeTimers();
    const listFiles = vi.fn(async () => [entry('a.md')]);
    const { rerender } = renderHook(
      ({ sig }) => useNotebookFileIndex(listFiles, sig, true),
      { initialProps: { sig: 0 } },
    );
    // Initial walk is immediate (zero-delay timer).
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(listFiles).toHaveBeenCalledTimes(1);

    // A burst of signal bumps collapses into a single debounced refetch.
    rerender({ sig: 1 });
    rerender({ sig: 2 });
    rerender({ sig: 3 });
    await act(async () => { await vi.advanceTimersByTimeAsync(FILE_INDEX_REFETCH_DEBOUNCE_MS); });
    expect(listFiles).toHaveBeenCalledTimes(2);
  });

  it('reflects fresh results after a change-signal refetch', async () => {
    const before = [entry('only-old.md')];
    const after = [entry('new-a.md'), entry('new-b.md')];
    let call = 0;
    const listFiles = vi.fn(async () => (++call === 1 ? before : after));
    const { result, rerender } = renderHook(
      ({ sig }) => useNotebookFileIndex(listFiles, sig, true),
      { initialProps: { sig: 0 } },
    );
    await waitFor(() => expect(result.current.files.map((f) => f.path)).toEqual(['only-old.md']));
    rerender({ sig: 1 });
    await waitFor(() => expect(result.current.files.map((f) => f.path)).toEqual(['new-a.md', 'new-b.md']));
  });

  it('surfaces an error when the walk fails', async () => {
    const listFiles = vi.fn(async () => { throw new Error('walk boom'); });
    const { result } = renderHook(() => useNotebookFileIndex(listFiles, 0, true));
    await waitFor(() => expect(result.current.error).toBe('walk boom'));
    expect(result.current.loading).toBe(false);
  });

  it('clears the index when disabled after being enabled', async () => {
    const listFiles = vi.fn(async () => [entry('a.md')]);
    const { result, rerender } = renderHook(
      ({ on }) => useNotebookFileIndex(listFiles, 0, on),
      { initialProps: { on: true } },
    );
    await waitFor(() => expect(result.current.files).toHaveLength(1));
    act(() => rerender({ on: false }));
    expect(result.current.files).toEqual([]);
  });
});
