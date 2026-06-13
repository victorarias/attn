import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NotebookBrowser, parseNotebookHref } from './NotebookBrowser';
import type { NotebookEntry, NotebookReadResult } from '../hooks/useDaemonSocket';

const ENTRIES: NotebookEntry[] = [
  { path: 'memory/index.md', kind: 'memory', title: 'Memory index', size: 10 },
  { path: 'journal/2026-06-13.md', kind: 'journal', title: '2026-06-13', size: 20 },
  { path: 'memory/decisions/foo.md', kind: 'memory', title: 'Foo decision', size: 30 },
];

function makeProps(overrides: Partial<React.ComponentProps<typeof NotebookBrowser>> = {}) {
  const listNotebook = vi.fn<() => Promise<NotebookEntry[]>>().mockResolvedValue(ENTRIES);
  const readNotebook = vi.fn<(path: string) => Promise<NotebookReadResult>>().mockImplementation((path) =>
    Promise.resolve({ path, content: `# ${path}\n\nSee [the decision](/memory/decisions/foo.md) for why.`, hash: 'h1' }),
  );
  const backlinksNotebook = vi.fn<(path: string) => Promise<NotebookEntry[]>>().mockResolvedValue([
    { path: 'journal/2026-06-13.md', kind: 'journal', title: '2026-06-13', size: 20 },
  ]);
  return {
    props: {
      isOpen: true,
      onClose: vi.fn(),
      listNotebook,
      readNotebook,
      backlinksNotebook,
      changeSignal: 0,
      ...overrides,
    },
    listNotebook,
    readNotebook,
    backlinksNotebook,
  };
}

describe('NotebookBrowser', () => {
  it('renders nothing when closed', () => {
    const { props } = makeProps({ isOpen: false });
    const { container } = render(<NotebookBrowser {...props} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('lists notes, opens the preferred note, and shows backlinks', async () => {
    const { props, listNotebook, readNotebook, backlinksNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    // The sidebar lists every note, grouped. ('Foo decision' is unique to the
    // sidebar; 'Memory index' also appears as the open note's title.)
    expect(await screen.findByText('Foo decision')).toBeInTheDocument();
    expect(screen.getByText('memory/decisions/foo.md')).toBeInTheDocument();
    expect(listNotebook).toHaveBeenCalledTimes(1);

    // memory/index.md is the preferred first selection and is read + rendered.
    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('memory/index.md'));
    expect(backlinksNotebook).toHaveBeenCalledWith('memory/index.md');
    // The rendered note body (h1 from the markdown content).
    expect(await screen.findByRole('heading', { level: 1, name: 'memory/index.md' })).toBeInTheDocument();
    // The backlink entry is shown.
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it('navigates when an in-notebook link is clicked', async () => {
    const { props, readNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    const link = await screen.findByRole('link', { name: 'the decision' });
    fireEvent.click(link);

    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('memory/decisions/foo.md'));
  });

  it('navigates when a backlink is clicked', async () => {
    const { props, readNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    const backlink = await screen.findByRole('button', { name: '2026-06-13' });
    fireEvent.click(backlink);

    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('journal/2026-06-13.md'));
  });

  it('re-fetches the tree and open note when the change signal bumps', async () => {
    const { props, listNotebook, readNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('memory/index.md'));
    const listCallsBefore = listNotebook.mock.calls.length;
    const openNoteReadsBefore = readNotebook.mock.calls.filter((c) => c[0] === 'memory/index.md').length;

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    await waitFor(() => expect(listNotebook.mock.calls.length).toBeGreaterThan(listCallsBefore));
    // The open note must be re-read, not just the tree — this is what makes the
    // live view reflect edits to the note the user is currently viewing.
    await waitFor(() =>
      expect(readNotebook.mock.calls.filter((c) => c[0] === 'memory/index.md').length).toBeGreaterThan(
        openNoteReadsBefore,
      ),
    );
  });

  it('clears the open note when a change signal shows it was deleted on disk', async () => {
    const { props, listNotebook } = makeProps();
    // First list (initial open) has the note; after the change signal it is gone
    // (an external delete the watcher surfaced).
    listNotebook.mockResolvedValue(ENTRIES.filter((e) => e.path !== 'memory/index.md'));
    listNotebook.mockResolvedValueOnce(ENTRIES);
    const { rerender } = render(<NotebookBrowser {...props} />);

    // The preferred note opens and renders.
    expect(await screen.findByRole('heading', { level: 1, name: 'memory/index.md' })).toBeInTheDocument();

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The deleted note's stale content is gone; the document returns to empty.
    expect(await screen.findByText('Nothing selected')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 1, name: 'memory/index.md' })).not.toBeInTheDocument();
  });

  it('shows the empty state and reads nothing when the notebook has no notes', async () => {
    const emptyList = vi.fn<() => Promise<NotebookEntry[]>>().mockResolvedValue([]);
    const { props, readNotebook } = makeProps({ listNotebook: emptyList });
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByText('No notes yet.')).toBeInTheDocument();
    // No phantom first-note read against a null/empty path.
    expect(readNotebook).not.toHaveBeenCalled();
  });

  it('moves focus into the dialog on open so the focus trap engages', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);

    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(dialog).toHaveFocus());
  });

  it('shows an error state when a note cannot be read but keeps the list', async () => {
    const { props, readNotebook } = makeProps();
    readNotebook.mockRejectedValueOnce(new Error('notebook: memory/index.md not found'));
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByText('Note unavailable')).toBeInTheDocument();
    // The sidebar still lists notes so the user can pick another.
    expect(screen.getByText('Foo decision')).toBeInTheDocument();
  });
});

describe('parseNotebookHref', () => {
  it('classifies a root-absolute .md target as in-notebook navigation', () => {
    expect(parseNotebookHref('/memory/decisions/foo.md')).toEqual({ kind: 'note', path: 'memory/decisions/foo.md', anchor: undefined });
  });

  it('strips an anchor from a note target', () => {
    expect(parseNotebookHref('/memory/foo.md#why')).toEqual({ kind: 'note', path: 'memory/foo.md', anchor: 'why' });
  });

  it('treats a bare fragment as an in-page anchor', () => {
    expect(parseNotebookHref('#section')).toEqual({ kind: 'fragment', anchor: 'section' });
  });

  it('treats http(s) and relative targets as external', () => {
    expect(parseNotebookHref('https://example.com').kind).toBe('external');
    expect(parseNotebookHref('./relative.md').kind).toBe('external');
    // A root-absolute path that is not a .md file is not in-notebook navigation.
    expect(parseNotebookHref('/etc/passwd').kind).toBe('external');
  });
});
