import './Sidebar.css';
import type { MouseEvent as ReactMouseEvent, ReactNode } from 'react';
import { useState } from 'react';
import { RenamePopover } from './RenamePopover';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { SessionActionsPopover } from './SessionActionsPopover';
import { GridLayoutControl } from './grid/GridLayoutControl';
import type { GridLayout } from './grid/gridLayout';
import { StateIndicator } from './StateIndicator';
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
  recoverable?: boolean;
  reviewLoopStatus?: string;
  chiefOfStaff?: boolean;
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

function reviewLoopIndicator(status?: string): { glyph: string; label: string } | null {
  switch (status) {
    case 'running':
      return { glyph: '⟳', label: 'Review loop running' };
    case 'awaiting_user':
      return { glyph: '?', label: 'Review loop needs input' };
    case 'completed':
      return { glyph: '✓', label: 'Review loop completed' };
    case 'stopped':
      return { glyph: '•', label: 'Review loop stopped' };
    case 'error':
      return { glyph: '!', label: 'Review loop error' };
    default:
      return null;
  }
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
  footerShortcuts?: FooterShortcut[];
  mutedWorkspaces?: SidebarWorkspace[];
  mutedExpanded?: boolean;
  onMutedExpandedChange?: (expanded: boolean) => void;
  onMuteWorkspace?: (workspaceId: string, endpointId?: string) => void;
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
  onSelectSession: (id: string) => void;
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
  shortcutHint?: string;
  onClick: () => void;
}

export interface FooterShortcut {
  label: string;
  active?: boolean;
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


export function ReviewLoopIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M8 2.5a5.5 5.5 0 1 0 5.2 7.2" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M10.5 2.7h3v3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="m13.5 2.7-2.9 2.9" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
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
  footerShortcuts,
  mutedWorkspaces = [],
  mutedExpanded: mutedExpandedProp,
  onMutedExpandedChange,
  onMuteWorkspace,
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
  onSelectSession,
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

  const isWorkspaceVisible = (workspace: SidebarWorkspace) => !isSessionless(workspace) || showSessionless;
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
  const dockShortcutHints = Array.from(new Map([
    ...headerActions
      .filter((action) => action.shortcutHint && !action.disabled)
      .map((action) => [action.shortcutHint as string, { label: action.shortcutHint as string, active: false }] as const),
    ...(footerShortcuts ?? []).map((shortcut) => [shortcut.label, shortcut] as const),
  ]).values());

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

      <div className="session-list">
        {visibleWorkspaces.map((workspace) => {
          const workspaceIndex = visualIndexOfWorkspace(workspace.id);
          return (
            <div
              key={`${workspace.endpointId || 'local'}:${workspace.id}`}
              className={`workspace-group ${selectedWorkspaceId === workspace.id ? 'selected' : ''}${workspaceDragClass(workspace)}`}
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
                {(onRenameWorkspace || onMuteWorkspace) && (
                  <span className="workspace-actions">
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
                return (
                  <div
                    key={session.id}
                    className={`session-item grouped ${selectedId === session.id ? 'selected' : ''} ${session.recoverable ? 'recoverable' : ''} ${session.reviewLoopStatus ? `session-item--loop-${session.reviewLoopStatus}` : ''}`}
                    data-testid={`sidebar-session-${session.id}`}
                    data-state={session.state}
                    onClick={() => onSelectSession(session.id)}
                    title={session.recoverable ? 'Session will be recovered when opened' : undefined}
                  >
                    <StateIndicator state={session.state} size="md" seed={session.id} />
                    <span className="session-label">{session.label}</span>
                    {session.endpointName && (
                      <span className={`session-endpoint-badge status-${session.endpointStatus || 'connected'}`}>
                        {session.endpointName}
                      </span>
                    )}
                    {session.recoverable && (
                      <span className="session-recoverable">recoverable</span>
                    )}
                    {session.chiefOfStaff && <ChiefOfStaffBadge />}
                    {reviewLoopIndicator(session.reviewLoopStatus) && (
                      <span
                        className={`session-loop-indicator session-loop-indicator--${session.reviewLoopStatus}`}
                        title={reviewLoopIndicator(session.reviewLoopStatus)?.label}
                        aria-label={reviewLoopIndicator(session.reviewLoopStatus)?.label}
                      >
                        {reviewLoopIndicator(session.reviewLoopStatus)?.glyph}
                      </span>
                    )}
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
                  </div>
                );
              })}
            </div>
          );
        })}
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

      <div className="sidebar-footer">
        <span className="sidebar-footer-label">Dock</span>
        {dockShortcutHints.map((shortcut) => (
          <span
            key={shortcut.label}
            className={`shortcut-hint ${shortcut.active ? 'active' : ''}`.trim()}
            data-active={shortcut.active ? 'true' : 'false'}
          >
            {shortcut.label}
          </span>
        ))}
        <span className="shortcut-hint">{`${formatShortcut('session.toggleSidebar')} sidebar`}</span>
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
