import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MarkdownReader } from './index';
import { sanitizeLinkUrl } from './markdownLinks';

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

// jsdom/happy-dom cannot run real mermaid; same mock as Markdown.test.tsx.
vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  },
}));

// The reader lazy-imports shiki; intercept it so tests stay hermetic and fast.
const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    `<span style="--shiki-light:#000;--shiki-dark:#fff">${code}</span>`),
}));
vi.mock('shiki', () => shikiMock);

function renderReader(content: string, allowLocalTargets = true) {
  return render(
    <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets={allowLocalTargets} />,
  );
}

describe('MarkdownReader source anchoring', () => {
  it('stamps data-source-line attributes on rendered blocks', () => {
    const { container } = renderReader('# Title\n\nFirst paragraph.\n\n- item one\n- item two\n');

    const heading = container.querySelector('h1');
    expect(heading).toHaveAttribute('data-source-line', '1');
    expect(heading).toHaveAttribute('data-block-id', 'b0-heading');

    const paragraph = container.querySelector('p');
    expect(paragraph).toHaveAttribute('data-source-line', '3');
    expect(paragraph).toHaveAttribute('data-source-line-end', '3');

    const items = container.querySelectorAll('li[data-source-line]');
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveAttribute('data-source-line', '5');
    expect(items[1]).toHaveAttribute('data-source-line', '6');
  });

  it('keeps raw-file line numbers for blocks after frontmatter (lineOffset plumbing)', () => {
    // Lines 1-4 are the frontmatter block; the paragraph sits on raw line 6.
    const { container } = renderReader('---\ntitle: Plan\ntags: [a, b]\n---\n\nBody paragraph.\n');

    const paragraph = container.querySelector('p');
    expect(paragraph).toHaveAttribute('data-source-line', '6');
    expect(paragraph).toHaveAttribute('data-source-line-end', '6');
  });

  it('stamps fenced code blocks across their full fence range', () => {
    const { container } = renderReader('intro\n\n```js\nconst x = 1;\n```\n');

    const pre = container.querySelector('pre');
    expect(pre).toHaveAttribute('data-source-line', '3');
    expect(pre).toHaveAttribute('data-source-line-end', '5');
  });
});

