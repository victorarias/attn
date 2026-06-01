import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { WorkspaceDockPanel, resolveMarkdownTarget } from './WorkspaceDockPanel';

const opener = vi.hoisted(() => ({
  openPath: vi.fn(async () => {}),
  openUrl: vi.fn(async () => {}),
}));

vi.mock('@tauri-apps/plugin-opener', () => opener);

function renderMarkdown(content: string) {
  return render(
    <WorkspaceDockPanel
      panel={{ type: 'panel', panelId: 'panel-markdown', panelKind: 'markdown', panelParams: '/tmp/project/README.md' }}
      workspaceId="workspace-1"
      content={{ path: '/tmp/project/README.md', content }}
      dragging={false}
      onClose={vi.fn()}
      onHeaderPointerDown={vi.fn()}
      onRequestContent={vi.fn()}
    />,
  );
}

describe('WorkspaceDockPanel Markdown rendering', () => {
  beforeEach(() => {
    opener.openPath.mockClear();
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

    expect(opener.openPath).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Open image: diagram' }));
    expect(opener.openPath).toHaveBeenCalledWith('/tmp/project/docs/diagram.png');
  });

  it('opens relative and external links through the Tauri opener', () => {
    renderMarkdown('[guide](docs/setup.md) [site](https://example.test/docs)');

    fireEvent.click(screen.getByRole('link', { name: 'guide' }));
    expect(opener.openPath).toHaveBeenCalledWith('/tmp/project/docs/setup.md');

    fireEvent.click(screen.getByRole('link', { name: 'site' }));
    expect(opener.openUrl).toHaveBeenCalledWith('https://example.test/docs');
  });
});
