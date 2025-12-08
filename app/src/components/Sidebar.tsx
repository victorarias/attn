import './Sidebar.css';

interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting';
}

interface SidebarProps {
  sessions: LocalSession[];
  selectedId: string | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onGoToDashboard: () => void;
}

export function Sidebar({
  sessions,
  selectedId,
  onSelectSession,
  onNewSession,
  onCloseSession,
  onGoToDashboard,
}: SidebarProps) {
  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <button className="home-btn" onClick={onGoToDashboard} title="Dashboard (⌘D)">
          ⌂
        </button>
        <span className="sidebar-title">Sessions</span>
        <button className="new-session-btn" onClick={onNewSession} title="New Session (⌘N)">
          +
        </button>
      </div>

      <div className="session-list">
        {sessions.map((session, index) => (
          <div
            key={session.id}
            className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
            onClick={() => onSelectSession(session.id)}
          >
            <span className={`state-indicator ${session.state}`} />
            <span className="session-label">{session.label}</span>
            <span className="session-shortcut">⌘{index + 1}</span>
            <button
              className="close-session-btn"
              onClick={(e) => {
                e.stopPropagation();
                onCloseSession(session.id);
              }}
              title="Close session (⌘W)"
            >
              ×
            </button>
          </div>
        ))}
      </div>

      <div className="sidebar-footer">
        <span className="shortcut-hint">⌘K drawer</span>
      </div>
    </div>
  );
}
