import { createElement, isValidElement, useEffect } from 'react';
import type { PointerEvent as ReactPointerEvent, ReactNode, Ref } from 'react';
import ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { invoke } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import type { TileContentState, TileLeaf } from '../../types/workspace';
import './WorkspaceDockTile.css';

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const segment = trimmed.split('/').pop();
  return segment && segment.length > 0 ? segment : trimmed;
}

const MAX_TILE_TITLE_LENGTH = 80;

// Reduce a single line of Markdown to readable plain text for a tile header:
// drop link/image syntax (keep the label) and emphasis/code markers, collapse
// whitespace. Intentionally shallow — this titles a header, it doesn't render.
function stripInlineMarkdown(text: string): string {
  return text
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`~]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

// Title a markdown tile from its content: the first meaningful line, with a
// leading ATX heading marker stripped. For the common `# Title` first line this
// yields the H1 text; otherwise it yields the beginning of the text. Returns
// null when the content carries no usable line (caller falls back to basename).
function markdownTitle(markdown: string): string | null {
  const lines = markdown.split('\n');
  let i = 0;
  // Skip a leading YAML frontmatter block, but only when it is actually closed
  // (a bare leading `---` is otherwise a horizontal rule we should keep).
  if (lines[0]?.trim() === '---') {
    let close = 1;
    while (close < lines.length && lines[close].trim() !== '---') close += 1;
    if (close < lines.length) i = close + 1;
  }
  for (; i < lines.length; i += 1) {
    const raw = lines[i].trim();
    if (!raw) continue;
    // Skip a thematic break (`---`, `***`, `___`) — a lone leading rule, or an
    // unclosed frontmatter fence, should not become the title.
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

// Display title for a tile header. Prefers a title derived from loaded markdown
// content, falling back to the file basename and finally the tile kind.
export function deriveTileTitle(tile: TileLeaf, content?: TileContentState): string {
  if (tile.tileKind === 'markdown' && content && !content.error) {
    const fromContent = markdownTitle(content.content);
    if (fromContent) return fromContent;
  }
  const path = content?.path || tile.tileParams || '';
  return path ? basename(path) : tile.tileKind;
}

type MarkdownTarget =
  | { kind: 'external'; value: string }
  | { kind: 'fragment'; value: string }
  | { kind: 'local'; value: string };

const safeLocalDocumentExtensions = new Set([
  '.md',
  '.markdown',
  '.pdf',
  '.rst',
  '.text',
  '.txt',
]);

const safeLocalImageExtensions = new Set([
  '.bmp',
  '.gif',
  '.jpeg',
  '.jpg',
  '.png',
  '.tif',
  '.tiff',
  '.webp',
]);

function localTargetExtension(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const slash = trimmed.lastIndexOf('/');
  const dot = trimmed.lastIndexOf('.');
  return dot > slash ? trimmed.slice(dot).toLowerCase() : '';
}

function isSafeLocalMarkdownTarget(path: string): boolean {
  const extension = localTargetExtension(path);
  return safeLocalDocumentExtensions.has(extension) || safeLocalImageExtensions.has(extension);
}

function isSafeLocalMarkdownImageTarget(path: string): boolean {
  return safeLocalImageExtensions.has(localTargetExtension(path));
}

function decodedPath(url: URL): string {
  try {
    return decodeURIComponent(url.pathname);
  } catch {
    return url.pathname;
  }
}

export function resolveMarkdownTarget(documentPath: string, target: string): MarkdownTarget | null {
  const trimmed = target.trim();
  if (!trimmed) {
    return null;
  }
  if (trimmed.startsWith('#')) {
    return { kind: 'fragment', value: trimmed };
  }
  if (trimmed.startsWith('//')) {
    return null;
  }

  const scheme = trimmed.match(/^([a-z][a-z0-9+.-]*):/i)?.[1]?.toLowerCase();
  if (scheme) {
    if (scheme === 'http' || scheme === 'https' || scheme === 'mailto') {
      return { kind: 'external', value: trimmed };
    }
    if (scheme !== 'file') {
      return null;
    }
    try {
      const url = new URL(trimmed);
      return url.hostname ? null : { kind: 'local', value: decodedPath(url) };
    } catch {
      return null;
    }
  }

  if (!documentPath) {
    return null;
  }
  try {
    const base = new URL('file:///');
    base.pathname = documentPath;
    const url = new URL(trimmed, base);
    return url.hostname ? null : { kind: 'local', value: decodedPath(url) };
  } catch {
    return null;
  }
}

function openMarkdownTarget(target: MarkdownTarget): void {
  if (target.kind === 'fragment') {
    return;
  }
  if (target.kind === 'local' && !isSafeLocalMarkdownTarget(target.value)) {
    console.warn('[WorkspaceDockTile] Blocked unsafe local Markdown target:', target.value);
    return;
  }
  const action = target.kind === 'local'
    ? invoke('open_safe_markdown_target', { path: target.value })
    : openUrl(target.value);
  void action.catch((error) => {
    console.warn('[WorkspaceDockTile] Failed to open Markdown target:', error);
  });
}

function markdownText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(markdownText).join('');
  }
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return markdownText(node.props.children);
  }
  return '';
}

