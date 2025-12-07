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
}

export function Sidebar({ sessions, selectedId, onSelectSession }: SidebarProps) {
  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <h2>Sessions</h2>
      </div>
      <div className="session-list">
        {sessions.length === 0 ? (
          <div className="empty-state">No sessions</div>
        ) : (
          sessions.map((session) => (
            <div
              key={session.id}
              className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
              onClick={() => onSelectSession(session.id)}
            >
              <span className={`state-indicator ${session.state}`} />
              <span className="session-label">{session.label}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
