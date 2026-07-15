import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { normalizeBrowserAddress, WorkspaceDockTile, resolveMarkdownTarget } from './WorkspaceDockTile';
import type { WorkspaceTileSessionOption } from './WorkspaceDockTile';
import { deriveTileTitle } from '../../utils/tilePresentation';
import type { TileLeaf } from '../../types/workspace';
import { setMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import type {
  MarkdownAnnotationsSubmitResult,
  MarkdownAnnotationsTransport,
} from '../MarkdownReader/annotations/transport';
import type { WireAnnotation } from '../MarkdownReader/annotations/types';

const opener = vi.hoisted(() => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/plugin-opener', () => opener);
const invokeMock = vi.mocked(invoke);

// jsdom cannot run real mermaid (it needs a canvas/layout engine); mock it the
// same way app/src/components/Markdown/Markdown.test.tsx does so the shared
// renderer's mermaid path is exercised here through the dock tile.
const mermaidMock = vi.hoisted(() => ({
  render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  initialize: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: mermaidMock.initialize,
    render: mermaidMock.render,
  },
}));

function renderMarkdown(content: string, allowLocalTargets = true) {
  return render(
    <WorkspaceDockTile
      tile={{ type: 'tile', tileId: 'tile-markdown', tileKind: 'markdown', tileParams: '/tmp/project/README.md' }}
      workspaceId="workspace-1"
      content={{ path: '/tmp/project/README.md', content }}
      allowLocalTargets={allowLocalTargets}
      dragging={false}
      onClose={vi.fn()}
      onHeaderPointerDown={vi.fn()}
      onRequestContent={vi.fn()}
    />,
  );
}

describe('WorkspaceDockTile Markdown rendering', () => {
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
    vi.mocked(isTauri).mockReturnValue(false);
    opener.openUrl.mockClear();
  });

  it('resolves local Markdown targets relative to the opened document', () => {
    expect(resolveMarkdownTarget('/tmp/project/README.md', 'docs/setup.md')).toEqual({
      kind: 'local',
      value: '/tmp/project/docs/setup.md',
    });
    expect(resolveMarkdownTarget('/tmp/project/README.md', 'https://example.test/guide')).toEqual({
      kind: 'external',
      value: 'https://example.test/guide',
    });
    expect(resolveMarkdownTarget('/tmp/project/README.md', 'javascript:alert(1)')).toBeNull();
  });

  it('blocks automatic remote image loads', () => {
    const { container } = renderMarkdown('![tracking](https://example.test/pixel?id=123)');

    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('[blocked image: tracking]')).toBeInTheDocument();
    expect(opener.openUrl).not.toHaveBeenCalled();
  });

  it('renders relative local images inline via the asset protocol', () => {
    const { container } = renderMarkdown('![diagram](docs/diagram.png)');

    const img = container.querySelector('img.md-reader-image');
    expect(img).toHaveAttribute('src', 'asset://localhost//tmp/project/docs/diagram.png');
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it('opens relative and external links through the Tauri opener', () => {
    renderMarkdown('[guide](docs/setup.md) [site](https://example.test/docs)');

    fireEvent.click(screen.getByRole('link', { name: 'guide' }));
    expect(invokeMock).toHaveBeenCalledWith('open_safe_markdown_target', {
      path: '/tmp/project/docs/setup.md',
    });

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(opener.openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('disables local targets for remote workspace content', () => {
    renderMarkdown('[guide](docs/setup.md) ![diagram](docs/diagram.png) [site](https://example.test/docs)', false);

    expect(screen.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(document.querySelector('img.md-reader-image')).toBeNull();

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(invokeMock).not.toHaveBeenCalled();
    expect(opener.openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });

  it('blocks executable-associated local targets from repository Markdown', () => {
    renderMarkdown('[guide](scripts/setup.command) ![diagram](scripts/setup.command)');

    expect(screen.queryByRole('link', { name: 'guide' })).toBeNull();
    expect(screen.getByText('guide')).toHaveAttribute(
      'title',
      'Blocked local target: /tmp/project/scripts/setup.command',
    );
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it('adds duplicate-safe heading ids for fragment links', () => {
    renderMarkdown('[Jump](#setup)\n\n## Setup\n\n## Setup');

    expect(screen.getByRole('link', { name: 'Jump' })).toHaveAttribute('href', '#setup');
    expect(screen.getAllByRole('heading', { name: 'Setup' }).map((heading) => heading.id)).toEqual([
      'setup',
      'setup-1',
    ]);
  });

  it('renders a mermaid fence as a diagram via the shared Markdown renderer', async () => {
    renderMarkdown('```mermaid\ngraph TD;\nA-->B;\n```');

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    expect(mermaidMock.render).toHaveBeenCalled();
  });
});

describe('deriveTileTitle', () => {
  const markdownTile: TileLeaf = {
    type: 'tile',
    tileId: 'tile-markdown',
    tileKind: 'markdown',
    tileParams: '/tmp/project/notes.md',
  };

  it('uses the H1 heading when the document leads with one', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '# Project notes\n\nbody' }))
      .toBe('Project notes');
  });

  it('strips a heading marker of any level and inline markdown', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '## **Setup** `steps`' }))
      .toBe('Setup steps');
  });

  it('falls back to the first non-empty line when there is no heading', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '\n\nJust some plain notes here' }))
      .toBe('Just some plain notes here');
  });

  it('skips a closed YAML frontmatter block', () => {
    const content = '---\ntitle: ignored\n---\n# Real title\n';
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content })).toBe('Real title');
  });

  it('keeps a leading horizontal rule as content when there is no closing fence', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '---\nstill text' }))
      .toBe('still text');
  });

  it('truncates a very long title with an ellipsis', () => {
    const long = `# ${'word '.repeat(40).trim()}`;
    const title = deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: long });
    expect(title.endsWith('…')).toBe(true);
    expect(title.length).toBeLessThanOrEqual(80);
  });

  it('falls back to the basename for empty or error content', () => {
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '   \n  ' })).toBe('notes.md');
    expect(deriveTileTitle(markdownTile, { path: '/tmp/project/notes.md', content: '', error: 'boom' }))
      .toBe('notes.md');
  });

  it('uses the basename before content loads, and tile kind without a path', () => {
    expect(deriveTileTitle(markdownTile, undefined)).toBe('notes.md');
    expect(deriveTileTitle({ type: 'tile', tileId: 'tile-x', tileKind: 'markdown' }, undefined)).toBe('markdown');
  });

  it('uses the host as the title for a browser tile', () => {
    expect(deriveTileTitle({
      type: 'tile',
      tileId: 'tile-browser',
      tileKind: 'browser',
      tileParams: 'http://localhost:3000/dashboard',
    })).toBe('localhost:3000');
  });
});

