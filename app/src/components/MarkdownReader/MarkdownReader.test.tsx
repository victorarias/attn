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

describe('MarkdownReader GitHub alerts', () => {
  const kinds = [
    ['NOTE', 'note', 'Note'],
    ['TIP', 'tip', 'Tip'],
    ['WARNING', 'warning', 'Warning'],
    ['CAUTION', 'caution', 'Caution'],
    ['IMPORTANT', 'important', 'Important'],
  ] as const;

  it.each(kinds)('renders [!%s] as an alert with icon, title, and class', (marker, kind, title) => {
    const { container } = renderReader(`> [!${marker}]\n> Alert body text.\n`);

    const alert = container.querySelector(`.md-alert.md-alert-${kind}`);
    expect(alert).toBeInTheDocument();
    expect(alert).toHaveAttribute('data-alert-kind', kind);
    expect(alert!.querySelector('.md-alert-title svg path')).toBeInTheDocument();
    expect(alert!.querySelector('.md-alert-title span')).toHaveTextContent(title);
    expect(alert).toHaveTextContent('Alert body text.');
    // The marker line is stripped from the body.
    expect(alert!.textContent).not.toContain(`[!${marker}]`);
    // No blockquote element remains for the alert.
    expect(container.querySelector('blockquote')).toBeNull();
  });

  it('is case-insensitive and keeps the anchoring attributes on the wrapper', () => {
    const { container } = renderReader('intro\n\n> [!note]\n> Body.\n');

    const alert = container.querySelector('.md-alert-note');
    expect(alert).toHaveAttribute('data-block-id', 'b1-blockquote');
    expect(alert).toHaveAttribute('data-source-line', '3');
    expect(alert).toHaveAttribute('data-source-line-end', '4');
  });

  it('keeps list content inside the alert body', () => {
    const { container } = renderReader('> [!TIP]\n> - item one\n> - item two\n');

    const alert = container.querySelector('.md-alert-tip')!;
    expect(alert.querySelectorAll('li')).toHaveLength(2);
  });

  it('leaves non-alert blockquotes untouched', () => {
    const { container } = renderReader('> Just a quote.\n\n> [!NOTE] trailing words disqualify\n');

    const quotes = container.querySelectorAll('blockquote');
    expect(quotes).toHaveLength(2);
    expect(container.querySelector('.md-alert')).toBeNull();
    // The pseudo-marker stays visible as regular text when not an alert.
    expect(quotes[1]).toHaveTextContent('[!NOTE] trailing words disqualify');
  });
});

describe('MarkdownReader task lists', () => {
  it('renders read-only checkboxes with correct checked state', () => {
    const { container } = renderReader('- [x] done thing\n- [ ] open thing\n');

    const boxes = container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]');
    expect(boxes).toHaveLength(2);
    expect(boxes[0].checked).toBe(true);
    expect(boxes[1].checked).toBe(false);
    for (const box of boxes) {
      expect(box).toBeDisabled();
    }
    expect(container.querySelectorAll('li.task-list-item')).toHaveLength(2);
    // Task-list items still anchor individually.
    expect(container.querySelectorAll('li.task-list-item[data-source-line]')).toHaveLength(2);
  });
});

describe('MarkdownReader tables', () => {
  const table = '| Col A | Col B |\n| --- | --- |\n| a1 | b1 |\n';

  it('wraps tables in an overflow wrapper that carries the anchoring attributes', () => {
    const { container } = renderReader(table);

    const wrap = container.querySelector('.md-table-wrap');
    expect(wrap).toBeInTheDocument();
    expect(wrap).toHaveAttribute('data-block-id', 'b0-table');
    expect(wrap).toHaveAttribute('data-source-line', '1');
    expect(wrap).toHaveAttribute('data-source-line-end', '3');

    // The table sits inside the wrapper and does NOT duplicate the anchor
    // (consumers count blocks by data-block-id).
    const tableEl = wrap!.querySelector('table');
    expect(tableEl).toBeInTheDocument();
    expect(tableEl).not.toHaveAttribute('data-block-id');
    expect(tableEl).not.toHaveAttribute('data-source-line');
  });

  it('keeps GFM column alignment', () => {
    const { container } = renderReader('| L | R |\n| :-- | --: |\n| a | b |\n');

    const cells = container.querySelectorAll('td');
    expect(cells[1]).toHaveStyle({ textAlign: 'right' });
  });
});

