// app/src/components/NotificationsPanel.tsx
//
// The global notifications feed, opened from the sidebar bell. It lists durable
// notifications (newest first), lets the user expand one to read the full body +
// error detail, mark it read, retry the underlying task, or mark all read. The
// feed's producer is the daemon task runner (a background task that exhausts its
// retries), so a notification whose source_kind is "task" carries a Retry that
// re-queues that task by id.
//
// Live data flow: the panel fetches on open and on every changeSignal bump (the
// notifications_updated broadcast). It never optimistically mutates rows — a
// mark-read / retry issues the command and the resulting broadcast drives the
// refetch, mirroring the Tasks panel's broadcast-authoritative pattern.
import { useCallback, useEffect, useRef, useState } from 'react';
import type { DaemonNotification, Task } from '../hooks/useDaemonSocket';
import './NotificationsPanel.css';

interface NotificationsPanelProps {
  open: boolean;
  onClose: () => void;
  listNotifications: () => Promise<{ notifications: DaemonNotification[]; unreadCount: number }>;
  markRead: (notificationId?: string) => Promise<number>;
  retryTask: (taskId: string) => Promise<Task | null>;
  // Bumps on every notifications_updated broadcast so an open panel re-lists.
  changeSignal: number;
}

// formatCreatedAt renders an RFC3339 created_at as a short relative phrase
// ("now", "5m ago", "3h ago", "2d ago"). Returns '' for an unparseable value.
function formatCreatedAt(iso: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const deltaSec = Math.round((Date.now() - t) / 1000);
  if (deltaSec < 5) return 'now';
  if (deltaSec < 60) return `${deltaSec}s ago`;
  if (deltaSec < 3600) return `${Math.round(deltaSec / 60)}m ago`;
  if (deltaSec < 86400) return `${Math.round(deltaSec / 3600)}h ago`;
  return `${Math.round(deltaSec / 86400)}d ago`;
}

export function NotificationsPanel({
  open,
  onClose,
  listNotifications,
  markRead,
  retryTask,
  changeSignal,
}: NotificationsPanelProps) {
  const [notifications, setNotifications] = useState<DaemonNotification[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  // The expanded row (its full body + detail + actions); null when collapsed.
  const [expandedId, setExpandedId] = useState<string | null>(null);
  // Notification source-ids whose Retry is in flight (button disabled meanwhile).
  const [retryingIds, setRetryingIds] = useState<Set<string>>(new Set());
  // Monotonic load token: a slow response from a superseded fetch is dropped.
  const seqRef = useRef(0);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      const next = await listNotifications();
      if (seqRef.current !== seq) return;
      setNotifications(next.notifications);
      setError(null);
    } catch (err) {
      if (seqRef.current !== seq) return;
      setError(err instanceof Error ? err.message : 'Could not load notifications');
    } finally {
      if (seqRef.current === seq) setLoading(false);
    }
  }, [listNotifications]);

  // Fetch on open and whenever a notifications_updated broadcast bumps the signal
  // (only while open — a closed panel ignores the churn).
  useEffect(() => {
    if (!open) return;
    void refresh();
  }, [open, refresh, changeSignal]);

  // Drop the staleness token when the panel closes so an in-flight fetch can't
  // stamp rows onto a reopened panel; also collapse any expanded row.
  useEffect(() => {
    if (open) return;
    seqRef.current += 1;
    setLoading(false);
    setExpandedId(null);
  }, [open]);

  const unreadCount = notifications.filter((n) => !n.read_at).length;

  // Expand a row; mark it read on first expand (broadcast drives the refetch that
  // flips its dot). Clicking an expanded row collapses it.
  const handleToggle = useCallback(
    (n: DaemonNotification) => {
      setExpandedId((cur) => (cur === n.id ? null : n.id));
      if (!n.read_at) {
        void markRead(n.id).catch(() => {
          /* the next broadcast/refetch reconciles */
        });
      }
    },
    [markRead],
  );

  const handleMarkAllRead = useCallback(() => {
    void markRead().catch(() => {
      /* reconciled by the next broadcast */
    });
  }, [markRead]);

  const handleRetry = useCallback(
    async (n: DaemonNotification) => {
      if (n.source_kind !== 'task' || !n.source_id) return;
      setRetryingIds((prev) => new Set(prev).add(n.id));
      try {
        await retryTask(n.source_id);
      } catch {
        /* a failed retry leaves the row as-is; a redead task adds a new row */
      } finally {
        setRetryingIds((prev) => {
          const nextSet = new Set(prev);
          nextSet.delete(n.id);
          return nextSet;
        });
      }
    },
    [retryTask],
  );

  if (!open) return null;

  return (
    <>
      <div className="notifications-panel-backdrop" onClick={onClose} />
      <div className="notifications-panel" role="dialog" aria-label="Notifications">
        <div className="notifications-panel-header">
          <span className="notifications-panel-title">Notifications</span>
          <button
            type="button"
            className="notifications-panel-markall"
            onClick={handleMarkAllRead}
            disabled={unreadCount === 0}
          >
            Mark all read
          </button>
          <button type="button" className="notifications-panel-close" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>

        <div className="notifications-panel-body">
          {error && (
            <div className="notifications-panel-state">
              <span>{error}</span>
              <button type="button" onClick={() => void refresh()}>
                Try again
              </button>
            </div>
          )}
          {!error && loading && notifications.length === 0 && (
            <div className="notifications-panel-state">Loading…</div>
          )}
          {!error && !loading && notifications.length === 0 && (
            <p className="notifications-panel-empty">No notifications.</p>
          )}
          {notifications.length > 0 && (
            <ul className="notifications-panel-list">
              {notifications.map((n) => {
                const expanded = expandedId === n.id;
                const unread = !n.read_at;
                const canRetry = n.source_kind === 'task' && !!n.source_id;
                return (
                  <li
                    key={n.id}
                    className={`notification-row${unread ? ' is-unread' : ''}${expanded ? ' is-expanded' : ''}`}
                  >
                    <button type="button" className="notification-row-head" onClick={() => handleToggle(n)}>
                      <span className="notification-dot" aria-hidden="true" />
                      <span className="notification-row-title">{n.title}</span>
                      <span className="notification-row-time">{formatCreatedAt(n.created_at)}</span>
                    </button>
                    {expanded ? (
                      <div className="notification-row-detail">
                        {n.body && <p className="notification-row-body">{n.body}</p>}
                        {n.detail && <pre className="notification-row-error">{n.detail}</pre>}
                        {canRetry && (
                          <button
                            type="button"
                            className="notification-row-retry"
                            onClick={() => void handleRetry(n)}
                            disabled={retryingIds.has(n.id)}
                          >
                            {retryingIds.has(n.id) ? 'Retrying…' : 'Retry'}
                          </button>
                        )}
                      </div>
                    ) : (
                      n.body && <p className="notification-row-preview">{n.body}</p>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
    </>
  );
}