describe('WorkspaceDockTile browser integration', () => {
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver;
  });

  it('closes the exact browser tile targeted by the native close command', async () => {
    const onClose = vi.fn();
    render(
      <WorkspaceDockTile
        tile={{
          type: 'tile',
          tileId: 'tile-browser',
          tileKind: 'browser',
          tileParams: 'https://backstage.spotify.net',
        }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={onClose}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
    );

    await screen.findByText('Error: In-app browser hosting requires the Tauri app');
    act(() => {
      window.dispatchEvent(new CustomEvent('attn:native-browser-close', {
        detail: 'browser-workspace-1-tile-browser',
      }));
    });

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('reloads the browser from its tile header', async () => {
    render(
      <WorkspaceDockTile
        tile={{
          type: 'tile',
          tileId: 'tile-browser',
          tileKind: 'browser',
          tileParams: 'https://backstage.spotify.net',
        }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={vi.fn()}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
    );

    await screen.findByText('Error: In-app browser hosting requires the Tauri app');
    fireEvent.click(screen.getByRole('button', { name: 'Reload browser' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
      label: 'browser-workspace-1-tile-browser',
      action: 'reload',
      params: undefined,
      selector: undefined,
      text: undefined,
    });
  });

  it('claims browser close ownership from header controls', async () => {
    vi.mocked(isTauri).mockReturnValue(true);
    render(
      <WorkspaceDockTile
        tile={{
          type: 'tile',
          tileId: 'tile-browser',
          tileKind: 'browser',
          tileParams: 'https://backstage.spotify.net',
        }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={vi.fn()}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
    );

    fireEvent.pointerDown(screen.getByRole('textbox', { name: 'Browser address' }));

    expect(invokeMock).toHaveBeenCalledWith('browser_host_claim_focus', {
      label: 'browser-workspace-1-tile-browser',
    });
  });

  it('navigates from the address bar and tracks native location changes', async () => {
    const onUpdateParams = vi.fn(async () => {});
    render(
      <WorkspaceDockTile
        tile={{
          type: 'tile',
          tileId: 'tile-browser',
          tileKind: 'browser',
          tileParams: 'https://backstage.spotify.net',
        }}
        workspaceId="workspace-1"
        dragging={false}
        onClose={vi.fn()}
        onUpdateParams={onUpdateParams}
        onHeaderPointerDown={vi.fn()}
        onRequestContent={vi.fn()}
      />,
    );
    const address = screen.getByRole('textbox', { name: 'Browser address' });

    fireEvent.change(address, { target: { value: 'example.com/docs' } });
    fireEvent.submit(address.closest('form')!);

    await waitFor(() => {
      expect(invokeMock).toHaveBeenCalledWith('browser_host_control', {
        label: 'browser-workspace-1-tile-browser',
        action: 'navigate',
        params: JSON.stringify({ url: 'https://example.com/docs' }),
        selector: undefined,
        text: undefined,
      });
    });

    act(() => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: {
          label: 'browser-workspace-1-tile-browser',
          url: 'https://example.com/redirected',
        },
      }));
    });

    await waitFor(() => {
      expect(address).toHaveValue('https://example.com/redirected');
      expect(onUpdateParams).toHaveBeenCalledWith('https://example.com/redirected');
    });

    act(() => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: {
          label: 'browser-workspace-1-tile-browser',
          url: 'https://example.com/redirected',
        },
      }));
    });
    expect(onUpdateParams).toHaveBeenCalledTimes(1);
  });

  it('normalizes host-and-port browser addresses', () => {
    expect(normalizeBrowserAddress('localhost:3000')).toBe('http://localhost:3000');
    expect(normalizeBrowserAddress('127.0.0.1:8080/path')).toBe('http://127.0.0.1:8080/path');
    expect(normalizeBrowserAddress('example.com:8080')).toBe('https://example.com:8080');
    expect(normalizeBrowserAddress('http://example.com:8080')).toBe('http://example.com:8080');
    expect(normalizeBrowserAddress('ftp://example.com')).toBe('ftp://example.com');
  });
});

