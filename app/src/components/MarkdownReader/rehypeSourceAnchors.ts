/**
 * Stamps stable anchoring attributes onto the hast tree so rendered blocks
 * trace back to raw-file source lines. Every top-level block element, plus
 * every `<li>` anywhere, gets `data-block-id` (deterministic: document-order
 * index + node type, so identical content yields identical ids) and
 * `data-source-line`/`-end` (1-based, in the RAW file).
 *
 * Markdown positions are relative to the parsed string, so a caller that
 * strips frontmatter must pass `lineOffset` = raw lines removed. Pure over the
 * tree — no DOM, no React.
 */

import type { Element, Root, RootContent } from "hast";

export interface RehypeSourceAnchorsOptions {
  /** Raw-file lines stripped before the parsed content began. */
  lineOffset?: number;
}

const BLOCK_TYPE_BY_TAG: Record<string, string> = {
  p: "paragraph",
  h1: "heading",
  h2: "heading",
  h3: "heading",
  h4: "heading",
  h5: "heading",
  h6: "heading",
  ul: "list",
  ol: "list",
  li: "list-item",
  pre: "code",
  blockquote: "blockquote",
  table: "table",
  hr: "thematic-break",
};

function blockType(tagName: string): string {
  return BLOCK_TYPE_BY_TAG[tagName] ?? tagName;
}

/**
 * Stamped always means fully anchored, so a node with no source position is
 * left untouched (false) and must not consume an index slot — consuming one
 * would shift the ids of real source blocks.
 */
function stamp(node: Element, index: number, lineOffset: number): boolean {
  const position = node.position;
  if (
    !position ||
    typeof position.start?.line !== "number" ||
    typeof position.end?.line !== "number"
  ) {
    return false;
  }
  const properties = (node.properties ??= {});
  properties.dataBlockId = `b${index}-${blockType(node.tagName)}`;
  properties.dataSourceLine = position.start.line + lineOffset;
  properties.dataSourceLineEnd = position.end.line + lineOffset;
  return true;
}

function isElement(node: Root | RootContent): node is Element {
  return node.type === "element";
}

export default function rehypeSourceAnchors(
  options: RehypeSourceAnchorsOptions = {},
) {
  const lineOffset = options.lineOffset ?? 0;

  return (tree: Root): void => {
    let nextIndex = 0;

    const walk = (node: Root | RootContent, isTopLevel: boolean): void => {
      if (
        isElement(node) &&
        (isTopLevel || node.tagName === "li") &&
        stamp(node, nextIndex, lineOffset)
      ) {
        nextIndex += 1;
      }
      if ("children" in node) {
        for (const child of node.children) {
          walk(child, node.type === "root");
        }
      }
    };

    walk(tree, false);
  };
}
