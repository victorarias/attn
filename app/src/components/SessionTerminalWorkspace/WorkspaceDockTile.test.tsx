import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { normalizeBrowserAddress, WorkspaceDockTile, resolveMarkdownTarget } from './WorkspaceDockTile';
import { deriveTileTitle } from '../../utils/tilePresentation';
import type { TileLeaf } from '../../types/workspace';

const opener = vi.hoisted(() => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/plugin-opener', () => opener);
const invokeMock = vi.mocked(invoke);

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

  it('opens relative local images only after an explicit click', () => {
    renderMarkdown('![diagram](docs/diagram.png)');

    expect(invokeMock).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Open image: diagram' }));
    expect(invokeMock).toHaveBeenCalledWith('open_safe_markdown_target', {
      path: '/tmp/project/docs/diagram.png',
    });
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
    expect(screen.queryByRole('button', { name: 'Open image: diagram' })).toBeNull();

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
