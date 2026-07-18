import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { createRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { NotebookSurface, type NotebookSurfaceHandle } from './NotebookSurface';
import type { FsEntry, FsExistsResult, FsReadAssetResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult } from '../hooks/useDaemonSocket';

// Off-root gating (editor-arbitrary-roots PR6): backlinksNotebook and sendToChief
// are OPTIONAL on NotebookSurface. NotebookTile omits both when a tile is bound to
// a filesystem root other than the Notebook's — this file proves the surface itself
// (root-unaware; it just renders the affordances it's handed) actually withholds
// the rail and the floating chief button when they're absent, and keeps rendering
// them when they're provided. NotebookBrowser.test.tsx already covers the
// always-both-provided modal path in depth; this file exercises the tile variant,
// which is the one real caller that can omit them.

const editorMock = vi.hoisted(() => ({
  current: null as null | {
    onSelectionChange?: (sel: { text: string; top: number; left: number } | null) => void;
  },
}));

vi.mock('./notebook/LiveMarkdownEditor', async () => {
  const { forwardRef, useImperativeHandle } = await import('react');
  return {
    LiveMarkdownEditor: forwardRef(function MockLiveMarkdownEditor(
      {
        value,
        onChange,
        onSelectionChange,
        ariaLabel,
      }: {
        value: string;
        onChange: (value: string) => void;
        onSelectionChange?: (sel: { text: string; top: number; left: number } | null) => void;
        ariaLabel?: string;
      },
      ref: React.Ref<{ scrollToPos: () => void; applyExternalContent: () => void; closeSearchPanel: () => boolean; focus: () => void }>,
    ) {
      editorMock.current = { onSelectionChange };
      useImperativeHandle(ref, () => ({
        scrollToPos: () => {},
        applyExternalContent: () => {},
        closeSearchPanel: () => false,
        focus: () => {},
      }), []);
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

const TREE: Record<string, FsEntry[]> = {
  '': [{ path: 'note.md', name: 'note.md', isDir: false, size: 32 }],
};

function makeProps(overrides: Partial<React.ComponentProps<typeof NotebookSurface>> = {}) {
  const listDir = vi
    .fn<(path: string) => Promise<FsEntry[]>>()
    .mockImplementation((path) => Promise.resolve(TREE[path] ?? []));
  const readFile = vi
    .fn<(path: string) => Promise<FsReadResult>>()
    .mockImplementation((path) => Promise.resolve({ path, content: `# ${path}\n\nBody text to select.`, hash: 'h1' }));
  const writeFile = vi
    .fn<(path: string, content: string, baseHash?: string) => Promise<FsWriteResult>>()
    .mockImplementation((path) => Promise.resolve({ path, hash: 'h2', conflict: false }));
  const existsFile = vi
    .fn<(path: string) => Promise<FsExistsResult>>()
    .mockImplementation((path) => Promise.resolve({ path, exists: true }));
  const readAsset = vi
    .fn<(path: string) => Promise<FsReadAssetResult>>()
    .mockImplementation((path) => Promise.resolve({ path, mimeType: 'image/png', dataBase64: '' }));
  const backlinksNotebook = vi
    .fn<(path: string) => Promise<NotebookEntry[]>>()
    .mockResolvedValue([{ path: 'other.md', type: 'note', title: 'Other', size: 10 }]);
  const sendToChief = vi
    .fn<(selection: string, sourcePath?: string) => Promise<NotebookSendToChiefResult>>()
    .mockResolvedValue({ path: 'inbox.md', nudged: false });
  return {
    props: {
      variant: 'tile' as const,
      active: true,
      initialPath: 'note.md',
      listDir,
      readFile,
      writeFile,
      existsFile,
      readAsset,
      changeSignal: 0,
      ...overrides,
    },
    listDir,
    readFile,
    writeFile,
    existsFile,
    readAsset,
    backlinksNotebook,
    sendToChief,
  };
}

function editor() {
  return screen.getByRole('textbox', { name: 'Note' }) as HTMLTextAreaElement;
}

async function waitForNoteLoaded() {
  await waitFor(() => expect(editor().value).toContain('# note.md'));
}

describe('NotebookSurface off-root gating (tile variant)', () => {
  // The tile variant runs useTileAutoFold, which observes its body via a real
  // ResizeObserver — not present under happy-dom. A no-op stub is enough; the
  // fold ladder itself is unit-tested separately (useTileAutoFold.test.ts).
  beforeEach(() => {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  });

  afterEach(() => {
    editorMock.current = null;
    vi.restoreAllMocks();
  });

  it('renders no backlinks/outline rail and never fetches backlinks when backlinksNotebook is absent', async () => {
    const { props, backlinksNotebook } = makeProps({ backlinksNotebook: undefined });
    render(<NotebookSurface {...props} />);
    await waitForNoteLoaded();

    // Give any (incorrect) fetch a chance to fire before asserting its absence.
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(backlinksNotebook).not.toHaveBeenCalled();
    expect(document.querySelector('.notebook-browser-rail')).toBeNull();
  });

  it('renders the backlinks/outline rail and fetches backlinks when backlinksNotebook is provided', async () => {
    const { props, backlinksNotebook } = makeProps();
    render(<NotebookSurface {...props} backlinksNotebook={backlinksNotebook} />);
    await waitForNoteLoaded();

    await waitFor(() => expect(backlinksNotebook).toHaveBeenCalledWith('note.md'));
    expect(document.querySelector('.notebook-browser-rail')).not.toBeNull();
  });

  it('never renders the floating "Send to chief" button when sendToChief is absent, even with a selection', async () => {
    const { props, sendToChief } = makeProps({ sendToChief: undefined });
    render(<NotebookSurface {...props} />);
    await waitForNoteLoaded();

    act(() => editorMock.current!.onSelectionChange!({ text: 'a key decision', top: 40, left: 60 }));

    expect(screen.queryByRole('button', { name: 'Send to chief' })).not.toBeInTheDocument();
    expect(sendToChief).not.toHaveBeenCalled();
  });

  it('renders the floating "Send to chief" button on a selection when sendToChief is provided', async () => {
    const { props, sendToChief } = makeProps();
    render(<NotebookSurface {...props} sendToChief={sendToChief} />);
    await waitForNoteLoaded();

    act(() => editorMock.current!.onSelectionChange!({ text: 'a key decision', top: 40, left: 60 }));

    expect(await screen.findByRole('button', { name: 'Send to chief' })).toBeInTheDocument();
  });
});

describe('NotebookSurface flushPendingSave handle (root-switch flush, PR #588 second review round)', () => {
  // NotebookTile keys NotebookSurface on `root`, so a root switch remounts it —
  // dropping any not-yet-autosaved edit (the autosave debounce is 700ms with no
  // unmount flush). flushPendingSave is the imperative escape hatch the root
  // switcher calls BEFORE swapping params, so the outgoing edit persists to the
  // OLD root's file instead of vanishing.
  beforeEach(() => {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  });

  afterEach(() => {
    editorMock.current = null;
    vi.restoreAllMocks();
  });

  it('flushPendingSave persists a dirty buffer immediately (ahead of the autosave debounce) and resolves "saved"', async () => {
    const { props, writeFile } = makeProps();
    const ref = createRef<NotebookSurfaceHandle>();
    render(<NotebookSurface {...props} ref={ref} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: 'edited body' } });

    // Called before the 700ms debounce would fire — writeFile must not have
    // been called yet via the autosave path.
    expect(writeFile).not.toHaveBeenCalled();

    let outcome: string | undefined;
    await act(async () => {
      outcome = await ref.current!.flushPendingSave();
    });

    expect(writeFile).toHaveBeenCalledWith('note.md', 'edited body', 'h1');
    expect(outcome).toBe('saved');
  });

  it('flushPendingSave resolves "conflict" and leaves the conflict banner showing when the write CAS-conflicts', async () => {
    const { props, writeFile } = makeProps();
    writeFile.mockResolvedValue({ path: 'note.md', conflict: true, currentHash: 'h-server' });
    const ref = createRef<NotebookSurfaceHandle>();
    render(<NotebookSurface {...props} ref={ref} />);
    await waitForNoteLoaded();

    fireEvent.change(editor(), { target: { value: 'edited body' } });

    let outcome: string | undefined;
    await act(async () => {
      outcome = await ref.current!.flushPendingSave();
    });

    expect(outcome).toBe('conflict');
    expect(document.querySelector('.notebook-browser-editor-conflict')).not.toBeNull();
  });

  it('flushPendingSave resolves "noop" when the buffer is already in sync', async () => {
    const { props, writeFile } = makeProps();
    const ref = createRef<NotebookSurfaceHandle>();
    render(<NotebookSurface {...props} ref={ref} />);
    await waitForNoteLoaded();

    let outcome: string | undefined;
    await act(async () => {
      outcome = await ref.current!.flushPendingSave();
    });

    expect(outcome).toBe('noop');
    expect(writeFile).not.toHaveBeenCalled();
  });
});
