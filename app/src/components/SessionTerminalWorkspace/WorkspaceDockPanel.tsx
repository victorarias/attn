import { useEffect } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { PanelContentState, PanelLeaf } from '../../types/workspace';
import './WorkspaceDockPanel.css';

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const segment = trimmed.split('/').pop();
  return segment && segment.length > 0 ? segment : trimmed;
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
  return <ReactMarkdown remarkPlugins={[remarkGfm]}>{content.content}</ReactMarkdown>;
}
