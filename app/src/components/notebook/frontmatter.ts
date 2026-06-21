// Parse a note's leading YAML frontmatter block for the in-editor frontmatter card.
// Pure (no DOM, no CodeMirror), so it runs in headless unit tests; the card widget
// and its cursor/focus-gated reveal are layered on top in frontmatterCard.ts.
//
// The notebook's frontmatter is keeper-written and structured, so we parse the small
// YAML subset it actually uses — `key: value`, flow lists `[a, b]`, and block lists
// (`key:` then indented `- item` lines) — rather than pulling in a full YAML parser.
// Anything fancier (nested maps, block scalars) is out of scope; escalate to js-yaml
// if notes ever need it.

export type FrontmatterValue = string | string[];

export interface Frontmatter {
  // The parsed top-level fields. Scalars are strings; list values are string[].
  fields: Record<string, FrontmatterValue>;
  // Source range of the whole `---\n…\n---` block. `from` is always 0 (frontmatter is
  // only frontmatter at the very top of the file); `to` is the start of the body (just
  // past the closing fence's newline) — the exact whole-line boundary a CodeMirror
  // block decoration needs.
  from: number;
  to: number;
}

// A frontmatter fence is a line that is exactly `---` (open) or `---`/`...` (close).
function isFence(line: string): boolean {
  const t = line.trim();
  return t === '---' || t === '...';
}

function stripQuotes(s: string): string {
  if (s.length >= 2) {
    const q = s[0];
    if ((q === '"' || q === "'") && s[s.length - 1] === q) return s.slice(1, -1);
  }
  return s;
}

function parseFields(lines: string[]): Record<string, FrontmatterValue> {
  const out: Record<string, FrontmatterValue> = {};
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    if (!line.trim() || line.trimStart().startsWith('#')) continue; // blank / comment
    // A top-level `key:` starts at column 0; indented lines are list items consumed
    // by the block-list look-ahead below.
    const m = /^([A-Za-z0-9_][\w-]*):[ \t]?(.*)$/.exec(line);
    if (!m) continue;
    const key = m[1];
    const rest = m[2].trim();
    if (rest === '') {
      // Block list: consume the following indented `- item` lines.
      const items: string[] = [];
      while (i + 1 < lines.length && /^\s*-\s+/.test(lines[i + 1])) {
        items.push(stripQuotes(lines[i + 1].replace(/^\s*-\s+/, '').trim()));
        i += 1;
      }
      out[key] = items.length ? items : '';
    } else if (rest.startsWith('[') && rest.endsWith(']')) {
      // Flow list: `[a, b, c]`.
      out[key] = rest
        .slice(1, -1)
        .split(',')
        .map((s) => stripQuotes(s.trim()))
        .filter((s) => s.length > 0);
    } else {
      out[key] = stripQuotes(rest);
    }
  }
  return out;
}

export function parseFrontmatter(doc: string): Frontmatter | null {
  const lines = doc.split('\n');
  // Frontmatter must OPEN on the very first line with an exact `---` (otherwise a `---`
  // mid-document is a horizontal rule, not frontmatter).
  if (lines.length === 0 || lines[0].trim() !== '---') return null;
  // Find the closing fence on its own line.
  let close = -1;
  for (let i = 1; i < lines.length; i += 1) {
    if (isFence(lines[i])) {
      close = i;
      break;
    }
  }
  if (close === -1) return null; // unterminated → treat as ordinary content, not frontmatter
  // Body starts just past the closing fence line (sum of each line + its '\n').
  let to = 0;
  for (let i = 0; i <= close; i += 1) to += lines[i].length + 1;
  to = Math.min(to, doc.length); // no trailing newline after the close → clamp
  return { fields: parseFields(lines.slice(1, close)), from: 0, to };
}