describe('MarkdownReader images + lightbox', () => {
  it('renders a relative local image inline through the asset protocol', () => {
    const { container } = renderReader('![diagram](docs/pic%20name.png)');

    const img = container.querySelector<HTMLImageElement>('img.md-reader-image')!;
    // Resolved against the doc dir, percent-decoded, then convertFileSrc'd.
    expect(img).toHaveAttribute('src', 'asset://localhost//tmp/project/docs/pic name.png');
    expect(img).toHaveAttribute('alt', 'diagram');
    expect(img).toHaveAttribute('loading', 'lazy');
  });

  it('keeps the blocked fallback for remote and unsafe images', () => {
    const { container } = renderReader(
      '![remote](https://example.test/pixel.png)\n\n![script](../evil.sh)\n',
    );

    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('[blocked image: remote]')).toBeInTheDocument();
    expect(screen.getByText('[blocked image: script]')).toBeInTheDocument();
  });

  it('blocks local images when local targets are disallowed (remote workspace)', () => {
    const { container } = renderReader('![diagram](docs/pic.png)', false);

    expect(container.querySelector('img')).toBeNull();
    expect(screen.getByText('[blocked image: diagram]')).toBeInTheDocument();
  });

  it('opens a lightbox on click, closes on Escape and backdrop click, not on image click', () => {
    const { container } = renderReader('![the diagram](docs/pic.png)');

    fireEvent.click(container.querySelector('img.md-reader-image')!);
    // Portal to document.body: it escapes the tile subtree.
    const lightbox = document.body.querySelector('.md-lightbox')!;
    expect(lightbox).toBeInTheDocument();
    expect(lightbox.parentElement).toBe(document.body);
    expect(lightbox.querySelector('.md-lightbox-img')).toHaveAttribute(
      'src',
      'asset://localhost//tmp/project/docs/pic.png',
    );
    expect(lightbox.querySelector('.md-lightbox-caption')).toHaveTextContent('the diagram');

    // Clicking the image itself does NOT close.
    fireEvent.click(lightbox.querySelector('.md-lightbox-img')!);
    expect(document.body.querySelector('.md-lightbox')).toBeInTheDocument();

    // Escape closes.
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(document.body.querySelector('.md-lightbox')).toBeNull();

    // Reopen, backdrop click closes.
    fireEvent.click(container.querySelector('img.md-reader-image')!);
    fireEvent.click(document.body.querySelector('.md-lightbox')!);
    expect(document.body.querySelector('.md-lightbox')).toBeNull();
  });

  it('omits the caption when the image has no alt text', () => {
    const { container } = renderReader('![](docs/pic.png)');

    fireEvent.click(container.querySelector('img.md-reader-image')!);
    expect(document.body.querySelector('.md-lightbox')).toBeInTheDocument();
    expect(document.body.querySelector('.md-lightbox-caption')).toBeNull();
    fireEvent.keyDown(window, { key: 'Escape' });
  });
});

describe('MarkdownReader raw HTML sanitization', () => {
  it('strips scripts, styles, and event handlers but keeps allowed elements', () => {
    const { container } = renderReader(
      'before\n\n<script>window.pwned = true;</script>\n\n<style>body { display: none; }</style>\n\n<div onclick="window.pwned = true" title="ok">clickable</div>\n\nUse <kbd>Cmd</kbd>+<kbd>C</kbd>, H<sub>2</sub>O and x<sup>2</sup>.<br>done\n',
    );

    const card = container.querySelector('.md-reader-card')!;
    expect(card.querySelector('script')).toBeNull();
    expect(card.querySelector('style')).toBeNull();
    // Stripped WITH their content — nothing leaks into prose.
    expect(card.textContent).not.toContain('pwned');
    expect(card.textContent).not.toContain('display: none');

    const div = screen.getByText('clickable');
    expect(div).not.toHaveAttribute('onclick');
    expect(div).toHaveAttribute('title', 'ok');

    expect(card.querySelectorAll('kbd')).toHaveLength(2);
    expect(card.querySelector('sub')).toHaveTextContent('2');
    expect(card.querySelector('sup')).toHaveTextContent('2');
    expect(card.querySelector('br')).toBeInTheDocument();
  });

  it('keeps <details>/<summary> (with open) and anchors them like any block', () => {
    const { container } = renderReader(
      '<details open>\n<summary>More</summary>\n\nHidden **body** text.\n\n</details>\n',
    );

    const details = container.querySelector('details')!;
    expect(details).toBeInTheDocument();
    expect(details.open).toBe(true);
    expect(details.querySelector('summary')).toHaveTextContent('More');
    // Markdown between the tags still renders as markdown (rehype-raw).
    expect(details.querySelector('strong')).toHaveTextContent('body');
    // Sanitize runs before the anchoring pass, so raw HTML blocks anchor too.
    expect(details).toHaveAttribute('data-block-id');
    expect(details).toHaveAttribute('data-source-line', '1');
  });

  it('cannot forge the reader anchoring attributes from author HTML', () => {
    const { container } = renderReader('<p data-block-id="b999-fake" data-source-line="999">spoof</p>\n');

    const spoof = screen.getByText('spoof');
    expect(spoof).not.toHaveAttribute('data-block-id', 'b999-fake');
    expect(spoof).not.toHaveAttribute('data-source-line', '999');
    // The real pass stamped it instead.
    expect(container.querySelector('[data-block-id="b0-p"], [data-block-id="b0-paragraph"]')).toBeInTheDocument();
  });
});