// ---- PR6: markdown annotation send flow (spec E13–E15, E18) -----------------

const SEND_PATH = '/tmp/project/README.md';
const SEND_DOC = 'First paragraph with target words inside it.\n';

/** A global (anchor-less) annotation — hydrates a count of 1 without any DOM
    anchor resolution, keeping these tests about the send flow, not anchoring. */
function globalNote(id = 'g1'): WireAnnotation {
  return { id, type: 'global', text: 'whole-doc note', created_at: 1 };
}

function makeSendTransport(seed: WireAnnotation[] = [globalNote()]) {
  const getSpy = vi.fn(async () => ({ annotations: seed, generation: 5 }));
  const saveSpy = vi.fn(async () => ({ stale: false }));
  const clearSpy = vi.fn(async (_path: string, generation: number) => ({ generation }));
  const submitSpy = vi.fn(
    async (): Promise<MarkdownAnnotationsSubmitResult> => ({ status: 'delivered', generation: 6 }),
  );
  const transport: MarkdownAnnotationsTransport = {
    getMarkdownAnnotations: getSpy,
    saveMarkdownAnnotations: saveSpy,
    clearMarkdownAnnotations: clearSpy,
    submitMarkdownAnnotations: submitSpy,
  };
  return { transport, getSpy, saveSpy, clearSpy, submitSpy };
}

const SEND_SESSIONS: WorkspaceTileSessionOption[] = [
  { sessionId: 'sess-a', label: 'alpha', state: 'working' },
  { sessionId: 'sess-b', label: 'beta', state: 'pending_approval' },
];

function sendTile(tileSessionId: string | undefined): TileLeaf {
  return {
    type: 'tile',
    tileId: 'tile-md',
    tileKind: 'markdown',
    tileParams: SEND_PATH,
    ...(tileSessionId !== undefined ? { tileSessionId } : {}),
  };
}

function renderSendTile({
  tileSessionId = 'sess-a',
  sessions = SEND_SESSIONS,
  onRetargetTile = vi.fn(),
}: {
  tileSessionId?: string;
  sessions?: WorkspaceTileSessionOption[];
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
} = {}) {
  const props = {
    workspaceId: 'workspace-1',
    content: { path: SEND_PATH, content: SEND_DOC },
    dragging: false,
    workspaceSessions: sessions,
    onClose: vi.fn(),
    onRetargetTile,
    onHeaderPointerDown: vi.fn(),
    onRequestContent: vi.fn(),
  };
  const view = render(<WorkspaceDockTile tile={sendTile(tileSessionId)} {...props} />);
  return {
    ...view,
    onRetargetTile,
    rebind: (nextSessionId: string) =>
      view.rerender(<WorkspaceDockTile tile={sendTile(nextSessionId)} {...props} />),
  };
}

