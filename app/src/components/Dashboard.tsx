// app/src/components/Dashboard.tsx
import { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import { DaemonPR, RateLimitState } from '../hooks/useDaemonSocket';
import { usePRsNeedingAttention } from '../hooks/usePRsNeedingAttention';
import { PRActions } from './PRActions';
import { StateIndicator } from './StateIndicator';
import { useDaemonContext } from '../contexts/DaemonContext';
import { getRepoName } from '../utils/repo';
import { openUrl } from '@tauri-apps/plugin-opener';
import type { UISessionState } from '../types/sessionState';
import { isTerminalDebugEnabled, formatResizeLog } from '../utils/terminalDebug';
import {
  clearTerminalRuntimeLog,
  isTerminalRuntimeTraceEnabled,
  setTerminalRuntimeTraceEnabled,
} from '../utils/terminalRuntimeLog';
import appIcon from '../assets/icon.png';
import './Dashboard.css';

type DashboardSession = {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  endpointName?: string;
  endpointStatus?: string;
  reviewLoopStatus?: string;
};

interface DashboardProps {
  sessions: DashboardSession[];
  mutedSessions?: DashboardSession[];
  prs: DaemonPR[];
  isLoading: boolean;
  isRefreshing?: boolean;
  refreshError?: string | null;
  rateLimit?: RateLimitState | null;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  onRefreshPRs?: () => void;
  onOpenPR?: (pr: DaemonPR) => void;
  onOpenSettings: () => void;
  onMutedGroupClick?: () => void;
}

export function Dashboard({
  sessions,
  mutedSessions = [],
  prs,
  isLoading,
  isRefreshing,
  refreshError,
  rateLimit,
  onSelectSession,
  onNewSession,
  onRefreshPRs,
  onOpenPR,
  onOpenSettings,
  onMutedGroupClick,
}: DashboardProps) {
  const reviewLoopIndicator = (status?: string): { glyph: string; label: string } | null => {
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
  };

  const renderEndpointBadge = (session: DashboardProps['sessions'][number]) => {
    if (!session.endpointName) {
      return null;
    }
    return (
      <span className={`session-endpoint-badge status-${session.endpointStatus || 'connected'}`}>
        {session.endpointName}
      </span>
    );
  };

  const waitingSessions = sessions.filter((s) => s.state === 'waiting_input');
  const pendingApprovalSessions = sessions.filter((s) => s.state === 'pending_approval');
  const launchingSessions = sessions.filter((s) => s.state === 'launching');
  const workingSessions = sessions.filter((s) => s.state === 'working');
  const idleSessions = sessions.filter((s) => s.state === 'idle');
  const unknownSessions = sessions.filter((s) => s.state === 'unknown');

  // Group PRs by repo
  const [collapsedRepos, setCollapsedRepos] = useState<Set<string>>(new Set());
  const [fadingPRs, setFadingPRs] = useState<Set<string>>(new Set());
  const { sendMuteRepo, sendPRVisited } = useDaemonContext();

  // PRs that are fully hidden (after fade animation)
  const [hiddenPRs, setHiddenPRs] = useState<Set<string>>(new Set());

  // Use centralized PR filtering hook
  const { activePRs, needsAttention } = usePRsNeedingAttention(prs, hiddenPRs);

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

  // Track hosts per repo (for host badges)
  const repoHosts = useMemo(() => {
    const map = new Map<string, Set<string>>();
    for (const pr of activePRs) {
      if (!pr.host) continue;
      const existing = map.get(pr.repo) || new Set<string>();
      existing.add(pr.host);
      map.set(pr.repo, existing);
    }
    return map;
  }, [activePRs]);

  // Group active PRs by repo
  const prsByRepo = useMemo(() => {
    const grouped = new Map<string, DaemonPR[]>();
    for (const pr of activePRs) {
      const existing = grouped.get(pr.repo) || [];
      grouped.set(pr.repo, [...existing, pr]);
    }
    return grouped;
  }, [activePRs]);

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

  // Terminal resize debug toggle
  const [termDebug, setTermDebug] = useState(isTerminalDebugEnabled);
  const [runtimeTraceEnabled, setRuntimeTraceEnabled] = useState(isTerminalRuntimeTraceEnabled);
  const copyBtnRef = useRef<HTMLButtonElement>(null);
  const toggleTermDebug = useCallback(() => {
    const next = !termDebug;
    try { window.localStorage.setItem('attn:terminal-debug', next ? '1' : '0'); } catch {}
    setTermDebug(next);
  }, [termDebug]);
  const toggleRuntimeTrace = useCallback(() => {
    const next = !runtimeTraceEnabled;
    setTerminalRuntimeTraceEnabled(next);
    if (next) {
      clearTerminalRuntimeLog();
    }
    setRuntimeTraceEnabled(next);
  }, [runtimeTraceEnabled]);
  const clearRuntimeTrace = useCallback(() => {
    clearTerminalRuntimeLog();
  }, []);
  const copyResizeLog = useCallback(() => {
    const log = formatResizeLog();
    navigator.clipboard.writeText(log).then(() => {
      const btn = copyBtnRef.current;
      if (btn) { btn.textContent = 'Copied!'; setTimeout(() => { btn.textContent = 'Copy resize log'; }, 1500); }
    }).catch(console.error);
  }, []);

  // Rate limit countdown
  const [rateLimitCountdown, setRateLimitCountdown] = useState<string | null>(null);
  useEffect(() => {
    if (!rateLimit) {
      setRateLimitCountdown(null);
      return;
    }

    const updateCountdown = () => {
      const now = Date.now();
      const resetTime = rateLimit.resetAt.getTime();
      const diff = resetTime - now;

      if (diff <= 0) {
        setRateLimitCountdown(null);
        return;
      }

      const minutes = Math.floor(diff / 60000);
      const seconds = Math.floor((diff % 60000) / 1000);
      setRateLimitCountdown(`${minutes}m ${seconds}s`);
    };

    updateCountdown();
    const interval = setInterval(updateCountdown, 1000);
    return () => clearInterval(interval);
  }, [rateLimit]);

  return (
    <div className="dashboard">
      <header className="dashboard-header">
        <div className="header-left">
          <img src={appIcon} alt="" className="dashboard-icon" />
          <div className="header-text">
            <h1>attn</h1>
            <span className="dashboard-subtitle">attention hub</span>
          </div>
        </div>
        <button
          className="settings-btn"
          onClick={onOpenSettings}
          title="Settings"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
          </svg>
        </button>
      </header>

      {/* Rate limit banner */}
      {rateLimitCountdown && (
        <div className="rate-limit-banner">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="12" cy="12" r="10"/>
            <polyline points="12 6 12 12 16 14"/>
          </svg>
          <span>GitHub rate limited. Resuming in {rateLimitCountdown}</span>
        </div>
      )}

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
            {sessions.length === 0 && mutedSessions.length === 0 ? (
              <div className="card-empty">No active sessions</div>
            ) : (
              <>
                {waitingSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-waiting">
                    <div className="group-label">Waiting for input</div>
                    {waitingSessions.map((s) => (
                      <div
                        key={s.id}
                        className={`session-row clickable ${s.reviewLoopStatus ? `session-row--loop-${s.reviewLoopStatus}` : ''}`}
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <StateIndicator state="waiting_input" size="sm" seed={s.id} />
                        <span className="session-name">{s.label}</span>
                        {renderEndpointBadge(s)}
                        {reviewLoopIndicator(s.reviewLoopStatus) && (
                          <span
                            className={`session-loop-indicator session-loop-indicator--${s.reviewLoopStatus}`}
                            title={reviewLoopIndicator(s.reviewLoopStatus)?.label}
                            aria-label={reviewLoopIndicator(s.reviewLoopStatus)?.label}
                          >
                            {reviewLoopIndicator(s.reviewLoopStatus)?.glyph}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
                {pendingApprovalSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-pending">
                    <div className="group-label">Pending approval</div>
                    {pendingApprovalSessions.map((s) => (
                      <div
                        key={s.id}
                        className={`session-row clickable ${s.reviewLoopStatus ? `session-row--loop-${s.reviewLoopStatus}` : ''}`}
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <StateIndicator state="pending_approval" size="sm" seed={s.id} />
                        <span className="session-name">{s.label}</span>
                        {renderEndpointBadge(s)}
                        {reviewLoopIndicator(s.reviewLoopStatus) && (
                          <span className={`session-loop-indicator session-loop-indicator--${s.reviewLoopStatus}`} title={reviewLoopIndicator(s.reviewLoopStatus)?.label} aria-label={reviewLoopIndicator(s.reviewLoopStatus)?.label}>
                            {reviewLoopIndicator(s.reviewLoopStatus)?.glyph}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
                {launchingSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-launching">
                    <div className="group-label">Launching</div>
                    {launchingSessions.map((s) => (
                      <div
                        key={s.id}
                        className={`session-row clickable ${s.reviewLoopStatus ? `session-row--loop-${s.reviewLoopStatus}` : ''}`}
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <StateIndicator state="launching" size="sm" seed={s.id} />
                        <span className="session-name">{s.label}</span>
                        {renderEndpointBadge(s)}
                        {reviewLoopIndicator(s.reviewLoopStatus) && (
                          <span className={`session-loop-indicator session-loop-indicator--${s.reviewLoopStatus}`} title={reviewLoopIndicator(s.reviewLoopStatus)?.label} aria-label={reviewLoopIndicator(s.reviewLoopStatus)?.label}>
                            {reviewLoopIndicator(s.reviewLoopStatus)?.glyph}
                          </span>
                        )}
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
                        className={`session-row clickable ${s.reviewLoopStatus ? `session-row--loop-${s.reviewLoopStatus}` : ''}`}
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <StateIndicator state="working" size="sm" seed={s.id} />
                        <span className="session-name">{s.label}</span>
                        {renderEndpointBadge(s)}
                        {reviewLoopIndicator(s.reviewLoopStatus) && (
                          <span className={`session-loop-indicator session-loop-indicator--${s.reviewLoopStatus}`} title={reviewLoopIndicator(s.reviewLoopStatus)?.label} aria-label={reviewLoopIndicator(s.reviewLoopStatus)?.label}>
                            {reviewLoopIndicator(s.reviewLoopStatus)?.glyph}
                          </span>
                        )}
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
                        className={`session-row clickable ${s.reviewLoopStatus ? `session-row--loop-${s.reviewLoopStatus}` : ''}`}
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <StateIndicator state="idle" size="sm" seed={s.id} />
                        <span className="session-name">{s.label}</span>
                        {renderEndpointBadge(s)}
                        {reviewLoopIndicator(s.reviewLoopStatus) && (
                          <span className={`session-loop-indicator session-loop-indicator--${s.reviewLoopStatus}`} title={reviewLoopIndicator(s.reviewLoopStatus)?.label} aria-label={reviewLoopIndicator(s.reviewLoopStatus)?.label}>
                            {reviewLoopIndicator(s.reviewLoopStatus)?.glyph}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
                {unknownSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-unknown">
                    <div className="group-label">Unknown / error</div>
                    {unknownSessions.map((s) => (
                      <div
                        key={s.id}
                        className={`session-row clickable ${s.reviewLoopStatus ? `session-row--loop-${s.reviewLoopStatus}` : ''}`}
                        data-testid={`session-${s.id}`}
                        data-state={s.state}
                        onClick={() => onSelectSession(s.id)}
                      >
                        <StateIndicator state="unknown" size="sm" seed={s.id} />
                        <span className="session-name">{s.label}</span>
                        {renderEndpointBadge(s)}
                        {reviewLoopIndicator(s.reviewLoopStatus) && (
                          <span className={`session-loop-indicator session-loop-indicator--${s.reviewLoopStatus}`} title={reviewLoopIndicator(s.reviewLoopStatus)?.label} aria-label={reviewLoopIndicator(s.reviewLoopStatus)?.label}>
                            {reviewLoopIndicator(s.reviewLoopStatus)?.glyph}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                )}
                {mutedSessions.length > 0 && (
                  <div
                    className="session-group muted-summary clickable"
                    data-testid="session-group-muted"
                    onClick={onMutedGroupClick}
                  >
                    <div className="group-label dim">Muted Sessions ({mutedSessions.length})</div>
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
              <span className="card-count">{needsAttention.length}</span>
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
                const repoName = getRepoName(repo);
                const isCollapsed = collapsedRepos.has(repo);
                const reviewCount = repoPRs.filter((p) => p.role === 'reviewer').length;
                const authorCount = repoPRs.filter((p) => p.role === 'author').length;
                const showHost = (repoHosts.get(repo)?.size || 0) > 1;

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
                            <button
                              type="button"
                              className="pr-link"
                              onClick={(e) => {
                                e.stopPropagation();
                                sendPRVisited(pr.id);
                                openUrl(pr.url).catch((err) =>
                                  console.error('[Dashboard] Failed to open PR URL:', err)
                                );
                              }}
                            >
                              <span className={`pr-role ${pr.role}`}>
                                {pr.role === 'reviewer'
                                  ? (pr.author?.toLowerCase().includes('bot') ? '🤖' : '👀')
                                  : '✏️'}
                              </span>
                              <span className="pr-number">#{pr.number}</span>
                              {showHost && pr.host && (
                                <span className="pr-host" title={pr.host}>{pr.host}</span>
                              )}
                              <span className="pr-title">{pr.title}</span>
                              {pr.role === 'author' && (
                                <span className="pr-reason">{pr.reason.replace(/_/g, ' ')}</span>
                              )}
                            </button>
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
                              number={pr.number}
                              prId={pr.id}
                              author={pr.author}
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
        <div className="footer-shortcuts">
          <span className="shortcut"><kbd>⌘N</kbd> new session</span>
          <span className="shortcut"><kbd>⌘1-9</kbd> switch session</span>
          <span className="shortcut"><kbd>⌘,</kbd> settings</span>
        </div>
        <div className="footer-debug">
          <button
            className={`debug-toggle ${termDebug ? 'active' : ''}`}
            onClick={toggleTermDebug}
            title="Toggle terminal resize debug overlay on each pane"
          >
            Resize debug {termDebug ? 'ON' : 'off'}
          </button>
          {termDebug && (
            <button
              ref={copyBtnRef}
              className="debug-copy-btn"
              onClick={copyResizeLog}
              title="Copy resize event log to clipboard"
            >
              Copy resize log
            </button>
          )}
          <button
            className={`debug-toggle ${runtimeTraceEnabled ? 'active' : ''}`}
            onClick={toggleRuntimeTrace}
            title="Toggle terminal runtime tracing to AppLocalData/debug/terminal-runtime.jsonl"
          >
            Runtime trace {runtimeTraceEnabled ? 'ON' : 'off'}
          </button>
          {runtimeTraceEnabled && (
            <button
              className="debug-copy-btn"
              onClick={clearRuntimeTrace}
              title="Clear the current terminal runtime trace log"
            >
              Clear runtime trace
            </button>
          )}
        </div>
      </footer>
    </div>
  );
}
