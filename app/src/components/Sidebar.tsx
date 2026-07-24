import './Sidebar.css';
import type { MouseEvent as ReactMouseEvent, PointerEvent as ReactPointerEvent, ReactNode } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import { RenamePopover } from './RenamePopover';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';
import { SessionActionsPopover } from './SessionActionsPopover';
import { GridLayoutControl } from './grid/GridLayoutControl';
import type { GridLayout } from './grid/gridLayout';
import { StateIndicator } from './StateIndicator';
import { SidebarNudgeBar, deriveNudgeMode } from './NudgeIndicator';
import { formatShortcut } from '../shortcuts';
import { isAttentionSessionState, type UISessionState } from '../types/sessionState';
import { tileContentKey, type TileContentState, type TileLeaf } from '../types/workspace';
import { deriveTileTitle } from '../utils/tilePresentation';
import type { WorkspaceWithSessions } from '../utils/workspaceViewModels';

interface LocalSession {
  id: string;
  label: string;
  state: UISessionState;
  agent?: string;
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
  endpointId?: string;
  endpointName?: string;
  endpointStatus?: string;
  chiefOfStaff?: boolean;
  delegatedFromChief?: boolean;
  ticketUnread?: boolean;
  nudgeFiresAt?: string;
}

type SidebarWorkspace = WorkspaceWithSessions<LocalSession>;

interface SelectedTile {
  workspaceId: string;
  tileId: string;
}

// A sessionless workspace only exists because the user left a docked tile
// behind (the daemon tears down workspaces that hold no leaves at all). They're
// hidden by default and revealed through the sidebar display popover; the
// preference lives in App so every workspace-order derivation stays consistent.
function isSessionless(workspace: SidebarWorkspace): boolean {
  return workspace.sessions.length === 0;
}

function TileSidebarRow({
  workspaceId,
  tile,
  content,
  selected,
  muted = false,
  onSelect,
  onClose,
  onReload,
}: {
  workspaceId: string;
  tile: TileLeaf;
  content?: TileContentState;
  selected: boolean;
  muted?: boolean;
  onSelect: () => void;
  onClose: () => void;
  onReload: () => void;
}) {
  const title = deriveTileTitle(tile, content);
  return (
    <div
      className={`session-item workspace-tile-item grouped ${selected ? 'selected' : ''} ${muted ? 'muted-session' : ''}`.trim()}
      data-testid={`sidebar-tile-${workspaceId}-${tile.tileId}`}
      data-tile-kind={tile.tileKind}
      onClick={onSelect}
    >
      <span className={`workspace-tile-indicator workspace-tile-indicator--${tile.tileKind}`} aria-hidden="true" />
      <span className="session-label">{title}</span>
      {!muted && (
        <div className="session-actions">
          {tile.tileKind === 'browser' && (
            <button
              className="session-action-btn reload-session-btn"
              data-testid={`reload-tile-${workspaceId}-${tile.tileId}`}
              onClick={(event) => {
                event.stopPropagation();
                onReload();
              }}
              title="Reload browser"
              aria-label={`Reload ${title}`}
            >
              ↻
            </button>
          )}
          <button
            className="session-action-btn close-session-btn"
            data-testid={`close-tile-${workspaceId}-${tile.tileId}`}
            onClick={(event) => {
              event.stopPropagation();
              onClose();
            }}
            title="Close tile"
            aria-label={`Close ${title}`}
          >
            ×
          </button>
        </div>
      )}
    </div>
  );
}

interface SidebarProps {
  workspaces: SidebarWorkspace[];
  visualOrder: SidebarWorkspace[];
  visualIndexByWorkspaceId: Map<string, number>;
  selectedId: string | null;
  selectedWorkspaceId: string | null;
  selectedTile?: SelectedTile | null;
  tileContents?: Record<string, TileContentState>;
  collapsed: boolean;
  headerActions: SidebarHeaderAction[];
  // Current grid shape + a handler to choose a new one (also opens grid mode).
  // Optional so existing Sidebar tests render without wiring the grid picker.
  gridLayout?: GridLayout;
  onSelectGridLayout?: (layout: GridLayout) => void;
  // Config-driven dock chips (ordered). Built in App from the keybindings config.
  dockItems?: DockItem[];
  dockCollapsed?: boolean;
  onToggleDockCollapsed?: () => void;
  mutedWorkspaces?: SidebarWorkspace[];
  mutedExpanded?: boolean;
  onMutedExpandedChange?: (expanded: boolean) => void;
  onMuteWorkspace?: (workspaceId: string, endpointId?: string) => void;
  onPinWorkspace?: (workspaceId: string, pinned: boolean) => void;
  onRenameSession?: (sessionId: string, label: string) => Promise<void>;
  onRenameWorkspace?: (workspaceId: string, title: string) => Promise<void>;
  onChangeChiefOfStaff?: (sessionId: string, enabled: boolean) => void;
  showSessionless?: boolean;
  onToggleShowSessionless?: () => void;
  leafDrag?: { sourceWorkspaceId: string; endpointId?: string } | null;
  dragHoverWorkspaceId?: string | null;
  onWorkspaceDragEnter?: (workspace: SidebarWorkspace) => void;
  onWorkspaceDragLeave?: (workspace: SidebarWorkspace) => void;
  onWorkspaceDragDrop?: (workspace: SidebarWorkspace) => void;
  // Drop the dragged leaf onto the "New workspace" zone at the foot of the list to
  // split it into a brand-new workspace. Wired in App to
  // sendWorkspaceMoveLeafToNewWorkspace.
  onNewWorkspaceDrop?: () => void;
  // Begin / end a leaf drag started by pressing a session row in the sidebar.
  // onSessionDragStart arms the same App-level leaf drag the in-view pane header
  // uses (leafId = the session's layout pane id), so the existing workspace-group
  // and "New workspace" drop targets handle the actual move/split. onSessionDragEnd
  // tears the drag down. Wired in App to handleLeafDragStart / handleLeafDragEnd.
  onSessionDragStart?: (workspaceId: string, endpointId: string | undefined, paneId: string) => void;
  onSessionDragEnd?: () => void;
  // Reorder a workspace by dropping its header onto an insertion seam. The
  // neighbour ids describe the seam: prevWorkspaceId ends up directly above the
  // moved workspace, nextWorkspaceId directly below. Either may be undefined when
  // dropping at the very top or bottom. Wired in App to sendSetWorkspaceRank.
  onWorkspaceReorder?: (args: {
    workspaceId: string;
    prevWorkspaceId?: string;
    nextWorkspaceId?: string;
  }) => void;
  onSelectSession: (id: string) => void;
  onTriggerNudge?: (id: string) => void;
  onSelectWorkspace: (id: string) => void;
  onSelectTile?: (workspaceId: string, tileId: string) => void;
  onCloseTile?: (workspaceId: string, tileId: string) => void;
  onReloadTile?: (workspaceId: string, tileId: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onReloadSession: (id: string) => void;
  onGoToDashboard: () => void;
  onToggleCollapse: () => void;
}

export interface SidebarHeaderAction {
  id: string;
  title: string;
  icon: ReactNode;
  disabled?: boolean;
  active?: boolean;
  toneClassName?: string;
  badge?: string | number;
  onClick: () => void;
}

export interface DockItem {
  id: string;
  /** Terse chip label, e.g. "diff". */
  label: string;
  /** Rendered key tokens, e.g. "⌘⇧G". Empty when the shortcut is unbound. */
  keys: string;
  active?: boolean;
  /** When set, the chip is an actionable button; otherwise an informational hint. */
  onClick?: () => void;
}

function HomeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2.5 7.2 8 2.8l5.5 4.4v5.8a.5.5 0 0 1-.5.5H9.5V9H6.5v4.5H3a.5.5 0 0 1-.5-.5Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}

function PlusIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M8 3v10M3 8h10" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}

function CollapseIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M10 3 5 8l5 5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function SettingsIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 8h10M3 4.5h10M3 11.5h10" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M6 4.5h1.8M9.2 8H11M5.2 11.5H7" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" />
    </svg>
  );
}

function ExpandIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M6 3l5 5-5 5" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}


export function WorkflowIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <rect x="1.5" y="6" width="3.5" height="4" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <rect x="11" y="2.5" width="3.5" height="3.5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <rect x="11" y="10" width="3.5" height="3.5" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M5 8h3v-3.75h3M8 8v3.75h3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function EditorIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 2.5h7.5v2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M8.5 3h4.5v4.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="m7 9 6-6" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M13 9.5v3a1 1 0 0 1-1 1H3.5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function DiffIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M5 3.2 2.5 5.7 5 8.2" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M11 7.8 13.5 10.3 11 12.8" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M13.5 5H7.2a2.7 2.7 0 0 0-2.7 2.7v.6A2.7 2.7 0 0 1 1.8 11H1.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function PRsIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M5 3.2a1.8 1.8 0 1 1-3.6 0 1.8 1.8 0 0 1 3.6 0ZM14.6 4.2a1.8 1.8 0 1 1-3.6 0 1.8 1.8 0 0 1 3.6 0ZM5 12.8a1.8 1.8 0 1 1-3.6 0 1.8 1.8 0 0 1 3.6 0Z" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M3.2 5v6M5 3.2h4.3a2 2 0 0 1 2 2v.5M5 12.8h4.5a2 2 0 0 0 2-2V6.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function NotebookIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4 2.5h7.5a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <path d="M4 2.5a1.5 1.5 0 0 0-1.5 1.5v8A1.5 1.5 0 0 0 4 13.5" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M6 5.5h4M6 8h4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
    </svg>
  );
}

