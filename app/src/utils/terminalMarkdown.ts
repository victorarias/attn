import type { Terminal } from '@xterm/xterm';

interface Formatting {
  bold: boolean;
  italic: boolean;
  strikethrough: boolean;
  underline: boolean;
}

const NO_FMT: Formatting = { bold: false, italic: false, strikethrough: false, underline: false };

function fmtEqual(a: Formatting, b: Formatting): boolean {
  return a.bold === b.bold && a.italic === b.italic &&
    a.strikethrough === b.strikethrough && a.underline === b.underline;
}

/**
 * Wrap a text segment with markdown markers based on its formatting.
 * Leading/trailing whitespace is kept outside the markers so they render correctly.
 */
function wrapMarkdown(text: string, fmt: Formatting): string {
  if (!text) return '';
  if (!fmt.bold && !fmt.italic && !fmt.strikethrough && !fmt.underline) return text;

  // Markdown markers must touch non-whitespace to render
  const match = text.match(/^(\s*)(.*?)(\s*)$/s);
  if (!match) return text;
  const [, leading, inner, trailing] = match;
  if (!inner) return text;

  let result = inner;
  if (fmt.underline) result = `<u>${result}</u>`;
  if (fmt.strikethrough) result = `~~${result}~~`;
  if (fmt.bold && fmt.italic) result = `***${result}***`;
  else if (fmt.bold) result = `**${result}**`;
  else if (fmt.italic) result = `*${result}*`;

  return leading + result + trailing;
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
 * Read selected region of terminal buffer and convert ANSI formatting to markdown.
 *
 * Mappings:
 *   Bold        -> **text**
 *   Italic      -> *text*
 *   Bold+Italic -> ***text***
 *   Strikethrough -> ~~text~~
 *   Underline   -> <u>text</u>
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

    // Cell range for this line (0-based, exclusive end)
    const cellStart = (y === startRow) ? range.start.x - 1 : 0;
    const cellEnd = (y === endRow) ? range.end.x : Math.min(line.length, term.cols);

    let lineResult = '';
    let currentFmt: Formatting = { ...NO_FMT };
    let currentText = '';

    for (let x = cellStart; x < cellEnd; x++) {
      const cell = line.getCell(x, reusableCell);
      if (!cell) continue;
      if (cell.getWidth() === 0) continue; // wide char continuation

      const cellFmt: Formatting = {
        bold: !!cell.isBold(),
        italic: !!cell.isItalic(),
        strikethrough: !!cell.isStrikethrough(),
        underline: !!cell.isUnderline(),
      };

      const ch = cell.getChars() || ' ';

      if (fmtEqual(cellFmt, currentFmt)) {
        currentText += ch;
      } else {
        lineResult += wrapMarkdown(currentText, currentFmt);
        currentText = ch;
        currentFmt = { ...cellFmt };
      }
    }
    lineResult += wrapMarkdown(currentText, currentFmt);

    // Soft-wrapped lines are continuations of the previous line
    if (line.isWrapped && rawLines.length > 0) {
      rawLines[rawLines.length - 1] += lineResult;
    } else {
      rawLines.push(lineResult);
    }
  }

  return cleanTerminalLines(rawLines).join('\n');
}
