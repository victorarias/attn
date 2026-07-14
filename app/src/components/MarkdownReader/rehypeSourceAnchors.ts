/**
 * rehypeSourceAnchors — stamps stable anchoring attributes onto the hast tree
 * so rendered markdown blocks can be traced back to raw-file source lines.
 *
 * Every top-level block element, plus every `<li>` anywhere in the tree
 * (list items anchor individually), receives:
 *
 * - `data-block-id`      deterministic id derived from document-order index +
 *                        node type (e.g. `b3-paragraph`). Identical content
 *                        always produces identical ids.
 * - `data-source-line`     1-based first line of the block in the RAW file.
 * - `data-source-line-end` 1-based last line of the block in the RAW file.
 *
 * Nodes without a source position (plugin-generated) get NO data attributes —
 * skip, don't guess — and don't consume an index slot.
 *
 * Frontmatter awareness: markdown positions are relative to the string that
 * was parsed. When the caller strips YAML frontmatter (or any prefix) before
 * parsing, it must pass `lineOffset` = number of raw-file lines removed, so
 * stamped lines always reflect the raw file (the `contentStartLine` lesson).
 *
 * Pure function of the tree — no DOM, no React.
 */

import type { Element, Root, RootContent } from "hast";

export interface RehypeSourceAnchorsOptions {
  /**
   * Number of raw-file lines stripped before the parsed content began.
   * Example: a raw file whose first 4 lines were removed (3 frontmatter
   * lines + 1 blank) parses with line 1 = raw line 5, so pass 4.
   */
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
 * Stamps the node and returns true, or returns false untouched when the node
 * has no source position (plugin-generated nodes): per the anchoring contract,
 * "stamped" always means "fully anchored" — a block id without a line range is
 * an inconsistent state downstream anchor code would have to special-case, and
 * an unstamped node must not consume an index slot (so its presence never
 * shifts the ids of real source blocks).
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

/**
 * Rehype plugin. Usable directly in react-markdown's `rehypePlugins`:
 *
 *   rehypePlugins={[[rehypeSourceAnchors, { lineOffset }]]}
 */
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
