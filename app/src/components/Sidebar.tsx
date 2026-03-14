import './Sidebar.css';
import type { ReactNode } from 'react';
import { StateIndicator } from './StateIndicator';
import { isAttentionSessionState, type UISessionState } from '../types/sessionState';
import type { SessionGroup } from '../utils/sessionGrouping';

interface LocalSession {
  id: string;
  label: string;
  state: UISessionState;
  agent?: string;
  branch?: string;
  isWorktree?: boolean;
  cwd?: string;
  recoverable?: boolean;
  reviewLoopStatus?: string;
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

interface SidebarProps {
  sessionGroups: SessionGroup<LocalSession>[];
  visualOrder: LocalSession[];
  visualIndexBySessionId: Map<string, number>;
  selectedId: string | null;
  collapsed: boolean;
  headerActions: SidebarHeaderAction[];
  footerShortcuts?: FooterShortcut[];
  onSelectSession: (id: string) => void;
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

export function Sidebar({
  sessionGroups,
  visualOrder,
  visualIndexBySessionId,
  selectedId,
  collapsed,
  headerActions,
  footerShortcuts,
  onSelectSession,
  onNewSession,
  onCloseSession,
  onReloadSession,
  onGoToDashboard,
  onToggleCollapse,
}: SidebarProps) {
  const visualIndexOf = (id: string) => visualIndexBySessionId.get(id) ?? -1;
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
          {visualOrder.map((session) => (
            <button
              key={session.id}
              className={`icon-btn session-icon ${selectedId === session.id ? 'active' : ''}`}
              onClick={() => onSelectSession(session.id)}
              title={`${session.label} (⌘${visualIndexOf(session.id) + 1})`}
            >
              ▸
              {isAttentionSessionState(session.state) && (
                <span className={`mini-badge ${session.state === 'pending_approval' ? 'pending' : ''} ${session.state === 'unknown' ? 'unknown' : ''}`} />
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
    <div className="sidebar">
      <div className="sidebar-header">
        <div className="sidebar-tool-row">
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
        </div>
        <div className="sidebar-header-row">
          <button className="home-btn" onClick={onGoToDashboard} title="Dashboard (⌘G)" aria-label="Dashboard">
            <HomeIcon />
          </button>
          <span className="sidebar-title">Sessions</span>
          <span className="home-shortcut">⌘G</span>
          <button className="new-session-btn" onClick={onNewSession} title="New Session (⌘N)" aria-label="New Session">
            <PlusIcon />
          </button>
          <button className="collapse-btn" onClick={onToggleCollapse} title="Collapse sidebar" aria-label="Collapse sidebar">
            <CollapseIcon />
          </button>
        </div>
      </div>

      <div className="session-list">
        {sessionGroups.map((group) => {
          const isSingleSession = group.sessions.length === 1;

          if (isSingleSession) {
            const session = group.sessions[0];
            const globalIndex = visualIndexOf(session.id);
            return (
              <div
                key={session.id}
                className={`session-item ${selectedId === session.id ? 'selected' : ''} ${session.recoverable ? 'recoverable' : ''} ${session.reviewLoopStatus ? `session-item--loop-${session.reviewLoopStatus}` : ''}`}
                data-testid={`sidebar-session-${session.id}`}
                data-state={session.state}
                onClick={() => onSelectSession(session.id)}
                title={session.recoverable ? 'Session will be recovered when opened' : undefined}
              >
                <StateIndicator state={session.state} size="md" seed={session.id} />
                <div className="session-info">
                  <span className="session-label">{session.label}</span>
                  {session.branch && (
                    <span className="session-branch">{session.branch}</span>
                  )}
                  {session.recoverable && (
                    <span className="session-recoverable">recoverable</span>
                  )}
                  {reviewLoopIndicator(session.reviewLoopStatus) && (
                    <span
                      className={`session-loop-indicator session-loop-indicator--${session.reviewLoopStatus}`}
                      title={reviewLoopIndicator(session.reviewLoopStatus)?.label}
                      aria-label={reviewLoopIndicator(session.reviewLoopStatus)?.label}
                    >
                      {reviewLoopIndicator(session.reviewLoopStatus)?.glyph}
                    </span>
                  )}
                </div>
                {session.isWorktree && <span className="worktree-indicator">⎇</span>}
                <span className="session-shortcut">⌘{globalIndex + 1}</span>
                <div className="session-actions">
                  <button
                    className="session-action-btn close-session-btn"
                    onClick={(e) => {
                      e.stopPropagation();
                      onCloseSession(session.id);
                    }}
                    title="Close session (⌘⇧W)"
                  >
                    ×
                  </button>
                  <button
                    className="session-action-btn reload-session-btn"
                    data-testid={`reload-session-${session.id}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      onReloadSession(session.id);
                    }}
                    title="Reload session"
                  >
                    ↻
                  </button>
                </div>
              </div>
            );
          }

          return (
            <div key={group.directory} className="session-group">
              <div className="session-group-header">
                <span className="session-label">{group.label}</span>
                {group.branch && (
                  <span className="session-branch">{group.branch}</span>
                )}
              </div>
              {group.sessions.map((session) => {
                const globalIndex = visualIndexOf(session.id);
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
                    {session.recoverable && (
                      <span className="session-recoverable">recoverable</span>
                    )}
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
                    <span className="session-shortcut">⌘{globalIndex + 1}</span>
                    <div className="session-actions">
                      <button
                        className="session-action-btn close-session-btn"
                        onClick={(e) => {
                          e.stopPropagation();
                          onCloseSession(session.id);
                        }}
                        title="Close session (⌘⇧W)"
                      >
                        ×
                      </button>
                      <button
                        className="session-action-btn reload-session-btn"
                        data-testid={`reload-session-${session.id}`}
                        onClick={(e) => {
                          e.stopPropagation();
                          onReloadSession(session.id);
                        }}
                        title="Reload session"
                      >
                        ↻
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          );
        })}
      </div>

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
        <span className="shortcut-hint">⌘⇧B sidebar</span>
      </div>
    </div>
  );
}
