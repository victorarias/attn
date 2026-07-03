// app/src/components/BackgroundTasksSettings.tsx
//
// The durable task runner's task list, surfaced in Settings › Background Tasks.
// This is the management view (state, attempts, next attempt, last error, Retry)
// that used to live in the notebook browser's collapsible Tasks section; tasks
// are a global daemon concern, not a notebook one, so they belong in Settings.
//
// Broadcast-authoritative, like the old panel: it fetches on mount and on every
// notebook_tasks_changed bump (taskChangeSignal), and a Retry only issues the
// command — the resulting broadcast drives the refetch that reflects truth.
import { useCallback, useEffect, useRef, useState } from 'react';
import type { NotebookTask } from '../hooks/useDaemonSocket';
import './BackgroundTasksSettings.css';

interface BackgroundTasksSettingsProps {
  listTasks: () => Promise<NotebookTask[]>;
  retryTask: (taskId: string) => Promise<NotebookTask | null>;
  // Bumps on every notebook_tasks_changed broadcast so the list re-fetches.
  taskChangeSignal: number;
}

// A terminal task isn't waiting on a next attempt, so its scheduled time is noise.
const TASK_TERMINAL_STATES = new Set(['done', 'dead']);

// formatNextAttempt renders an RFC3339 next_attempt_at as a short relative phrase
// ("in 2m", "5s ago", "now"). Returns '' for an unparseable/zero timestamp.
function formatNextAttempt(iso: string): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  if (new Date(t).getUTCFullYear() <= 1) return '';
  const now = Date.now();
  const deltaSec = Math.round((t - now) / 1000);
  const abs = Math.abs(deltaSec);
  if (abs < 5) return 'now';
  const unit = abs < 60 ? `${abs}s` : abs < 3600 ? `${Math.round(abs / 60)}m` : `${Math.round(abs / 3600)}h`;
  return deltaSec >= 0 ? `in ${unit}` : `${unit} ago`;
}

export function BackgroundTasksSettings({
  listTasks,
  retryTask,
  taskChangeSignal,
}: BackgroundTasksSettingsProps) {
  const [tasks, setTasks] = useState<NotebookTask[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [retryingIds, setRetryingIds] = useState<Set<string>>(new Set());
  const seqRef = useRef(0);

  const refresh = useCallback(async () => {
    const seq = ++seqRef.current;
    setLoading(true);
    try {
      const next = await listTasks();
      if (seqRef.current !== seq) return;
      setTasks(next);
      setError(null);
    } catch (err) {
      if (seqRef.current !== seq) return;
      setError(err instanceof Error ? err.message : 'Could not load tasks');
    } finally {
      if (seqRef.current === seq) setLoading(false);
    }
  }, [listTasks]);

  useEffect(() => {
    void refresh();
  }, [refresh, taskChangeSignal]);

  const handleRetry = useCallback(
    async (taskId: string) => {
      setRetryingIds((prev) => new Set(prev).add(taskId));
      try {
        await retryTask(taskId);
      } catch {
        /* a failed retry leaves the row as-is; the next broadcast reconciles */
      } finally {
        setRetryingIds((prev) => {
          const next = new Set(prev);
          next.delete(taskId);
          return next;
        });
      }
    },
    [retryTask],
  );

  return (
    <section className="settings-block">
      <div className="settings-block-intro">
        <span className="settings-kicker">Background Tasks</span>
        <h3>Durable task runner</h3>
        <p className="settings-description">
          Background work attn runs for you — context compaction, session summaries, workspace
          narration, and ticket reconciliation. A task that exhausts its retries becomes a
          notification you can retry from here.
        </p>
      </div>

      <div className="settings-block-body">
        {error && (
          <div className="background-tasks-state">
            <span>{error}</span>
            <button type="button" onClick={() => void refresh()}>
              Try again
            </button>
          </div>
        )}
        {!error && loading && tasks.length === 0 && (
          <div className="background-tasks-state">Loading tasks…</div>
        )}
        {!error && !loading && tasks.length === 0 && (
          <p className="background-tasks-empty">No background tasks.</p>
        )}
        {tasks.length > 0 && (
          <ul className="background-tasks-list">
            {tasks.map((task) => {
              const nextAttempt = TASK_TERMINAL_STATES.has(task.state)
                ? ''
                : formatNextAttempt(task.next_attempt_at);
              const canRetry = task.state === 'failed' || task.state === 'dead';
              return (
                <li className="background-task" key={task.id}>
                  <div className="background-task-head">
                    <span className={`background-task-badge is-${task.state}`} title={task.state}>
                      {task.state}
                    </span>
                    <span className="background-task-subject" title={`${task.kind}:${task.subject}`}>
                      {task.kind}:{task.subject}
                    </span>
                    {canRetry && (
                      <button
                        type="button"
                        className="background-task-retry"
                        onClick={() => void handleRetry(task.id)}
                        disabled={retryingIds.has(task.id)}
                      >
                        {retryingIds.has(task.id) ? 'Retrying…' : 'Retry'}
                      </button>
                    )}
                  </div>
                  <div className="background-task-meta">
                    <span>attempts: {task.attempts}</span>
                    {nextAttempt && <span>next: {nextAttempt}</span>}
                  </div>
                  {task.last_error && (
                    <p className="background-task-error" title={task.last_error}>
                      {task.last_error}
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </section>
  );
}
