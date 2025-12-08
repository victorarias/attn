// app/src/components/Dashboard.tsx
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';
import './Dashboard.css';

interface DashboardProps {
  sessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting';
    cwd: string;
  }>;
  daemonSessions: DaemonSession[];
  prs: DaemonPR[];
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
}

export function Dashboard({
  sessions,
  daemonSessions: _daemonSessions,
  prs,
  onSelectSession,
  onNewSession,
}: DashboardProps) {
  const waitingSessions = sessions.filter((s) => s.state === 'waiting');
  const workingSessions = sessions.filter((s) => s.state === 'working');

  return (
    <div className="dashboard">
      <header className="dashboard-header">
        <h1>attn</h1>
        <span className="dashboard-subtitle">attention hub</span>
      </header>

      <div className="dashboard-grid">
        {/* Sessions Card */}
        <div className="dashboard-card">
          <div className="card-header">
            <h2>Sessions</h2>
            <button className="card-action" onClick={onNewSession}>
              + New
            </button>
          </div>
          <div className="card-body">
            {sessions.length === 0 ? (
              <div className="card-empty">No active sessions</div>
            ) : (
              <>
                {waitingSessions.length > 0 && (
                  <div className="session-group">
                    <div className="group-label">Waiting for input</div>
                    {waitingSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot waiting" />
                        <span className="session-name">{s.label}</span>
                      </div>
                    ))}
                  </div>
                )}
                {workingSessions.length > 0 && (
                  <div className="session-group">
                    <div className="group-label">Working</div>
                    {workingSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot working" />
                        <span className="session-name">{s.label}</span>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        </div>

        {/* PRs Card - placeholder for now */}
        <div className="dashboard-card">
          <div className="card-header">
            <h2>Pull Requests</h2>
            <span className="card-count">{prs.length}</span>
          </div>
          <div className="card-body">
            {prs.length === 0 ? (
              <div className="card-empty">No PRs need attention</div>
            ) : (
              <div className="card-empty">PR list coming soon</div>
            )}
          </div>
        </div>
      </div>

      <footer className="dashboard-footer">
        <span className="shortcut"><kbd>⌘N</kbd> new session</span>
        <span className="shortcut"><kbd>⌘1-9</kbd> switch session</span>
      </footer>
    </div>
  );
}
