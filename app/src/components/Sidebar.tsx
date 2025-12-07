import './Sidebar.css';

interface Session {
  id: string;
  label: string;
  state: 'working' | 'waiting';
}

interface SidebarProps {
  sessions: Session[];
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
}

export function Sidebar({
  sessions,
  selectedId,
  onSelectSession,
  onNewSession,
  onCloseSession,
}: SidebarProps) {
  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <h2>Sessions</h2>
        <button className="new-session-btn" onClick={onNewSession} title="New Session">
          +
        </button>
      </div>
      <div className="session-list">
        {sessions.length === 0 ? (
          <div className="empty-state">
            No sessions
            <button className="start-session-btn" onClick={onNewSession}>
              Start a session
            </button>
          </div>
        ) : (
          sessions.map((session) => (
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
  );
}
