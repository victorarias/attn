import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotebookBrowser, parseNotebookHref } from './NotebookBrowser';
import type { FsEntry, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult, NotebookTask } from '../hooks/useDaemonSocket';

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
  // Character offsets passed to the editor's imperative scrollToPos handle, so a test
  // can assert the outline jumped the editor to a heading (the real scroll is a CM
  // browser behavior, covered by the Playwright harness).
  scrollCalls: [] as number[],
}));

vi.mock('./notebook/LiveMarkdownEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react');
  return {
    LiveMarkdownEditor: forwardRef(function MockLiveMarkdownEditor(
      {
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
      },
      ref: React.Ref<{ scrollToPos: (pos: number) => void }>,
    ) {
      editorMock.current = { onFollowLink, onSelectionChange };
      useImperativeHandle(ref, () => ({ scrollToPos: (pos: number) => editorMock.scrollCalls.push(pos) }), []);
      return (
        <textarea
          aria-label={ariaLabel ?? 'Note'}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        />
      );
    }),
  };
});

// A fixture filesystem the lazy sidebar tree lists per-directory ('' = root). The root
// holds two folders plus a plain-text file and a binary file directly (so a single
// click selects them without expanding); knowledge/ holds the preferred index.
const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'knowledge', name: 'knowledge', isDir: true, size: 0 },
    { path: 'journal', name: 'journal', isDir: true, size: 0 },
    { path: 'notes.txt', name: 'notes.txt', isDir: false, size: 64 },
    { path: 'cover.png', name: 'cover.png', isDir: false, size: 4096 },
  ],
  knowledge: [
    { path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 128 },
    { path: 'knowledge/areas', name: 'areas', isDir: true, size: 0 },
  ],
  'knowledge/areas': [{ path: 'knowledge/areas/foo.md', name: 'foo.md', isDir: false, size: 30 }],
  journal: [{ path: 'journal/2026-06-13.md', name: '2026-06-13.md', isDir: false, size: 20 }],
};

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
  const listDir = vi
    .fn<(path: string) => Promise<FsEntry[]>>()
    .mockImplementation((path) => Promise.resolve(TREE[path] ?? []));
  const readFile = vi.fn<(path: string) => Promise<FsReadResult>>().mockImplementation((path) =>
    Promise.resolve({ path, content: `# ${path}\n\nSee [the decision](/knowledge/areas/foo.md) for why.`, hash: 'h1' }),
  );
  const backlinksNotebook = vi.fn<(path: string) => Promise<NotebookEntry[]>>().mockResolvedValue([
    { path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 },
  ]);
  const writeFile = vi
    .fn<(path: string, content: string, baseHash?: string) => Promise<FsWriteResult>>()
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
      listDir,
      readFile,
      backlinksNotebook,
      writeFile,
      sendToChief,
      changeSignal: 0,
      listTasks,
      retryTask,
      taskChangeSignal: 0,
      ...overrides,
    },
    listDir,
    readFile,
    backlinksNotebook,
    writeFile,
    sendToChief,
    listTasks,
    retryTask,
  };
}

// The single live markdown editor surface (mocked to a textarea, aria-label 'Note').
function editor() {
  return screen.getByRole('textbox', { name: 'Note' }) as HTMLTextAreaElement;
}

// Wait for the preferred note (knowledge/index.md) to load into the editor.
async function waitForNoteLoaded() {
  await waitFor(() => expect(editor().value).toContain('# knowledge/index.md'));
}

// Follow an in-notebook link to navigate (the editor reports a mod-click via
// onFollowLink). Used in place of a sidebar click, which would require expanding the
// lazy tree to reach a nested note.
function followLink(href: string) {
  act(() => editorMock.current!.onFollowLink!(href));
}

const FOO = '/knowledge/areas/foo.md';

