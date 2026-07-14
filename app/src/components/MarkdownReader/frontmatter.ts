/**
 * Flat YAML frontmatter extraction for the metadata card.
 *
 * The reader parses the FULL document with remark-frontmatter, so remark
 * positions already refer to raw-file lines (anchor lineOffset stays 0).
 * This module only extracts the values the card renders: string / number /
 * boolean scalars and flat string arrays (`[a, b]` or dash lists). Nested
 * objects and multi-line scalars are skipped — PR2 scope, per spec §6.
 */

export interface FrontmatterEntry {
  key: string;
  value: string | string[];
}

export interface ExtractedFrontmatter {
  entries: FrontmatterEntry[];
  /**
   * Number of raw-file lines the frontmatter block occupies, delimiters
   * included. This is the `lineOffset` a caller would pass to
   * rehypeSourceAnchors IF it stripped the block before parsing.
   */
  lineCount: number;
}

const NONE: ExtractedFrontmatter = { entries: [], lineCount: 0 };

function unquote(value: string): string {
  if (value.length >= 2 && ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))) {
    return value.slice(1, -1);
  }
  return value;
}

function parseInlineArray(value: string): string[] | null {
  if (!value.startsWith('[') || !value.endsWith(']')) {
    return null;
  }
  const inner = value.slice(1, -1).trim();
  if (!inner) {
    return [];
  }
  return inner.split(',').map((item) => unquote(item.trim())).filter((item) => item.length > 0);
}

/**
 * Returns the flat entries of a leading `---` YAML frontmatter block, or no
 * entries when the document has none (or the block never closes).
 */
export function extractFrontmatter(content: string): ExtractedFrontmatter {
  const lines = content.split('\n');
  if ((lines[0] ?? '').trim() !== '---') {
    return NONE;
  }
  let closing = -1;
  for (let i = 1; i < lines.length; i++) {
    const trimmed = lines[i].trim();
    if (trimmed === '---' || trimmed === '...') {
      closing = i;
      break;
    }
  }
  if (closing === -1) {
    return NONE;
  }

  const entries: FrontmatterEntry[] = [];
  let pendingListKey: string | null = null;
  let pendingList: string[] = [];
  const flushPendingList = () => {
    // A pending key with no dash items was a nested object or empty value —
    // out of card scope, dropped.
    if (pendingListKey !== null && pendingList.length > 0) {
      entries.push({ key: pendingListKey, value: pendingList });
    }
    pendingListKey = null;
    pendingList = [];
  };

  for (let i = 1; i < closing; i++) {
    const line = lines[i];
    if (!line.trim() || line.trim().startsWith('#')) {
      continue;
    }
    const dashItem = line.match(/^\s+-\s+(.*)$/);
    if (dashItem && pendingListKey !== null) {
      pendingList.push(unquote(dashItem[1].trim()));
      continue;
    }
    flushPendingList();
    // Only top-level `key: value` lines; indented lines (nested objects) skip.
    const keyed = line.match(/^([A-Za-z0-9_-]+)\s*:(.*)$/);
    if (!keyed) {
      continue;
    }
    const key = keyed[1];
    const rawValue = keyed[2].trim();
    if (!rawValue) {
      // Either a dash list follows, or a nested object (skipped on flush).
      pendingListKey = key;
      pendingList = [];
      continue;
    }
    const inlineArray = parseInlineArray(rawValue);
    entries.push({ key, value: inlineArray ?? unquote(rawValue) });
  }
  flushPendingList();

  return { entries, lineCount: closing + 1 };
}
