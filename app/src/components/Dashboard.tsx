// app/src/components/Dashboard.tsx
import { useState, useMemo, useCallback, useEffect } from 'react';
import { DaemonEndpoint, DaemonPR, RateLimitState } from '../hooks/useDaemonSocket';
import { usePRsNeedingAttention } from '../hooks/usePRsNeedingAttention';
import { PRActions } from './PRActions';
import { StateIndicator } from './StateIndicator';
import { ChiefOfStaffBadge } from './ChiefOfStaffBadge';
import { useDaemonContext } from '../contexts/DaemonContext';
import { getRepoName } from '../utils/repo';
import { openUrl } from '@tauri-apps/plugin-opener';
import type { UISessionState } from '../types/sessionState';
import { compareTurnOrder, compareWakeOrder, formatTurnAge } from '../utils/queueBands';
import { formatWakeTime, isSnoozed } from '../utils/snoozeDurations';
import { useNow, TURN_AGE_TICK_MS } from '../hooks/useNow';
import appIcon from '../assets/icon.png';
import './Dashboard.css';

type DashboardSession = {
  id: string;
  label: string;
  state: UISessionState;
  cwd: string;
  endpointName?: string;
  endpointStatus?: string;
  chiefOfStaff?: boolean;
  // The daemon's answer to "does this owe me a turn", and since when. Read,
  // never derived from state: an agent can sit in waiting_input with its turn
  // already settled, which is exactly the case grouping by state gets wrong.
  turnOwed?: boolean;
  turnOpenedAt?: string;
  // When a deferred agent comes back. Its presence is what takes the agent out
  // of every other group here: a snoozed agent's state is no longer an answer to
  // anything the user asked, so listing it under Working or Idle says the one
  // thing that is not true of it — that it is waiting on nothing.
  turnSnoozedUntil?: string;
};

/**
 * The state groups, in the order they read best: the states that used to mean
 * "this wants you" first, then the ones that are just what is happening.
 *
 * In queue mode these describe *settled* agents only — whether an agent wants
 * you is the turn's business, and answering it twice is how home ends up
 * announcing "Waiting for input (3)" directly beneath "All settled".
 */
const STATE_GROUPS: { state: UISessionState; label: string; testId: string }[] = [
  { state: 'waiting_input', label: 'Waiting for input', testId: 'session-group-waiting' },
  { state: 'pending_approval', label: 'Pending approval', testId: 'session-group-pending' },
  { state: 'launching', label: 'Launching', testId: 'session-group-launching' },
  { state: 'working', label: 'Working', testId: 'session-group-working' },
  { state: 'scheduled', label: 'Scheduled', testId: 'session-group-scheduled' },
  { state: 'idle', label: 'Idle', testId: 'session-group-idle' },
  { state: 'recoverable', label: 'Recoverable', testId: 'session-group-recoverable' },
  { state: 'unknown', label: 'Unknown / error', testId: 'session-group-unknown' },
];

type DashboardWorkspace = {
  id: string;
  title: string;
  sessions: DashboardSession[];
};

interface DashboardProps {
  sessions: DashboardSession[];
  mutedWorkspaces?: DashboardWorkspace[];
  prs: DaemonPR[];
  isLoading: boolean;
  isRefreshing?: boolean;
  refreshError?: string | null;
  rateLimit?: RateLimitState | null;
  endpoints?: DaemonEndpoint[];
  onRebootstrapEndpoint?: (endpointId: string) => Promise<void>;
  onSelectSession: (id: string) => void;
  onNewSession: () => void;
  // Ending a snooze early. Home is the second place a deferral can be undone,
  // and the only one that does not depend on the queue arrangement being on —
  // the shortcut and the command menu are both queue-gated, so without this a
  // snooze made in queue mode would have no way out once it was turned off.
  onWakeTurn?: (id: string) => void;
  onRefreshPRs?: () => void;
  onOpenPR?: (pr: DaemonPR) => void;
  onOpenSettings: () => void;
  onMutedGroupClick?: () => void;
  // Whether the queue arrangement is on. It selects home's whole shape: turns
  // first with the settled rest below, or the plain state grouping that is all
  // there is to say when nothing tracks turns.
  queueModeEnabled?: boolean;
  // Whether home is waiting for the queue to refill: on, the next turn to open
  // takes the user to it. Owned by App, which arms it when it hands the user
  // here after the last turn closed and clears it when they walk here
  // themselves. Home's job is to say which of the two happened and let the user
  // change it — an app that moves you on its own has to show you the switch.
  followNextTurn?: boolean;
  onToggleFollowNextTurn?: () => void;
}