describe('NotebookBrowser', () => {
  afterEach(() => {
    editorMock.current = null;
    editorMock.scrollCalls.length = 0;
    vi.restoreAllMocks();
  });

  it('renders nothing when closed', () => {
    const { props } = makeProps({ isOpen: false });
    const { container } = render(<NotebookBrowser {...props} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('opens the preferred note and shows its content + backlinks', async () => {
    const { props, readFile, backlinksNotebook } = makeProps();
    render(<NotebookBrowser {...props} />);

    // knowledge/index.md is the preferred first selection and is read + loaded.
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/index.md'));
    expect(backlinksNotebook).toHaveBeenCalledWith('knowledge/index.md');
    // The document header shows the file's basename, and its content loads into the editor.
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();
    await waitForNoteLoaded();
    // The backlink entry is shown.
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it('renders the filesystem tree in the sidebar and opens a clicked text file in a plain editor', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);

    // The sidebar lists the root's immediate children as a lazy tree.
    expect(await screen.findByRole('treeitem', { name: 'knowledge' })).toBeInTheDocument();
    expect(screen.getByRole('treeitem', { name: 'journal' })).toBeInTheDocument();
    const notes = screen.getByRole('treeitem', { name: 'notes.txt' });

    // Clicking a non-markdown text file opens it in the plain text editor (no markdown
    // editor, no backlinks) — exercising the FileTree -> loadFile wiring.
    fireEvent.click(notes);
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('notes.txt'));
    const plain = (await screen.findByRole('textbox', { name: 'File contents' })) as HTMLTextAreaElement;
    expect(plain.value).toContain('# notes.txt');
    expect(screen.getByRole('heading', { level: 2, name: 'notes.txt' })).toBeInTheDocument();
    // The context rail (outline + backlinks) is a markdown affordance; a text file
    // shows neither section.
    expect(screen.queryByText('Outline')).not.toBeInTheDocument();
    expect(screen.queryByText('Backlinks')).not.toBeInTheDocument();
  });

  it('shows the note outline in the context rail and jumps to a heading on click', async () => {
    const { props, readFile } = makeProps();
    const body = '# Top\n\nintro\n\n## Middle\n\ntext\n\n### Deep\n';
    readFile.mockImplementation((path) => Promise.resolve({ path, content: body, hash: 'h1' }));
    render(<NotebookBrowser {...props} />);

    // The rail lists the note's ATX headings in document order.
    expect(await screen.findByRole('button', { name: 'Top' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Middle' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Deep' })).toBeInTheDocument();

    // Clicking a heading scrolls the editor to that heading's character offset.
    fireEvent.click(screen.getByRole('button', { name: 'Middle' }));
    expect(editorMock.scrollCalls).toContain(body.indexOf('## Middle'));
  });

  it('collapses the outline section when its header is toggled', async () => {
    const { props, readFile } = makeProps();
    readFile.mockImplementation((path) =>
      Promise.resolve({ path, content: '# Only heading\n\nbody', hash: 'h1' }),
    );
    render(<NotebookBrowser {...props} />);

    expect(await screen.findByRole('button', { name: 'Only heading' })).toBeInTheDocument();
    // The section header (named "Outline" + its count badge) toggles the body closed;
    // the heading item disappears.
    fireEvent.click(screen.getByRole('button', { name: /^Outline/ }));
    expect(screen.queryByRole('button', { name: 'Only heading' })).not.toBeInTheDocument();
  });

  it('opens a binary file as a read-only placeholder without reading it', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('treeitem', { name: 'cover.png' });

    fireEvent.click(screen.getByRole('treeitem', { name: 'cover.png' }));

    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    expect(screen.getByText("cover.png can't be opened here yet.")).toBeInTheDocument();
    // A binary file is never read (fs_read returns a string, meaningless for bytes).
    expect(readFile).not.toHaveBeenCalledWith('cover.png');
  });

  it('does not read a binary file when the Notebook is reopened with one selected', async () => {
    const { props, readFile } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await screen.findByRole('treeitem', { name: 'cover.png' });

    // Select the binary file, then close and reopen the Notebook.
    fireEvent.click(screen.getByRole('treeitem', { name: 'cover.png' }));
    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    rerender(<NotebookBrowser {...props} isOpen={false} />);
    rerender(<NotebookBrowser {...props} isOpen />);

    // The reopen "keep current selection" probe must preserve the placeholder WITHOUT
    // reading the binary — fs_read is never called for binary bytes, on click or reopen.
    expect(await screen.findByText('Preview not available')).toBeInTheDocument();
    expect(readFile).not.toHaveBeenCalledWith('cover.png');
  });

  it('navigates when an in-notebook link is followed', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    followLink(FOO);

    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/areas/foo.md'));
    expect(await screen.findByRole('heading', { level: 2, name: 'foo' })).toBeInTheDocument();
  });

  it('navigates when a backlink is clicked', async () => {
    const { props, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    fireEvent.click(await screen.findByRole('button', { name: '2026-06-13' }));

    await waitFor(() => expect(readFile).toHaveBeenCalledWith('journal/2026-06-13.md'));
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
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();

    // Follow a link to another note: its content appears before its backlinks resolve —
    // no stale previous-file content stranded under the new selection.
    followLink(FOO);
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));
    expect(screen.getByRole('heading', { level: 2, name: 'foo' })).toBeInTheDocument();

    // Backlinks fills in later, independently.
    await act(async () => {
      resolveBacklinks([{ path: 'journal/2026-06-13.md', type: 'journal', title: '2026-06-13', size: 20 }]);
    });
    expect(await screen.findByRole('button', { name: '2026-06-13' })).toBeInTheDocument();
  });

  it("clears the previous note's backlinks the moment a new note is opened, not after the slow walk", async () => {
    const { props, backlinksNotebook } = makeProps();
    // Resolve note A's backlinks but DEFER note B's, so we can prove A's "Linked from"
    // list never lingers under B while B's (intentionally slow) backlink walk runs.
    let resolveB: (e: NotebookEntry[]) => void = () => {};
    let calls = 0;
    backlinksNotebook.mockImplementation(() => {
      calls += 1;
      if (calls === 1) {
        return Promise.resolve([
          { path: 'journal/2026-06-13.md', type: 'journal', title: 'Backlink to A', size: 20 },
        ]);
      }
      return new Promise<NotebookEntry[]>((resolve) => {
        resolveB = resolve;
      });
    });
    render(<NotebookBrowser {...props} />);

    // Note A's backlink renders.
    expect(await screen.findByRole('button', { name: 'Backlink to A' })).toBeInTheDocument();

    // Navigate to note B; its backlinks are still pending.
    followLink(FOO);
    await waitFor(() => expect(editor().value).toContain('# knowledge/areas/foo.md'));

    // A's backlink is gone immediately — replaced by a loading line, NOT the misleading
    // "No other note links here." empty state (the walk isn't done yet).
    expect(screen.queryByRole('button', { name: 'Backlink to A' })).not.toBeInTheDocument();
    expect(screen.getByText('Finding backlinks…')).toBeInTheDocument();
    expect(screen.queryByText('No other note links here.')).not.toBeInTheDocument();

    // B's own backlinks fill in when its walk resolves.
    await act(async () => {
      resolveB([{ path: 'knowledge/index.md', type: 'note', title: 'Backlink to B', size: 10 }]);
    });
    expect(await screen.findByRole('button', { name: 'Backlink to B' })).toBeInTheDocument();
    expect(screen.queryByText('Finding backlinks…')).not.toBeInTheDocument();
  });

  it('re-lists the tree and reloads the open note when the change signal bumps', async () => {
    const { props, listDir, readFile } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/index.md'));
    // The sidebar tree lists the root on mount.
    await waitFor(() => expect(listDir.mock.calls.some((c) => c[0] === '')).toBe(true));
    const rootListsBefore = listDir.mock.calls.filter((c) => c[0] === '').length;
    const openNoteReadsBefore = readFile.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length;

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The tree re-lists its root (FileTree's own changeSignal-driven refresh)...
    await waitFor(() =>
      expect(listDir.mock.calls.filter((c) => c[0] === '').length).toBeGreaterThan(rootListsBefore),
    );
    // ...and the open note is re-read — this is what makes the live view reflect edits
    // to the note the user is currently viewing.
    await waitFor(() =>
      expect(readFile.mock.calls.filter((c) => c[0] === 'knowledge/index.md').length).toBeGreaterThan(
        openNoteReadsBefore,
      ),
    );
  });

  it('shows the file as unavailable when a change-signal reload finds it deleted', async () => {
    const { props, readFile } = makeProps();
    // The open note reads fine initially (the probe), then its reload (triggered by the
    // change signal) rejects — an external delete the watcher surfaced.
    let indexReads = 0;
    readFile.mockImplementation((path) => {
      if (path === 'knowledge/index.md') {
        indexReads += 1;
        if (indexReads >= 2) return Promise.reject(new Error('fs: knowledge/index.md not found'));
      }
      return Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' });
    });
    const { rerender } = render(<NotebookBrowser {...props} />);
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The reload failed: the document pane honestly reports the file is gone.
    expect(await screen.findByText('File unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2, name: 'index' })).not.toBeInTheDocument();
  });

  it('keeps the open note when the tree refresh fails transiently', async () => {
    const { props, listDir } = makeProps();
    // The tree lists fine on mount; its change-signal refresh then rejects (a transient
    // WS hiccup). The open note still reloads independently via readFile.
    let rootLists = 0;
    listDir.mockImplementation((path) => {
      if (path === '') {
        rootLists += 1;
        if (rootLists >= 2) return Promise.reject(new Error('socket closed'));
      }
      return Promise.resolve(TREE[path] ?? []);
    });
    const { rerender } = render(<NotebookBrowser {...props} />);
    expect(await screen.findByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();

    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // A failed tree refresh is isolated to the sidebar: the open document stays put and
    // does not fall back to an error/empty state.
    await waitFor(() => expect(rootLists).toBeGreaterThan(1));
    expect(screen.getByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();
    expect(screen.queryByText('File unavailable')).not.toBeInTheDocument();
  });

  it('shows the empty state and selects nothing when the root is empty', async () => {
    const { props, readFile } = makeProps({
      listDir: vi.fn<(path: string) => Promise<FsEntry[]>>().mockResolvedValue([]),
    });
    // No preferred entry point exists, so every probe read rejects.
    readFile.mockRejectedValue(new Error('fs: not found'));
    render(<NotebookBrowser {...props} />);

    // The document pane falls back to the empty state; the tree shows its own empty line.
    expect(await screen.findByText('Nothing selected')).toBeInTheDocument();
    expect(await screen.findByText('This folder is empty.')).toBeInTheDocument();
  });

  it('moves focus into the dialog on open so the focus trap engages', async () => {
    const { props } = makeProps();
    render(<NotebookBrowser {...props} />);

    const dialog = await screen.findByRole('dialog');
    await waitFor(() => expect(dialog).toHaveFocus());
  });

  it('shows an error state when a navigated-to file cannot be read but keeps the tree', async () => {
    const { props, readFile } = makeProps();
    // The preferred note opens fine, but foo.md fails to read when navigated to.
    readFile.mockImplementation((path) =>
      path === 'knowledge/areas/foo.md'
        ? Promise.reject(new Error('fs: knowledge/areas/foo.md not found'))
        : Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' }),
    );
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

    followLink(FOO);

    expect(await screen.findByText('File unavailable')).toBeInTheDocument();
    // The sidebar tree still lists files so the user can pick another.
    expect(screen.getByRole('treeitem', { name: 'knowledge' })).toBeInTheDocument();
  });

  it('autosaves an edited note via hash-CAS and shows a Saved indicator', async () => {
    const { props, writeFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Type into the single live surface — there is no edit mode to enter.
    fireEvent.change(editor(), { target: { value: '# edited\n' } });

    // The debounced autosave persists against the loaded hash (h1); no Save button.
    await waitFor(
      () => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# edited\n', 'h1'),
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
    const { props, writeFile } = makeProps();
    // First autosave conflicts (note changed on disk); the overwrite then succeeds.
    writeFile
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
    await waitFor(() => expect(writeFile).toHaveBeenLastCalledWith('knowledge/index.md', '# mine\n', 'hX'));
    await waitFor(() => expect(screen.queryByText(/changed on disk/i)).not.toBeInTheDocument());
  });

  it('flushes an unsaved buffer when navigating away before autosave fires', async () => {
    const { props, writeFile, readFile } = makeProps();
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Type, then immediately navigate away (faster than the 700ms autosave debounce).
    fireEvent.change(editor(), { target: { value: '# quick edit\n' } });
    followLink(FOO);

    // The outgoing buffer is flushed against its loaded hash, so the edit isn't lost,
    // and (the flush having succeeded) the navigation lands on the new note.
    await waitFor(() => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# quick edit\n', 'h1'));
    await waitFor(() => expect(readFile).toHaveBeenCalledWith('knowledge/areas/foo.md'));
    expect(await screen.findByRole('heading', { level: 2, name: 'foo' })).toBeInTheDocument();
  });

  it('aborts navigation and surfaces the conflict when the navigate flush conflicts', async () => {
    const { props, writeFile, readFile } = makeProps();
    // The flush triggered by navigating away conflicts (the note changed on disk).
    writeFile.mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' });
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Type, then navigate away before the autosave fires; the flush hits the conflict.
    fireEvent.change(editor(), { target: { value: '# mine\n' } });
    followLink(FOO);

    // The flush wrote against the loaded hash and came back conflicted, so the
    // navigation is abandoned: we stay on the current note, the conflict banner shows,
    // and the buffer is intact — the edit is NOT lost behind a silent navigation.
    await waitFor(() => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# mine\n', 'h1'));
    expect(await screen.findByText(/changed on disk/i)).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 2, name: 'index' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { level: 2, name: 'foo' })).not.toBeInTheDocument();
    expect(editor().value).toBe('# mine\n');
    expect(readFile).not.toHaveBeenCalledWith('knowledge/areas/foo.md');
  });

  it('does not stamp a stale reload-from-disk onto a note navigated to mid-reload', async () => {
    const { props, writeFile, readFile } = makeProps();
    // Autosave conflicts (so the "Reload from disk" affordance appears); the later
    // navigate flush then succeeds, superseding the in-flight reload.
    writeFile
      .mockResolvedValueOnce({ path: 'knowledge/index.md', conflict: true, currentHash: 'hX' })
      .mockResolvedValueOnce({ path: 'knowledge/index.md', hash: 'h2', conflict: false });
    // Defer the reload read of index (the on-open probe is read #1; the reload is #2) so
    // navigation can outrun it.
    let indexReads = 0;
    let resolveReload: (r: FsReadResult) => void = () => {};
    readFile.mockImplementation((path) => {
      if (path === 'knowledge/index.md') {
        indexReads += 1;
        if (indexReads === 2) return new Promise<FsReadResult>((resolve) => { resolveReload = resolve; });
      }
      return Promise.resolve({ path, content: `# ${path}\n\nbody`, hash: 'h1' });
    });
    render(<NotebookBrowser {...props} />);
    await waitFor(() => expect(editor().value).toContain('# knowledge/index.md'));

    // Edit → the debounced autosave conflicts → the reconcile banner appears.
    fireEvent.change(editor(), { target: { value: '# mine\n' } });
    expect(await screen.findByText(/changed on disk/i, undefined, { timeout: 2500 })).toBeInTheDocument();

    // Start a reload from disk (its read is deferred), then navigate away before it
    // resolves; the navigate flush succeeds and lands us on the new note.
    fireEvent.click(screen.getByRole('button', { name: 'Reload from disk' }));
    followLink(FOO);
    await screen.findByRole('heading', { level: 2, name: 'foo' });
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
    const { props, readFile, listDir } = makeProps();
    const { rerender } = render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: '# my draft\n' } });

    const rootListsBefore = listDir.mock.calls.filter((c) => c[0] === '').length;
    const readsBefore = readFile.mock.calls.length;
    rerender(<NotebookBrowser {...props} changeSignal={1} />);

    // The change-signal effect runs (tree re-listed by FileTree)...
    await waitFor(() =>
      expect(listDir.mock.calls.filter((c) => c[0] === '').length).toBeGreaterThan(rootListsBefore),
    );
    // ...but the open note is NOT reloaded under the buffer, and the edit survives.
    expect(editor().value).toBe('# my draft\n');
    expect(readFile.mock.calls.length).toBe(readsBefore);
  });

  it('does not stamp a mid-autosave result onto a note the user navigated to', async () => {
    const { props, writeFile } = makeProps();
    // Defer the first write so the user can navigate before it resolves.
    let resolveWrite: (r: FsWriteResult) => void = () => {};
    writeFile.mockImplementationOnce(
      () => new Promise<FsWriteResult>((resolve) => { resolveWrite = resolve; }),
    );
    render(<NotebookBrowser {...props} />);
    await waitForNoteLoaded();

    // Edit note A (knowledge/index.md); its autosave is now in flight.
    fireEvent.change(editor(), { target: { value: '# A edited\n' } });
    await waitFor(
      () => expect(writeFile).toHaveBeenCalledWith('knowledge/index.md', '# A edited\n', 'h1'),
      { timeout: 2000 },
    );

    // Navigate to note B (foo.md) before A's save resolves. B loads.
    followLink(FOO);
    await screen.findByRole('heading', { level: 2, name: 'foo' });
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
    await screen.findByRole('heading', { level: 2, name: 'index' });

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
    await screen.findByRole('heading', { level: 2, name: 'index' });

    act(() => editorMock.current!.onSelectionChange!(null));

    expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument();
    expect(sendToChief).not.toHaveBeenCalled();
  });

  it('surfaces an error when sending to the chief fails', async () => {
    const { props, sendToChief } = makeProps();
    sendToChief.mockRejectedValueOnce(new Error('no chief reachable'));
    render(<NotebookBrowser {...props} />);
    await screen.findByRole('heading', { level: 2, name: 'index' });

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
    await screen.findByRole('heading', { level: 2, name: 'index' });

    // Select + send from note A; the send is now in flight.
    act(() => editorMock.current!.onSelectionChange!({ text: 'from A', top: 40, left: 60 }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send to chief' }));
    await waitFor(() => expect(sendToChief).toHaveBeenCalledWith('from A', 'knowledge/index.md'));

    // Navigate to note B before A's send resolves; B loads.
    followLink(FOO);
    await screen.findByRole('heading', { level: 2, name: 'foo' });

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
