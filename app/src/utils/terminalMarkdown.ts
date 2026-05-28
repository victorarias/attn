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
