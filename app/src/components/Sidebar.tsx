import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';
import './Sidebar.css';

interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting';
}

interface SidebarProps {
  // Local sessions (PTY sessions in this app)
  localSessions: LocalSession[];
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  // Daemon sessions (from cm daemon via WebSocket)
  daemonSessions: DaemonSession[];
  // PRs
  prs: DaemonPR[];
  isConnected: boolean;
}

export function Sidebar({
  localSessions,
  selectedId,
  onSelectSession,
  onNewSession,
  onCloseSession,
  daemonSessions,
  prs,
  isConnected,
}: SidebarProps) {
  // Filter PRs that need attention (waiting and not muted)
  const waitingPRs = prs.filter((pr) => pr.state === 'waiting' && !pr.muted);

  return (
    <div className="sidebar">
      {/* Local Sessions */}
      <div className="sidebar-section">
        <div className="sidebar-header">
          <h2>Sessions</h2>
          <button className="new-session-btn" onClick={onNewSession} title="New Session">
            +
          </button>
        </div>
        <div className="session-list">
          {localSessions.length === 0 ? (
            <div className="empty-state">
              No sessions
              <button className="start-session-btn" onClick={onNewSession}>
                Start a session
              </button>
            </div>
          ) : (
            localSessions.map((session) => (
              <div
                key={session.id}
                className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
                onClick={() => onSelectSession(session.id)}
              >
                <span className={`state-indicator ${session.state}`} />
                <span className="session-label">{session.label}</span>
                <button
                  className="close-session-btn"
                  onClick={(e) => {
                    e.stopPropagation();
                    onCloseSession(session.id);
                  }}
                  title="Close session"
                >
                  ×
                </button>
              </div>
            ))
          )}
        </div>
      </div>

      {/* Daemon Sessions (other cm sessions) */}
      {daemonSessions.length > 0 && (
        <div className="sidebar-section">
          <div className="sidebar-header">
            <h2>Other Sessions</h2>
            <span className={`connection-indicator ${isConnected ? 'connected' : ''}`} />
          </div>
          <div className="session-list">
            {daemonSessions.map((session) => (
              <div key={session.id} className="session-item daemon-session">
                <span className={`state-indicator ${session.state}`} />
                <span className="session-label">{session.label}</span>
                {session.todos && session.todos.length > 0 && (
                  <span className="todo-count">{session.todos.length}</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* PRs needing attention */}
      {waitingPRs.length > 0 && (
        <div className="sidebar-section">
          <div className="sidebar-header">
            <h2>PRs</h2>
            <span className="pr-count">{waitingPRs.length}</span>
          </div>
          <div className="pr-list">
            {waitingPRs.map((pr) => (
              <a
                key={pr.id}
                href={pr.url}
                target="_blank"
                rel="noopener noreferrer"
                className={`pr-item ${pr.role}`}
              >
                <span className="pr-repo">{pr.repo.split('/')[1]}</span>
                <span className="pr-number">#{pr.number}</span>
                <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
              </a>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
