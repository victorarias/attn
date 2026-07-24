import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MarkdownOpener } from './MarkdownOpener';

const RECENTS = [
  { path: '/repo/docs/plan.md', lastAt: '2026-07-24T10:00:00Z' },
  { path: '/other/journal.md', lastAt: '2026-07-23T10:00:00Z' },
];

const BROWSE = {
  success: true,
  input_path: '~/notes/',
  directory: '/home/victor/notes',
  home_path: '/home/victor',
  entries: [
    { name: 'archive', path: '/home/victor/notes/archive', is_dir: true },
    { name: 'ideas.md', path: '/home/victor/notes/ideas.md', is_dir: false },
  ],
};

function renderOpener(overrides: Partial<React.ComponentProps<typeof MarkdownOpener>> = {}) {
  const props = {
    root: '/repo',
    loadRecents: vi.fn().mockResolvedValue(RECENTS),
    loadIndex: vi.fn().mockResolvedValue({ files: ['docs/design.md', 'README.md'], truncated: false }),
    browseDirectory: vi.fn().mockResolvedValue(BROWSE),
    onPick: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
  render(<MarkdownOpener {...props} />);
  return props;
}

const rows = () => screen.queryAllByRole('option').map((row) => row.textContent);

describe('MarkdownOpener', () => {
  it('lists recents on an empty query, not the whole index', async () => {
    renderOpener();
    await waitFor(() => expect(rows()).toHaveLength(2));
    // Recents are labeled relative to the fuzzy root when they live under it.
    expect(rows()[0]).toContain('docs/plan.md');
    expect(rows()[1]).toContain('/other/journal.md');
  });

  it('opens on recents before the index resolves', async () => {
    let resolveIndex: (value: { files: string[]; truncated: boolean }) => void = () => {};
    const loadIndex = vi.fn().mockReturnValue(new Promise<{ files: string[]; truncated: boolean }>((resolve) => {
      resolveIndex = resolve;
    }));
    renderOpener({ loadIndex });

    // The palette is usable while the enumeration is still running: a cold
    // index must never delay Cmd+P.
    await waitFor(() => expect(rows()).toHaveLength(2));
    resolveIndex({ files: ['docs/design.md'], truncated: false });
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'design' } });
    await waitFor(() => expect(rows()[0]).toContain('docs/design.md'));
  });

  it('ranks recents and index results in one list once typing starts', async () => {
    renderOpener();
    await waitFor(() => expect(rows()).toHaveLength(2));

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'd' } });
    await waitFor(() => expect(rows().length).toBeGreaterThan(1));
    // One list: a remembered file and an index-only file both appear.
    const text = rows().join('|');
    expect(text).toContain('docs/plan.md');
    expect(text).toContain('docs/design.md');
  });

  it('picks the absolute path, not the displayed label', async () => {
    const { onPick } = renderOpener();
    await waitFor(() => expect(rows()).toHaveLength(2));

    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith('/repo/docs/plan.md');
  });

  it('skips the index and lists recents when there is no root', async () => {
    const { loadIndex } = renderOpener({ root: null });
    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(loadIndex).not.toHaveBeenCalled();
  });

  it('says the index is capped rather than implying the list is complete', async () => {
    renderOpener({ loadIndex: vi.fn().mockResolvedValue({ files: [], truncated: true }) });
    await waitFor(() => expect(rows()).toHaveLength(2));

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'zzzz' } });
    await waitFor(() => expect(screen.getByText(/index is capped/)).toBeTruthy());
  });

  describe('path mode', () => {
    const type = (value: string) => fireEvent.change(screen.getByRole('combobox'), { target: { value } });

    it('lists the typed directory instead of fuzzy-matching', async () => {
      const { browseDirectory } = renderOpener();
      await waitFor(() => expect(rows()).toHaveLength(2));

      type('~/notes/');
      await waitFor(() => expect(rows()).toHaveLength(2));
      // Directories are marked with a trailing slash; files are not.
      expect(rows()[0]).toContain('archive/');
      expect(rows()[1]).toContain('ideas.md');
      // Markdown-only, so the daemon's listing carries the same filter as the index.
      await waitFor(() => expect(browseDirectory).toHaveBeenCalledWith('~/notes/', undefined, ['md']));
    });

    it('opens a file in path mode by its absolute path', async () => {
      const { onPick } = renderOpener();
      await waitFor(() => expect(rows()).toHaveLength(2));

      type('~/notes/');
      await waitFor(() => expect(rows()[1]).toContain('ideas.md'));
      fireEvent.keyDown(screen.getByRole('combobox'), { key: 'ArrowDown' });
      fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });

      expect(onPick).toHaveBeenCalledWith('/home/victor/notes/ideas.md');
    });

    it('descends into a directory instead of opening it', async () => {
      const { onPick } = renderOpener();
      await waitFor(() => expect(rows()).toHaveLength(2));

      type('~/notes/');
      await waitFor(() => expect(rows()[0]).toContain('archive/'));
      fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });

      // The palette stays open on the deeper directory rather than picking it,
      // and keeps the ~-shortened spelling the user was reading.
      expect(onPick).not.toHaveBeenCalled();
      expect(screen.getByRole('combobox')).toHaveValue('~/notes/archive/');
    });

    it('resolves a relative query against the fuzzy root, not the daemon cwd', async () => {
      const { browseDirectory } = renderOpener();
      await waitFor(() => expect(rows()).toHaveLength(2));

      type('./docs/');
      await waitFor(() => expect(browseDirectory).toHaveBeenCalledWith('/repo/docs/', undefined, ['md']));
    });

    it('does not browse for a query that merely contains a slash', async () => {
      const { browseDirectory } = renderOpener();
      await waitFor(() => expect(rows()).toHaveLength(2));

      type('docs/plan');
      await waitFor(() => expect(rows()[0]).toContain('docs/plan.md'));
      expect(browseDirectory).not.toHaveBeenCalled();
    });
  });
});
