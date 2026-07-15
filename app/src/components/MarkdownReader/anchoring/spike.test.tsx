/**
 * DOM-side anchoring tests that need the REAL reader render:
 *
 * 1. Pipeline parity — for every [data-block-id] element the chrome-skipped
 *    DOM walk text equals extractBlockTexts' text for that id. This single
 *    test pins the headless pipeline to the live one forever.
 * 2. The paint spike end-to-end: marker-driven paint, live-reload rebase
 *    survival, orphan on rewrite. happy-dom has no CSS.highlights, so the
 *    spike paints via MarkPainter — spans we can assert directly.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, render, waitFor } from '@testing-library/react';
import { MarkdownReader } from '../index';
import { blockDomText, resolveDomRange } from './domRange';
import { extractBlockTexts } from './extractBlocks';

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  },
}));

// Mirrors REAL shiki `structure: 'inline'` output: per-line spans joined by
// <br> ELEMENTS (no '\n' text nodes) — the exact shape CodeBlock must repair
// for anchoring offset parity.
const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    code
      .split('\n')
      .map((line) => `<span style="--shiki-light:#000;--shiki-dark:#fff">${line}</span>`)
      .join('<br>')),
}));
vi.mock('shiki', () => shikiMock);

function renderReader(content: string) {
  return render(
    <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets={true} />,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
});

const KITCHEN_SINK = `---
title: Parity
---

# Deploy :rocket:

A paragraph with "smart quotes", an em---dash, \`inline code\`, **bold**,
and a [link](https://example.com).

> [!NOTE]
> Alert body text stays anchored.

> A plain blockquote.

- item one
- item two has 👍👍 emoji
  - nested item
- [ ] task item

1. first
2. second

| Col A | Col B |
| ----- | ----- |
| a1    | b1    |

\`\`\`js
const x = 1;
\`\`\`

\`\`\`mermaid
graph TD; A-->B;
\`\`\`

![remote](https://example.com/pic.png)

Last paragraph 3--5 range...
`;

describe('pipeline parity (headless extraction vs live DOM)', () => {
  it('chrome-skipped DOM text equals extractBlockTexts for every stamped block', () => {
    const { container } = renderReader(KITCHEN_SINK);
    const blocks = extractBlockTexts(KITCHEN_SINK);
    expect(blocks.length).toBeGreaterThan(10);

    const domBlocks = [...container.querySelectorAll('[data-block-id]')];
    expect(domBlocks.map((el) => el.getAttribute('data-block-id'))).toEqual(
      blocks.map((b) => b.blockId),
    );

    for (const block of blocks) {
      if (block.nonPaintable) {
        continue; // mermaid: code text becomes an svg diagram (documented divergence)
      }
      const el = container.querySelector(`[data-block-id="${block.blockId}"]`)!;
      expect(el, block.blockId).not.toBeNull();
      expect(blockDomText(el), block.blockId).toBe(block.text);
    }
  });

  it('keeps parity and offsets for a multi-line shiki block AFTER async hydration', async () => {
    // Real shiki renders line breaks as <br> elements; without CodeBlock's
    // newline repair every offset past line 1 shifts and the wrong text paints.
    const doc = 'Intro.\n\n```js\nconst a = 1;\nconst b = 2;\nconst c = 3;\n```\n';
    const { container } = renderReader(doc);
    await waitFor(() => expect(container.querySelector('.md-shiki')).not.toBeNull());

    const block = extractBlockTexts(doc).find((b) => b.text.includes('const b'))!;
    const el = container.querySelector(`[data-block-id="${block.blockId}"]`)!;
    expect(blockDomText(el)).toBe(block.text);

    // A line-3 anchor resolves to exactly its own characters post-hydration.
    const start = block.text.indexOf('const c = 3;');
    const range = resolveDomRange(el, start, start + 'const c = 3;'.length);
    expect(range?.toString()).toBe('const c = 3;');
  });
});

const SPIKE_DOC = `# Title

Intro paragraph before the target.

The anchored sentence lives here, surrounded by more prose.

- item one
- item two

<!-- attn-anchor-spike: "anchored sentence" -->
<!-- attn-anchor-spike: deletion "item two" -->
`;

function paintedTexts(container: HTMLElement, kind: string): string {
  return [...container.querySelectorAll(`.md-mark-${kind}`)]
    .map((el) => el.textContent)
    .join('');
}

describe('anchor paint spike', () => {
  it('paints marker-driven anchors (comment + deletion) without rendering the markers', () => {
    const { container } = renderReader(SPIKE_DOC);

    expect(container.textContent).not.toContain('attn-anchor-spike');
    expect(paintedTexts(container, 'comment')).toBe('anchored sentence');
    expect(paintedTexts(container, 'deletion')).toBe('item two');

    const state = window.__attnAnchorSpike!.list();
    expect(state.mode).toBe('mark'); // happy-dom: MarkPainter fallback
    expect(state.anchors).toHaveLength(2);
    expect(state.anchors.every((a) => a.state === 'painted')).toBe(true);
  });

  it('survives a live reload that shifts lines: rebases and repaints', () => {
    const { container, rerender } = renderReader(SPIKE_DOC);
    const before = window.__attnAnchorSpike!.list().anchors[0];

    const shifted = `prepended line\n\nanother prepended paragraph\n\n${SPIKE_DOC}`;
    rerender(
      <MarkdownReader content={shifted} path="/tmp/project/README.md" allowLocalTargets={true} />,
    );

    expect(paintedTexts(container, 'comment')).toBe('anchored sentence');
    const after = window.__attnAnchorSpike!.list().anchors[0];
    expect(after.state).toBe('painted');
    expect(after.exact).toBe('anchored sentence');
    expect(after.startLine).toBeGreaterThan(before.startLine!);
  });

  it('orphans (and unpaints) when the annotated sentence is rewritten', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { container, rerender } = renderReader(SPIKE_DOC);

    const rewritten = SPIKE_DOC.replace(
      'The anchored sentence lives here, surrounded by more prose.',
      'Completely different words now occupy this position instead.',
    );
    rerender(
      <MarkdownReader content={rewritten} path="/tmp/project/README.md" allowLocalTargets={true} />,
    );

    expect(paintedTexts(container, 'comment')).toBe('');
    // The deletion anchor is untouched and stays painted.
    expect(paintedTexts(container, 'deletion')).toBe('item two');

    const anchors = window.__attnAnchorSpike!.list().anchors;
    const orphan = anchors.find((a) => a.kind === 'comment')!;
    expect(orphan.state).toBe('orphan');
    expect(warn).toHaveBeenCalledWith(
      '[md-anchor-spike]',
      'orphan',
      expect.any(String),
      expect.any(String),
    );
  });

  it('console annotate() paints and survives reloads like a marker', () => {
    const doc = `Alpha paragraph.\n\nBeta paragraph to annotate.\n\n<!-- attn-anchor-spike: "Alpha" -->\n`;
    const { container, rerender } = renderReader(doc);

    act(() => {
      window.__attnAnchorSpike!.annotate('Beta paragraph');
    });
    expect(paintedTexts(container, 'comment')).toContain('Beta paragraph');

    rerender(
      <MarkdownReader
        content={`prepended\n\n${doc}`}
        path="/tmp/project/README.md"
        allowLocalTargets={true}
      />,
    );
    const manual = window.__attnAnchorSpike!.list().anchors.find((a) => a.key.startsWith('manual:'))!;
    expect(manual.state).toBe('painted');
    expect(paintedTexts(container, 'comment')).toContain('Beta paragraph');
  });

  it('anchors the FIRST block containing the marker text (spec §8 order)', () => {
    const doc =
      'A duplicate phrase lives here.\n\nAgain the duplicate phrase lives here.\n\n' +
      '<!-- attn-anchor-spike: "duplicate phrase" -->\n';
    renderReader(doc);
    const anchors = window.__attnAnchorSpike!.list().anchors;
    expect(anchors).toHaveLength(1);
    expect(anchors[0].state).toBe('painted');
    expect(anchors[0].startLine).toBe(1); // first paragraph, not the later one
  });

  it('orphans a marker whose text only exists in a nonPaintable (mermaid) block', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const doc = '```mermaid\ngraph TD; A-->B;\n```\n\n<!-- attn-anchor-spike: "graph TD" -->\n';
    const { container } = renderReader(doc);

    expect(container.querySelectorAll('[data-md-mark]')).toHaveLength(0);
    const anchors = window.__attnAnchorSpike!.list().anchors;
    expect(anchors).toHaveLength(1);
    expect(anchors[0].state).toBe('orphan');
    expect(warn).toHaveBeenCalled();
  });

  it('does not register paints for documents without markers', () => {
    const { container } = renderReader('# Plain\n\nNo spike here.\n');
    expect(container.querySelectorAll('[data-md-mark]')).toHaveLength(0);
    expect(window.__attnAnchorSpike!.list().anchors).toHaveLength(0);
  });
});
