/**
 * Detects GitHub-style alert blockquotes and tags them for the reader's
 * blockquote renderer. A blockquote whose first paragraph's FIRST LINE is
 * exactly `[!NOTE|TIP|WARNING|CAUTION|IMPORTANT]` (case-insensitive, marker
 * owning the whole line, as GitHub does) loses that line and gains
 * `dataAlertKind`.
 *
 * Runs AFTER rehypeSourceAnchors, so the blockquote keeps its stamped
 * `data-block-id`/`data-source-line*` and its range covers the marker line.
 */

import type { Element, ElementContent, Root, RootContent } from "hast";

export const ALERT_KINDS = ["note", "tip", "warning", "caution", "important"] as const;
export type AlertKind = (typeof ALERT_KINDS)[number];

const MARKER = /^\[!(NOTE|TIP|WARNING|CAUTION|IMPORTANT)\]\s*$/i;

function isElement(node: Root | RootContent | ElementContent): node is Element {
  return node.type === "element";
}

/** First element child of the blockquote, provided everything before it is whitespace text. */
function leadingParagraph(blockquote: Element): Element | null {
  for (const child of blockquote.children) {
    if (child.type === "text") {
      if (child.value.trim() !== "") {
        return null;
      }
      continue;
    }
    if (isElement(child)) {
      return child.tagName === "p" ? child : null;
    }
    return null;
  }
  return null;
}

/** Strip the marker line and return the kind, or null leaving the tree alone. */
function detectAndStripMarker(blockquote: Element): AlertKind | null {
  const paragraph = leadingParagraph(blockquote);
  const first = paragraph?.children[0];
  if (!paragraph || !first || first.type !== "text") {
    return null;
  }

  const newlineAt = first.value.indexOf("\n");
  const firstLine = newlineAt === -1 ? first.value : first.value.slice(0, newlineAt);
  const match = MARKER.exec(firstLine);
  if (!match) {
    return null;
  }

  if (newlineAt === -1) {
    // Marker owned the text node; drop it and any `<br>` two-space syntax left.
    paragraph.children.shift();
    const next = paragraph.children[0];
    if (next && isElement(next) && next.tagName === "br") {
      paragraph.children.shift();
    }
  } else {
    first.value = first.value.slice(newlineAt + 1);
  }

  const remaining = paragraph.children[0];
  if (remaining && remaining.type === "text") {
    remaining.value = remaining.value.replace(/^\n/, "");
    if (remaining.value === "") {
      paragraph.children.shift();
    }
  }
  if (paragraph.children.length === 0) {
    blockquote.children.splice(blockquote.children.indexOf(paragraph), 1);
  }

  return match[1].toLowerCase() as AlertKind;
}

export default function rehypeAlerts() {
  return (tree: Root): void => {
    const walk = (node: Root | RootContent): void => {
      if (isElement(node) && node.tagName === "blockquote") {
        const kind = detectAndStripMarker(node);
        if (kind) {
          (node.properties ??= {}).dataAlertKind = kind;
        }
      }
      if ("children" in node) {
        for (const child of node.children) {
          walk(child);
        }
      }
    };
    walk(tree);
  };
}
