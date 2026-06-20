import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotebookBrowser, parseNotebookHref } from './NotebookBrowser';
import type { NotebookEntry, NotebookReadResult, NotebookSendToChiefResult, NotebookTask, NotebookWriteResult } from '../hooks/useDaemonSocket';

// The live editor is CodeMirror-backed, which cannot mount under happy-dom (its
// async measure pass throws). The real editing experience (live preview, typing,
// link-follow, selection) is covered by the Playwright harness; here we mock the
// editor to a controlled textarea and expose its callbacks so these tests can drive
// the surrounding orchestration (autosave, conflict, flush, navigation guards).
const editorMock = vi.hoisted(() => ({
  current: null as null | {
    onFollowLink?: (href: string) => void;
    onSelectionChange?: (sel: { text: string; top: number; left: number } | null) => void;
  },
}));

vi.mock('./notebook/LiveMarkdownEditor', () => ({
  LiveMarkdownEditor: ({
    value,
    onChange,
    onFollowLink,
    onSelectionChange,
    ariaLabel,
  }: {
    value: string;
    onChange: (value: string) => void;
    onFollowLink?: (href: string) => void;
    onSelectionChange?: (sel: { text: string; top: number; left: number } | null) => void;
    ariaLabel?: string;
  }) => {
    editorMock.current = { onFollowLink, onSelectionChange };
    return (
      <textarea
        aria-label={ariaLabel ?? 'Note'}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  },
}));

const ENTRIES: NotebookEntry[] = [
  { path: 'knowledge/index.md', type: 'note', title: 'Knowledge index', size: 10 },
  { path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 },
  { path: 'knowledge/areas/foo.md', type: 'note', title: 'Foo decision', size: 30 },
];

const TASKS: NotebookTask[] = [
  {
    id: 'task-failed',
    kind: 'narrate',
    subject: 'workspace-alpha',
    state: 'failed',
    attempts: 2,
    next_attempt_at: '2026-06-14T12:00:00Z',
    created_at: '2026-06-14T11:00:00Z',
    updated_at: '2026-06-14T11:30:00Z',
    last_error: 'fake narrate failed: nothing new',
  },
  {
    id: 'task-done',
    kind: 'summarize',
    subject: 'session-42',
    state: 'done',
    attempts: 1,
    next_attempt_at: '0001-01-01T00:00:00Z',
    created_at: '2026-06-14T10:00:00Z',
    updated_at: '2026-06-14T10:05:00Z',
  },
];

function makeProps(overrides: Partial<React.ComponentProps<typeof NotebookBrowser>> = {}) {
  const listNotebook = vi.fn<() => Promise<NotebookEntry[]>>().mockResolvedValue(ENTRIES);
  const readNotebook = vi.fn<(path: string) => Promise<NotebookReadResult>>().mockImplementation((path) =>
    Promise.resolve({ path, content: `# ${path}\n\nSee [the decision](/knowledge/areas/foo.md) for why.`, hash: 'h1' }),
  );
  const backlinksNotebook = vi.fn<(path: string) => Promise<NotebookEntry[]>>().mockResolvedValue([
    { path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 },
  ]);
  const writeNotebook = vi
    .fn<(path: string, content: string, baseHash?: string) => Promise<NotebookWriteResult>>()
    .mockImplementation((path) => Promise.resolve({ path, hash: 'h2', conflict: false }));
  const sendToChief = vi
    .fn<(selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>>()
    .mockResolvedValue({ path: 'inbox.md', nudged: false });
  const listTasks = vi.fn<() => Promise<NotebookTask[]>>().mockResolvedValue(TASKS);
  const retryTask = vi
    .fn<(taskId: string) => Promise<NotebookTask | null>>()
    .mockImplementation((taskId) => Promise.resolve(TASKS.find((t) => t.id === taskId) ?? null));
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
      listTasks,
      retryTask,
      taskChangeSignal: 0,
      ...overrides,
    },
    listNotebook,
    readNotebook,
    backlinksNotebook,
    writeNotebook,
    sendToChief,
    listTasks,
    retryTask,
  };
}

