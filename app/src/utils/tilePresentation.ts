import type { TileContentState, TileLeaf } from '../types/workspace';

export function tilePathBasename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const segment = trimmed.split('/').pop();
  return segment && segment.length > 0 ? segment : trimmed;
}

const MAX_TILE_TITLE_LENGTH = 80;

function stripInlineMarkdown(text: string): string {
  return text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`~]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

function markdownTitle(markdown: string): string | null {
  const lines = markdown.split('\n');
  let i = 0;
  if (lines[0]?.trim() === '---') {
    let close = 1;
    while (close < lines.length && lines[close].trim() !== '---') close += 1;
    if (close < lines.length) i = close + 1;
  }
  for (; i < lines.length; i += 1) {
    const raw = lines[i].trim();
    if (!raw) continue;
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(raw.replace(/\s/g, ''))) continue;
    const withoutHeading = raw.replace(/^#{1,6}\s+/, '').replace(/\s+#*$/, '');
    const cleaned = stripInlineMarkdown(withoutHeading);
    if (!cleaned) continue;
    return cleaned.length > MAX_TILE_TITLE_LENGTH
      ? `${cleaned.slice(0, MAX_TILE_TITLE_LENGTH - 1).trimEnd()}…`
      : cleaned;
  }
  return null;
}

export function deriveTileTitle(tile: TileLeaf, content?: TileContentState): string {
  if (tile.tileKind === 'browser' && tile.tileParams) {
    try {
      return new URL(tile.tileParams).host || tile.tileParams;
    } catch {
      return tile.tileParams;
    }
  }
  if (tile.tileKind === 'markdown' && content && !content.error) {
    const fromContent = markdownTitle(content.content);
    if (fromContent) return fromContent;
  }
  const path = content?.path || tile.tileParams || '';
  return path ? tilePathBasename(path) : tile.tileKind;
}
