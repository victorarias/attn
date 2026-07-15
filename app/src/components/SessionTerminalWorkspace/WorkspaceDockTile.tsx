import { useEffect, useRef, useState } from 'react';
import type {
  FormEvent,
  PointerEvent as ReactPointerEvent,
  Ref,
} from 'react';
import { browserHostLabel, claimBrowserHostFocus, controlBrowserHost } from '../../browser/host';
import type { TileContentState, TileLeaf } from '../../types/workspace';
import { deriveTileTitle } from '../../utils/tilePresentation';
import { BrowserTileBody } from './BrowserTileBody';
import { MarkdownReader } from '../MarkdownReader';
import { NotebookTile } from '../notebook/NotebookTile';
import './WorkspaceDockTile.css';

// Link/image target resolution moved to the reader; re-exported here for
// existing consumers.
export { resolveMarkdownTarget } from '../MarkdownReader/markdownLinks';

// Browser and notebook tiles manage their own scroll + chrome, so their body
// drops the padding/overflow and fills the frame. The markdown reader draws
// its own centered card, so its body only keeps the scroll (no padding).
function bodyKindModifier(tileKind: string): string {
  if (tileKind === 'browser') return 'workspace-dock-tile-body--browser';
  if (tileKind === 'notebook') return 'workspace-dock-tile-body--notebook';
  if (tileKind === 'markdown') return 'workspace-dock-tile-body--markdown';
  return '';
}

export function normalizeBrowserAddress(value: string): string {
  const trimmed = value.trim();
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)) return trimmed;
  const localHost = /^(?:localhost|127(?:\.\d{1,3}){3}|\[::1\])(?::\d+)?(?:[/?#]|$)/i.test(trimmed);
  return `${localHost ? 'http' : 'https'}://${trimmed}`;
}

interface WorkspaceDockTileProps {
  tile: TileLeaf;
  workspaceId: string;
  content?: TileContentState;
  allowLocalTargets?: boolean;
  dragging: boolean;
  visible?: boolean;
  onClose: () => void;
  onUpdateParams?: (tileParams: string) => Promise<unknown> | void;
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
  visible = true,
  onClose,
  onUpdateParams,
  onHeaderPointerDown,
  onRequestContent,
  bodyRef,
}: WorkspaceDockTileProps) {
  // Pull the current content on mount and whenever the tile retargets a new
  // file. Live-reload updates then arrive as broadcasts (no re-request needed).
  useEffect(() => {
    if (tile.tileKind === 'markdown') {
      onRequestContent(workspaceId, tile.tileId);
    }
  }, [workspaceId, tile.tileId, tile.tileKind, tile.tileParams, onRequestContent]);

  const path = content?.path || tile.tileParams || '';
  const title = deriveTileTitle(tile, content);
  const browserLabel = browserHostLabel(workspaceId, tile.tileId);
  const [browserAddress, setBrowserAddress] = useState(tile.tileParams || '');
  const pendingBrowserParamsRef = useRef<string | null>(null);

  useEffect(() => {
    setBrowserAddress(tile.tileParams || '');
    if (pendingBrowserParamsRef.current === tile.tileParams) {
      pendingBrowserParamsRef.current = null;
    }
  }, [tile.tileParams]);

  useEffect(() => {
    if (tile.tileKind !== 'browser') {
      return;
    }
    const handleLocation = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail;
      if (
        typeof detail === 'object'
        && detail !== null
        && 'label' in detail
        && 'url' in detail
        && detail.label === browserLabel
        && typeof detail.url === 'string'
      ) {
        setBrowserAddress(detail.url);
        if (
          detail.url !== tile.tileParams
          && detail.url !== pendingBrowserParamsRef.current
        ) {
          pendingBrowserParamsRef.current = detail.url;
          void Promise.resolve(onUpdateParams?.(detail.url)).catch((error) => {
            if (pendingBrowserParamsRef.current === detail.url) {
              pendingBrowserParamsRef.current = null;
            }
            console.warn('[WorkspaceDockTile] Failed to persist browser location:', error);
          });
        }
      }
    };
    window.addEventListener('attn:browser-location', handleLocation);
    return () => {
      window.removeEventListener('attn:browser-location', handleLocation);
    };
  }, [browserLabel, onUpdateParams, tile.tileKind, tile.tileParams]);

  const reloadBrowser = () => {
    void controlBrowserHost(workspaceId, tile.tileId, 'reload').catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to reload browser:', error);
    });
  };
  const navigateBrowser = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const trimmed = browserAddress.trim();
    if (!trimmed) {
      return;
    }
    const target = normalizeBrowserAddress(trimmed);
    setBrowserAddress(target);
    void controlBrowserHost(
      workspaceId,
      tile.tileId,
      'navigate',
      JSON.stringify({ url: target }),
    ).catch((error) => {
      console.warn('[WorkspaceDockTile] Failed to navigate browser:', error);
    });
  };

  return (
    <div
      className={`workspace-dock-tile ${dragging ? 'workspace-dock-tile--dragging' : ''}`.trim()}
      data-browser-host-owner={tile.tileKind === 'browser' ? true : undefined}
      onPointerDownCapture={tile.tileKind === 'browser' ? () => claimBrowserHostFocus(browserLabel) : undefined}
    >
      <div
        className="workspace-dock-tile-header"
        onPointerDown={onHeaderPointerDown}
        title={path || 'Drag to re-dock'}
      >
        {tile.tileKind === 'browser' ? (
          <form
            className="workspace-dock-tile-address-form"
            onSubmit={navigateBrowser}
            onPointerDown={(event) => event.stopPropagation()}
          >
            <input
              className="workspace-dock-tile-address"
              type="text"
              value={browserAddress}
              aria-label="Browser address"
              spellCheck={false}
              onChange={(event) => setBrowserAddress(event.target.value)}
              onFocus={(event) => event.currentTarget.select()}
            />
          </form>
        ) : (
          <span className="workspace-dock-tile-title">{title}</span>
        )}
        <div className="workspace-dock-tile-actions">
          {tile.tileKind === 'browser' ? (
            <button
              type="button"
              className="workspace-dock-tile-action"
              title="Reload browser"
              aria-label="Reload browser"
              onPointerDown={(event) => event.stopPropagation()}
              onClick={reloadBrowser}
            >
              ↻
            </button>
          ) : null}
          <button
            type="button"
            className="workspace-dock-tile-action"
            title="Close tile"
            aria-label="Close tile"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </div>
      <div
        className={`workspace-dock-tile-body ${bodyKindModifier(tile.tileKind)}`.trim()}
        ref={bodyRef}
        tabIndex={-1}
      >
        {tile.tileKind === 'markdown' ? (
          <MarkdownBody content={content} allowLocalTargets={allowLocalTargets} />
        ) : tile.tileKind === 'browser' ? (
          <BrowserTileBody
            workspaceId={workspaceId}
            tileId={tile.tileId}
            url={tile.tileParams || ''}
            dragging={dragging}
            visible={visible}
            onClose={onClose}
          />
        ) : tile.tileKind === 'notebook' ? (
          // The notebook tile self-serves its content over the fs surface (via
          // context); the only tile→params write is the opened file's path.
          <NotebookTile
            initialPath={tile.tileParams || null}
            onOpenFile={(openedPath) => {
              void Promise.resolve(onUpdateParams?.(openedPath)).catch((error) => {
                console.warn('[WorkspaceDockTile] Failed to persist notebook path:', error);
              });
            }}
          />
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
    <MarkdownReader
      content={content.content}
      path={content.path}
      allowLocalTargets={allowLocalTargets}
      annotationsEnabled
    />
  );
}