// The single live editor surface (mocked to a textarea).
function editor() {
  return screen.getByRole('textbox', { name: 'Note' }) as HTMLTextAreaElement;
}

// Wait for the preferred note (knowledge/index.md) to load into the editor.
async function waitForNoteLoaded() {
  await waitFor(() => expect(editor().value).toContain('# knowledge/index.md'));
}

describe('NotebookBrowser', () => {
  afterEach(() => {
    editorMock.current = null;
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
    // sidebar; the open note's title shows in the document header instead.)
    expect(await screen.findByText('Foo decision')).toBeInTheDocument();
    expect(screen.getByText('knowledge/areas/foo.md')).toBeInTheDocument();
    expect(listNotebook).toHaveBeenCalledTimes(1);

    // knowledge/index.md is the preferred first selection and is read + loaded.
    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('knowledge/index.md'));
    expect(backlinksNotebook).toHaveBeenCalledWith('knowledge/index.md');
    // The document header shows the note's title, and its content loads into the editor.
    expect(await screen.findByRole('heading', { level: 2, name: 'Knowledge index' })).toBeInTheDocument();
    await waitForNoteLoaded();
    // The backlink entry is shown.
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it('buckets notes into PARA sidebar groups by their top-level path', async () => {
    const { props } = makeProps({
      listNotebook: vi.fn<() => Promise<NotebookEntry[]>>().mockResolvedValue([
        { path: 'journal/2026-06-13.md', type: 'journal', title: 'A journal day', size: 20 },
        { path: 'knowledge/projects/launch/index.md', type: 'note', title: 'Launch project', size: 30 },
        { path: 'knowledge/areas/infra.md', type: 'note', title: 'Infra area', size: 30 },
        { path: 'knowledge/index.md', type: 'note', title: 'Knowledge root', size: 10 },
      ]),
    });
    render(<NotebookBrowser {...props} />);

    // Each note falls under its PARA group header (knowledge/projects -> Projects,
    // knowledge/areas -> Areas, journal -> Journal, knowledge/index.md -> Knowledge).
    expect(await screen.findByText('Launch project')).toBeInTheDocument();
    for (const label of ['Journal', 'Projects', 'Areas', 'Knowledge']) {
      expect(screen.getByText(label)).toBeInTheDocument();
    }
  });

  it('navigates when an in-notebook link is followed', async () => {
    const { props, readNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'Knowledge index' });

    // The editor reports a mod-click on an in-notebook link via onFollowLink.
    act(() => editorMock.current!.onFollowLink!('/knowledge/areas/foo.md'));

    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('knowledge/areas/foo.md'));
  });

  it('navigates when a backlink is clicked', async () => {
    const { props, readNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'Knowledge index' });

    const backlink = await screen.findByRole('button', { name: '2026-06-13' });
    fireEvent.click(backlink);

    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('journal/2026-06-13.md'));
  });

  it('renders the clicked note immediately without waiting on the slower backlinks fetch', async () => {
    const { props, backlinksNotebook } = makeProps();
    // Backlinks walks every note in the daemon and is far slower than a single file
    // read; defer it so we can prove the editor content does NOT wait on it.
    let resolveBacklinks: (e: NotebookEntry[]) => void = () => {};
    backlinksNotebook.mockImplementation(
      () => new Promise<NotebookEntry[]>((resolve) => { resolveBacklinks = resolve; }),
    );
    render(<NotebookBrowser {...props} />);

    // The preferred note's content renders even though its backlinks are still pending.
    await waitForNoteLoaded();
    expect(await screen.findByRole('heading', { level: 2, name: 'Knowledge index' })).toBeInTheDocument();

    // Click another note: its content appears before its backlinks resolve — no stale
    // previous-file content stranded under the new selection.
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));
    expect(screen.getByRole('heading', { level: 2, name: 'Foo decision' })).toBeInTheDocument();

    // Backlinks fills in later, independently.
    await act(async () => {
      resolveBacklinks([{ path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 }]);
    });
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it('re-fetches the tree and open note when the change signal bumps', async () => {
    const { props, listNotebook, readNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('knowledge/index.md'));
    const listCallsBefore = listNotebook.mock.calls.length;
    const openNoteReadsBefore = readNotebook.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length;

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    await waitFor(() => expect(listNotebook.mock.calls.length).toBeGreaterThan(listCallsBefore));
    // The open note must be re-read, not just the tree — this is what makes the
    // live view reflect edits to the note the user is currently viewing.
    await waitFor(() =>
      expect(readNotebook.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length).toBeGreaterThan(
        openNoteReadsBefore,
      ),
    );
  });

  it('clears the open note when a change signal shows it was deleted on disk', async () => {
    const { props, listNotebook } = makeProps();
    // First list (initial open) has the note; after the change signal it is gone
    // (an external delete the watcher surfaced).
    listNotebook.mockResolvedValue(ENTRIES.filter((e) => e.path !== 'knowledge/index.md'));
    listNotebook.mockResolvedValueOnce(ENTRIES);
    const { rerender } = render(<NotebookBrowser {...props} />);

    // The preferred note opens and shows its title.
    expect(await screen.findByRole('heading', { level: 2, name: 'Knowledge index' })).toBeInTheDocument();

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The deleted note is gone; the document returns to the empty state.
    expect(await screen.findByText('Nothing selected')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2, name: 'Knowledge index' })).not.toBeInTheDocument();
  });

  it('keeps the open note when a change-signal refresh fails transiently', async () => {
    const { props, listNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('heading', { level: 2, name: 'Knowledge index' })).toBeInTheDocument();

    // The initial open succeeded; the refresh triggered by the change signal now
    // rejects (a transient WS hiccup, not a deletion).
    listNotebook.mockRejectedValueOnce(new Error('socket closed'));
    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // A failed refresh must NOT be mistaken for an empty/deleted tree: the open
    // note stays put and the document does not fall back to the empty state.
    await waitFor(() => expect(listNotebook.mock.calls.length).toBeGreaterThan(1));
    expect(screen.getByRole('heading', { level: 2, name: 'Knowledge index' })).toBeInTheDocument();
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
    readNotebook.mockRejectedValueOnce(new Error('notebook: knowledge/index.md not found'));
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByText('Note unavailable')).toBeInTheDocument();
    // The sidebar still lists notes so the user can pick another.
    expect(screen.getByText('Foo decision')).toBeInTheDocument();
  });

  it('autosaves an edited note via hash-CAS and shows a Saved indicator', async () => {
    const { props, writeNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Type into the single live surface — there is no edit mode to enter.
    fireEvent.change(editor(), { target: { value: '# edited\n' } });

    // The debounced autosave persists against the loaded hash (h1); no Save button.
    await waitFor(
      () => expect(writeNotebook).toHaveBeenCalledWith('knowledge/index.md', '# edited\n', 'h1'),
      { timeout: 2000 },
    );
    expect(await screen.findByText('Saved')).toBeInTheDocument();
  });

  it('there is no view/edit toggle — the surface is always editable', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // The editor is present and editable on open; no Edit/Save/Cancel controls exist.
    expect(editor()).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument();
  });

  it('surfaces an autosave conflict and lets the user overwrite the on-disk version', async () => {
    const { props, writeNotebook } = makeProps();
    // First autosave conflicts (note changed on disk); the overwrite then succeeds.
    writeNotebook
      .mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' })
      .mockResolvedValueOnce({ path: 'knowledge/index.md', hash: 'h3', conflict: false });
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# mine\n' } });

    // The conflict is surfaced and the buffer is intact.
    expect(await screen.findByText(/changed on disk/i)).toBeInTheDocument();
    expect(editor().value).toBe('# mine\n');

    // Overwrite saves the buffer against the current on-disk hash.
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite anyway' }));
    await waitFor(() => expect(writeNotebook).toHaveBeenLastCalledWith('knowledge/index.md', '# mine\n', 'hX'));
    await waitFor(() => expect(screen.queryByText(/changed on disk/i)).not.toBeInTheDocument());
  });

  it('flushes an unsaved buffer when navigating away before autosave fires', async () => {
    const { props, writeNotebook, readNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Type, then immediately navigate away (faster than the 700ms autosave debounce).
    fireEvent.change(editor(), { target: { value: '# quick edit\n' } });
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));

    // The outgoing buffer is flushed against its loaded hash, so the edit isn't lost,
    // and (the flush having succeeded) the navigation lands on the new note.
    await waitFor(() => expect(writeNotebook).toHaveBeenCalledWith('knowledge/index.md', '# quick edit\n', 'h1'));
    await waitFor(() => expect(readNotebook).toHaveBeenCalledWith('knowledge/areas/foo.md'));
    expect(await screen.findByRole('heading', { level: 2, name: 'Foo decision' })).toBeInTheDocument();
  });

  it('aborts navigation and surfaces the conflict when the navigate flush conflicts', async () => {
    const { props, writeNotebook, readNotebook } = makeProps();
    // The flush triggered by navigating away conflicts (the note changed on disk).
    writeNotebook.mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' });
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Type, then navigate away before the autosave fires; the flush hits the conflict.
    fireEvent.change(editor(), { target: { value: '# mine\n' } });
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));

    // The flush wrote against the loaded hash and came back conflicted, so the
    // navigation is abandoned: we stay on the current note, the conflict banner shows,
    // and the buffer is intact — the edit is NOT lost behind a silent navigation.
    await waitFor(() => expect(writeNotebook).toHaveBeenCalledWith('knowledge/index.md', '# mine\n', 'h1'));
    expect(await screen.findByText(/changed on disk/i)).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'Knowledge index' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2, name: 'Foo decision' })).not.toBeInTheDocument();
    expect(editor().value).toBe('# mine\n');
    expect(readNotebook).not.toHaveBeenCalledWith('knowledge/areas/foo.md');
  });

  it('does not stamp a stale reload-from-disk onto a note navigated to mid-reload', async () => {
    const { props, writeNotebook, readNotebook } = makeProps();
    // Autosave conflicts (so the "Reload from disk" affordance appears); the later
    // navigate flush then succeeds, superseding the in-flight reload.
    writeNotebook
      .mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' })
      .mockResolvedValueOnce({ path: 'knowledge/index.md', hash: 'h2', conflict: false });
    // Defer the SECOND read of index (the reload) so navigation can outrun it.
    let indexReads = 0;
    let resolveReload: (r: NotebookReadResult) => void = () => {};
    readNotebook.mockImplementation((path) => {
      if (path === 'knowledge/index.md') {
        indexReads += 1;
        if (indexReads === 2) return new Promise<NotebookReadResult>((resolve) => { resolveReload = resolve; });
      }
      return Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' });
    });
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Edit → the debounced autosave conflicts → the reconcile banner appears.
    fireEvent.change(editor(), { target: { value: '# mine\n' } });
    expect(await screen.findByText(/changed on disk/i, undefined, { timeout: 2500 })).toBeInTheDocument();

    // Start a reload from disk (its read is deferred), then navigate away before it
    // resolves; the navigate flush succeeds and lands us on the new note.
    fireEvent.click(screen.getByRole('button', { name: 'Reload from disk' }));
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));
    await screen.findByRole('heading', { level: 2, name: 'Foo decision' });
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));

    // The stale reload of index now resolves — it must NOT stamp its content over the
    // note the user navigated to.
    await act(async () => {
      resolveReload({ path: 'knowledge/index.md', content: '# reloaded index\n', hash: 'hX' });
    });
    expect(editor().value).not.toContain('# reloaded index');
    expect(editor().value).toContain('# knowledge/areas/foo.md');
  });

  it('keeps the buffer when a change signal arrives with unsaved edits (no clobber)', async () => {
    const { props, readNotebook, listNotebook } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# my draft\n' } });

    const listBefore = listNotebook.mock.calls.length;
    const readsBefore = readNotebook.mock.calls.length;
    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The change-signal effect runs (tree refreshed)...
    await waitFor(() => expect(listNotebook.mock.calls.length).toBeGreaterThan(listBefore));
    // ...but the open note is NOT reloaded under the buffer, and the edit survives.
    expect(editor().value).toBe('# my draft\n');
    expect(readNotebook.mock.calls.length).toBe(readsBefore);
  });

  it('does not stamp a mid-autosave result onto a note the user navigated to', async () => {
    const { props, writeNotebook } = makeProps();
    // Defer the first write so the user can navigate before it resolves.
    let resolveWrite: (r: NotebookWriteResult) => void = () => {};
    writeNotebook.mockImplementationOnce(
      () => new Promise<NotebookWriteResult>((resolve) => { resolveWrite = resolve; }),
    );
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Edit note A (knowledge/index.md); its autosave is now in flight.
    fireEvent.change(editor(), { target: { value: '# A edited\n' } });
    await waitFor(
      () => expect(writeNotebook).toHaveBeenCalledWith('knowledge/index.md', '# A edited\n', 'h1'),
      { timeout: 2000 },
    );

    // Navigate to note B (foo.md) before A's save resolves. B loads.
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));
    await screen.findByRole('heading', { level: 2, name: 'Foo decision' });
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));

    // A's stale save resolves. It must NOT overwrite B's pane (no A body under B,
    // no spurious "Saved") — the save targeted A's bytes, but no longer applies.
    await act(async () => {
      resolveWrite({ path: 'knowledge/index.md', hash: 'h2', conflict: false });
    });

    expect(editor().value).not.toContain('# A edited');
    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
  });

  it('auto-dismisses the Saved indicator after a successful autosave', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# edited\n' } });

    // The indicator appears on save success...
    expect(await screen.findByText('Saved')).toBeInTheDocument();
    // ...and clears itself rather than lingering while the user keeps reading.
    await waitFor(() => expect(screen.queryByText('Saved')).not.toBeInTheDocument(), { timeout: 4000 });
  }, 10000);

  it('sends an editor selection to the chief and shows the outcome', async () => {
    const { props, sendToChief } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'Knowledge index' });

    // The editor reports a non-empty selection; the floating action appears.
    act(() => editorMock.current!.onSelectionChange!({ text: 'a key decision', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }));

    // The selection + its source note go to the daemon, and the outcome is shown.
    await waitFor(() => expect(sendToChief).toHaveBeenCalledWith('a key decision', 'knowledge/index.md'));
    expect(await screen.findByText("Added to chief's inbox")).toBeInTheDocument();
    // The floating button clears once the send lands.
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument());
  });

  it('shows no send-to-chief action for a collapsed selection', async () => {
    const { props, sendToChief } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'Knowledge index' });

    act(() => editorMock.current!.onSelectionChange!(null));

    expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument();
    expect(sendToChief).not.toHaveBeenCalled();
  });

  it('surfaces an error when sending to the chief fails', async () => {
    const { props, sendToChief } = makeProps();
    sendToChief.mockRejectedValueOnce(new Error('no chief reachable'));
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'Knowledge index' });

    act(() => editorMock.current!.onSelectionChange!({ text: 'something', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }));

    expect(await screen.findByText('no chief reachable')).toBeInTheDocument();
  });

  it('does not flash a send-to-chief outcome on a note navigated to mid-send', async () => {
    const { props, sendToChief } = makeProps();
    // Defer the send so the user can navigate before it resolves.
    let resolveSend: (r: NotebookSendToChiefResult) => void = () => {};
    sendToChief.mockImplementationOnce(
      () => new Promise<NotebookSendToChiefResult>((resolve) => { resolveSend = resolve; }),
    );
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'Knowledge index' });

    // Select + send from note A; the send is now in flight.
    act(() => editorMock.current!.onSelectionChange!({ text: 'from A', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }));
    await waitFor(() => expect(sendToChief).toHaveBeenCalledWith('from A', 'knowledge/index.md'));

    // Navigate to note B before A's send resolves; B loads.
    fireEvent.click(screen.getByRole('button', { name: /Foo decision/ }));
    await screen.findByRole('heading', { level: 2, name: 'Foo decision' });

    // A's stale send resolves — its outcome must NOT flash on note B.
    await act(async () => {
      resolveSend({ path: 'inbox.md', nudged: false });
    });
    expect(screen.queryByText("Added to chief's inbox")).not.toBeInTheDocument();
  });

  // Open the collapsible Tasks section and wait for the seeded rows to fetch.
  async function openTasks() {
    fireEvent.click(await screen.findByRole('button', { name: /^Tasks$/ }));
  }

  it('lists durable runner tasks (kind:subject + state) when the Tasks section is opened', async () => {
    const { props, listTasks } = makeProps();
    render(<NotebookBrowser {...props} />);

    // The section is collapsed on open, so no fetch yet.
    expect(listTasks).not.toHaveBeenCalled();
    await openTasks();

    // Both seeded rows render their kind:subject and state.
    expect(await screen.findByText('narrate:workspace-alpha')).toBeInTheDocument();
    expect(screen.getByText('summarize:session-42')).toBeInTheDocument();
    expect(screen.getByText('failed')).toBeInTheDocument();
    expect(screen.getByText('done')).toBeInTheDocument();
    expect(listTasks).toHaveBeenCalledTimes(1);
  });

  it('shows a Retry button on a failed/dead task but not on a done task', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);
    await openTasks();

    // Exactly one Retry button (the failed task); the done task has none.
    const retryButtons = await screen.findAllByRole('button', { name: 'Retry' });
    expect(retryButtons).toHaveLength(1);
    // It belongs to the failed row, not the done row.
    const failedRow = screen.getByText('narrate:workspace-alpha').closest('.notebook-browser-task') as HTMLElement;
    const doneRow = screen.getByText('summarize:session-42').closest('.notebook-browser-task') as HTMLElement;
    expect(failedRow).toContainElement(retryButtons[0]);
    expect(doneRow.querySelector('.notebook-browser-task-retry')).toBeNull();
  });

  it('calls retryTask with the task id when Retry is clicked', async () => {
    const { props, retryTask } = makeProps();
    render(<NotebookBrowser {...props} />);
    await openTasks();

    const retry = await screen.findByRole('button', { name: 'Retry' });
    fireEvent.click(retry);
    await waitFor(() => expect(retryTask).toHaveBeenCalledWith('task-failed'));
    // The in-flight mark clears once retryTask resolves, re-enabling the button —
    // wait for it so the state update settles inside the test (no act warning).
    await waitFor(() => expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled());
  });
});

describe('parseNotebookHref', () => {
  it('classifies a root-absolute .md target as in-notebook navigation', () => {
    expect(parseNotebookHref('/knowledge/areas/foo.md')).toEqual({ kind: 'note', path: 'knowledge/areas/foo.md', anchor: undefined });
  });

  it('strips an anchor from a note target', () => {
    expect(parseNotebookHref('/knowledge/areas/foo.md#why')).toEqual({ kind: 'note', path: 'knowledge/areas/foo.md', anchor: 'why' });
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
