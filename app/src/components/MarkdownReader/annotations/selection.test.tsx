/**
 * evaluateSelection — pure selection→pending-anchor mapping over a real
 * rendered reader DOM (happy-dom Ranges, synthetic SelectionLike objects;
 * no fake mouse geometry).
 */

import { afterEach, describe, expect, it, vi } from 'vitest';
import { render } from '@testing-library/react';
import { MarkdownReader } from '../index';
import { extractBlockTexts } from '../anchoring';
import { evaluateSelection, type SelectionLike } from './selection';

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(async () => {}),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async () => ({ svg: '<svg data-testid="mermaid-svg"></svg>' })),
  },
}));

const shikiMock = vi.hoisted(() => ({
  codeToHtml: vi.fn(async (code: string) =>
    code
      .split('\n')
      .map((line) => `<span>${line}</span>`)
      .join('<br>')),
}));
vi.mock('shiki', () => shikiMock);

afterEach(() => {
  vi.restoreAllMocks();
});

const DOC = [
  'First paragraph with target words inside it.',
  '',
  'Second block of plain prose here.',
  '',
].join('\n');

function setup(content = DOC) {
  const { container, unmount } = render(
    <MarkdownReader content={content} path="/tmp/project/README.md" allowLocalTargets />,
  );
  const root = container as HTMLElement;
  const blocks = extractBlockTexts(content);
  return { container, root, blocks, unmount };
}

function findTextNode(scope: Element, needle: string): { node: Text; index: number } {
  const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
  let node: Node | null;
  while ((node = walker.nextNode())) {
    const text = node as Text;
    const index = text.data.indexOf(needle);
    if (index >= 0) {
      return { node: text, index };
    }
  }
  throw new Error(`text node containing ${JSON.stringify(needle)} not found`);
}

function selectionFromRange(range: Range): SelectionLike {
  return {
    isCollapsed: range.collapsed,
    rangeCount: 1,
    anchorNode: range.startContainer,
    focusNode: range.endContainer,
    toString: () => range.toString(),
    getRangeAt: () => range,
  };
}

function selectNeedle(root: HTMLElement, needle: string): SelectionLike {
  const { node, index } = findTextNode(root, needle);
  const range = document.createRange();
  range.setStart(node, index);
  range.setEnd(node, index + needle.length);
  return selectionFromRange(range);
}

describe('evaluateSelection', () => {
  it('maps a single-block selection to an anchor with the exact text', () => {
    const { root, blocks } = setup();
    const pending = evaluateSelection(root, selectNeedle(root, 'target words'), DOC, blocks);
    expect(pending).not.toBeNull();
    expect(pending!.anchor.exact).toBe('target words');
    expect(pending!.selectionText).toBe('target words');
    expect(pending!.clamped).toBe(false);
    expect(pending!.isCodeBlock).toBe(false);
    const block = blocks.find((b) => b.blockId === pending!.blockId)!;
    expect(block.text.slice(pending!.anchor.start, pending!.anchor.end)).toBe('target words');
  });

  it('trims whitespace edges off the anchored slice', () => {
    const { root, blocks } = setup();
    const pending = evaluateSelection(root, selectNeedle(root, ' target words '), DOC, blocks);
    expect(pending).not.toBeNull();
    expect(pending!.anchor.exact).toBe('target words');
  });

  it('rejects collapsed and whitespace-only selections', () => {
    const { root, blocks } = setup();

    const { node, index } = findTextNode(root, 'target');
    const collapsed = document.createRange();
    collapsed.setStart(node, index);
    collapsed.setEnd(node, index);
    expect(evaluateSelection(root, selectionFromRange(collapsed), DOC, blocks)).toBeNull();

    expect(evaluateSelection(root, selectNeedle(root, ' '), DOC, blocks)).toBeNull();
    expect(evaluateSelection(root, null, DOC, blocks)).toBeNull();
  });

  it('rejects selections whose endpoint sits in excepted chrome (E2)', () => {
    const { root, blocks } = setup();
    const block = root.querySelector('[data-block-id]')!;
    const chrome = document.createElement('span');
    chrome.setAttribute('data-md-no-annotate', '');
    chrome.textContent = 'chrome text';
    block.appendChild(chrome);

    const { node: chromeText } = findTextNode(chrome, 'chrome');
    const { node: bodyText, index } = findTextNode(root, 'target');
    const range = document.createRange();
    range.setStart(bodyText, index);
    range.setEnd(chromeText, 6);
    const selection: SelectionLike = {
      ...selectionFromRange(range),
      toString: () => 'target words inside it. chrome',
    };
    expect(evaluateSelection(root, selection, DOC, blocks)).toBeNull();
  });

  it('rejects selections escaping the reader root', () => {
    const { root, blocks } = setup();
    const outside = document.createElement('p');
    outside.textContent = 'outside text';
    document.body.appendChild(outside);
    try {
      const { node: inside, index } = findTextNode(root, 'target');
      const range = document.createRange();
      range.setStart(inside, index);
      range.setEnd(outside.firstChild as Text, 7);
      expect(evaluateSelection(root, selectionFromRange(range), DOC, blocks)).toBeNull();
    } finally {
      outside.remove();
    }
  });

  it('clamps a cross-block selection to the first block and flags it (E3)', () => {
    const { root, blocks } = setup();
    const { node: startNode, index } = findTextNode(root, 'target words');
    const { node: endNode, index: endIndex } = findTextNode(root, 'plain prose');
    const range = document.createRange();
    range.setStart(startNode, index);
    range.setEnd(endNode, endIndex + 'plain prose'.length);

    const pending = evaluateSelection(root, selectionFromRange(range), DOC, blocks);
    expect(pending).not.toBeNull();
    expect(pending!.clamped).toBe(true);
    const firstBlock = blocks.find((b) => b.blockId === pending!.blockId)!;
    expect(firstBlock.text).toContain('target words');
    // Clamped to the end of the first block (post whitespace-trim).
    expect(pending!.anchor.exact).toBe(firstBlock.text.slice(pending!.anchor.start).trimEnd());
    expect(pending!.anchor.end).toBe(firstBlock.text.length);
  });

  it('flags code-block ownership for the toolbar mode switch', async () => {
    const codeDoc = 'Intro.\n\n```js\nconst a = 1;\n```\n';
    const { root, blocks } = setup(codeDoc);
    const pending = evaluateSelection(root, selectNeedle(root, 'const a'), codeDoc, blocks);
    expect(pending).not.toBeNull();
    expect(pending!.isCodeBlock).toBe(true);
  });
});
