import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type {
  ChangeEvent,
  FocusEvent as ReactFocusEvent,
  FormEvent,
  PointerEvent as ReactPointerEvent,
  Ref,
  RefObject,
} from 'react';
import { open } from '@tauri-apps/plugin-dialog';
import { browserHostLabel, claimBrowserHostFocus, controlBrowserHost } from '../../browser/host';
import { parseNotebookTileParams, serializeNotebookTileParams, type TileContentState, type TileLeaf } from '../../types/workspace';
import { deriveTileTitle, tilePathBasename } from '../../utils/tilePresentation';
import { BrowserTileBody } from './BrowserTileBody';
import { MarkdownReader } from '../MarkdownReader';
import type { MarkdownAnnotationsSendHandle } from '../MarkdownReader';
import {
  fileMarkdownSource,
  seedMarkdownSource,
  type MarkdownDocumentSource,
} from '../MarkdownReader/documentSource';
import { getMarkdownAnnotationsTransport } from '../MarkdownReader/annotations/transport';
import type { MarkdownAnnotationsDestination } from '../MarkdownReader/annotations/transport';
import { useAnnotationSend } from '../../annotations/useAnnotationSend';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useNotebookSurfaceContext } from '../../contexts/NotebookSurfaceContext';
import { NotebookTile } from '../notebook/NotebookTile';
import type { NotebookSurfaceHandle } from '../NotebookSurface';
import { SeedDocumentView, type SeedDocument } from '../SeedDocumentView';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import type { Seed } from '../../hooks/useDaemonSocket';
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
  if (tileKind === 'seed') return 'workspace-dock-tile-body--markdown';
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
  gardenSeeds?: Seed[];
  // The owning workspace's directory (Workspace.directory), for the notebook
  // tile's root switcher's "Workspace — <dir>" option. Absent for tile-only
  // workspaces with no directory.
  workspaceDirectory?: string;
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

/** Header send state for an annotated document tile. `sent` auto-clears after ~4s;
    `skipped`/`warning`/`error` persist until the next action. `warning`
    covers accepted-but-draft-clear-failed: the payload reached its destination
    but the daemon still holds the draft, so local state is kept to match. */
type MarkdownSendResult =
  | { kind: 'sent'; destination: 'session' | 'seed' }
  | { kind: 'skipped' }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string };

const SEND_SENT_CLEAR_MS = 4000;
const SKIPPED_APPROVAL_MESSAGE = 'Target is waiting for approval — not sent';
const NOT_HYDRATED_MESSAGE = 'Annotations are still syncing — try again in a moment';
const NO_GARDEN_SEEDS: Seed[] = [];

