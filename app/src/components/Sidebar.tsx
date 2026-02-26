import './Sidebar.css';
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
}

interface SidebarProps {
  sessionGroups: SessionGroup<LocalSession>[];
  visualOrder: LocalSession[];
  visualIndexBySessionId: Map<string, number>;
  selectedId: string | null;
  collapsed: boolean;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onReloadSession: (id: string) => void;
  onGoToDashboard: () => void;
  onToggleCollapse: () => void;
}

export function Sidebar({
  sessionGroups,
  visualOrder,
  visualIndexBySessionId,
  selectedId,
  collapsed,
  onSelectSession,
  onNewSession,
  onCloseSession,
  onReloadSession,
  onGoToDashboard,
  onToggleCollapse,
}: SidebarProps) {
  const visualIndexOf = (id: string) => visualIndexBySessionId.get(id) ?? -1;

  if (collapsed) {
    return (
      <div className="sidebar collapsed">
        <div className="icon-rail">
          <button className="icon-btn" onClick={onGoToDashboard} title="Dashboard (⌘D)">
            ⌂
          </button>
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
            +
          </button>
          <div className="icon-spacer" />
          <button className="icon-btn expand-btn" onClick={onToggleCollapse} title="Expand sidebar">
            »
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <button className="home-btn" onClick={onGoToDashboard} title="Dashboard (⌘D)">
          ⌂
        </button>
        <span className="home-shortcut">⌘D</span>
        <span className="sidebar-title">Sessions</span>
        <button className="collapse-btn" onClick={onToggleCollapse} title="Collapse sidebar">
          «
        </button>
        <button className="new-session-btn" onClick={onNewSession} title="New Session (⌘N)">
          +
        </button>
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
                className={`session-item ${selectedId === session.id ? 'selected' : ''} ${session.recoverable ? 'recoverable' : ''}`}
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
                    title="Close session (⌘W)"
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
                    className={`session-item grouped ${selectedId === session.id ? 'selected' : ''} ${session.recoverable ? 'recoverable' : ''}`}
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
                    {session.isWorktree && <span className="worktree-indicator">⎇</span>}
                    <span className="session-shortcut">⌘{globalIndex + 1}</span>
                    <div className="session-actions">
                      <button
                        className="session-action-btn close-session-btn"
                        onClick={(e) => {
                          e.stopPropagation();
                          onCloseSession(session.id);
                        }}
                        title="Close session (⌘W)"
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
        <span className="shortcut-hint">⌘K drawer</span>
        <span className="shortcut-hint">⌘B branch</span>
        <span className="shortcut-hint">⌘⇧B sidebar</span>
      </div>
    </div>
  );
}
