/**
 * extractBlocks — headless run of the reader pipeline, yielding each stamped
 * block's rendered text. Pure string/data code: no DOM, no React.
 *
 * The processor below is byte-for-byte the same pipeline `MarkdownReader`
 * runs (react-markdown uses these exact remark-rehype options when
 * rehype-raw is present), so offsets computed against the extracted text are
 * valid against the live DOM's text nodes: `rehypeProseTransforms` mutates
 * hast text nodes in place before React ever sees them, and React renders
 * text nodes verbatim (including the `\n` separator text nodes
 * mdast-util-to-hast emits between nested blocks). The pipeline-parity
 * fixture in the test suite pins this equivalence against the live reader.
 *
 * Normalization rule (the whole rule — nothing else): a block's `text` is
 * the concatenation of all hast text-node values in its subtree, in tree
 * order. No whitespace collapsing, no trimming, no NFC. Offsets are UTF-16
 * code units into this string. Two deliberate divergence fixes:
 *
 * - `pre` subtrees drop one trailing `\n` (hast keeps it; CodeBlock renders
 *   `text.replace(/\n$/, '')`).
 * - React-added chrome (alert titles, copy buttons, blocked-image fallbacks)
 *   has no hast text at all; the DOM walker skips `[data-md-chrome]`
 *   subtrees so both sides agree.
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

/**
 * Headless unified processor mirroring MarkdownReader's `remarkPlugins` +
 * `rehypePlugins` exactly (order is load-bearing — see index.tsx).
 */
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

/** Plain subtree text concat (no block bookkeeping) — used for `pre` innards. */
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
 * Extract every stamped block's rendered text from `content`, in document
 * order. `parentId`/`startInParent` record where each nested block's text
 * sits inside its nearest stamped ancestor (a descendant's text is always a
 * contiguous slice of the ancestor's), which is what makes `ownerBlockFor`
 * possible on this flat list.
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
  // diverges from the DOM for EVERY open block whose text includes it — the
  // pre itself when stamped, and any stamped ancestor (li containing a nested
  // mermaid fence) either way.
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
        // walkInner marks this block (and ancestors) nonPaintable when the
        // subtree turns out to be a mermaid pre.
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
      // CodeBlock renders `text.replace(/\n$/, '')`; hast keeps the trailing
      // newline. Strip exactly one so text-space matches the DOM. Nothing is
      // ever stamped inside a `pre`, so collapsing the subtree here is safe.
      if (isMermaidPre(node)) {
        // Every open block's text will include the diagram's code text: the
        // pre itself when stamped, plus stamped ancestors (li) either way.
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
 * Canonical owner selection: when `[start, end)` in `blockId`'s text is fully
 * contained in a stamped descendant (li inside ul), the deepest such
 * descendant owns the range. Returns the owning block plus the range
 * translated into its text.
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
