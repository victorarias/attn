import { createElement, isValidElement, useEffect } from 'react';
import type { PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { invoke } from '@tauri-apps/api/core';
import { openUrl } from '@tauri-apps/plugin-opener';
import type { PanelContentState, PanelLeaf } from '../../types/workspace';
import './WorkspaceDockPanel.css';

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const segment = trimmed.split('/').pop();
  return segment && segment.length > 0 ? segment : trimmed;
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
    console.warn('[WorkspaceDockPanel] Blocked unsafe local Markdown target:', target.value);
    return;
  }
  const action = target.kind === 'local'
    ? invoke('open_safe_markdown_target', { path: target.value })
    : openUrl(target.value);
  void action.catch((error) => {
    console.warn('[WorkspaceDockPanel] Failed to open Markdown target:', error);
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
          <span className="workspace-dock-panel-blocked-image" title={src}>
            [blocked image: {alt || src || 'unknown source'}]
          </span>
        );
      }
      return (
        <button
          type="button"
          className="workspace-dock-panel-local-image"
          title={target.value}
          onClick={() => openMarkdownTarget(target)}
        >
          Open image: {alt || basename(target.value)}
        </button>
      );
    },
  };
}

interface WorkspaceDockPanelProps {
  panel: PanelLeaf;
  workspaceId: string;
  content?: PanelContentState;
  allowLocalTargets?: boolean;
  dragging: boolean;
  onClose: () => void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onRequestContent: (workspaceId: string, panelId: string) => void;
}

export function WorkspaceDockPanel({
  panel,
  workspaceId,
  content,
  allowLocalTargets = true,
  dragging,
  onClose,
  onHeaderPointerDown,
  onRequestContent,
}: WorkspaceDockPanelProps) {
  // Pull the current content on mount and whenever the panel retargets a new
  // file. Live-reload updates then arrive as broadcasts (no re-request needed).
  useEffect(() => {
    onRequestContent(workspaceId, panel.panelId);
  }, [workspaceId, panel.panelId, panel.panelParams, onRequestContent]);

  const path = content?.path || panel.panelParams || '';
  const title = path ? basename(path) : panel.panelKind;

  return (
    <div className={`workspace-dock-panel ${dragging ? 'workspace-dock-panel--dragging' : ''}`.trim()}>
      <div
        className="workspace-dock-panel-header"
        onPointerDown={onHeaderPointerDown}
        title={path || 'Drag to re-dock'}
      >
        <span className="workspace-dock-panel-title">{title}</span>
        <button
          type="button"
          className="workspace-dock-panel-close"
          title="Close panel"
          aria-label="Close panel"
          onPointerDown={(event) => event.stopPropagation()}
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <div className="workspace-dock-panel-body">
        {panel.panelKind === 'markdown' ? (
          <MarkdownBody content={content} allowLocalTargets={allowLocalTargets} />
        ) : (
          <div className="workspace-dock-panel-message">Unsupported panel: {panel.panelKind}</div>
        )}
      </div>
    </div>
  );
}

function MarkdownBody({ content, allowLocalTargets }: { content?: PanelContentState; allowLocalTargets: boolean }) {
  if (content === undefined) {
    return <div className="workspace-dock-panel-message">Loading…</div>;
  }
  if (content.error) {
    return <div className="workspace-dock-panel-message workspace-dock-panel-error">{content.error}</div>;
  }
  if (content.content.trim().length === 0) {
    return <div className="workspace-dock-panel-message">This file is empty.</div>;
  }
  return (
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents(content.path, allowLocalTargets)}>
      {content.content}
    </ReactMarkdown>
  );
}