function sendButton() {
  return screen.getByRole('button', { name: /^(Send \d+|Sending…)$/ });
}

function picker() {
  return screen.getByRole('combobox', { name: 'Send annotations to session' }) as HTMLSelectElement;
}

/** Dispatch ⌘Enter the way the real key arrives: a window-capture keydown. */
function pressCmdEnter(target: EventTarget = window): KeyboardEvent {
  const event = new KeyboardEvent('keydown', {
    key: 'Enter',
    metaKey: true,
    bubbles: true,
    cancelable: true,
  });
  act(() => {
    target.dispatchEvent(event);
  });
  return event;
}

describe('WorkspaceDockTile markdown send flow', () => {
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    setMarkdownAnnotationsTransport(null);
  });

  it('defaults the picker to the bound session and flags approval-blocked options (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    renderSendTile();

    expect(picker().value).toBe('sess-a');
    expect(screen.getByRole('option', { name: 'alpha' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'beta ⏸ approval' })).toBeInTheDocument();
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 1');
    });
    expect(sendButton()).toBeEnabled();
  });

  it('retargets through onRetargetTile and follows the layout broadcast echo (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    const onRetargetTile = vi.fn(async () => {});
    const { rebind } = renderSendTile({ onRetargetTile });
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 1'); // hydration settled
    });

    fireEvent.change(picker(), { target: { value: 'sess-b' } });
    expect(onRetargetTile).toHaveBeenCalledWith('sess-b');
    // The daemon persists the binding and echoes it back via the layout
    // broadcast; only that echo moves the select.
    rebind('sess-b');
    expect(picker().value).toBe('sess-b');
  });

  it('a retarget takes effect immediately for both the picker and Send — no broadcast wait (E13)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const onRetargetTile = vi.fn(async () => {});
    const { rebind } = renderSendTile({ onRetargetTile });
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.change(picker(), { target: { value: 'sess-b' } });
    expect(onRetargetTile).toHaveBeenCalledWith('sess-b');
    // Optimistic: the daemon's layout-broadcast echo has NOT landed yet
    // (broadcasts can lag by seconds under WS load), but the pick already
    // shows in the picker…
    expect(picker().value).toBe('sess-b');
    // …and a Send inside that window goes to the NEW target, never sess-a.
    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(SEND_PATH, 'sess-b', []);
    });

    // The echo landing simply confirms the pick.
    rebind('sess-b');
    expect(picker().value).toBe('sess-b');
  });

  it('rolls the picker back to the persisted binding when the retarget request fails (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    const onRetargetTile = vi.fn(async () => {
      throw new Error('retarget rejected');
    });
    renderSendTile({ onRetargetTile });
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.change(picker(), { target: { value: 'sess-b' } });
    expect(picker().value).toBe('sess-b'); // optimistic
    await waitFor(() => {
      expect(picker().value).toBe('sess-a'); // rolled back on failure
    });
  });

  it('shows a disabled No-session placeholder when the bound session left the workspace (E13)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport().transport);
    renderSendTile({ tileSessionId: 'sess-gone' });

    expect(picker().value).toBe('');
    const placeholder = screen.getByRole('option', { name: 'No session' }) as HTMLOptionElement;
    expect(placeholder.disabled).toBe(true);
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 1'); // annotations hydrated…
    });
    expect(sendButton()).toBeDisabled(); // …but there is no target
  });

  it('disables Send with zero annotations (E14)', async () => {
    setMarkdownAnnotationsTransport(makeSendTransport([]).transport);
    renderSendTile();

    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 0');
    });
    expect(sendButton()).toBeDisabled();
  });

  it('delivers: Sending… → Sent ✓, list empties locally without re-fetch or second clear (E14)', async () => {
    const { transport, getSpy, clearSpy, submitSpy } = makeSendTransport();
    let resolveSubmit: (result: MarkdownAnnotationsSubmitResult) => void = () => {};
    submitSpy.mockImplementation(
      () => new Promise<MarkdownAnnotationsSubmitResult>((resolve) => {
        resolveSubmit = resolve;
      }),
    );
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    expect(sendButton()).toHaveTextContent('Sending…');
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(SEND_PATH, 'sess-a', []);
    });

    await act(async () => {
      resolveSubmit({ status: 'delivered', generation: 9 });
    });
    expect(screen.getByRole('status')).toHaveTextContent('Sent ✓');
    // Delivered clear is a local mirror: no re-hydrate, no second daemon clear.
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 0');
    });
    expect(sendButton()).toBeDisabled();
    expect(getSpy).toHaveBeenCalledTimes(1);
    expect(clearSpy).not.toHaveBeenCalled();
  });

  it('delivered-but-clear-failed keeps annotations and surfaces the warning, not Sent ✓ (E14)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockResolvedValue({
      status: 'delivered',
      error: 'delivered; failed to clear drafts: disk full',
    });
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('failed to clear drafts');
    });
    // The daemon draft survived, so local state must NOT be emptied — the
    // sidebar keeps matching what the store will re-hydrate.
    expect(sendButton()).toHaveTextContent('Send 1');
    expect(sendButton()).toBeEnabled();
    expect(screen.queryByText('Sent ✓')).toBeNull();
  });

  it('refuses to Send while the draft is not hydrated (stale-draft guard, E14)', async () => {
    // Hydration never settles (daemon slow/socket blip): local annotations
    // exist only in memory, so a submit would make the daemon format its
    // STALE stored draft. Send must refuse instead.
    const { transport, getSpy, submitSpy } = makeSendTransport();
    getSpy.mockImplementation(() => new Promise(() => {}));
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();

    // Create a local annotation through the sidebar's global-comment flow.
    fireEvent.click(screen.getByTitle('Show annotations'));
    fireEvent.click(screen.getByTitle('Add a document-wide comment'));
    fireEvent.change(screen.getByPlaceholderText('Add a global comment...'), {
      target: { value: 'unsaved local note' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 1');
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('still syncing');
    });
    expect(submitSpy).not.toHaveBeenCalled();
    expect(sendButton()).toHaveTextContent('Send 1'); // nothing lost
  });

  it('keeps annotations and explains when the target is waiting on approval (E15)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockResolvedValue({ status: 'skipped_pending_approval' });
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent(
        'Target is waiting for approval — not sent',
      );
    });
    expect(sendButton()).toHaveTextContent('Send 1'); // draft kept for retry
    expect(sendButton()).toBeEnabled();
  });

  it('keeps annotations and surfaces the message on a rejected submit (E15)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    submitSpy.mockRejectedValue(new Error('session not found'));
    setMarkdownAnnotationsTransport(transport);
    renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    fireEvent.click(sendButton());
    await waitFor(() => {
      expect(screen.getByRole('status')).toHaveTextContent('session not found');
    });
    expect(sendButton()).toHaveTextContent('Send 1');
  });

  it('⌘Enter sends when focus is inside the tile and annotations exist (E18)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const { container } = renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    fireEvent.focusIn(body);
    const event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(true); // dispatcher consumed it
    await waitFor(() => {
      expect(submitSpy).toHaveBeenCalledWith(SEND_PATH, 'sess-a', []);
    });
  });

  it('⌘Enter never fires from a textarea, without tile focus, or at zero annotations (E18)', async () => {
    const { transport, submitSpy } = makeSendTransport();
    setMarkdownAnnotationsTransport(transport);
    const { container, unmount } = renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toBeEnabled();
    });

    // Focus elsewhere (e.g. a terminal pane): the shortcut is not REGISTERED,
    // so the key is not consumed and falls through to the PTY untouched.
    let event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);
    expect(submitSpy).not.toHaveBeenCalled();

    // Inside the tile but typing in a textarea (the annotation popover): the
    // def's editableTarget:'native' skips it — the popover's own ⌘Enter
    // handler submits the comment instead.
    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    const textarea = document.createElement('textarea');
    body.appendChild(textarea);
    fireEvent.focusIn(textarea);
    event = pressCmdEnter(textarea);
    expect(event.defaultPrevented).toBe(false);
    expect(submitSpy).not.toHaveBeenCalled();
    unmount();

    // Zero annotations: registration is gated on count > 0.
    setMarkdownAnnotationsTransport(makeSendTransport([]).transport);
    const zero = renderSendTile();
    await waitFor(() => {
      expect(sendButton()).toHaveTextContent('Send 0');
    });
    const zeroBody = zero.container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    fireEvent.focusIn(zeroBody);
    event = pressCmdEnter();
    expect(event.defaultPrevented).toBe(false);
    expect(submitSpy).not.toHaveBeenCalled();
  });
});