export function MarkdownIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <rect x="1.5" y="3" width="13" height="10" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.3" />
      <path d="M3.6 10.4V5.6l2 2.4 2-2.4v4.8" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M10.6 5.6v4.2M9 8.4l1.6 1.6 1.6-1.6" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Sidebar({
  workspaces,
  visualOrder,
  visualIndexByWorkspaceId,
  selectedId,
  selectedWorkspaceId,
  selectedTile = null,
  tileContents = {},
  collapsed,
  headerActions,
  gridLayout,
  onSelectGridLayout,
  dockItems = [],
  dockCollapsed = false,
  onToggleDockCollapsed,
  mutedWorkspaces = [],
  mutedExpanded: mutedExpandedProp,
  onMutedExpandedChange,
  onMuteWorkspace,
  onPinWorkspace,
  onRenameSession,
  onRenameWorkspace,
  onChangeChiefOfStaff,
  showSessionless = false,
  onToggleShowSessionless,
  leafDrag = null,
  dragHoverWorkspaceId = null,
  onWorkspaceDragEnter,
  onWorkspaceDragLeave,
  onWorkspaceDragDrop,
  onNewWorkspaceDrop,
  onSessionDragStart,
  onSessionDragEnd,
  onWorkspaceReorder,
  onSelectSession,
  onTriggerNudge,
  onSelectWorkspace,
  onSelectTile,
  onCloseTile,
  onReloadTile,
  onNewSession,
  onCloseSession,
  onReloadSession,
  onGoToDashboard,
  onToggleCollapse,
}: SidebarProps) {
  const [mutedExpandedLocal, setMutedExpandedLocal] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [displayMode, setDisplayMode] = useState<'open' | 'tight' | 'boxed'>('boxed');
  const [renameTarget, setRenameTarget] = useState<{
    kind: 'session' | 'workspace';
    id: string;
    name: string;
    anchor: { top: number; left: number };
  } | null>(null);
  const [sessionActionsTarget, setSessionActionsTarget] = useState<{
    id: string;
    label: string;
    chiefOfStaff: boolean;
    anchor: { top: number; left: number };
  } | null>(null);

  const openRename = (
    kind: 'session' | 'workspace',
    id: string,
    name: string,
    event: ReactMouseEvent,
  ) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setRenameTarget({ kind, id, name, anchor: { top: rect.bottom + 4, left: rect.left } });
  };
  const openSessionActions = (
    session: LocalSession,
    event: ReactMouseEvent,
  ) => {
    event.stopPropagation();
    const rect = event.currentTarget.getBoundingClientRect();
    setSessionActionsTarget({
      id: session.id,
      label: session.label,
      chiefOfStaff: Boolean(session.chiefOfStaff),
      anchor: { top: rect.bottom + 4, left: rect.right - 190 },
    });
  };
  const mutedExpanded = mutedExpandedProp ?? mutedExpandedLocal;
  const setMutedExpanded = (v: boolean) => {
    setMutedExpandedLocal(v);
    onMutedExpandedChange?.(v);
  };

  const isWorkspaceVisible = (workspace: SidebarWorkspace) => workspace.pinned || !isSessionless(workspace) || showSessionless;
  const visibleWorkspaces = workspaces.filter(isWorkspaceVisible);
  const visibleVisualOrder = visualOrder.filter(isWorkspaceVisible);
  const visibleVisualIndexByWorkspaceId = new Map(
    visibleVisualOrder.map((workspace, index) => [workspace.id, index]),
  );

  const canAcceptLeafDrag = (workspace: SidebarWorkspace) => Boolean(
    leafDrag
      && workspace.id !== leafDrag.sourceWorkspaceId
      && (workspace.endpointId || '') === (leafDrag.endpointId || ''),
  );

  const workspaceDragClass = (workspace: SidebarWorkspace) => {
    if (!leafDrag) {
      return '';
    }
    if (!canAcceptLeafDrag(workspace)) {
      return ' workspace-group--drag-disabled';
    }
    if (dragHoverWorkspaceId === workspace.id) {
      return ' workspace-group--drag-entering';
    }
    return ' workspace-group--drag-target';
  };
  const visualIndexOfWorkspace = (id: string) => (
    visibleVisualIndexByWorkspaceId.get(id) ?? visualIndexByWorkspaceId.get(id) ?? -1
  );

  // --- Workspace reorder (header drag onto an insertion seam) ---
  // A press on a workspace header arms a reorder once the pointer crosses a small
  // threshold; a sub-threshold release stays a plain selection click. Only
  // unmuted, visible, same-endpoint workspaces participate — muted workspaces live
  // in their own section and keep a separate order space. The body of a group is a
  // no-op: dropping there snaps to the nearest seam, so workspaces never merge.
  const REORDER_THRESHOLD = 6;
  // The set of workspaces a drag may reorder within: the dragged workspace's
  // endpoint, in visible visual order. Empty when no drag is active.
  const [reorderDrag, setReorderDrag] = useState<{ workspaceId: string; endpointId?: string } | null>(null);
  // True while the cursor is over the "New workspace" drop-zone during a leaf drag,
  // so the zone shows its active/hover styling. Reset on leave and on drop.
  const [newWorkspaceDropActive, setNewWorkspaceDropActive] = useState(false);
  // Floating ghost label following the cursor while dragging a session row out of
  // the sidebar (null when no session drag is active), plus the session id of the
  // dragged row so it can render dimmed. The drop itself is handled by the same
  // workspace-group / "New workspace" targets the in-view pane drag uses.
  const [sessionDragGhost, setSessionDragGhost] = useState<{ x: number; y: number; label: string } | null>(null);
  const [draggingSessionId, setDraggingSessionId] = useState<string | null>(null);
  const sessionDragRef = useRef<{ pointerId: number; startX: number; startY: number; armed: boolean } | null>(null);
  const suppressNextSessionClickRef = useRef(false);
  // The insertion slot (0..N) the cursor is currently nearest. null = none yet.
  const [reorderSeamIndex, setReorderSeamIndex] = useState<number | null>(null);
  // Per-interaction refs so listeners read fresh values without re-binding.
  const reorderDragRef = useRef<{
    workspaceId: string;
    endpointId?: string;
    pointerId: number;
    startX: number;
    startY: number;
    armed: boolean;
    sourceEl: HTMLElement;
  } | null>(null);
  const reorderSeamIndexRef = useRef<number | null>(null);
  // Set true the moment a drag arms so the click that follows pointerup selects
  // nothing (a sub-threshold press leaves this false and still selects).
  const suppressNextHeaderClickRef = useRef(false);

  const reorderParticipants = (endpointId?: string): SidebarWorkspace[] => (
    visibleVisualOrder.filter((workspace) => (workspace.endpointId || '') === (endpointId || ''))
  );

  const updateReorderSeam = useCallback((index: number | null) => {
    reorderSeamIndexRef.current = index;
    setReorderSeamIndex(index);
  }, []);

  // Pick the insertion seam whose vertical centre is closest to the cursor. The
  // group body resolves to the nearer of its two bounding seams (same math), so a
  // drop is always a reorder, never a merge.
  const nearestSeamIndex = useCallback((clientY: number): number | null => {
    const list = document.querySelector('.session-list');
    if (!list) {
      return null;
    }
    const seams = Array.from(list.querySelectorAll<HTMLElement>('.workspace-reorder-seam'));
    let best: number | null = null;
    let bestDist = Infinity;
    for (const seam of seams) {
      const index = Number(seam.dataset.seamIndex);
      if (Number.isNaN(index)) {
        continue;
      }
      const rect = seam.getBoundingClientRect();
      const center = (rect.top + rect.bottom) / 2;
      const dist = Math.abs(clientY - center);
      if (dist < bestDist) {
        bestDist = dist;
        best = index;
      }
    }
    return best;
  }, []);

  const endReorderDrag = useCallback(() => {
    reorderDragRef.current = null;
    setReorderDrag(null);
    updateReorderSeam(null);
  }, [updateReorderSeam]);

  // Translate the dropped seam slot into the neighbour ids the daemon expects.
  // Dropping back into the moved workspace's own slot is a no-op.
  const commitReorder = useCallback((
    workspaceId: string,
    endpointId: string | undefined,
    seamIndex: number | null,
  ) => {
    if (seamIndex == null || !onWorkspaceReorder) {
      return;
    }
    const participants = visibleVisualOrder.filter(
      (workspace) => (workspace.endpointId || '') === (endpointId || ''),
    );
    const fromIndex = participants.findIndex((workspace) => workspace.id === workspaceId);
    if (fromIndex < 0) {
      return;
    }
    // Removing the moved row shifts later rows up by one, so slots fromIndex and
    // fromIndex+1 both land it back where it started.
    if (seamIndex === fromIndex || seamIndex === fromIndex + 1) {
      return;
    }
    const remaining = participants.filter((workspace) => workspace.id !== workspaceId);
    const insertAt = seamIndex > fromIndex ? seamIndex - 1 : seamIndex;
    const prevWorkspaceId = insertAt > 0 ? remaining[insertAt - 1]?.id : undefined;
    const nextWorkspaceId = insertAt < remaining.length ? remaining[insertAt]?.id : undefined;
    onWorkspaceReorder({ workspaceId, prevWorkspaceId, nextWorkspaceId });
  }, [onWorkspaceReorder, visibleVisualOrder]);

  const handleHeaderPointerDown = useCallback((
    workspace: SidebarWorkspace,
    event: ReactPointerEvent<HTMLDivElement>,
  ) => {
    // Only a primary-button press on the header chrome itself arms a reorder. The
    // rename/mute buttons stop propagation, so this never fires from them.
    if (event.button !== 0 || !onWorkspaceReorder) {
      return;
    }
    const sourceEl = event.currentTarget;
    reorderDragRef.current = {
      workspaceId: workspace.id,
      endpointId: workspace.endpointId,
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      armed: false,
      sourceEl,
    };

    const onMove = (moveEvent: PointerEvent) => {
      const drag = reorderDragRef.current;
      if (!drag || moveEvent.pointerId !== drag.pointerId) {
        return;
      }
      if (!drag.armed) {
        const dx = moveEvent.clientX - drag.startX;
        const dy = moveEvent.clientY - drag.startY;
        if (Math.hypot(dx, dy) < REORDER_THRESHOLD) {
          return;
        }
        drag.armed = true;
        suppressNextHeaderClickRef.current = true;
        try {
          sourceEl.setPointerCapture(drag.pointerId);
        } catch {
          // setPointerCapture can throw if the pointer is already gone; ignore.
        }
        setReorderDrag({ workspaceId: drag.workspaceId, endpointId: drag.endpointId });
      }
      updateReorderSeam(nearestSeamIndex(moveEvent.clientY));
    };

    const finish = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onCancel);
      const drag = reorderDragRef.current;
      if (drag) {
        try {
          sourceEl.releasePointerCapture(drag.pointerId);
        } catch {
          // releasePointerCapture throws when capture was never taken; ignore.
        }
      }
    };

    const onUp = (upEvent: PointerEvent) => {
      const drag = reorderDragRef.current;
      finish();
      if (!drag || upEvent.pointerId !== drag.pointerId) {
        endReorderDrag();
        return;
      }
      if (drag.armed) {
        commitReorder(drag.workspaceId, drag.endpointId, reorderSeamIndexRef.current);
      }
      endReorderDrag();
    };

    const onCancel = () => {
      finish();
      endReorderDrag();
    };

    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onCancel);
  }, [onWorkspaceReorder, nearestSeamIndex, updateReorderSeam, endReorderDrag, commitReorder]);

  // The participating set for the live drag (same endpoint as the dragged ws),
  // indexed so each rendered workspace knows which seam slot precedes it. The
  // trailing seam slot equals the participant count.
  const reorderActiveParticipants = reorderDrag
    ? reorderParticipants(reorderDrag.endpointId)
    : [];
  const reorderSeamIndexByWorkspaceId = reorderDrag
    ? new Map(reorderActiveParticipants.map((workspace, index) => [workspace.id, index]))
    : null;
  const reorderTrailingSeamIndex = reorderActiveParticipants.length;
  const lastReorderParticipantId = reorderActiveParticipants[reorderActiveParticipants.length - 1]?.id;

  const renderReorderSeam = (index: number) => (
    <div
      className={`workspace-reorder-seam ${reorderSeamIndex === index ? 'active' : ''}`.trim()}
      data-testid={`workspace-reorder-seam-${index}`}
      data-seam-index={index}
      aria-hidden="true"
      // Hovering a seam directly highlights it; the pointermove handler still
      // snaps to the nearest seam when the cursor is over a group body.
      onPointerEnter={() => {
        if (reorderDragRef.current?.armed) {
          updateReorderSeam(index);
        }
      }}
    >
      <span className="workspace-reorder-seam-line" />
    </div>
  );

  const handleHeaderClickCapture = useCallback((event: ReactMouseEvent) => {
    // Swallow the click that fires right after an armed drag's pointerup so the
    // release does not also select the workspace.
    if (suppressNextHeaderClickRef.current) {
      suppressNextHeaderClickRef.current = false;
      event.preventDefault();
      event.stopPropagation();
    }
  }, []);

  // Tear down a live reorder if the component unmounts mid-drag.
  useEffect(() => endReorderDrag, [endReorderDrag]);

  // A press on a session row arms a leaf drag once the pointer crosses the
  // threshold; a sub-threshold release stays a plain selection click. Unlike the
  // workspace reorder, this takes NO pointer capture: the workspace-group and
  // "New workspace" drop targets rely on their own pointer handlers firing as the
  // cursor moves over them, exactly like the in-view pane drag. So this gesture
  // only arms the App-level leaf drag and floats a ghost; the existing targets do
  // the move. Requires a layout pane id (sessions without one aren't in a pane).
  const SESSION_DRAG_THRESHOLD = 6;
  const handleSessionPointerDown = useCallback((
    workspace: SidebarWorkspace,
    paneId: string,
    sessionId: string,
    label: string,
    event: ReactPointerEvent<HTMLDivElement>,
  ) => {
    // Child buttons (the ••• actions) stop propagation, so this only fires from a
    // primary-button press on the row chrome itself.
    if (event.button !== 0 || !onSessionDragStart) {
      return;
    }
    // A fresh press always starts un-suppressed, so a drag that ended over another
    // element (leaving the flag set) can't swallow this row's next real click.
    suppressNextSessionClickRef.current = false;
    sessionDragRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      armed: false,
    };

    const onMove = (moveEvent: PointerEvent) => {
      const drag = sessionDragRef.current;
      if (!drag || moveEvent.pointerId !== drag.pointerId) {
        return;
      }
      if (!drag.armed) {
        const dx = moveEvent.clientX - drag.startX;
        const dy = moveEvent.clientY - drag.startY;
        if (Math.hypot(dx, dy) < SESSION_DRAG_THRESHOLD) {
          return;
        }
        drag.armed = true;
        suppressNextSessionClickRef.current = true;
        setDraggingSessionId(sessionId);
        onSessionDragStart(workspace.id, workspace.endpointId, paneId);
      }
      setSessionDragGhost({ x: moveEvent.clientX, y: moveEvent.clientY, label });
    };

    const finish = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onCancel);
    };

    const teardownDrag = (armed: boolean) => {
      sessionDragRef.current = null;
      setSessionDragGhost(null);
      setDraggingSessionId(null);
      // Only tear down the App-level drag if we actually started one — a
      // sub-threshold press never armed it.
      if (armed) {
        onSessionDragEnd?.();
      }
    };

    const onUp = () => {
      const drag = sessionDragRef.current;
      finish();
      teardownDrag(Boolean(drag?.armed));
    };

    const onCancel = () => {
      const drag = sessionDragRef.current;
      finish();
      teardownDrag(Boolean(drag?.armed));
    };

    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onCancel);
  }, [onSessionDragStart, onSessionDragEnd]);

  // Swallow the click that fires right after an armed session drag's pointerup so
  // the release does not also select the session.
  const handleSessionClickCapture = useCallback((event: ReactMouseEvent) => {
    if (suppressNextSessionClickRef.current) {
      suppressNextSessionClickRef.current = false;
      event.preventDefault();
      event.stopPropagation();
    }
  }, []);

  if (collapsed) {
    return (
      <div className="sidebar collapsed">
        <div className="icon-rail">
          <button className="icon-btn" onClick={onGoToDashboard} title="Dashboard (⌘G)">
            <HomeIcon />
          </button>
          <div className="icon-divider" />
          {gridLayout && onSelectGridLayout && (
            <GridLayoutControl layout={gridLayout} onSelect={onSelectGridLayout} />
          )}
          {headerActions.map((action) => (
            <button
              key={action.id}
              className={`icon-btn sidebar-tool-btn ${action.active ? 'active' : ''} ${action.toneClassName || ''}`}
              onClick={action.onClick}
              title={action.title}
              disabled={action.disabled}
              aria-label={action.title}
            >
              {action.icon}
              {action.badge !== undefined && (
                <span className="sidebar-tool-badge">{typeof action.badge === 'number' && action.badge > 9 ? '9+' : action.badge}</span>
              )}
            </button>
          ))}
          <div className="icon-divider" />
          {visibleVisualOrder.map((workspace) => (
            <button
              key={workspace.id}
              className={`icon-btn session-icon ${selectedWorkspaceId === workspace.id ? 'active' : ''} ${isSessionless(workspace) ? 'sessionless' : ''}`}
              onClick={() => onSelectWorkspace(workspace.id)}
              title={`${workspace.title} (⌘${visualIndexOfWorkspace(workspace.id) + 1})`}
            >
              ▸
              {workspace.sessions.some((session) => isAttentionSessionState(session.state)) && (
                <span className={`mini-badge ${workspace.status === 'pending_approval' ? 'pending' : ''} ${workspace.status === 'unknown' ? 'unknown' : ''}`} />
              )}
            </button>
          ))}
          <button className="icon-btn" onClick={onNewSession} title="New Session (⌘N)">
            <PlusIcon />
          </button>
          <div className="icon-spacer" />
          <button className="icon-btn expand-btn" onClick={onToggleCollapse} title="Expand sidebar">
            <ExpandIcon />
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className={`sidebar sidebar--display-${displayMode}`}>
      <div className="sidebar-header">
        <div className="sidebar-tool-row">
          <button className="home-btn" onClick={onGoToDashboard} title="Dashboard (⌘G)" aria-label="Dashboard">
            <HomeIcon />
          </button>
          {gridLayout && onSelectGridLayout && (
            <GridLayoutControl layout={gridLayout} onSelect={onSelectGridLayout} />
          )}
          {headerActions.map((action) => (
            <button
              key={action.id}
              className={`sidebar-tool-btn ${action.active ? 'active' : ''} ${action.toneClassName || ''}`}
              onClick={action.onClick}
              title={action.title}
              disabled={action.disabled}
              aria-label={action.title}
            >
              {action.icon}
              {action.badge !== undefined && (
                <span className="sidebar-tool-badge">{typeof action.badge === 'number' && action.badge > 9 ? '9+' : action.badge}</span>
              )}
            </button>
          ))}
          <button className="collapse-btn" onClick={onToggleCollapse} title="Collapse sidebar" aria-label="Collapse sidebar">
            <CollapseIcon />
          </button>
        </div>
        <div className="sidebar-header-row">
          <span className="sidebar-title">Workspaces</span>
          <span className="home-shortcut">⌘G</span>
          <button className="new-session-btn" onClick={onNewSession} title="New Session (⌘N)" aria-label="New Session">
            <PlusIcon />
          </button>
          <div className="sidebar-settings-anchor">
            <button
              className={`sidebar-settings-btn ${settingsOpen ? 'active' : ''}`}
              onClick={() => setSettingsOpen((open) => !open)}
              title="Sidebar settings"
              aria-label="Sidebar settings"
              aria-expanded={settingsOpen}
            >
              <SettingsIcon />
            </button>
            {settingsOpen && (
              <div className="sidebar-settings-popover" role="dialog" aria-label="Sidebar settings">
                <span className="sidebar-settings-label">Display</span>
                <div className="sidebar-display-toggle" role="group" aria-label="Sidebar display">
                  {(['open', 'tight', 'boxed'] as const).map((mode) => (
                    <button
                      key={mode}
                      className={displayMode === mode ? 'active' : ''}
                      onClick={() => {
                        setDisplayMode(mode);
                      }}
                    >
                      {mode}
                    </button>
                  ))}
                </div>
                <button
                  type="button"
                  className="sidebar-settings-switch-row"
                  role="switch"
                  aria-checked={showSessionless}
                  data-testid="toggle-show-sessionless"
                  onClick={() => onToggleShowSessionless?.()}
                >
                  <span className="sidebar-settings-switch-label">Tile-only workspaces</span>
                  <span className={`sidebar-settings-switch ${showSessionless ? 'on' : ''}`} aria-hidden="true" />
                </button>
              </div>
            )}
          </div>
        </div>
      </div>

      <div className={`session-list ${reorderDrag ? 'session-list--reordering' : ''}`.trim()}>
        {visibleWorkspaces.map((workspace) => {
          const workspaceIndex = visualIndexOfWorkspace(workspace.id);
          const seamIndex = reorderSeamIndexByWorkspaceId?.get(workspace.id);
          const isReorderSource = reorderDrag?.workspaceId === workspace.id;
          return (
            <div className="workspace-row" key={`${workspace.endpointId || 'local'}:${workspace.id}`}>
              {seamIndex !== undefined && renderReorderSeam(seamIndex)}
              <div
                className={`workspace-group ${selectedWorkspaceId === workspace.id ? 'selected' : ''}${isReorderSource ? ' workspace-group--reorder-source' : ''}${workspaceDragClass(workspace)}`}
                data-testid={`sidebar-workspace-${workspace.id}`}
                onPointerEnter={() => {
                  if (canAcceptLeafDrag(workspace)) {
                    onWorkspaceDragEnter?.(workspace);
                  }
                }}
                onPointerLeave={() => {
                  if (canAcceptLeafDrag(workspace)) {
                    onWorkspaceDragLeave?.(workspace);
                  }
                }}
                onPointerUp={() => {
                  if (canAcceptLeafDrag(workspace)) {
                    onWorkspaceDragDrop?.(workspace);
                  }
                }}
              >
              <div
                role="button"
                tabIndex={0}
                className="workspace-group-header"
                onPointerDown={(event) => handleHeaderPointerDown(workspace, event)}
                onClickCapture={handleHeaderClickCapture}
                onClick={() => onSelectWorkspace(workspace.id)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    onSelectWorkspace(workspace.id);
                  }
                }}
              >
                {isSessionless(workspace) ? (
                  <span
                    className="workspace-neutral-indicator"
                    data-testid="workspace-neutral-indicator"
                    title="Tile-only workspace — no active session"
                  />
                ) : (
                  <StateIndicator state={(workspace.status as UISessionState | undefined) || 'idle'} size="md" seed={workspace.id} />
                )}
                <span className="workspace-label">{workspace.title}</span>
                {workspace.endpointId && workspace.sessions[0]?.endpointName && (
                  <span className={`session-endpoint-badge status-${workspace.sessions[0].endpointStatus || 'connected'}`}>
                    {workspace.sessions[0].endpointName}
                  </span>
                )}
                <span className="session-shortcut">⌘{workspaceIndex + 1}</span>
                {(onRenameWorkspace || onMuteWorkspace || onPinWorkspace) && (
                  <span className="workspace-actions">
                    {onPinWorkspace && (
                      <button
                        type="button"
                        className={`workspace-action-btn pin-workspace-btn${workspace.pinned ? ' pinned' : ''}`}
                        data-testid={`pin-workspace-${workspace.id}`}
                        onClick={(e) => {
                          e.stopPropagation();
                          onPinWorkspace(workspace.id, !workspace.pinned);
                        }}
                        title={workspace.pinned ? 'Unpin workspace' : 'Pin workspace'}
                        aria-label={`${workspace.pinned ? 'Unpin' : 'Pin'} workspace ${workspace.title}`}
                      >
                        {workspace.pinned ? '\u{1F4CC}' : '\u{1F4CD}'}
                      </button>
                    )}
                    {onRenameWorkspace && (
                      <button
                        type="button"
                        className="workspace-action-btn rename-workspace-btn"
                        data-testid={`rename-workspace-${workspace.id}`}
                        onClick={(e) => openRename('workspace', workspace.id, workspace.title, e)}
                        title="Rename workspace"
                        aria-label={`Rename workspace ${workspace.title}`}
                      >
                        ✎
                      </button>
                    )}
                    {onMuteWorkspace && (
                    <button
                      type="button"
                      className="workspace-action-btn mute-workspace-btn"
                      data-testid={`mute-workspace-${workspace.id}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        onMuteWorkspace(workspace.id, workspace.endpointId);
                      }}
                      title="Mute workspace"
                      aria-label={`Mute workspace ${workspace.title}`}
                    >
                      ⊘
                    </button>
                    )}
                  </span>
                )}
              </div>
              {workspace.children.map((child) => {
                if (child.kind === 'tile') {
                  return (
                    <TileSidebarRow
                      key={child.id}
                      workspaceId={workspace.id}
                      tile={child.tile}
                      content={tileContents[tileContentKey(workspace.id, child.tile.tileId)]}
                      selected={selectedTile?.workspaceId === workspace.id && selectedTile.tileId === child.tile.tileId}
                      onSelect={() => onSelectTile?.(workspace.id, child.tile.tileId)}
                      onClose={() => onCloseTile?.(workspace.id, child.tile.tileId)}
                      onReload={() => onReloadTile?.(workspace.id, child.tile.tileId)}
                    />
                  );
                }
                const session = child.session;
                const paneId = child.paneId;
                const draggable = Boolean(paneId && onSessionDragStart);
                return (
                  <div
                    key={session.id}
                    className={`session-item grouped ${selectedId === session.id ? 'selected' : ''} ${session.state === 'recoverable' ? 'recoverable' : ''} ${draggable ? 'session-item--draggable' : ''} ${draggingSessionId === session.id ? 'session-item--dragging' : ''}`.trim().replace(/\s+/g, ' ')}
                    data-testid={`sidebar-session-${session.id}`}
                    data-state={session.state}
                    onClick={() => onSelectSession(session.id)}
                    onClickCapture={draggable ? handleSessionClickCapture : undefined}
                    onPointerDown={draggable && paneId
                      ? (event) => handleSessionPointerDown(workspace, paneId, session.id, session.label, event)
                      : undefined}
                    title={session.state === 'recoverable' ? 'Session will be recovered when opened' : undefined}
                  >
                    <StateIndicator state={session.state} size="md" seed={session.id} />
                    <span className="session-label">{session.label}</span>
                    {session.endpointName && (
                      <span className={`session-endpoint-badge status-${session.endpointStatus || 'connected'}`}>
                        {session.endpointName}
                      </span>
                    )}
                    {session.state === 'recoverable' && (
                      <span className="session-recoverable">recoverable</span>
                    )}
                    {session.chiefOfStaff && <ChiefOfStaffBadge />}
                    {session.delegatedFromChief && <DelegatedFromChiefBadge />}
                    {session.isWorktree && <span className="worktree-indicator">⎇</span>}
                    <div className="session-actions">
                      <button
                        className="session-action-btn session-more-btn"
                        data-testid={`session-actions-${session.id}`}
                        onClick={(event) => openSessionActions(session, event)}
                        title={`Session actions (${formatShortcut('session.close')} closes)`}
                        aria-label={`Actions for ${session.label}`}
                      >
                        •••
                      </button>
                    </div>
                    {(() => {
                      const nudgeMode = deriveNudgeMode({
                        ticketUnread: session.ticketUnread,
                        nudgeFiresAt: session.nudgeFiresAt,
                        state: session.state,
                        isActive: selectedId === session.id,
                      });
                      return nudgeMode ? (
                        <SidebarNudgeBar
                          mode={nudgeMode}
                          firesAt={session.nudgeFiresAt}
                          onTrigger={() => onTriggerNudge?.(session.id)}
                        />
                      ) : null;
                    })()}
                  </div>
                );
              })}
              </div>
              {workspace.id === lastReorderParticipantId && renderReorderSeam(reorderTrailingSeamIndex)}
            </div>
          );
        })}
        {leafDrag && (
          <div
            className={`new-workspace-dropzone${newWorkspaceDropActive ? ' new-workspace-dropzone--active' : ''}`}
            data-testid="new-workspace-dropzone"
            onPointerEnter={() => setNewWorkspaceDropActive(true)}
            onPointerLeave={() => setNewWorkspaceDropActive(false)}
            onPointerUp={() => {
              setNewWorkspaceDropActive(false);
              onNewWorkspaceDrop?.();
            }}
          >
            <span className="new-workspace-dropzone-plus">＋</span>
            <span className="new-workspace-dropzone-label">New workspace</span>
          </div>
        )}
        {sessionDragGhost && (
          <div
            className="session-drag-ghost"
            data-testid="session-drag-ghost"
            style={{ left: sessionDragGhost.x + 12, top: sessionDragGhost.y + 12 }}
          >
            {sessionDragGhost.label}
          </div>
        )}
      </div>

      {mutedWorkspaces.length > 0 && (
        <div className="muted-sessions-section">
          <button
            className="muted-sessions-header"
            onClick={() => setMutedExpanded(!mutedExpanded)}
            aria-expanded={mutedExpanded}
          >
            <span className={`muted-sessions-chevron ${mutedExpanded ? 'expanded' : ''}`}>▸</span>
            Muted Workspaces ({mutedWorkspaces.length})
          </button>
          {mutedExpanded && (
            <div className="muted-sessions-list">
              {mutedWorkspaces.map((workspace) => (
                <div
                  key={`${workspace.endpointId || 'local'}:${workspace.id}`}
                  className={`workspace-group muted-workspace ${selectedWorkspaceId === workspace.id ? 'selected' : ''}${workspaceDragClass(workspace)}`}
                  data-testid={`sidebar-muted-workspace-${workspace.id}`}
                  onPointerEnter={() => {
                    if (canAcceptLeafDrag(workspace)) {
                      onWorkspaceDragEnter?.(workspace);
                    }
                  }}
                  onPointerLeave={() => {
                    if (canAcceptLeafDrag(workspace)) {
                      onWorkspaceDragLeave?.(workspace);
                    }
                  }}
                  onPointerUp={() => {
                    if (canAcceptLeafDrag(workspace)) {
                      onWorkspaceDragDrop?.(workspace);
                    }
                  }}
                >
                  <div
                    role="button"
                    tabIndex={0}
                    className="workspace-group-header"
                    onClick={() => onSelectWorkspace(workspace.id)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault();
                        onSelectWorkspace(workspace.id);
                      }
                    }}
                  >
                    <StateIndicator state={(workspace.status as UISessionState | undefined) || 'idle'} size="md" seed={workspace.id} />
                    <span className="workspace-label">{workspace.title}</span>
                    {onMuteWorkspace && (
                      <span className="workspace-actions">
                        <button
                          type="button"
                          className="workspace-action-btn unmute-workspace-btn"
                          onClick={(e) => {
                            e.stopPropagation();
                            onMuteWorkspace(workspace.id, workspace.endpointId);
                          }}
                          title="Unmute workspace"
                          aria-label={`Unmute workspace ${workspace.title}`}
                        >
                          ⊙
                        </button>
                      </span>
                    )}
                  </div>
                  <div className="muted-workspace-sessions">
                    {workspace.children.map((child) => {
                      if (child.kind === 'tile') {
                        return (
                          <TileSidebarRow
                            key={child.id}
                            workspaceId={workspace.id}
                            tile={child.tile}
                            content={tileContents[tileContentKey(workspace.id, child.tile.tileId)]}
                            selected={selectedTile?.workspaceId === workspace.id && selectedTile.tileId === child.tile.tileId}
                            muted
                            onSelect={() => onSelectTile?.(workspace.id, child.tile.tileId)}
                            onClose={() => onCloseTile?.(workspace.id, child.tile.tileId)}
                            onReload={() => onReloadTile?.(workspace.id, child.tile.tileId)}
                          />
                        );
                      }
                      const session = child.session;
                      return (
                        <div
                          key={session.id}
                          className={`session-item grouped muted-session ${selectedId === session.id ? 'selected' : ''}`}
                          data-testid={`sidebar-session-${session.id}`}
                          data-state={session.state}
                          onClick={() => onSelectSession(session.id)}
                        >
                          <StateIndicator state={session.state} size="md" seed={session.id} />
                          <span className="session-label">{session.label}</span>
                          {session.chiefOfStaff && <ChiefOfStaffBadge />}
                          {session.delegatedFromChief && <DelegatedFromChiefBadge />}
                          {session.endpointName && (
                            <span className={`session-endpoint-badge status-${session.endpointStatus || 'connected'}`}>
                              {session.endpointName}
                            </span>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      <div className={`sidebar-footer ${dockCollapsed ? 'sidebar-footer--collapsed' : ''}`.trim()}>
        <div className="sidebar-footer-head">
          <span className="sidebar-footer-label">Dock</span>
          {onToggleDockCollapsed && (
            <button
              type="button"
              className="dock-collapse-btn"
              onClick={onToggleDockCollapsed}
              aria-expanded={!dockCollapsed}
              title={dockCollapsed ? 'Show dock' : 'Hide dock'}
              aria-label={dockCollapsed ? 'Show dock' : 'Hide dock'}
            >
              {dockCollapsed ? '+' : '−'}
            </button>
          )}
        </div>
        {!dockCollapsed && (
          <div className="sidebar-dock-items">
            {dockItems.map((item) => (
              item.onClick ? (
                <button
                  key={item.id}
                  type="button"
                  className={`shortcut-hint shortcut-hint--action ${item.active ? 'active' : ''}`.trim()}
                  data-active={item.active ? 'true' : 'false'}
                  onClick={item.onClick}
                  title={item.label}
                >
                  {item.keys && <span className="shortcut-hint-keys">{item.keys}</span>}
                  {item.label}
                </button>
              ) : (
                <span
                  key={item.id}
                  className={`shortcut-hint ${item.active ? 'active' : ''}`.trim()}
                  data-active={item.active ? 'true' : 'false'}
                >
                  {item.keys && <span className="shortcut-hint-keys">{item.keys}</span>}
                  {item.label}
                </span>
              )
            ))}
          </div>
        )}
      </div>

      {renameTarget && (
        <RenamePopover
          key={`${renameTarget.kind}:${renameTarget.id}`}
          initialValue={renameTarget.name}
          label={renameTarget.kind === 'workspace' ? 'Rename workspace' : 'Rename session'}
          anchor={renameTarget.anchor}
          onSubmit={async (value) => {
            if (renameTarget.kind === 'workspace') {
              await onRenameWorkspace?.(renameTarget.id, value);
            } else {
              await onRenameSession?.(renameTarget.id, value);
            }
          }}
          onClose={() => setRenameTarget(null)}
        />
      )}
      {sessionActionsTarget && (
        <SessionActionsPopover
          sessionLabel={sessionActionsTarget.label}
          chiefOfStaff={sessionActionsTarget.chiefOfStaff}
          anchor={sessionActionsTarget.anchor}
          canRename={Boolean(onRenameSession)}
          onRename={() => {
            setRenameTarget({
              kind: 'session',
              id: sessionActionsTarget.id,
              name: sessionActionsTarget.label,
              anchor: sessionActionsTarget.anchor,
            });
          }}
          onChangeChiefOfStaff={(enabled) => onChangeChiefOfStaff?.(sessionActionsTarget.id, enabled)}
          onCloseSession={() => onCloseSession(sessionActionsTarget.id)}
          onReloadSession={() => onReloadSession(sessionActionsTarget.id)}
          onClose={() => setSessionActionsTarget(null)}
        />
      )}
    </div>
  );
}
