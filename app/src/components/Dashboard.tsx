// app/src/components/Dashboard.tsx
import { useState, useMemo } from 'react';
import { DaemonSession, DaemonPR } from '../hooks/useDaemonSocket';
import { PRActions } from './PRActions';
import { useMuteStore } from '../store/mutes';
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

  // Group PRs by repo
  const [collapsedRepos, setCollapsedRepos] = useState<Set<string>>(new Set());
  const { mutedPRs, mutedRepos, muteRepo } = useMuteStore();

  const prsByRepo = useMemo(() => {
    const activePRs = prs.filter((p) => !p.muted && !mutedPRs.has(p.id) && !mutedRepos.has(p.repo));
    const grouped = new Map<string, DaemonPR[]>();
    for (const pr of activePRs) {
      const existing = grouped.get(pr.repo) || [];
      grouped.set(pr.repo, [...existing, pr]);
    }
    return grouped;
  }, [prs, mutedPRs, mutedRepos]);

  const toggleRepo = (repo: string) => {
    setCollapsedRepos((prev) => {
      const next = new Set(prev);
      if (next.has(repo)) {
        next.delete(repo);
      } else {
        next.add(repo);
      }
      return next;
    });
  };

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

        {/* PRs Card */}
        <div className="dashboard-card">
          <div className="card-header">
            <h2>Pull Requests</h2>
            <span className="card-count">{prs.filter((p) => !p.muted && !mutedPRs.has(p.id) && !mutedRepos.has(p.repo)).length}</span>
          </div>
          <div className="card-body scrollable">
            {prsByRepo.size === 0 ? (
              <div className="card-empty">No PRs need attention</div>
            ) : (
              Array.from(prsByRepo.entries()).map(([repo, repoPRs]) => {
                const repoName = repo.split('/')[1] || repo;
                const isCollapsed = collapsedRepos.has(repo);
                const reviewCount = repoPRs.filter((p) => p.role === 'reviewer').length;
                const authorCount = repoPRs.filter((p) => p.role === 'author').length;

                return (
                  <div key={repo} className="pr-repo-group">
                    <div className="repo-header">
                      <div
                        className="repo-header-content clickable"
                        onClick={() => toggleRepo(repo)}
                      >
                        <span className={`collapse-icon ${isCollapsed ? 'collapsed' : ''}`}>▾</span>
                        <span className="repo-name">{repoName}</span>
                        <span className="repo-counts">
                          {reviewCount > 0 && <span className="count review">{reviewCount} review</span>}
                          {authorCount > 0 && <span className="count author">{authorCount} yours</span>}
                        </span>
                      </div>
                      <button
                        className="repo-mute-btn"
                        onClick={(e) => {
                          e.stopPropagation();
                          muteRepo(repo);
                        }}
                        title="Mute all PRs from this repo"
                      >
                        ⊘
                      </button>
                    </div>
                    {!isCollapsed && (
                      <div className="repo-prs">
                        {repoPRs.map((pr) => (
                          <div key={pr.id} className="pr-row" data-testid="pr-card">
                            <a
                              href={pr.url}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="pr-link"
                            >
                              <span className={`pr-role ${pr.role}`}>
                                {pr.role === 'reviewer' ? '👀' : '✏️'}
                              </span>
                              <span className="pr-number">#{pr.number}</span>
                              <span className="pr-title">{pr.title}</span>
                              {pr.role === 'author' && (
                                <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
                              )}
                            </a>
                            <PRActions repo={pr.repo} number={pr.number} prId={pr.id} />
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                );
              })
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