function markdownSlug(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-') || 'section';
}

function markdownComponents(documentPath: string, allowLocalTargets: boolean): Components {
  const slugCounts = new Map<string, number>();
  const heading = (level: number) => ({ children }: { children?: ReactNode }) => {
    const base = markdownSlug(markdownText(children));
    const count = slugCounts.get(base) ?? 0;
    slugCounts.set(base, count + 1);
    const id = count === 0 ? base : `${base}-${count}`;
    return createElement(`h${level}`, { id }, children);
  };

  return {
    h1: heading(1),
    h2: heading(2),
    h3: heading(3),
    h4: heading(4),
    h5: heading(5),
    h6: heading(6),
    a({ href, children }) {
      const target = href ? resolveMarkdownTarget(documentPath, href) : null;
      if (!target) {
        return <span>{children}</span>;
      }
      if (target.kind === 'local' && (!allowLocalTargets || !isSafeLocalMarkdownTarget(target.value))) {
        return <span title={`Blocked local target: ${target.value}`}>{children}</span>;
      }
      if (target.kind === 'fragment') {
        return <a href={target.value}>{children}</a>;
      }
      return (
        <a
          href={href}
          title={target.kind === 'local' ? target.value : undefined}
          onClick={(event) => {
            event.preventDefault();
            openMarkdownTarget(target);
          }}
        >
          {children}
        </a>
      );
    },
    img({ src, alt }) {
      const target = src ? resolveMarkdownTarget(documentPath, src) : null;
      if (!target || target.kind !== 'local' || !allowLocalTargets || !isSafeLocalMarkdownImageTarget(target.value)) {
        return (
          <span className="workspace-dock-tile-blocked-image" title={src}>
            [blocked image: {alt || src || 'unknown source'}]
          </span>
        );
      }
      return (
        <button
          type="button"
          className="workspace-dock-tile-local-image"
          title={target.value}
          onClick={() => openMarkdownTarget(target)}
        >
          Open image: {alt || basename(target.value)}
        </button>
      );
    },
  };
}

interface WorkspaceDockTileProps {
  tile: TileLeaf;
  workspaceId: string;
  content?: TileContentState;
  allowLocalTargets?: boolean;
  dragging: boolean;
  onClose: () => void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onRequestContent: (workspaceId: string, tileId: string) => void;
  // Handle to the scrollable body. A tile-only workspace has no terminal to
  // focus on select, so the workspace focuses this element to enable keyboard
  // scrolling. Left undefined for tiles that never receive select-time focus.
  bodyRef?: Ref<HTMLDivElement>;
}

export function WorkspaceDockTile({
  tile,
  workspaceId,
  content,
  allowLocalTargets = true,
  dragging,
  onClose,
  onHeaderPointerDown,
  onRequestContent,
  bodyRef,
}: WorkspaceDockTileProps) {
  // Pull the current content on mount and whenever the tile retargets a new
  // file. Live-reload updates then arrive as broadcasts (no re-request needed).
  useEffect(() => {
    onRequestContent(workspaceId, tile.tileId);
  }, [workspaceId, tile.tileId, tile.tileParams, onRequestContent]);

  const path = content?.path || tile.tileParams || '';
  const title = deriveTileTitle(tile, content);

  return (
    <div className={`workspace-dock-tile ${dragging ? 'workspace-dock-tile--dragging' : ''}`.trim()}>
      <div
        className="workspace-dock-tile-header"
        onPointerDown={onHeaderPointerDown}
        title={path || 'Drag to re-dock'}
      >
        <span className="workspace-dock-tile-title">{title}</span>
        <button
          type="button"
          className="workspace-dock-tile-close"
          title="Close tile"
          aria-label="Close tile"
          onPointerDown={(event) => event.stopPropagation()}
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className="workspace-dock-tile-body" ref={bodyRef} tabIndex={-1}>
        {tile.tileKind === 'markdown' ? (
          <MarkdownBody content={content} allowLocalTargets={allowLocalTargets} />
        ) : (
          <div className="workspace-dock-tile-message">Unsupported tile: {tile.tileKind}</div>
        )}
      </div>
    </div>
  );
}

function MarkdownBody({ content, allowLocalTargets }: { content?: TileContentState; allowLocalTargets: boolean }) {
  if (content === undefined) {
    return <div className="workspace-dock-tile-message">Loading…</div>;
  }
  if (content.error) {
    return <div className="workspace-dock-tile-message workspace-dock-tile-error">{content.error}</div>;
  }
  if (content.content.trim().length === 0) {
    return <div className="workspace-dock-tile-message">This file is empty.</div>;
  }
  return (
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents(content.path, allowLocalTargets)}>
      {content.content}
    </ReactMarkdown>
  );
}
