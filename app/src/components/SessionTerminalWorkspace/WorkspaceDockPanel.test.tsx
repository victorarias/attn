import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { invoke } from '@tauri-apps/api/core';
import { WorkspaceDockPanel, resolveMarkdownTarget } from './WorkspaceDockPanel';

const opener = vi.hoisted(() => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/plugin-opener', () => opener);
const invokeMock = vi.mocked(invoke);

function renderMarkdown(content: string, allowLocalTargets = true) {
  return render(
    <WorkspaceDockPanel
      panel={{ type: 'panel', panelId: 'panel-markdown', panelKind: 'markdown', panelParams: '/tmp/project/README.md' }}
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

describe('WorkspaceDockPanel Markdown rendering', () => {
  beforeEach(() => {
    invokeMock.mockReset();
    invokeMock.mockResolvedValue(undefined);
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
