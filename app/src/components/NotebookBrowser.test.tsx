import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotebookBrowser, parseNotebookHref } from './NotebookBrowser';
import type { NotebookEntry, NotebookReadResult, NotebookSendToChiefResult, NotebookWriteResult } from '../hooks/useDaemonSocket';

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
  const writeNotebook = vi
    .fn<(path: string, content: string, baseHash?: string) => Promise<NotebookWriteResult>>()
    .mockImplementation((path) => Promise.resolve({ path, hash: 'h2', conflict: false }));
  const sendToChief = vi
    .fn<(selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>>()
    .mockResolvedValue({ path: 'inbox.md', nudged: false });
  return {
    props: {
      isOpen: true,
      onClose: vi.fn(),
      listNotebook,
      readNotebook,
      backlinksNotebook,
      writeNotebook,
      sendToChief,
      changeSignal: 0,
      ...overrides,
    },
    listNotebook,
    readNotebook,
    backlinksNotebook,
    writeNotebook,
    sendToChief,
  };
}

// mockSelection stubs window.getSelection so a mouseup on the rendered markdown
// behaves as if `text` is highlighted (empty/whitespace = collapsed selection).
function mockSelection(text: string) {
  const rect = { top: 40, left: 60, width: 120, height: 18 } as DOMRect;
  const sel = {
    toString: () => text,
    rangeCount: text.trim() ? 1 : 0,
    getRangeAt: () => ({ getBoundingClientRect: () => rect }),
  } as unknown as Selection;
  return vi.spyOn(window, 'getSelection').mockReturnValue(sel);
}