export function WorkspaceDockTile({
  tile,
  workspaceId,
  content,
  allowLocalTargets = true,
  dragging,
  visible = true,
  workspaceSessions = [],
  gardenSeeds = NO_GARDEN_SEEDS,
  workspaceDirectory,
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

  // Notebook tileParams may be the legacy bare-path string or the {root, path}
  // JSON envelope a root-bound tile persists — parse either way so the header
  // tooltip and annotation transport see the plain open path, not raw JSON.
  const path = content?.path
    || (tile.tileKind === 'notebook' ? parseNotebookTileParams(tile.tileParams).path : tile.tileParams)
    || '';
  const baseTitle = deriveTileTitle(tile, content);
  const browserLabel = browserHostLabel(workspaceId, tile.tileId);
  const [browserAddress, setBrowserAddress] = useState(tile.tileParams || '');
  const pendingBrowserParamsRef = useRef<string | null>(null);
  // Handle to the docked NotebookTile's NotebookSurface, so the root switcher
  // (below) can flush a dirty buffer to the OLD root before swapping params —
  // otherwise the 700ms autosave debounce can lose an in-flight edit across
  // a root switch, or worse, persist it to the wrong root after remount.
  const notebookSurfaceRef = useRef<NotebookSurfaceHandle | null>(null);

  // ---- rendered-document annotation send flow ------------------------------
  const isMarkdown = tile.tileKind === 'markdown';
  const isSeed = tile.tileKind === 'seed';
  const isAnnotatedDocument = isMarkdown || isSeed;
  const [seedDocument, setSeedDocument] = useState<SeedDocument | null>(null);
  const title = isSeed ? (seedDocument?.seed.title || path || baseTitle) : baseTitle;
  const documentSource = useMemo(
    () => (isSeed ? seedMarkdownSource(path) : fileMarkdownSource(workspaceId, path)),
    [isSeed, path, workspaceId],
  );
  const annotationsSendRef = useRef<MarkdownAnnotationsSendHandle | null>(null);
  const [annotationCount, setAnnotationCount] = useState(0);
  // Focus-within on the tile root gates the ⌘Enter shortcut's registration:
  // when focus sits in a terminal pane the shortcut
  // must not exist at all, so the key falls through to the PTY untouched.
  const [hasFocusWithin, setHasFocusWithin] = useState(false);

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
  const seedTenderSessionId = seedDocument?.tender_holds
    ? seedDocument.seed.tender_session.trim()
    : '';
  const primaryDestination = useMemo<MarkdownAnnotationsDestination | null>(() => {
    if (isSeed) {
      if (!seedDocument || !path) return null;
      return seedTenderSessionId
        ? { kind: 'session', sessionId: seedTenderSessionId }
        : { kind: 'seed', seedId: path };
    }
    return targetSessionId ? { kind: 'session', sessionId: targetSessionId } : null;
  }, [isSeed, path, seedDocument, seedTenderSessionId, targetSessionId]);
  const transportAvailable = getMarkdownAnnotationsTransport() !== null;

  const performAnnotationSend = useCallback((
    destination: MarkdownAnnotationsDestination | null,
  ): MarkdownSendResult | null | Promise<MarkdownSendResult> => {
    const handle = annotationsSendRef.current;
    const transport = getMarkdownAnnotationsTransport();
    if (annotationCount === 0 || !destination || !handle || !transport || !path) {
      return null;
    }
    if (!handle.isHydrated()) {
      // The daemon draft has not been loaded (hydrate in flight or failed):
      // local edits are unsaved, so the daemon would format a STALE stored
      // draft — not what the sidebar shows. Refuse rather than mis-deliver.
      return { kind: 'error', message: NOT_HYDRATED_MESSAGE };
    }
    return (async () => {
      // Flush the 500ms save debounce first so the daemon formats a draft
      // that includes the last keystroke's edit.
      await handle.flushPendingSave();
      const result = await transport.submitMarkdownAnnotations(
        documentSource,
        destination,
        handle.getOrphanedIds(),
      );
      if ((result.status === 'delivered' || result.status === 'noted') && result.error) {
        // The daemon accepted the payload but kept its draft; local state
        // stays with that surviving source of truth.
        return { kind: 'warning', message: result.error };
      }
      if (result.status === 'delivered' || result.status === 'noted') {
        handle.applyDeliveredClear(result.generation ?? 0);
        return { kind: 'sent', destination: result.status === 'noted' ? 'seed' : 'session' };
      }
      if (result.status === 'skipped_pending_approval') {
        return { kind: 'skipped' };
      }
      return { kind: 'error', message: result.error || 'Send failed' };
    })();
  }, [annotationCount, documentSource, path]);

  const sendEnabled = isAnnotatedDocument
      && visible
      && hasFocusWithin
      && annotationCount > 0
      && !!primaryDestination
      && transportAvailable;
  const {
    outcome: sendOutcome,
    send: sendNow,
    sendAlternative,
    clearOutcome: clearSendOutcome,
  } = useAnnotationSend<MarkdownSendResult>({
    send: () => performAnnotationSend(primaryDestination),
    shortcutId: 'markdown.sendAnnotations',
    enabled: sendEnabled,
    sentClearMs: SEND_SENT_CLEAR_MS,
  });
  const sendStatus = sendOutcome ?? { kind: 'idle' as const };

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
  const sendDisabled = sending || annotationCount === 0 || !primaryDestination || !transportAvailable;
  const [seedDestinationMenuOpen, setSeedDestinationMenuOpen] = useState(false);
  const seedDestinationGroupRef = useRef<HTMLDivElement>(null);
  const seedDestinationCaretRef = useRef<HTMLButtonElement>(null);
  const seedDestinationItemRef = useRef<HTMLButtonElement>(null);
  const closeSeedDestinationMenu = useCallback((restoreFocus = false) => {
    setSeedDestinationMenuOpen(false);
    if (restoreFocus) {
      window.requestAnimationFrame(() => seedDestinationCaretRef.current?.focus());
    }
  }, []);
  useEscapeStack(() => closeSeedDestinationMenu(true), seedDestinationMenuOpen);
  useEffect(() => {
    if (!seedDestinationMenuOpen) return;
    seedDestinationItemRef.current?.focus();
    const handleMouseDown = (event: MouseEvent) => {
      if (!seedDestinationGroupRef.current?.contains(event.target as Node)) {
        closeSeedDestinationMenu();
      }
    };
    document.addEventListener('mousedown', handleMouseDown);
    return () => document.removeEventListener('mousedown', handleMouseDown);
  }, [closeSeedDestinationMenu, seedDestinationMenuOpen]);
  useEffect(() => {
    if (!seedTenderSessionId) setSeedDestinationMenuOpen(false);
  }, [seedTenderSessionId]);

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

  // Root switcher (notebook tiles only). Lives in the tile chrome rather than
  // NotebookSurface because the dock tile already owns tileParams writes (see
  // the notebook body's onOpenFile below) — the switcher just writes a
  // different field of the same params.
  const { effectiveNotebookRoot } = useNotebookSurfaceContext();
  const isNotebook = tile.tileKind === 'notebook';
  const currentRoot = isNotebook ? parseNotebookTileParams(tile.tileParams).root : undefined;
  const ROOT_BROWSE_VALUE = '__browse__';
  const workspaceDirIsRoot = !!workspaceDirectory && workspaceDirectory !== effectiveNotebookRoot;
  // The current root shows as its own option only when it's neither of the two
  // named shortcuts already covering it — otherwise it'd duplicate "Notebook"
  // or "Workspace — <dir>".
  const currentRootIsOther = !!currentRoot
    && currentRoot !== effectiveNotebookRoot
    && currentRoot !== workspaceDirectory;

  const handleRootChange = useCallback((event: ChangeEvent<HTMLSelectElement>) => {
    const value = event.target.value;
    if (value === ROOT_BROWSE_VALUE) {
      void open({ directory: true, multiple: false, title: 'Choose editor root' }).then(async (selected) => {
        if (!selected || typeof selected !== 'string') {
          return; // cancelled — the select is controlled by parsed params, snaps back on re-render
        }
        // Flush the outgoing root's dirty buffer BEFORE swapping params — the
        // surface persists it to the OLD root via the same instance that has
        // it open, since the param swap is what remounts NotebookSurface onto
        // the new root (NotebookTile keys on `root`).
        const outcome = notebookSurfaceRef.current ? await notebookSurfaceRef.current.flushPendingSave() : 'noop';
        if (outcome === 'conflict' || outcome === 'error') {
          // The surface's own conflict/save-error banner is already visible;
          // the controlled select snaps back on re-render since params never
          // changed. Aborting here is what keeps the edit from vanishing
          // behind a root swap.
          return;
        }
        void Promise.resolve(onUpdateParams?.(serializeNotebookTileParams({ root: selected }))).catch((error) => {
          console.warn('[WorkspaceDockTile] Failed to persist browsed notebook root:', error);
        });
      }).catch((error) => {
        console.warn('[WorkspaceDockTile] Failed to open root browse dialog:', error);
      });
      return;
    }
    void (async () => {
      // Same flush-then-switch rule as the browse-dialog branch above.
      const outcome = notebookSurfaceRef.current ? await notebookSurfaceRef.current.flushPendingSave() : 'noop';
      if (outcome === 'conflict' || outcome === 'error') {
        return;
      }
      // A chosen dir (or any non-browse option): path is deliberately omitted —
      // the open file is meaningless under a different root, so the tile drops
      // back to its tree view.
      void Promise.resolve(onUpdateParams?.(serializeNotebookTileParams({ root: value || undefined }))).catch((error) => {
        console.warn('[WorkspaceDockTile] Failed to persist notebook root:', error);
      });
    })();
  }, [onUpdateParams]);

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
      onFocus={isAnnotatedDocument ? handleTileFocus : undefined}
      onBlur={isAnnotatedDocument ? handleTileBlur : undefined}
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
        {isNotebook ? (
          <select
            className="workspace-dock-tile-root-picker"
            aria-label="Editor root"
            value={currentRoot ?? ''}
            // The header is the drag handle; interacting with the picker must
            // not start a re-dock drag (mirrors the session-picker below).
            onPointerDown={(event) => event.stopPropagation()}
            onChange={handleRootChange}
          >
            <option value="">Notebook</option>
            {workspaceDirIsRoot && (
              <option value={workspaceDirectory}>Workspace — {tilePathBasename(workspaceDirectory as string)}</option>
            )}
            {currentRootIsOther && (
              <option value={currentRoot}>{tilePathBasename(currentRoot as string)}</option>
            )}
            <option value={ROOT_BROWSE_VALUE}>Browse…</option>
          </select>
        ) : null}
        {isAnnotatedDocument ? (
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
                {sendStatus.destination === 'seed' ? 'Noted ✓' : 'Sent ✓'}
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
            {isSeed ? (
              <div
                ref={seedDestinationGroupRef}
                className="workspace-dock-tile-seed-submit"
                role="group"
                aria-label="Submit seed annotations"
              >
                <button
                  type="button"
                  className={`workspace-dock-tile-send-button${seedTenderSessionId ? ' workspace-dock-tile-send-button--split-primary' : ''}`}
                  disabled={sendDisabled}
                  title={seedTenderSessionId
                    ? 'Send annotations to the tending session (⌘Enter)'
                    : 'Leave annotations as a note on the seed (⌘Enter)'}
                  onClick={sendNow}
                >
                  {sending
                    ? (seedTenderSessionId ? 'Sending…' : 'Noting…')
                    : seedTenderSessionId
                      ? `Send ${annotationCount}`
                      : `Note on seed ${annotationCount}`}
                </button>
                {seedTenderSessionId ? (
                  <>
                    <button
                      ref={seedDestinationCaretRef}
                      type="button"
                      className="workspace-dock-tile-send-button workspace-dock-tile-send-button--split-caret"
                      aria-label="More annotation destinations"
                      aria-haspopup="menu"
                      aria-expanded={seedDestinationMenuOpen}
                      disabled={sendDisabled}
                      onClick={() => setSeedDestinationMenuOpen((open) => !open)}
                    >
                      ▾
                    </button>
                    {seedDestinationMenuOpen ? (
                      <div className="workspace-dock-tile-seed-submit-menu" role="menu">
                        <button
                          ref={seedDestinationItemRef}
                          type="button"
                          role="menuitem"
                          onClick={() => {
                            closeSeedDestinationMenu();
                            sendAlternative(() => performAnnotationSend({ kind: 'seed', seedId: path }));
                          }}
                        >
                          Note on seed
                        </button>
                      </div>
                    ) : null}
                  </>
                ) : null}
              </div>
            ) : (
              <>
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
                    clearSendOutcome();
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
              </>
            )}
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
            source={documentSource}
            allowLocalTargets={allowLocalTargets}
            onAnnotationsCountChange={setAnnotationCount}
            annotationsSendRef={annotationsSendRef}
          />
        ) : tile.tileKind === 'seed' ? (
          <SeedTileBody
            seedId={path}
            gardenSeeds={gardenSeeds}
            onDocument={setSeedDocument}
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
          // tileParams may be the legacy bare-path string (rootless tile) or the
          // {root, path} JSON envelope a root-bound tile persists.
          (() => {
            const { root, path: openPath } = parseNotebookTileParams(tile.tileParams);
            return (
              <NotebookTile
                ref={notebookSurfaceRef}
                initialPath={openPath || null}
                root={root}
                onOpenFile={(openedPath) => {
                  const nextParams = serializeNotebookTileParams({ root, path: openedPath });
                  void Promise.resolve(onUpdateParams?.(nextParams)).catch((error) => {
                    console.warn('[WorkspaceDockTile] Failed to persist notebook path:', error);
                  });
                }}
              />
            );
          })()
        ) : (
          <div className="workspace-dock-tile-message">Unsupported tile: {tile.tileKind}</div>
        )}
      </div>
    </div>
  );
}

