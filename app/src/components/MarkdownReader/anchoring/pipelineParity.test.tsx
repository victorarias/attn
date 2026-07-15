/**
 * Pipeline parity — for every [data-block-id] element the chrome-skipped DOM
 * walk text equals extractBlockTexts' text for that id. This single test pins
 * the headless pipeline to the live one forever. (Moved out of the deleted
 * PR4 spike test file; the paint-spike tests it lived with are superseded by
 * annotations/useAnnotations.test.tsx.)
 */

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
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
