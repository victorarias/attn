/**
 * Remove fixed-grid whitespace artifacts from captured terminal lines.
 */
export function cleanTerminalLines(lines: string[]): string[] {
  const result = lines.map((line) => line.trimEnd());
  if (result.length <= 1) return result;

  const nonEmpty = result.filter((line) => line.length > 0);
  if (nonEmpty.length === 0) return result;

  const overallMin = Math.min(...nonEmpty.map((line) => line.search(/\S|$/)));
  if (overallMin > 0) {
    return result.map((line) => line.length > 0 ? line.substring(overallMin) : line);
  }

  const restNonEmpty = result.slice(1).filter((line) => line.length > 0);
  if (restNonEmpty.length > 0) {
    const restMin = Math.min(...restNonEmpty.map((line) => line.search(/\S|$/)));
    if (restMin > 0) {
      return [result[0], ...result.slice(1).map((line) => line.length > 0 ? line.substring(restMin) : line)];
    }
  }

  return result;
}

export interface TerminalMarkdownRun {
  text: string;
  bold: boolean;
  italic: boolean;
  strikethrough: boolean;
  underline: boolean;
  colored: boolean;
}

export interface TerminalMarkdownLine {
  runs: TerminalMarkdownRun[];
  wrapped?: boolean;
}

function shouldAllowInlineCode(runs: TerminalMarkdownRun[]): boolean {
  let defaultNonSpace = 0;
  let totalNonSpace = 0;
  for (const run of runs) {
    const count = run.text.replace(/\s/g, '').length;
    totalNonSpace += count;
    if (!run.colored) defaultNonSpace += count;
  }
  return totalNonSpace > 0 && (defaultNonSpace / totalNonSpace) >= 0.3;
}

function runToMarkdown(run: TerminalMarkdownRun, allowInlineCode: boolean): string {
  const { text, bold, italic, strikethrough, underline, colored } = run;
  if (!text) return '';

  const hasSemanticAttribute = bold || italic || strikethrough;
  const useCode = allowInlineCode && colored && !hasSemanticAttribute;
  if (!hasSemanticAttribute && !underline && !useCode) return text;

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

export function terminalStyledSelectionToMarkdown(lines: TerminalMarkdownLine[]): string {
  const rawLines: string[] = [];
  for (const line of lines) {
    const allowInlineCode = shouldAllowInlineCode(line.runs);
    const text = line.runs.map((run) => runToMarkdown(run, allowInlineCode)).join('');
    if (line.wrapped && rawLines.length > 0) {
      rawLines[rawLines.length - 1] += text;
    } else {
      rawLines.push(text);
    }
  }
  return cleanTerminalLines(rawLines).join('\n');
}