function MarkdownBody({
  content,
  source,
  allowLocalTargets,
  onAnnotationsCountChange,
  annotationsSendRef,
}: {
  content?: TileContentState;
  source: MarkdownDocumentSource;
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
      source={source}
      allowLocalTargets={allowLocalTargets}
      annotationsEnabled
      onAnnotationsCountChange={onAnnotationsCountChange}
      annotationsSendRef={annotationsSendRef}
    />
  );
}

function SeedTileBody({
  seedId,
  gardenSeeds,
  onDocument,
  onAnnotationsCountChange,
  annotationsSendRef,
}: {
  seedId: string;
  gardenSeeds: Seed[];
  onDocument: (document: SeedDocument | null) => void;
  onAnnotationsCountChange: (count: number) => void;
  annotationsSendRef: RefObject<MarkdownAnnotationsSendHandle | null>;
}) {
  const { sendSeedDocumentGet, sendOpenMarkdown } = useDaemonApi();
  const [document, setDocument] = useState<SeedDocument | null>(null);
  const [error, setError] = useState<string | null>(null);
  const liveSeed = useMemo(
    () => gardenSeeds.find((seed) => seed.id === seedId) ?? null,
    [gardenSeeds, seedId],
  );
  const displayedDocument = useMemo(() => {
    if (!document || !liveSeed || liveSeed.rev < document.seed.rev) return document;
    const tenderChanged = liveSeed.tender_session !== document.seed.tender_session
      || liveSeed.tender_member !== document.seed.tender_member;
    return {
      ...document,
      seed: liveSeed,
      tender_holds: tenderChanged
        ? Boolean(liveSeed.tender_session || liveSeed.tender_member)
        : document.tender_holds,
    };
  }, [document, liveSeed]);

  // Every garden fact re-pushes the seeds snapshot. Notes and child changes do
  // not necessarily touch this seed's own revision, so the array identity is
  // the live invalidation signal for the complete reader document.
  useEffect(() => {
    if (!seedId) {
      setDocument(null);
      onDocument(null);
      setError('No seed is associated with this tile.');
      return;
    }
    let ignore = false;
    setError(null);
    void sendSeedDocumentGet(seedId)
      .then((next) => {
        if (ignore) return;
        if (liveSeed && liveSeed.rev >= next.seed.rev) {
          const tenderChanged = liveSeed.tender_session !== next.seed.tender_session
            || liveSeed.tender_member !== next.seed.tender_member;
          setDocument({
            ...next,
            seed: liveSeed,
            tender_holds: tenderChanged
              ? Boolean(liveSeed.tender_session || liveSeed.tender_member)
              : next.tender_holds,
          });
        } else {
          setDocument(next);
        }
      })
      .catch((readError) => {
        if (ignore) return;
        setError(readError instanceof Error ? readError.message : `Could not read ${seedId}`);
      });
    return () => {
      ignore = true;
    };
  }, [gardenSeeds, liveSeed, seedId, sendSeedDocumentGet]);

  useEffect(() => {
    onDocument(displayedDocument);
  }, [displayedDocument, onDocument]);

  useEffect(() => () => onDocument(null), [onDocument]);

  if (!displayedDocument) {
    return (
      <div className={`workspace-dock-tile-message${error ? ' workspace-dock-tile-error' : ''}`}>
        {error || 'Loading seed…'}
      </div>
    );
  }

  return (
    <SeedDocumentView
      document={displayedDocument}
      annotationsEnabled
      onAnnotationsCountChange={onAnnotationsCountChange}
      annotationsSendRef={annotationsSendRef}
      onOpenMarkdownArtifact={(path) => {
        void sendOpenMarkdown(path, '').catch((openError) => {
          console.error('[SeedDocument] Could not open markdown artifact:', openError);
        });
      }}
    />
  );
}