describe('NotebookBrowser', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

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

  it('keeps the open note when a change-signal refresh fails transiently', async () => {
    const { props, listNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('heading', { level: 1, name: 'memory/index.md' })).toBeInTheDocument();

    // The initial open succeeded; the refresh triggered by the change signal now
    // rejects (a transient WS hiccup, not a deletion).
    listNotebook.mockRejectedValueOnce(new Error('socket closed'));
    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // A failed refresh must NOT be mistaken for an empty/deleted tree: the open
    // note stays put and the document does not fall back to the empty state.
    await waitFor(() => expect(listNotebook.mock.calls.length).toBeGreaterThan(1));
    expect(screen.getByRole('heading', { level: 1, name: 'memory/index.md' })).toBeInTheDocument();
    expect(screen.queryByText('Nothing selected')).not.toBeInTheDocument();
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

  it('edits and saves a note via hash-CAS, then returns to the rendered view', async () => {
    const { props, writeNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));

    const textarea = await screen.findByRole('textbox', { name: 'Edit note' });
    fireEvent.change(textarea, { target: { value: '# edited\n' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    // Saved against the hash the note was loaded with (h1).
    await waitFor(() => expect(writeNotebook).toHaveBeenCalledWith('memory/index.md', '# edited\n', 'h1'));
    // The editor closes, the rendered view shows the new content, and a Saved
    // indicator appears.
    expect(await screen.findByRole('heading', { level: 1, name: 'edited' })).toBeInTheDocument();
    expect(screen.getByText('Saved')).toBeInTheDocument();
    expect(screen.queryByRole('textbox', { name: 'Edit note' })).not.toBeInTheDocument();
  });

  it('surfaces a save conflict and lets the user overwrite the on-disk version', async () => {
    const { props, writeNotebook } = makeProps();
    // First save conflicts (note changed on disk); the overwrite then succeeds.
    writeNotebook
      .mockResolvedValueOnce({ path: 'memory/index.md', conflict: true, currentHash: 'hX' })
      .mockResolvedValueOnce({ path: 'memory/index.md', hash: 'h3', conflict: false });
    render(<NotebookBrowser {...props} />);

    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(await screen.findByRole('textbox', { name: 'Edit note' }), { target: { value: '# mine\n' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    // The conflict is surfaced and the editor stays open with the draft intact.
    expect(await screen.findByText(/changed on disk/i)).toBeInTheDocument();
    expect((screen.getByRole('textbox', { name: 'Edit note' }) as HTMLTextAreaElement).value).toBe('# mine\n');

    // Overwrite saves the draft against the current on-disk hash.
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite anyway' }));
    await waitFor(() => expect(writeNotebook).toHaveBeenLastCalledWith('memory/index.md', '# mine\n', 'hX'));
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Edit note' })).not.toBeInTheDocument());
  });

  it('discards the draft on cancel without saving', async () => {
    const { props, writeNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(await screen.findByRole('textbox', { name: 'Edit note' }), { target: { value: 'junk' } });
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(await screen.findByRole('heading', { level: 1, name: 'memory/index.md' })).toBeInTheDocument();
    expect(writeNotebook).not.toHaveBeenCalled();
  });

  it('keeps the draft when a change signal arrives mid-edit (no clobber)', async () => {
    const { props, readNotebook, listNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);

    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(await screen.findByRole('textbox', { name: 'Edit note' }), { target: { value: '# my draft\n' } });

    const listBefore = listNotebook.mock.calls.length;
    const readsBefore = readNotebook.mock.calls.length;
    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The change-signal effect runs (tree refreshed)...
    await waitFor(() => expect(listNotebook.mock.calls.length).toBeGreaterThan(listBefore));
    // ...but the open note is NOT reloaded under the editor, and the draft survives.
    expect((screen.getByRole('textbox', { name: 'Edit note' }) as HTMLTextAreaElement).value).toBe('# my draft\n');
    expect(readNotebook.mock.calls.length).toBe(readsBefore);
  });

  it('does not stamp a mid-save result onto a note the user navigated to', async () => {
    const { props, writeNotebook } = makeProps();
    // Defer the write so the user can navigate before it resolves.
    let resolveWrite: (r: NotebookWriteResult) => void = () => {};
    writeNotebook.mockImplementationOnce(
      () => new Promise<NotebookWriteResult>((resolve) => { resolveWrite = resolve; }),
    );
    render(<NotebookBrowser {...props} />);

    // Edit note A (memory/index.md) and click Save; the write is now in flight.
    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(await screen.findByRole('textbox', { name: 'Edit note' }), { target: { value: '# A edited\n' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(writeNotebook).toHaveBeenCalledWith('memory/index.md', '# A edited\n', 'h1'));

    // Navigate to note B (foo.md) before A's save resolves. B loads and renders.
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));
    await screen.findByRole('heading', { level: 1, name: 'memory/decisions/foo.md' });

    // A's stale save now resolves. It must NOT overwrite B's pane (no A body under
    // B, no spurious "Saved" badge) — the save targeted A's bytes correctly, but
    // its result no longer applies to what's on screen.
    await act(async () => {
      resolveWrite({ path: 'memory/index.md', hash: 'h2', conflict: false });
    });

    expect(screen.getByRole('heading', { level: 1, name: 'memory/decisions/foo.md' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 1, name: 'A edited' })).not.toBeInTheDocument();
    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
  });

  it('auto-dismisses the Saved badge after a successful save', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { props } = makeProps();
      render(<NotebookBrowser {...props} />);

      await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });
      fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
      fireEvent.change(await screen.findByRole('textbox', { name: 'Edit note' }), { target: { value: '# edited\n' } });
      fireEvent.click(screen.getByRole('button', { name: 'Save' }));

      // The badge appears on save success...
      expect(await screen.findByText('Saved')).toBeInTheDocument();
      // ...and clears itself rather than lingering while the user keeps reading.
      act(() => { vi.advanceTimersByTime(2600); });
      await waitFor(() => expect(screen.queryByText('Saved')).not.toBeInTheDocument());
    } finally {
      vi.useRealTimers();
    }
  });

  it('sends a highlighted selection to the chief and shows the outcome', async () => {
    const { props, sendToChief } = makeProps();
    const { container } = render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });

    // Highlight text in the rendered note; the floating action appears.
    mockSelection('a key decision');
    fireEvent.mouseUp(container.querySelector('.notebook-browser-markdown') as HTMLElement);
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }));

    // The selection + its source note go to the daemon, and the outcome is shown.
    await waitFor(() => expect(sendToChief).toHaveBeenCalledWith('a key decision', 'memory/index.md'));
    expect(await screen.findByText("Added to chief's inbox")).toBeInTheDocument();
    // The floating button clears once the send lands.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument());
  });

  it('shows no send-to-chief action for an empty selection', async () => {
    const { props, sendToChief } = makeProps();
    const { container } = render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });

    mockSelection('   '); // whitespace only
    fireEvent.mouseUp(container.querySelector('.notebook-browser-markdown') as HTMLElement);

    expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument();
    expect(sendToChief).not.toHaveBeenCalled();
  });

  it('surfaces an error when sending to the chief fails', async () => {
    const { props, sendToChief } = makeProps();
    sendToChief.mockRejectedValueOnce(new Error('no chief reachable'));
    const { container } = render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 1, name: 'memory/index.md' });

    mockSelection('something');
    fireEvent.mouseUp(container.querySelector('.notebook-browser-markdown') as HTMLElement);
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }));

    expect(await screen.findByText('no chief reachable')).toBeInTheDocument();
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
