import './Sidebar.css';

interface LocalSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
}

interface SidebarProps {
  sessions: LocalSession[];
  selectedId: string | null;
  collapsed: boolean;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onCloseSession: (id: string) => void;
  onGoToDashboard: () => void;
  onToggleCollapse: () => void;
}

export function Sidebar({
  sessions,
  selectedId,
  collapsed,
  onSelectSession,
  onNewSession,
  onCloseSession,
  onGoToDashboard,
  onToggleCollapse,
}: SidebarProps) {
  if (collapsed) {
    return (
      <div className="sidebar collapsed">
        <div className="icon-rail">
          <button className="icon-btn" onClick={onGoToDashboard} title="Dashboard (⌘D)">
            ⌂
          </button>
          <div className="icon-divider" />
          {sessions.map((session, index) => (
            <button
              key={session.id}
              className={`icon-btn session-icon ${selectedId === session.id ? 'active' : ''}`}
              onClick={() => onSelectSession(session.id)}
              title={`${session.label} (⌘${index + 1})`}
            >
              ▸
              {session.state === 'waiting_input' && <span className="mini-badge" />}
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
        {sessions.map((session, index) => (
          <div
            key={session.id}
            className={`session-item ${selectedId === session.id ? 'selected' : ''}`}
            data-testid={`sidebar-session-${session.id}`}
            data-state={session.state}
            onClick={() => onSelectSession(session.id)}
          >
            <span className={`state-indicator ${session.state}`} data-testid="state-indicator" />
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
        <span className="shortcut-hint">⌘B sidebar</span>
      </div>
    </div>
  );
}
