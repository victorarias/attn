import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useFilesystemSuggestions } from './useFilesystemSuggestions';
import type { BrowseDirectoryResult } from './useDaemonSocket';

describe('useFilesystemSuggestions', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('refreshes immediately and clears stale results when the target endpoint changes', async () => {
    let resolveRemote: ((value: BrowseDirectoryResult) => void) | null = null;
    const browseDirectory = vi.fn(async (_inputPath: string, endpointId?: string) => {
      if (endpointId) {
        return await new Promise<BrowseDirectoryResult>((resolve) => {
          resolveRemote = resolve;
        });
      }
      return {
        success: true,
        input_path: '~/projects/',
        directory: '/Users/victor/projects',
        entries: [{ name: 'local-repo', path: '/Users/victor/projects/local-repo' }],
        home_path: '/Users/victor',
      };
    });

    const { result, rerender } = renderHook(
      ({ inputPath, endpointId }) => useFilesystemSuggestions(inputPath, endpointId, browseDirectory),
      { initialProps: { inputPath: '~/projects/', endpointId: undefined as string | undefined } },
    );

    await act(async () => {
      vi.advanceTimersByTime(150);
      await Promise.resolve();
    });

    expect(result.current.currentDir).toBe('~/projects');
    expect(result.current.suggestions.map((entry) => entry.name)).toEqual(['local-repo']);

    await act(async () => {
      rerender({ inputPath: '~/projects/', endpointId: 'ep-1' });
      await Promise.resolve();
    });

    expect(result.current.currentDir).toBe('');
    expect(result.current.suggestions).toEqual([]);

    await act(async () => {
      resolveRemote?.({
        success: true,
        input_path: '~/projects/',
        directory: '/home/remote/projects',
        entries: [{ name: 'remote-repo', path: '/home/remote/projects/remote-repo' }],
        home_path: '/home/remote',
      });
      await Promise.resolve();
    });

    expect(browseDirectory).toHaveBeenCalledWith('~/projects/', 'ep-1');
    expect(result.current.currentDir).toBe('~/projects');
    expect(result.current.suggestions.map((entry) => entry.name)).toEqual(['remote-repo']);
  });

  it('keeps the typing debounce when only the input changes on the same endpoint', async () => {
    const browseDirectory = vi.fn(async () => ({
      success: true,
      input_path: '~/projects/',
      directory: '/Users/victor/projects',
      entries: [],
      home_path: '/Users/victor',
    }));

    const { rerender } = renderHook(
      ({ inputPath, endpointId }) => useFilesystemSuggestions(inputPath, endpointId, browseDirectory),
      { initialProps: { inputPath: '~/projects/', endpointId: undefined as string | undefined } },
    );

    await act(async () => {
      vi.advanceTimersByTime(150);
      await Promise.resolve();
    });

    expect(browseDirectory).toHaveBeenCalledTimes(1);

    act(() => {
      rerender({ inputPath: '~/projects/attn', endpointId: undefined });
    });

    expect(browseDirectory).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(149);
      await Promise.resolve();
    });

    expect(browseDirectory).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(1);
      await Promise.resolve();
    });

    expect(browseDirectory).toHaveBeenCalledTimes(2);
    expect(browseDirectory).toHaveBeenLastCalledWith('~/projects/attn', undefined);
  });
});
