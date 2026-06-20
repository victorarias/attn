import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { FileTree } from './FileTree';
import type { FsEntry } from '../../hooks/useDaemonSocket';

// A tiny fixture tree: root has a directory (areas/) and a file (index.md); areas/
// holds one file (foo.md). listDir returns the children for a given directory.
const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'areas', name: 'areas', isDir: true, size: 0 },
    { path: 'index.md', name: 'index.md', isDir: false, size: 10, modified: '2026-06-20T00:00:00Z' },
  ],
  areas: [{ path: 'areas/foo.md', name: 'foo.md', isDir: false, size: 5 }],
};

function makeListDir() {
  return vi.fn<(path: string) => Promise<FsEntry[]>>().mockImplementation((p) => Promise.resolve(TREE[p] ?? []));
}

describe('FileTree', () => {
  afterEach(() => vi.restoreAllMocks());

  it('lists the root on mount and renders its directories and files', async () => {
    const listDir = makeListDir();
    render(<FileTree listDir={listDir} selectedPath={null} onSelectFile={vi.fn()} />);

    expect(await screen.findByRole('treeitem', { name: 'areas' })).toBeInTheDocument();
    expect(screen.getByRole('treeitem', { name: 'index.md' })).toBeInTheDocument();
    expect(listDir).toHaveBeenCalledWith('');
    // Shallow: the nested file is not listed until areas/ is expanded.
    expect(screen.queryByRole('treeitem', { name: 'foo.md' })).not.toBeInTheDocument();
  });

  it('expands a directory lazily, caches it on collapse/re-expand, and never re-fetches', async () => {
    const listDir = makeListDir();
    render(<FileTree listDir={listDir} selectedPath={null} onSelectFile={vi.fn()} />);

    const areas = await screen.findByRole('treeitem', { name: 'areas' });
    expect(areas).toHaveAttribute('aria-expanded', 'false');
    expect(listDir).not.toHaveBeenCalledWith('areas');

    // Expand: areas/ is listed and its child appears.
    fireEvent.click(areas);
    expect(await screen.findByRole('treeitem', { name: 'foo.md' })).toBeInTheDocument();
    expect(screen.getByRole('treeitem', { name: 'areas' })).toHaveAttribute('aria-expanded', 'true');
    expect(listDir).toHaveBeenCalledWith('areas');
    expect(listDir.mock.calls.filter(([p]) => p === 'areas')).toHaveLength(1);

    // Collapse: the child is hidden.
    fireEvent.click(screen.getByRole('treeitem', { name: 'areas' }));
    await waitFor(() => expect(screen.queryByRole('treeitem', { name: 'foo.md' })).not.toBeInTheDocument());

    // Re-expand: served from cache, no second fetch for areas/.
    fireEvent.click(screen.getByRole('treeitem', { name: 'areas' }));
    expect(await screen.findByRole('treeitem', { name: 'foo.md' })).toBeInTheDocument();
    expect(listDir.mock.calls.filter(([p]) => p === 'areas')).toHaveLength(1);
  });

  it('calls onSelectFile when a file is clicked and marks the selected file', async () => {
    const listDir = makeListDir();
    const onSelectFile = vi.fn();
    const { rerender } = render(
      <FileTree listDir={listDir} selectedPath={null} onSelectFile={onSelectFile} />,
    );

    const file = await screen.findByRole('treeitem', { name: 'index.md' });
    fireEvent.click(file);
    expect(onSelectFile).toHaveBeenCalledWith('index.md');
    // Clicking a file never triggers a directory listing.
    expect(listDir).toHaveBeenCalledTimes(1);

    // When it becomes the selection, it is marked current.
    rerender(<FileTree listDir={listDir} selectedPath="index.md" onSelectFile={onSelectFile} />);
    expect(screen.getByRole('treeitem', { name: 'index.md' })).toHaveAttribute('aria-current', 'true');
  });

  it('re-lists the root and any open directory when the change signal bumps', async () => {
    const listDir = makeListDir();
    const { rerender } = render(
      <FileTree listDir={listDir} selectedPath={null} onSelectFile={vi.fn()} changeSignal={0} />,
    );

    fireEvent.click(await screen.findByRole('treeitem', { name: 'areas' }));
    await screen.findByRole('treeitem', { name: 'foo.md' });
    const rootCalls = listDir.mock.calls.filter(([p]) => p === '').length;
    const areasCalls = listDir.mock.calls.filter(([p]) => p === 'areas').length;

    // A change signal re-lists the root AND the open directory (preserving expansion).
    rerender(<FileTree listDir={listDir} selectedPath={null} onSelectFile={vi.fn()} changeSignal={1} />);
    await waitFor(() => {
      expect(listDir.mock.calls.filter(([p]) => p === '').length).toBe(rootCalls + 1);
      expect(listDir.mock.calls.filter(([p]) => p === 'areas').length).toBe(areasCalls + 1);
    });
    expect(screen.getByRole('treeitem', { name: 'foo.md' })).toBeInTheDocument();
  });

  it('surfaces a directory listing error without blanking the tree', async () => {
    const listDir = vi
      .fn<(path: string) => Promise<FsEntry[]>>()
      .mockRejectedValue(new Error('fsdoc: permission denied'));
    render(<FileTree listDir={listDir} selectedPath={null} onSelectFile={vi.fn()} />);

    expect(await screen.findByText('fsdoc: permission denied')).toBeInTheDocument();
  });
});
