/**
 * Headless run of the reader pipeline yielding each stamped block's rendered
 * text. Pure string/data code: no DOM, no React. The processor below must stay
 * byte-for-byte MarkdownReader's pipeline, so offsets over the extracted text
 * hold against the live DOM's text nodes; the pipeline-parity fixture pins it.
 *
 * Normalization rule, entire: a block's `text` is every hast text-node value
 * in its subtree concatenated in tree order — no collapsing, trimming, or NFC
 * — indexed in UTF-16 code units. Two deliberate divergence fixes: `pre`
 * subtrees drop one trailing `\n` to match CodeBlock's render, and React-added
 * chrome has no hast text at all, so the DOM walker skips `[data-md-chrome]`.
 */

import type { Element, Root, RootContent } from 'hast';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import remarkParse from 'remark-parse';
import remarkRehype from 'remark-rehype';
import { unified } from 'unified';
import rehypeAlerts from '../rehypeAlerts';
import rehypeHeadingSlugs from '../rehypeHeadingSlugs';
import rehypeProseTransforms from '../proseTransforms';
import rehypeSourceAnchors from '../rehypeSourceAnchors';
import { readerSanitizeSchema } from '../sanitizeSchema';
import type { BlockText } from './types';

/** Mirrors MarkdownReader's plugin list; order is load-bearing (index.tsx). */
function readerProcessor() {
  return unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkFrontmatter)
    .use(remarkRehype, { allowDangerousHtml: true })
    .use(rehypeRaw)
    .use(rehypeSanitize, readerSanitizeSchema)
    .use(rehypeSourceAnchors, { lineOffset: 0 })
    .use(rehypeAlerts)
    .use(rehypeHeadingSlugs)
    .use(rehypeProseTransforms);
}

/** Run the full reader pipeline headlessly; returns the final hast root. */
export function runReaderPipeline(content: string): Root {
  const processor = readerProcessor();
  return processor.runSync(processor.parse(content)) as Root;
}

function isElement(node: Root | RootContent): node is Element {
  return node.type === 'element';
}

function subtreeText(node: RootContent): string {
  if (node.type === 'text') {
    return node.value;
  }
  if ('children' in node) {
    return node.children.map(subtreeText).join('');
  }
  return '';
}

function isMermaidPre(pre: Element): boolean {
  for (const child of pre.children) {
    if (isElement(child) && child.tagName === 'code') {
      const className = child.properties?.className;
      const classes = Array.isArray(className) ? className.map(String) : [];
      return classes.includes('language-mermaid');
    }
  }
  return false;
}

interface OpenBlock {
  out: BlockText;
}

/**
 * Every stamped block's rendered text, in document order. A descendant's text
 * is always a contiguous slice of its ancestor's, recorded as
 * `parentId`/`startInParent` — that is what `ownerBlockFor` walks.
 */
export function extractBlockTexts(content: string): BlockText[] {
  const blocks: BlockText[] = [];
  const stack: OpenBlock[] = [];

  const append = (value: string): void => {
    for (const open of stack) {
      open.out.text += value;
    }
  };

  // A mermaid pre renders as an svg with none of its code text, so text-space
  // diverges for every open block whose text includes it, ancestors included.
  const markStackNonPaintable = (): void => {
    for (const open of stack) {
      open.out.nonPaintable = true;
    }
  };

  const walk = (node: Root | RootContent): void => {
    if (node.type === 'text') {
      append(node.value);
      return;
    }
    if (isElement(node)) {
      const blockId = node.properties?.dataBlockId;
      if (typeof blockId === 'string') {
        const parent = stack[stack.length - 1]?.out ?? null;
        const out: BlockText = {
          blockId,
          startLine: Number(node.properties?.dataSourceLine),
          endLine: Number(node.properties?.dataSourceLineEnd),
          text: '',
          depth: stack.length,
          parentId: parent ? parent.blockId : null,
          startInParent: parent ? parent.text.length : 0,
        };
        blocks.push(out);
        stack.push({ out });
        walkInner(node);
        stack.pop();
        return;
      }
      walkInner(node);
      return;
    }
    if ('children' in node) {
      for (const child of node.children) {
        walk(child);
      }
    }
  };

  const walkInner = (node: Element | Root): void => {
    if (isElement(node) && node.tagName === 'pre') {
      // Strip exactly one trailing newline to match CodeBlock's render; nothing
      // is ever stamped inside a `pre`, so collapsing the subtree is safe.
      if (isMermaidPre(node)) {
        markStackNonPaintable();
      }
      append(subtreeText(node).replace(/\n$/, ''));
      return;
    }
    for (const child of node.children) {
      walk(child);
    }
  };

  walk(runReaderPipeline(content));
  return blocks;
}

/**
 * Canonical owner selection: the deepest stamped descendant fully containing
 * `[start, end)` owns it. Returns that block and the range in its text-space.
 */
export function ownerBlockFor(
  blocks: BlockText[],
  blockId: string,
  start: number,
  end: number,
): { block: BlockText; start: number; end: number } {
  let current = blocks.find((b) => b.blockId === blockId);
  if (!current) {
    throw new Error(`ownerBlockFor: unknown blockId ${blockId}`);
  }
  let s = start;
  let e = end;
  for (;;) {
    const child = blocks.find(
      (b) =>
        b.parentId === current!.blockId &&
        b.startInParent <= s &&
        e <= b.startInParent + b.text.length,
    );
    if (!child) {
      return { block: current, start: s, end: e };
    }
    s -= child.startInParent;
    e -= child.startInParent;
    current = child;
  }
}