describe('MarkdownReader content re-render gate', () => {
  it('same content: no remount — user-toggled <details> stays open on identical re-render', () => {
    const content = '<details>\n<summary>More</summary>\n\nBody.\n\n</details>\n';
    const { container, rerender } = render(
      <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets={true} />,
    );

    const details = container.querySelector('details')!;
    expect(details.open).toBe(false);
    // User toggles it open: DOM-owned state React knows nothing about.
    details.open = true;

    rerender(
      <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets={true} />,
    );

    const after = container.querySelector('details')!;
    expect(after.isSameNode(details)).toBe(true);
    expect(after.open).toBe(true);
  });

  it('opening the lightbox does not re-render (or remount) the document subtree', () => {
    const { container } = renderReader('![pic](docs/pic.png)\n\n<details>\n<summary>More</summary>\n\nBody.\n\n</details>\n');

    const details = container.querySelector('details')!;
    details.open = true;

    fireEvent.click(container.querySelector('img.md-reader-image')!);
    expect(document.body.querySelector('.md-lightbox')).toBeInTheDocument();

    const after = container.querySelector('details')!;
    expect(after.isSameNode(details)).toBe(true);
    expect(after.open).toBe(true);
    fireEvent.keyDown(window, { key: 'Escape' });
  });

  it('changed content: the subtree re-renders (and resets DOM state)', () => {
    const content = '<details>\n<summary>More</summary>\n\nBody.\n\n</details>\n';
    const { container, rerender } = render(
      <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets={true} />,
    );
    const details = container.querySelector('details')!;
    details.open = true;

    rerender(
      <MarkdownReader
        content={`${content}\nNew paragraph.\n`}
        path="/tmp/project/README.md"
        allowLocalTargets={true}
      />,
    );

    expect(screen.getByText('New paragraph.')).toBeInTheDocument();
  });
});

describe('MarkdownReader prose transforms', () => {
  it('applies smart punctuation and emoji to prose but never to code or flags', async () => {
    const { container } = renderReader(
      'He said "hello" -- ranges 3--5 work... :rocket:\n\nRun `bun --watch` with --verbose\n\n```sh\necho "raw" 3--5\n```\n',
    );
    // Let the fenced block's async shiki hydration settle (avoids act noise).
    await act(async () => {});

    const text = container.querySelector('.md-reader-card')!.textContent!;
    expect(text).toContain('“hello”');
    expect(text).toContain('3–5');
    expect(text).toContain('…');
    expect(text).toContain('🚀');
    // Bare -- between words is never rewritten (narrowed en-dash rule).
    expect(text).toContain('"hello" -- ranges'.replace('"hello"', '“hello”'));
    // CLI flags survive, both in prose and in code.
    expect(text).toContain('--verbose');
    expect(container.querySelector(':not(pre) > code')).toHaveTextContent('bun --watch');
    expect(container.querySelector('pre code')).toHaveTextContent('echo "raw" 3--5');
  });

  it('transforms link labels but not hrefs', () => {
    renderReader('["quoted label"](https://example.test/a--b)\n');

    const link = screen.getByRole('link', { name: '“quoted label”' });
    expect(link).toHaveAttribute('href', 'https://example.test/a--b');
  });
});
