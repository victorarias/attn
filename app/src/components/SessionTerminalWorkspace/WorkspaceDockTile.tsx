import { useCallback, useEffect, useRef, useState } from 'react';
import type {
  FocusEvent as ReactFocusEvent,
  FormEvent,
  PointerEvent as ReactPointerEvent,
  Ref,
  RefObject,
} from 'react';
import { browserHostLabel, claimBrowserHostFocus, controlBrowserHost } from '../../browser/host';
import type { TileContentState, TileLeaf } from '../../types/workspace';
import { deriveTileTitle } from '../../utils/tilePresentation';
import { BrowserTileBody } from './BrowserTileBody';
import { MarkdownReader } from '../MarkdownReader';
import type { MarkdownAnnotationsSendHandle } from '../MarkdownReader';
import { getMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import { useShortcut } from '../../shortcuts';
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

/** One selectable Send target in the markdown tile header's session picker. */
export interface WorkspaceTileSessionOption {
  sessionId: string;
  label: string;
  state?: string;
}

interface WorkspaceDockTileProps {
  tile: TileLeaf;
  workspaceId: string;
  content?: TileContentState;
  allowLocalTargets?: boolean;
  dragging: boolean;
  visible?: boolean;
  // The workspace's agent sessions — the markdown tile's retarget options.
  workspaceSessions?: WorkspaceTileSessionOption[];
  onClose: () => void;
  onUpdateParams?: (tileParams: string) => Promise<unknown> | void;
  // Rebind the tile's session binding (markdown Send target). Persisted by the
  // daemon via workspace_layout_update_tile's tile_session_id; the layout
  // broadcast echoes the new binding back into `tile.tileSessionId`.
  onRetargetTile?: (sessionId: string) => Promise<unknown> | void;
  onHeaderPointerDown: (event: ReactPointerEvent<HTMLDivElement>) => void;
  onRequestContent: (workspaceId: string, tileId: string) => void;
  // Handle to the scrollable body. A tile-only workspace has no terminal to
  // focus on select, so the workspace focuses this element to enable keyboard
  // scrolling. Left undefined for tiles that never receive select-time focus.
  bodyRef?: Ref<HTMLDivElement>;
}

/** Header send state for a markdown tile. `sent` auto-clears after ~4s;
    `skipped`/`warning`/`error` persist until the next action. `warning`
    covers delivered-but-draft-clear-failed: the payload reached the session
    but the daemon still holds the draft, so local state is kept to match. */
type SendStatus =
  | { kind: 'idle' }
  | { kind: 'sending' }
  | { kind: 'sent' }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

const SEND_SENT_CLEAR_MS = 4000;
const SKIPPED_APPROVAL_MESSAGE = 'Target is waiting for approval — not sent';
const NOT_HYDRATED_MESSAGE = 'Annotations are still syncing — try again in a moment';

export function WorkspaceDockTile({
  tile,
  workspaceId,
  content,
  allowLocalTargets = true,
  dragging,
  visible = true,
  workspaceSessions = [],
  onClose,
  onUpdateParams,
  onRetargetTile,
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

  // ---- markdown annotation send flow (PR6) ---------------------------------
  const isMarkdown = tile.tileKind === 'markdown';
  const annotationsSendRef = useRef<MarkdownAnnotationsSendHandle | null>(null);
  const [annotationCount, setAnnotationCount] = useState(0);
  const [sendStatus, setSendStatus] = useState<SendStatus>({ kind: 'idle' });
  // Focus-within on the tile root gates the ⌘Enter shortcut's REGISTRATION
  // (see useShortcut below): when focus sits in a terminal pane the shortcut
  // must not exist at all, so the key falls through to the PTY untouched.
  const [hasFocusWithin, setHasFocusWithin] = useState(false);
  const sendingRef = useRef(false);
  const sentClearTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (sentClearTimerRef.current) {
      clearTimeout(sentClearTimerRef.current);
    }
  }, []);

  // The Send target: the tile's persisted session binding, but only while that
  // session is still in the workspace — otherwise the picker shows a disabled
  // "No session" placeholder and Send is disabled.
  //
  // Optimistic retarget: `tile.tileSessionId` only updates when the daemon's
  // layout broadcast echoes the rebind back, which can lag well behind the
  // click under WS load. The user's pick is held locally in the meantime and
  // is authoritative for BOTH the picker value and the Send target — "Send
  // always goes to the currently selected target". Cleared when the echo
  // catches up or the retarget request fails.
  const boundSessionId = tile.tileSessionId ?? '';
  const [pendingTargetSessionId, setPendingTargetSessionId] = useState<string | null>(null);
  useEffect(() => {
    if (pendingTargetSessionId !== null && boundSessionId === pendingTargetSessionId) {
      setPendingTargetSessionId(null); // broadcast echo caught up
    }
  }, [boundSessionId, pendingTargetSessionId]);
  const pendingInWorkspace = pendingTargetSessionId !== null
    && workspaceSessions.some((s) => s.sessionId === pendingTargetSessionId);
  const boundInWorkspace = workspaceSessions.some((s) => s.sessionId === boundSessionId);
  const targetSessionId = pendingInWorkspace
    ? (pendingTargetSessionId as string)
    : boundInWorkspace
      ? boundSessionId
      : '';
  const transportAvailable = getMarkdownAnnotationsTransport() !== null;

  const setSendResult = useCallback((status: SendStatus) => {
    if (sentClearTimerRef.current) {
      clearTimeout(sentClearTimerRef.current);
      sentClearTimerRef.current = null;
    }
    setSendStatus(status);
    if (status.kind === 'sent') {
      sentClearTimerRef.current = setTimeout(() => {
        sentClearTimerRef.current = null;
        setSendStatus((prev) => (prev.kind === 'sent' ? { kind: 'idle' } : prev));
      }, SEND_SENT_CLEAR_MS);
    }
  }, []);

  const sendNow = useCallback(() => {
    const handle = annotationsSendRef.current;
    const transport = getMarkdownAnnotationsTransport();
    if (sendingRef.current || annotationCount === 0 || !targetSessionId || !handle || !transport || !path) {
      return;
    }
    if (!handle.isHydrated()) {
      // The daemon draft has not been loaded (hydrate in flight or failed):
      // local edits are unsaved, so the daemon would format a STALE stored
      // draft — not what the sidebar shows. Refuse rather than mis-deliver.
      setSendResult({ kind: 'error', message: NOT_HYDRATED_MESSAGE });
      return;
    }
    sendingRef.current = true;
    setSendResult({ kind: 'sending' });
    void (async () => {
      try {
        // Flush the 500ms save debounce first so the daemon formats a draft
        // that includes the last keystroke's edit.
        await handle.flushPendingSave();
        const result = await transport.submitMarkdownAnnotations(
          path,
          targetSessionId,
          handle.getOrphanedIds(),
        );
        if (result.status === 'delivered' && result.error) {
          // Delivered, but the daemon FAILED to clear its draft afterwards
          // (spec B.7): keep local state — it still matches the surviving
          // daemon draft — and surface the qualified outcome instead of a
          // clean "Sent ✓" over a silently emptied list.
          setSendResult({ kind: 'warning', message: result.error });
        } else if (result.status === 'delivered') {
          // Daemon already tombstone-cleared; mirror locally without a second
          // clear and seed the generation counter from the new floor.
          handle.applyDeliveredClear(result.generation ?? 0);
          setSendResult({ kind: 'sent' });
        } else if (result.status === 'skipped_pending_approval') {
          setSendResult({ kind: 'skipped' }); // annotations kept for retry
        } else {
          setSendResult({ kind: 'error', message: result.error || 'Send failed' });
        }
      } catch (error) {
        setSendResult({
          kind: 'error',
          message: error instanceof Error ? error.message : 'Send failed',
        });
      } finally {
        sendingRef.current = false;
      }
    })();
  }, [annotationCount, path, setSendResult, targetSessionId]);

  // ⌘Enter — registration-gated (not handler-gated): the dispatcher consumes
  // the event whenever a matching handler is registered, so an always-on
  // no-op handler would eat the terminal's ⌘Enter. The def's
  // `editableTarget: 'native'` additionally keeps it out of textareas (the
  // annotation popover's own ⌘Enter submits the comment there).
  useShortcut(
    'markdown.sendAnnotations',
    sendNow,
    isMarkdown
      && visible
      && hasFocusWithin
      && annotationCount > 0
      && !!targetSessionId
      && transportAvailable,
  );

  const handleTileFocus = useCallback(() => {
    setHasFocusWithin(true);
  }, []);
  const handleTileBlur = useCallback((event: ReactFocusEvent<HTMLDivElement>) => {
    // focusout semantics: only clear when focus truly left the tile subtree.
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setHasFocusWithin(false);
    }
  }, []);

  const sending = sendStatus.kind === 'sending';
  const sendDisabled = sending || annotationCount === 0 || !targetSessionId || !transportAvailable;

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
      onFocus={isMarkdown ? handleTileFocus : undefined}
      onBlur={isMarkdown ? handleTileBlur : undefined}
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
        {isMarkdown ? (
          <div
            className="workspace-dock-tile-send"
            // The header is the drag handle; interacting with the send
            // controls must not start a re-dock drag.
            onPointerDown={(event) => event.stopPropagation()}
          >
            {sendStatus.kind === 'sending' ? (
              <span className="workspace-dock-tile-send-status" role="status">Sending…</span>
            ) : sendStatus.kind === 'sent' ? (
              <span className="workspace-dock-tile-send-status workspace-dock-tile-send-status--ok" role="status">
                Sent ✓
              </span>
            ) : sendStatus.kind === 'skipped' ? (
              <span className="workspace-dock-tile-send-status workspace-dock-tile-send-status--warn" role="status">
                {SKIPPED_APPROVAL_MESSAGE}
              </span>
            ) : sendStatus.kind === 'warning' ? (
              <span
                className="workspace-dock-tile-send-status workspace-dock-tile-send-status--warn"
                role="status"
                title={sendStatus.message}
              >
                {sendStatus.message}
              </span>
            ) : sendStatus.kind === 'error' ? (
              <span
                className="workspace-dock-tile-send-status workspace-dock-tile-send-status--error"
                role="status"
                title={sendStatus.message}
              >
                {sendStatus.message}
              </span>
            ) : null}
            <select
              className="workspace-dock-tile-session-picker"
              aria-label="Send annotations to session"
              value={targetSessionId}
              onChange={(event) => {
                const sessionId = event.target.value;
                if (!sessionId || sessionId === targetSessionId) {
                  return;
                }
                // Optimistic: the pick takes effect immediately (picker value
                // AND Send target); the daemon echo confirms it later. On a
                // failed retarget request, roll back to the persisted binding.
                setPendingTargetSessionId(sessionId);
                setSendResult({ kind: 'idle' });
                void Promise.resolve(onRetargetTile?.(sessionId)).catch((error) => {
                  console.warn('[WorkspaceDockTile] Failed to retarget tile session:', error);
                  setPendingTargetSessionId((prev) => (prev === sessionId ? null : prev));
                });
              }}
            >
              {!targetSessionId && (
                <option value="" disabled>
                  No session
                </option>
              )}
              {workspaceSessions.map((session) => (
                <option key={session.sessionId} value={session.sessionId}>
                  {session.label}
                  {session.state === 'pending_approval' ? ' ⏸ approval' : ''}
                </option>
              ))}
            </select>
            <button
              type="button"
              className="workspace-dock-tile-send-button"
              disabled={sendDisabled}
              title="Send annotations to the selected session (⌘Enter)"
              onClick={sendNow}
            >
              {sending ? 'Sending…' : `Send ${annotationCount}`}
            </button>
          </div>
        ) : null}
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
          <MarkdownBody
            content={content}
            allowLocalTargets={allowLocalTargets}
            onAnnotationsCountChange={setAnnotationCount}
            annotationsSendRef={annotationsSendRef}
          />
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

function MarkdownBody({
  content,
  allowLocalTargets,
  onAnnotationsCountChange,
  annotationsSendRef,
}: {
  content?: TileContentState;
  allowLocalTargets: boolean;
  onAnnotationsCountChange: (count: number) => void;
  annotationsSendRef: RefObject<MarkdownAnnotationsSendHandle | null>;
}) {
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
      onAnnotationsCountChange={onAnnotationsCountChange}
      annotationsSendRef={annotationsSendRef}
    />
  );
}
