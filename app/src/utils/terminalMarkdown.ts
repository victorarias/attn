import type { Terminal } from '@xterm/xterm';

interface TextRun {
  text: string;
  bold: boolean;
  italic: boolean;
  strikethrough: boolean;
  underline: boolean;
  colored: boolean; // non-default foreground color
}

/**
 * Clean terminal whitespace artifacts from lines:
 * - Trim trailing whitespace (terminal cell padding)
 * - Dedent common leading whitespace (terminal cursor offset artifact)
 */
export function cleanTerminalLines(lines: string[]): string[] {
  const result = lines.map(l => l.trimEnd());
  if (result.length <= 1) return result;

  const nonEmpty = result.filter(l => l.length > 0);
  if (nonEmpty.length === 0) return result;

  // Standard dedent: all lines share a common indent
  const overallMin = Math.min(...nonEmpty.map(l => l.search(/\S|$/)));
  if (overallMin > 0) {
    return result.map(l => l.length > 0 ? l.substring(overallMin) : l);
  }

  // Terminal artifact: first line starts at cursor column, lines 2+ are offset
  const restNonEmpty = result.slice(1).filter(l => l.length > 0);
  if (restNonEmpty.length > 0) {
    const restMin = Math.min(...restNonEmpty.map(l => l.search(/\S|$/)));
    if (restMin > 0) {
      return [result[0], ...result.slice(1).map(l => l.length > 0 ? l.substring(restMin) : l)];
    }
  }

  return result;
}

/**
 * Determine if colored segments in this line should be treated as inline code.
 *
 * Heuristic: if >= 30% of non-whitespace characters use the default foreground
 * color, the line is prose with inline code references. Otherwise it's a
 * code/diff block where every token is colored — backticking would be noisy.
 */
function shouldAllowInlineCode(runs: TextRun[]): boolean {
  let defaultNonSpace = 0;
  let totalNonSpace = 0;
  for (const run of runs) {
    const n = run.text.replace(/\s/g, '').length;
    totalNonSpace += n;
    if (!run.colored) defaultNonSpace += n;
  }
  return totalNonSpace > 0 && (defaultNonSpace / totalNonSpace) >= 0.3;
}

/**
 * Convert a text run to its markdown representation.
 *
 * Priority:
 *   bold/italic/strikethrough → markdown attribute markers
 *   colored (no semantic attrs) → `inline code` (when line allows it)
 *   underline (non-colored)    → <u>text</u>
 */
function runToMarkdown(run: TextRun, allowInlineCode: boolean): string {
  const { text, bold, italic, strikethrough, underline, colored } = run;
  if (!text) return '';

  const hasSemanticAttr = bold || italic || strikethrough;
  const useCode = allowInlineCode && colored && !hasSemanticAttr;
  if (!hasSemanticAttr && !underline && !useCode) return text;

  // Markdown markers must touch non-whitespace to render
  const match = text.match(/^(\s*)(.*?)(\s*)$/s);
  if (!match) return text;
  const [, leading, inner, trailing] = match;
  if (!inner) return text;

  let result = inner;

  if (useCode) {
    result = inner.includes('`') ? `\`\` ${inner} \`\`` : `\`${inner}\``;
  } else {
    if (underline && !colored) result = `<u>${result}</u>`;
    if (strikethrough) result = `~~${result}~~`;
    if (bold && italic) result = `***${result}***`;
    else if (bold) result = `**${result}**`;
    else if (italic) result = `*${result}*`;
  }

  return leading + result + trailing;
}

/**
 * Read selected region of terminal buffer and convert formatting to markdown.
 *
 * Two-pass per line:
 *   1. Build text runs from buffer cells (tracking bold, italic, color, etc.)
 *   2. Decide per-line whether colored segments are inline code (prose) or
 *      syntax highlighting (code block), then apply markdown markers.
 */
export function bufferSelectionToMarkdown(term: Terminal): string {
  const range = term.getSelectionPosition();
  if (!range) return term.getSelection();

  const buffer = term.buffer.active;
  const startRow = range.start.y - 1; // 0-based
  const endRow = range.end.y - 1;
  const reusableCell = buffer.getNullCell();

  const rawLines: string[] = [];

  for (let y = startRow; y <= endRow; y++) {
    const line = buffer.getLine(y);
    if (!line) {
      rawLines.push('');
      continue;
    }

    const cellStart = (y === startRow) ? range.start.x - 1 : 0;
    const cellEnd = (y === endRow) ? range.end.x : Math.min(line.length, term.cols);

    // Pass 1: build text runs with formatting attributes
    const runs: TextRun[] = [];
    let cur: TextRun | null = null;

    for (let x = cellStart; x < cellEnd; x++) {
      const cell = line.getCell(x, reusableCell);
      if (!cell || cell.getWidth() === 0) continue;

      const b = !!cell.isBold();
      const i = !!cell.isItalic();
      const s = !!cell.isStrikethrough();
      const u = !!cell.isUnderline();
      const c = !cell.isFgDefault();
      const ch = cell.getChars() || ' ';

      if (cur && cur.bold === b && cur.italic === i && cur.strikethrough === s && cur.underline === u && cur.colored === c) {
        cur.text += ch;
      } else {
        if (cur) runs.push(cur);
        cur = { text: ch, bold: b, italic: i, strikethrough: s, underline: u, colored: c };
      }
    }
    if (cur) runs.push(cur);

    // Pass 2: convert runs to markdown
    const allowCode = shouldAllowInlineCode(runs);
    let lineResult = '';
    for (const run of runs) {
      lineResult += runToMarkdown(run, allowCode);
    }

    // Soft-wrapped lines are continuations of the previous line
    if (line.isWrapped && rawLines.length > 0) {
      rawLines[rawLines.length - 1] += lineResult;
    } else {
      rawLines.push(lineResult);
    }
  }

  return cleanTerminalLines(rawLines).join('\n');
}
