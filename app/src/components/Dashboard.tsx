// app/src/components/Dashboard.tsx
import { useState, useMemo, useCallback } from 'react';
import { DaemonPR, DaemonSettings } from '../hooks/useDaemonSocket';
import { PRActions } from './PRActions';
import { SettingsModal } from './SettingsModal';
import { useDaemonContext } from '../contexts/DaemonContext';
import { useDaemonStore } from '../store/daemonSessions';
import './Dashboard.css';

interface DashboardProps {
  sessions: Array<{
    id: string;
    label: string;
    state: 'working' | 'waiting_input' | 'idle';
    cwd: string;
  }>;
  prs: DaemonPR[];
  isLoading: boolean;
  isRefreshing?: boolean;
  refreshError?: string | null;
  settings: DaemonSettings;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onRefreshPRs?: () => void;
  onOpenPR?: (pr: DaemonPR) => void;
  onSetSetting: (key: string, value: string) => void;
}

export function Dashboard({
  sessions,
  prs,
  isLoading,
  isRefreshing,
  refreshError,
  settings,
  onSelectSession,
  onNewSession,
  onRefreshPRs,
  onOpenPR,
  onSetSetting,
}: DashboardProps) {
  const waitingSessions = sessions.filter((s) => s.state === 'waiting_input');
  const workingSessions = sessions.filter((s) => s.state === 'working');
  const idleSessions = sessions.filter((s) => s.state === 'idle');

  // Group PRs by repo
  const [collapsedRepos, setCollapsedRepos] = useState<Set<string>>(new Set());
  const [fadingPRs, setFadingPRs] = useState<Set<string>>(new Set());
  const [settingsOpen, setSettingsOpen] = useState(false);
  const { sendMuteRepo, sendPRVisited } = useDaemonContext();
  const { isRepoMuted, repoStates } = useDaemonStore();

  // Get list of muted repos for settings modal
  const mutedRepos = useMemo(() =>
    repoStates.filter(r => r.muted).map(r => r.repo),
    [repoStates]
  );

  // PRs that are fully hidden (after fade animation)
  const [hiddenPRs, setHiddenPRs] = useState<Set<string>>(new Set());

  // Handle PR action completion (approve/merge success)
  // Only fade out on merge - approved PRs stay visible (dimmed)
  const handleActionComplete = useCallback((prId: string, action: 'approve' | 'merge') => {
    if (action === 'merge') {
      // Add to fading set to trigger CSS animation
      setFadingPRs(prev => new Set(prev).add(prId));
      // After animation completes, fully hide the PR
      setTimeout(() => {
        setHiddenPRs(prev => new Set(prev).add(prId));
      }, 350); // Slightly longer than 0.3s animation
    }
    // For approve, the PR stays visible but will be dimmed via approved_by_me flag
  }, []);

  const prsByRepo = useMemo(() => {
    // Filter PRs using daemon mute state (individual PR mutes in p.muted, repo mutes via isRepoMuted)
    // Also filter out merged PRs after fade animation completes
    const activePRs = prs.filter((p) => !p.muted && !isRepoMuted(p.repo) && !hiddenPRs.has(p.id));
    const grouped = new Map<string, DaemonPR[]>();
    for (const pr of activePRs) {
      const existing = grouped.get(pr.repo) || [];
      grouped.set(pr.repo, [...existing, pr]);
    }
    return grouped;
  }, [prs, isRepoMuted, repoStates, hiddenPRs]);

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
        <div className="header-left">
          <h1>attn</h1>
          <span className="dashboard-subtitle">attention hub</span>
        </div>
        <button
          className="settings-btn"
          onClick={() => setSettingsOpen(true)}
          title="Settings"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
          </svg>
        </button>
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
                  <div className="session-group" data-testid="session-group-waiting">
                    <div className="group-label">Waiting for input</div>
                    {waitingSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot waiting-input" data-testid="state-indicator" />
                        <span className="session-name">{s.label}</span>
                      </div>
                    ))}
                  </div>
                )}
                {workingSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-working">
                    <div className="group-label">Working</div>
                    {workingSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot working" data-testid="state-indicator" />
                        <span className="session-name">{s.label}</span>
                      </div>
                    ))}
                  </div>
                )}
                {idleSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-idle">
                    <div className="group-label">Idle</div>
                    {idleSessions.map((s) => (
                      <div
                        key={s.id}
                        className="session-row clickable"
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <span className="state-dot idle" data-testid="state-indicator" />
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
            <div className="card-header-actions">
              <button
                className={`refresh-btn ${isRefreshing ? 'refreshing' : ''} ${refreshError ? 'error' : ''}`}
                onClick={onRefreshPRs}
                disabled={isRefreshing}
                title={refreshError || 'Refresh PRs (⌘R)'}
              >
                {isRefreshing ? (
                  <span className="refresh-dots">
                    <span /><span /><span />
                  </span>
                ) : refreshError ? (
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                    <circle cx="12" cy="12" r="10" />
                    <line x1="12" y1="8" x2="12" y2="12" />
                    <line x1="12" y1="16" x2="12.01" y2="16" />
                  </svg>
                ) : (
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
                    <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
                    <path d="M21 3v5h-5" />
                  </svg>
                )}
              </button>
              <span className="card-count">{prs.filter((p) => !p.muted && !isRepoMuted(p.repo) && (!p.approved_by_me || p.has_new_changes)).length}</span>
            </div>
          </div>
          <div className="card-body scrollable">
            {isLoading ? (
              <div className="pr-loading">
                <div className="pr-loading-status">Fetching PRs...</div>
                <div className="pr-skeleton-row">
                  <div className="pr-skeleton-dot" />
                  <div className="pr-skeleton-number" />
                  <div className="pr-skeleton-title" />
                </div>
                <div className="pr-skeleton-row">
                  <div className="pr-skeleton-dot" />
                  <div className="pr-skeleton-number" />
                  <div className="pr-skeleton-title" />
                </div>
                <div className="pr-skeleton-row">
                  <div className="pr-skeleton-dot" />
                  <div className="pr-skeleton-number" />
                  <div className="pr-skeleton-title" />
                </div>
              </div>
            ) : prsByRepo.size === 0 ? (
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
                          sendMuteRepo(repo);
                        }}
                        title="Mute all PRs from this repo"
                      >
                        ⊘
                      </button>
                    </div>
                    {!isCollapsed && (
                      <div className="repo-prs">
                        {repoPRs.map((pr) => {
                          // Determine if this is an approved PR without changes (should be dimmed)
                          const isApprovedNoChanges = pr.approved_by_me && !pr.has_new_changes;
                          return (
                          <div
                            key={pr.id}
                            className={`pr-row ${fadingPRs.has(pr.id) ? 'fading-out' : ''} ${isApprovedNoChanges ? 'approved' : ''}`}
                            data-testid="pr-card"
                          >
                            <a
                              href={pr.url}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="pr-link"
                              onClick={() => sendPRVisited(pr.id)}
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
                            <div className="pr-badges">
                              {pr.has_new_changes && (
                                <span className="badge-changes" title="New commits/comments since your last visit">updated</span>
                              )}
                              {pr.approved_by_me && (
                                <span className="badge-approved" title="You approved this PR">✓</span>
                              )}
                              {pr.ci_status && pr.ci_status !== 'none' && (
                                <span className={`ci-status ${pr.ci_status}`} title={`CI ${pr.ci_status}`}></span>
                              )}
                            </div>
                            <PRActions
                              repo={pr.repo}
                              number={pr.number}
                              prId={pr.id}
                              onActionComplete={handleActionComplete}
                              onOpen={onOpenPR ? () => onOpenPR(pr) : undefined}
                            />
                          </div>
                        );})}
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
        <span className="shortcut"><kbd>⌘R</kbd> refresh PRs</span>
      </footer>

      <SettingsModal
        isOpen={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        mutedRepos={mutedRepos}
        onUnmuteRepo={sendMuteRepo}
        settings={settings}
        onSetSetting={onSetSetting}
      />
    </div>
  );
}