export function Dashboard({
  sessions,
  mutedWorkspaces = [],
  prs,
  isLoading,
  isRefreshing,
  refreshError,
  rateLimit,
  endpoints,
  onRebootstrapEndpoint,
  onSelectSession,
  onNewSession,
  onWakeTurn,
  onRefreshPRs,
  onOpenPR,
  onOpenSettings,
  onMutedGroupClick,
  queueModeEnabled = false,
  followNextTurn = false,
  onToggleFollowNextTurn,
}: DashboardProps) {
  const now = useNow(TURN_AGE_TICK_MS);
  const [snoozedExpanded, setSnoozedExpanded] = useState(false);

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

  const chiefSession = sessions.find((session) => session.chiefOfStaff);

  /**
   * Agents the user deferred, soonest wake first, in queue order and out of
   * everything below.
   *
   * Not gated on the queue arrangement, unlike the sidebar's section. A snooze
   * can only be *made* with the queue on, but it outlives being turned off, and
   * every other way to wake one early is queue-gated too — so with the setting
   * off this list is the deferral's only remaining way out.
   *
   * The chief is here like any other agent, which is where home parts from the
   * sidebar: the sidebar pulls the chief out of its bands entirely, but home's
   * Sessions card lists it alongside the rest, so leaving it out would put a
   * deferred chief back under a state group that cannot describe it.
   */
  const snoozedSessions = useMemo(() => (
    sessions.filter((s) => isSnoozed(s.turnSnoozedUntil, now)).sort(compareWakeOrder)
  ), [sessions, now]);

  const awakeSessions = useMemo(() => {
    const deferred = new Set(snoozedSessions.map((s) => s.id));
    return sessions.filter((s) => !deferred.has(s.id));
  }, [sessions, snoozedSessions]);

  // The chief is left out of the turns, exactly as the sidebar band leaves it
  // out: it has its own card here and its own slot there. That does mean home
  // can read "all settled" while the chief wants you — the same thing the
  // sidebar says, which is the lesser of the two surprises.
  const turnSessions = useMemo(() => (
    queueModeEnabled
      ? awakeSessions.filter((s) => s.turnOwed && !s.chiefOfStaff).sort(compareTurnOrder)
      : []
  ), [queueModeEnabled, awakeSessions]);

  // With the queue off nothing has been settled, so every session is grouped by
  // state and there is no second band to keep them out of.
  const settledSessions = useMemo(() => (
    queueModeEnabled
      ? awakeSessions.filter((s) => !(s.turnOwed && !s.chiefOfStaff))
      : awakeSessions
  ), [queueModeEnabled, awakeSessions]);

  const stateGroups = useMemo(() => (
    STATE_GROUPS
      .map((group) => ({ ...group, rows: settledSessions.filter((s) => s.state === group.state) }))
      .filter((group) => group.rows.length > 0)
  ), [settledSessions]);

  const allSettled = queueModeEnabled && turnSessions.length === 0 && sessions.length > 0;

  // What is still in flight once nothing is owed. "All settled" with six agents
  // mid-turn is a different situation from "all settled" with everything parked,
  // and the line is the only thing that tells them apart.
  const stillRunning = useMemo(() => {
    const counts = [
      { label: 'working', n: settledSessions.filter((s) => s.state === 'working').length },
      { label: 'launching', n: settledSessions.filter((s) => s.state === 'launching').length },
      { label: 'scheduled', n: settledSessions.filter((s) => s.state === 'scheduled').length },
      { label: 'recoverable', n: settledSessions.filter((s) => s.state === 'recoverable').length },
    ].filter((entry) => entry.n > 0);
    if (counts.length === 0) {
      return 'Nothing is running.';
    }
    return counts.map((entry) => `${entry.n} ${entry.label}`).join(' · ');
  }, [settledSessions]);

  // `wake` replaces the age on a deferred row rather than joining it: the only
  // question a snoozed row answers is when it comes back, and the turn it would
  // have aged is already closed.
  const renderSessionRow = (
    s: DashboardSession,
    { age, wake }: { age?: string; wake?: string } = {},
  ) => (
    <div
      key={s.id}
      className="session-row clickable"
      data-testid={`session-${s.id}`}
      data-state={s.state}
      onClick={() => onSelectSession(s.id)}
    >
      <StateIndicator state={s.state} size="sm" seed={s.id} />
      <span className="session-name">{s.label}</span>
      {s.chiefOfStaff && <ChiefOfStaffBadge compact />}
      {renderEndpointBadge(s)}
      {age && <span className="session-turn-age">{age}</span>}
      {wake && <span className="session-wake-at">{wake}</span>}
      {wake && onWakeTurn && (
        <button
          type="button"
          className="session-wake-btn"
          data-testid={`session-wake-${s.id}`}
          title="Wake now — bring it back to the queue"
          aria-label={`Wake ${s.label}`}
          onClick={(event) => {
            event.stopPropagation();
            onWakeTurn(s.id);
          }}
        >
          ↩
        </button>
      )}
    </div>
  );

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

  /**
   * The PRs you opened, ahead of the ones you were asked to look at.
   *
   * Your own are the short list and the one you come here to find, so they lead
   * and they stay flat — a repo header over two rows is a level of structure
   * that only costs a click. The review side keeps the repo groups, because that
   * is where the volume is and where muting a whole repo is the act that helps.
   */
  const yourPRs = useMemo(() => activePRs.filter((pr) => pr.role === 'author'), [activePRs]);
  const reviewPRs = useMemo(() => activePRs.filter((pr) => pr.role !== 'author'), [activePRs]);

  // Group the review side by repo
  const prsByRepo = useMemo(() => {
    const grouped = new Map<string, DaemonPR[]>();
    for (const pr of reviewPRs) {
      const existing = grouped.get(pr.repo) || [];
      grouped.set(pr.repo, [...existing, pr]);
    }
    return grouped;
  }, [reviewPRs]);

  // One row, two homes. `showRepo` names the repo inline, which the flat "Yours"
  // list needs and a repo group already answers above its rows.
  const renderPRRow = (pr: DaemonPR, { showRepo = false }: { showRepo?: boolean } = {}) => {
    // Determine if this is an approved PR without changes (should be dimmed)
    const isApprovedNoChanges = pr.approved_by_me && !pr.has_new_changes;
    const showHost = (repoHosts.get(pr.repo)?.size || 0) > 1;
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
          {showRepo && <span className="pr-repo-inline">{getRepoName(pr.repo)}</span>}
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
    );
  };

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

  const [syncingEndpointId, setSyncingEndpointId] = useState<string | null>(null);
  const handleSync = useCallback(async (endpointId: string) => {
    if (!onRebootstrapEndpoint || syncingEndpointId) return;
    setSyncingEndpointId(endpointId);
    try {
      await onRebootstrapEndpoint(endpointId);
    } finally {
      setSyncingEndpointId(null);
    }
  }, [onRebootstrapEndpoint, syncingEndpointId]);

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
          data-testid="settings-button"
          onClick={onOpenSettings}
          title="Settings"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="12" cy="12" r="3"/>
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
          </svg>
        </button>
      </header>

      {/* The queue's terminus. Only in queue mode, because only there does an
          agent stop wanting you without its state changing. */}
      {allSettled && (
        <div className="all-settled-banner" data-testid="all-settled">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
            <polyline points="20 6 9 17 4 12" />
          </svg>
          <div className="all-settled-body">
            <span className="all-settled-title">All settled</span>
            <span className="all-settled-detail">{stillRunning}</span>
            {/* The wait, stated and reversible. It sits inside the banner
                because it only means anything while nothing is owed: the moment
                a turn opens it has either taken the user there or it has not,
                and that is the whole promise. */}
            {onToggleFollowNextTurn && (
              <label className="all-settled-follow" data-testid="follow-next-turn">
                <input
                  type="checkbox"
                  checked={followNextTurn}
                  onChange={onToggleFollowNextTurn}
                />
                <span>Take me to the next agent that needs you</span>
              </label>
            )}
          </div>
        </div>
      )}

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

      {/* Version mismatch banners */}
      {endpoints?.filter(ep => ep.status === 'version_mismatch' || ep.status === 'version_ahead' || ep.status === 'binary_mismatch').map(ep => (
        <div
          key={ep.id}
          className={`version-mismatch-banner${ep.status === 'version_ahead' ? ' version-mismatch-banner--ahead' : ''}`}
        >
          <svg className="version-mismatch-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
            <line x1="12" y1="9" x2="12" y2="13"/>
            <line x1="12" y1="17" x2="12.01" y2="17"/>
          </svg>
          <div className="version-mismatch-body">
            <span className="version-mismatch-name">{ep.name}</span>
            {ep.status_message && (
              <span className="version-mismatch-message">{ep.status_message}</span>
            )}
            {ep.status === 'version_ahead' && (
              <span className="version-mismatch-warning">Syncing will downgrade the remote — data migration may be required.</span>
            )}
          </div>
          <button
            className="version-mismatch-sync-btn"
            aria-label={`Sync ${ep.name}`}
            disabled={syncingEndpointId === ep.id}
            onClick={() => handleSync(ep.id)}
          >
            {syncingEndpointId === ep.id ? '…' : 'Sync'}
          </button>
        </div>
      ))}

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
            {sessions.length === 0 && mutedWorkspaces.length === 0 ? (
              <div className="card-empty">No active sessions</div>
            ) : (
              <>
                {turnSessions.length > 0 && (
                  <div className="session-group" data-testid="session-group-turns">
                    <div className="group-label">
                      Your turn
                      <span className="group-count">{turnSessions.length}</span>
                    </div>
                    {turnSessions.map((s) => (
                      renderSessionRow(s, { age: formatTurnAge(s.turnOpenedAt, now) })
                    ))}
                  </div>
                )}
                {/* Settled is a heading, not a group: the state groups below it
                    are what those agents are doing, now that whether they want
                    you has already been answered above. */}
                {queueModeEnabled && stateGroups.length > 0 && (
                  <div className="group-label group-label--section" data-testid="session-group-settled">
                    Settled
                  </div>
                )}
                {stateGroups.map((group) => (
                  <div key={group.state} className="session-group" data-testid={group.testId}>
                    <div className="group-label">{group.label}</div>
                    {group.rows.map((s) => renderSessionRow(s))}
                  </div>
                ))}
                {/* Collapsed at the foot of the card, above the muted
                    workspaces, exactly as the sidebar arranges the same two:
                    both are the quiet end, and "not yet" sits nearer to your
                    attention than "not ever". A snooze surfaces itself when it
                    wakes, so the section is for checking on a promise or
                    breaking it early — neither worth standing room. */}
                {snoozedSessions.length > 0 && (
                  <div className="session-group snoozed-group" data-testid="session-group-snoozed">
                    <button
                      type="button"
                      className="group-label group-toggle"
                      data-testid="session-group-snoozed-header"
                      aria-expanded={snoozedExpanded}
                      onClick={() => setSnoozedExpanded((open) => !open)}
                    >
                      <span className={`collapse-icon ${snoozedExpanded ? '' : 'collapsed'}`}>▾</span>
                      Snoozed
                      <span className="group-count">{snoozedSessions.length}</span>
                    </button>
                    {snoozedExpanded && snoozedSessions.map((s) => (
                      renderSessionRow(s, { wake: formatWakeTime(s.turnSnoozedUntil, now) })
                    ))}
                  </div>
                )}
                {mutedWorkspaces.length > 0 && (
                  <div
                    className="session-group muted-summary clickable"
                    data-testid="session-group-muted"
                    onClick={onMutedGroupClick}
                  >
                    <div className="group-label dim">Muted Workspaces ({mutedWorkspaces.length})</div>
                  </div>
                )}
              </>
            )}
          </div>
        </div>

        <div className="dashboard-card chief-dispatch-card" data-testid="chief-of-staff-card">
          <div className="card-header">
            <h2>Chief of Staff</h2>
          </div>
          <div className="card-body">
            {!chiefSession ? (
              <div className="card-empty">Assign a session as chief to track delegated work.</div>
            ) : (
              <div className="chief-session-block" data-testid="chief-session-summary">
                <div className="chief-section-label">Chief session</div>
                <button
                  type="button"
                  className="chief-session-summary"
                  onClick={() => onSelectSession(chiefSession.id)}
                >
                  <StateIndicator state={chiefSession.state} size="sm" seed={chiefSession.id} />
                  <span className="chief-session-name">{chiefSession.label}</span>
                  <ChiefOfStaffBadge compact />
                  <span className="chief-session-state">
                    Session: {chiefSession.state.replace('_', ' ')}
                  </span>
                </button>
              </div>
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
            ) : activePRs.length === 0 ? (
              <div className="card-empty">No PRs need attention</div>
            ) : (
              <>
                {yourPRs.length > 0 && (
                  <div className="pr-section" data-testid="pr-section-yours">
                    <div className="pr-section-header">
                      <span>Yours</span>
                      <span className="pr-section-count">{yourPRs.length}</span>
                    </div>
                    {yourPRs.map((pr) => renderPRRow(pr, { showRepo: true }))}
                  </div>
                )}
                {reviewPRs.length > 0 && (
                  <div className="pr-section" data-testid="pr-section-review">
                    <div className="pr-section-header">
                      <span>Review requested</span>
                      <span className="pr-section-count">{reviewPRs.length}</span>
                    </div>
                    {Array.from(prsByRepo.entries()).map(([repo, repoPRs]) => {
                      const repoName = getRepoName(repo);
                      const isCollapsed = collapsedRepos.has(repo);

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
                                <span className="count review">{repoPRs.length} review</span>
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
                              {repoPRs.map((pr) => renderPRRow(pr))}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
