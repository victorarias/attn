import { describe, expect, it, vi } from 'vitest';
import { fsIndexToNotebookEntries } from './App';
import type { FsIndexResult } from './hooks/useDaemonSocket';

// Pure-logic coverage for the fs_index -> NotebookEntry adapter that backs the
// ⌘P finder (both notebook tiles and the fullscreen NotebookBrowser) since PR5
// switched the finder off notebook_list onto the root-scoped fs_index command.
// See makeNotebookSurfaceDaemon / notebookBrowserListFiles in App.tsx.

describe('fsIndexToNotebookEntries', () => {
  it('maps each fs_index path to a NotebookEntry with size 0', () => {
    const result: FsIndexResult = {
      root: '/Users/victor/code/attn',
      files: ['README.md', 'docs/plan.md'],
      truncated: false,
    };

    expect(fsIndexToNotebookEntries(result)).toEqual([
      { path: 'README.md', size: 0 },
      { path: 'docs/plan.md', size: 0 },
    ]);
  });

  it('returns an empty list for an empty index', () => {
    const result: FsIndexResult = { root: '/Users/victor/code/attn', files: [], truncated: false };
    expect(fsIndexToNotebookEntries(result)).toEqual([]);
  });

  it('still returns entries for a truncated result (partial beats none) but warns', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const result: FsIndexResult = {
      root: '/Users/victor/code/attn',
      files: ['README.md'],
      truncated: true,
    };

    expect(fsIndexToNotebookEntries(result)).toEqual([{ path: 'README.md', size: 0 }]);
    expect(warnSpy).toHaveBeenCalledTimes(1);
    expect(warnSpy.mock.calls[0][0]).toContain('[App]');

    warnSpy.mockRestore();
  });

  it('does not warn when the result is not truncated', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const result: FsIndexResult = { root: '/Users/victor/code/attn', files: ['a.md'], truncated: false };

    fsIndexToNotebookEntries(result);

    expect(warnSpy).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });
});
