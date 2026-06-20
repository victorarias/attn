// Parse a markdown document into its heading outline for the context rail's
// jump-to-heading list. Pure (no DOM), so it runs in the same happy-dom unit tests
// as the rest of the browser orchestration; the editor's actual scroll-on-click is a
// browser behavior covered by the Playwright harness.
//
// We parse ATX headings only (`# …` through `###### …`) — the form the notebook's
// own notes use. Setext underlines (`===`/`---`) are intentionally ignored: they're
// rare here and ambiguous against horizontal rules and table separators.

export interface OutlineHeading {
  // 1..6, from the count of leading '#'.
  level: number;
  // The heading text, with the leading '#'s and any closing '#'s stripped.
  text: string;
  // Character offset of the heading line's start in the source. The editor scrolls
  // this position to the top of the viewport on click, so it must index into the
  // SAME string the editor holds (the live draft).
  pos: number;
  // 1-based source line number — a stable React key and handy for debugging.
  line: number;
}

// A heading is up to three leading spaces (CommonMark's limit before it becomes an
// indented code block), 1–6 '#', then at least one space and the text. The trailing
// run of '#'s (an optional closing sequence) is stripped. Requiring the space after
// the hashes excludes `#hashtag`, which is not a heading.
const ATX_HEADING = /^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$/;
// A fenced code block opens/closes on a line of 3+ backticks or 3+ tildes. A `#`
// line inside a fence is code, not a heading, so we must track fence state.
const FENCE = /^[ \t]*(`{3,}|~{3,})/;

export function parseOutline(md: string): OutlineHeading[] {
  const out: OutlineHeading[] = [];
  if (!md) return out;
  const lines = md.split('\n');
  let offset = 0;
  let fenceChar = '';
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = FENCE.exec(line);
    if (fence) {
      const char = fence[1][0];
      // An open fence closes only on a matching marker char; a different marker
      // inside it is just code. An unfenced matching marker opens a new fence.
      if (fenceChar === '') fenceChar = char;
      else if (fenceChar === char) fenceChar = '';
    } else if (fenceChar === '') {
      const heading = ATX_HEADING.exec(line);
      if (heading) {
        const text = heading[2].trim();
        // An empty heading (`## `) carries nothing to jump-label, so skip it.
        if (text) out.push({ level: heading[1].length, text, pos: offset, line: i + 1 });
      }
    }
    offset += line.length + 1; // +1 for the '\n' removed by split
  }
  return out;
}