describe('MarkdownReader frontmatter card', () => {
  it('renders scalar rows and tag chips, and never renders the raw block as prose', () => {
    const { container } = renderReader('---\ntitle: My Plan\ntags: [alpha, beta]\n---\n\nBody.\n');

    expect(screen.getByText('title:')).toBeInTheDocument();
    expect(screen.getByText('My Plan')).toBeInTheDocument();
    expect(screen.getByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
    expect(container.querySelector('.md-frontmatter')).toBeInTheDocument();
    // remark-frontmatter swallows the yaml node: no stray '---' or key text in prose.
    expect(container.querySelector('.md-reader-card')?.textContent).not.toContain('---');
    expect(container.querySelectorAll('h2')).toHaveLength(0);
  });

  it('renders no card when the document has no frontmatter', () => {
    const { container } = renderReader('# Plain\n\nBody.\n');
    expect(container.querySelector('.md-frontmatter')).toBeNull();
  });
});

describe('MarkdownReader link sanitization', () => {
  it('sanitizeLinkUrl kills javascript:/data:/vbscript: and passes normal urls', () => {
    expect(sanitizeLinkUrl('javascript:alert(1)')).toBeNull();
    expect(sanitizeLinkUrl(' JavaScript:alert(1)')).toBeNull();
    expect(sanitizeLinkUrl('data:text/html,<script>')).toBeNull();
    expect(sanitizeLinkUrl('vbscript:msgbox')).toBeNull();
    expect(sanitizeLinkUrl('https://example.test/x')).toBe('https://example.test/x');
    expect(sanitizeLinkUrl('docs/setup.md')).toBe('docs/setup.md');
    expect(sanitizeLinkUrl('#fragment')).toBe('#fragment');
  });

  it('renders dangerous links as plain text', () => {
    renderReader('[boom](javascript:alert(1)) and [leak](data:text/html,x)');

    expect(screen.queryByRole('link', { name: 'boom' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'leak' })).toBeNull();
    expect(screen.getByText('boom')).toBeInTheDocument();
    expect(screen.getByText('leak')).toBeInTheDocument();
  });

  it('keeps heading ids GitHub-sluggy with dedup', () => {
    renderReader('## Configuração!\n\n## Configuração?\n');

    expect(screen.getAllByRole('heading').map((h) => h.id)).toEqual([
      'configuração',
      'configuração-1',
    ]);
  });

  it('scrolls the tile body to a fragment target instead of navigating', () => {
    const { container } = render(
      <div className="workspace-dock-tile-body">
        <MarkdownReader content={'[Jump](#setup)\n\n## Setup\n'} path="/tmp/project/README.md" />
      </div>,
    );
    const body = container.querySelector<HTMLElement>('.workspace-dock-tile-body')!;
    const scrollTo = vi.fn();
    body.scrollTo = scrollTo as unknown as typeof body.scrollTo;

    fireEvent.click(screen.getByRole('link', { name: 'Jump' }));

    expect(scrollTo).toHaveBeenCalledTimes(1);
    expect(scrollTo).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' }));
  });

  it('resolves fragment links inside the OWN tile when another tile has the same heading id', () => {
    // Two tiles rendering the same document produce duplicate heading ids
    // (sluggers dedup per document, not per DOM); the second tile's link must
    // scroll ITS body, not silently die on the first tile's element.
    const content = '[Jump](#setup)\n\n## Setup\n';
    const { container } = render(
      <>
        <div className="workspace-dock-tile-body" data-testid="tile-1">
          <MarkdownReader content={content} path="/tmp/project/README.md" />
        </div>
        <div className="workspace-dock-tile-body" data-testid="tile-2">
          <MarkdownReader content={content} path="/tmp/project/README.md" />
        </div>
      </>,
    );
    const bodies = container.querySelectorAll<HTMLElement>('.workspace-dock-tile-body');
    const firstScrollTo = vi.fn();
    const secondScrollTo = vi.fn();
    bodies[0].scrollTo = firstScrollTo as unknown as HTMLElement['scrollTo'];
    bodies[1].scrollTo = secondScrollTo as unknown as HTMLElement['scrollTo'];

    fireEvent.click(screen.getAllByRole('link', { name: 'Jump' })[1]);

    expect(secondScrollTo).toHaveBeenCalledTimes(1);
    expect(firstScrollTo).not.toHaveBeenCalled();
  });
});

describe('MarkdownReader code blocks', () => {
  beforeEach(() => {
    shikiMock.codeToHtml.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('hydrates fenced code with shiki dual-theme spans', async () => {
    const { container } = renderReader('```ts\nconst x = 1;\n```\n');

    // Paints plain text immediately.
    expect(container.querySelector('pre code')).toHaveTextContent('const x = 1;');

    await waitFor(() => {
      expect(container.querySelector('code.md-shiki span')).toBeInTheDocument();
    });
    expect(shikiMock.codeToHtml).toHaveBeenCalledWith('const x = 1;', expect.objectContaining({
      lang: 'ts',
      themes: { light: 'github-light-default', dark: 'github-dark-default' },
    }));
  });

  it('falls back to plain text when the language is unknown', async () => {
    shikiMock.codeToHtml.mockRejectedValueOnce(new Error('unknown lang'));
    const { container } = renderReader('```nonsense-lang\nplain body\n```\n');

    await waitFor(() => {
      expect(shikiMock.codeToHtml).toHaveBeenCalled();
    });
    expect(container.querySelector('code.md-shiki')).toBeNull();
    expect(container.querySelector('pre code')).toHaveTextContent('plain body');
  });

  it('copies the code and flips the button to Copied! for 2s', async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    renderReader('```ts\nconst x = 1;\n```\n');

    const button = screen.getByRole('button', { name: 'Copy code' });
    vi.useFakeTimers();
    fireEvent.click(button);
    await act(async () => {}); // flush the clipboard promise

    expect(writeText).toHaveBeenCalledWith('const x = 1;');
    expect(screen.getByRole('button', { name: 'Copied!' })).toHaveAttribute('title', 'Copied!');

    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.getByRole('button', { name: 'Copy code' })).toHaveAttribute('title', 'Copy code');
  });

  it('does not remount code blocks (re-running shiki) on identical-prop re-renders', async () => {
    const content = '```ts\nconst x = 1;\n```\n';
    const { container, rerender } = renderReader(content);
    await waitFor(() => {
      expect(container.querySelector('code.md-shiki')).toBeInTheDocument();
    });
    expect(shikiMock.codeToHtml).toHaveBeenCalledTimes(1);

    rerender(
      <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets={true} />,
    );
    await act(async () => {});

    // Memoized: the parent re-render never reached react-markdown, so the
    // highlighted block survived instead of flashing back to plain text.
    expect(shikiMock.codeToHtml).toHaveBeenCalledTimes(1);
    expect(container.querySelector('code.md-shiki')).toBeInTheDocument();
  });

  it('renders mermaid fences as diagrams without codeblock chrome', async () => {
    const { container } = renderReader('```mermaid\ngraph TD;\nA-->B;\n```\n');

    await waitFor(() => {
      expect(screen.getByTestId('mermaid-svg')).toBeInTheDocument();
    });
    expect(container.querySelector('.md-codeblock')).toBeNull();
    expect(shikiMock.codeToHtml).not.toHaveBeenCalled();
  });
});
