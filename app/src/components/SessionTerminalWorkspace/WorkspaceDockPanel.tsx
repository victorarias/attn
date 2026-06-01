import { useEffect } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';
import ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { openPath, openUrl } from '@tauri-apps/plugin-opener';
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
  const action = target.kind === 'local' ? openPath(target.value) : openUrl(target.value);
  void action.catch((error) => {
    console.warn('[WorkspaceDockPanel] Failed to open Markdown target:', error);
  });
}

function markdownComponents(documentPath: string): Components {
  return {
    a({ href, children }) {
      const target = href ? resolveMarkdownTarget(documentPath, href) : null;
      if (!target) {
        return <span>{children}</span>;
      }
      if (target.kind === 'fragment') {
        return <a href={target.value}>{children}</a>;
      }
      return (
        <a
          href={href}
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
      if (!target || target.kind !== 'local') {
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
  dragging: boolean;
  onClose: () => void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onRequestContent: (workspaceId: string, panelId: string) => void;
}

export function WorkspaceDockPanel({
  panel,
  workspaceId,
  content,
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
          <MarkdownBody content={content} />
        ) : (
          <div className="workspace-dock-panel-message">Unsupported panel: {panel.panelKind}</div>
        )}
      </div>
    </div>
  );
}

function MarkdownBody({ content }: { content?: PanelContentState }) {
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
    <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents(content.path)}>
      {content.content}
    </ReactMarkdown>
  );
}
