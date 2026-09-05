import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  GitOperation,
  Worktree,
  WorktreeRepository,
  WorktreeSweepEntry,
  WorktreeListResult,
  WorktreeSweepLogResult,
} from '../types/generated';
import { useWorktreeStore } from '../store/worktrees';
import './WorktreesPanel.css';

export interface WorktreeSessionRef {
  id: string;
  label: string;
  directory: string;
}

export interface WorktreesPanelProps {
  isOpen: boolean;
  onClose: () => void;
  listWorktrees: () => Promise<WorktreeListResult>;
  getSweepLog: (mainRepo?: string, limit?: number) => Promise<WorktreeSweepLogResult>;
  setKeep: (path: string, keep: boolean) => Promise<Worktree>;
  refreshWorktrees: () => Promise<boolean>;
  deleteWorktree: (path: string, force: boolean) => Promise<void>;
  sessions: WorktreeSessionRef[];
  gitOperations: Record<string, GitOperation>;
  onSelectSession: (sessionId: string) => void;
}

const sweepLogPageSize = 30;

function baseName(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  const cut = trimmed.lastIndexOf('/');
  return cut < 0 ? trimmed : trimmed.slice(cut + 1);
}

export function branchLabel(worktree: Worktree): string {
  if (worktree.detached) {
    const sha = worktree.head_sha ?? '';
    return sha ? `detached ${sha.slice(0, 8)}` : 'detached';
  }
  return worktree.branch || '—';
}

export function stateChips(worktree: Worktree): string[] {
  const chips: string[] = [];
  if (worktree.prunable) chips.push('stale');
  if (worktree.dirty) chips.push(`dirty ${worktree.dirty_files ?? 0}`);
  if (worktree.stashes) chips.push(`${worktree.stashes} stashed`);
  if (worktree.unpushed) chips.push(`${worktree.unpushed} ahead`);
  if (worktree.merged_signal) chips.push(`merged · ${worktree.merged_signal}`);
  if (!worktree.observed_at) chips.push('not refreshed yet');
  return chips;
}

export function sweepLabel(worktree: Worktree): string {
  const status = worktree.sweep_status ?? '';
  if (worktree.pinned) return 'kept forever';
  if (!status) return 'not decided yet';
  if (status !== 'scheduled') return status.replace(/_/g, ' ');
  const at = worktree.sweep_at ? new Date(worktree.sweep_at) : null;
  if (!at || Number.isNaN(at.getTime())) return 'scheduled';
  return `removing on ${at.toLocaleDateString()}`;
}

function runningOperationFor(gitOperations: Record<string, GitOperation>, path: string): boolean {
  return Object.values(gitOperations).some(
    (operation) => operation.status === 'running' && operation.path === path,
  );
}

export function sessionsInWorktree(sessions: WorktreeSessionRef[], path: string): WorktreeSessionRef[] {
  return sessions.filter(
    (session) => session.directory === path || session.directory.startsWith(`${path}/`),
  );
}

export function WorktreesPanel({
  isOpen,
  onClose,
  listWorktrees,
  getSweepLog,
  setKeep,
  refreshWorktrees,
  deleteWorktree,
  sessions,
  gitOperations,
  onSelectSession,
}: WorktreesPanelProps) {
  const worktrees = useWorktreeStore((store) => store.worktrees);
  const repositories = useWorktreeStore((store) => store.repositories);
  const sweepLog = useWorktreeStore((store) => store.sweepLog);
  const loaded = useWorktreeStore((store) => store.loaded);

  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busyPaths, setBusyPaths] = useState<Record<string, boolean>>({});
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [showLog, setShowLog] = useState(false);

  const load = useCallback(() => {
    listWorktrees()
      .then((result) => {
        useWorktreeStore.getState().replace(
          result.worktrees as Worktree[],
          result.repositories as WorktreeRepository[],
        );
        setError(null);
      })
      .catch((failure: Error) => setError(failure.message));
  }, [listWorktrees]);

  useEffect(() => {
    if (!isOpen) return;
    load();
  }, [isOpen, load]);

  useEffect(() => {
    if (!isOpen || !showLog) return;
    getSweepLog(undefined, sweepLogPageSize)
      .then((result) => useWorktreeStore.getState().replaceSweepLog(result.entries as WorktreeSweepEntry[]))
      .catch((failure: Error) => setError(failure.message));
  }, [isOpen, showLog, getSweepLog]);

  const integrationByRepo = useMemo(() => {
    const map = new Map<string, string>();
    for (const repository of repositories) {
      if (repository.integration_branch) {
        map.set(repository.main_repo, repository.integration_branch);
      }
    }
    return map;
  }, [repositories]);

  const grouped = useMemo(() => {
    const byRepo = new Map<string, Worktree[]>();
    for (const worktree of worktrees) {
      const rows = byRepo.get(worktree.main_repo) ?? [];
      rows.push(worktree);
      byRepo.set(worktree.main_repo, rows);
    }
    return [...byRepo.entries()]
      .map(([mainRepo, rows]) => ({
        mainRepo,
        rows: rows.slice().sort((a, b) => a.path.localeCompare(b.path)),
      }))
      .sort((a, b) => a.mainRepo.localeCompare(b.mainRepo));
  }, [worktrees]);

  const withBusy = useCallback(async (path: string, work: () => Promise<void>) => {
    setBusyPaths((current) => ({ ...current, [path]: true }));
    try {
      await work();
      setError(null);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setBusyPaths((current) => {
        const next = { ...current };
        delete next[path];
        return next;
      });
    }
  }, []);

  const togglePin = useCallback((worktree: Worktree) => {
    const keep = !worktree.pinned;
    return withBusy(worktree.path, async () => {
      const updated = await setKeep(worktree.path, keep);
      useWorktreeStore.getState().observe(updated);
    });
  }, [setKeep, withBusy]);

  // The daemon's push drops the row and writes the entry; a local guess would
  // be a second one.
  const remove = useCallback((worktree: Worktree) => {
    setConfirmDelete(null);
    return withBusy(worktree.path, () => deleteWorktree(worktree.path, Boolean(worktree.dirty)));
  }, [deleteWorktree, withBusy]);

  const requestRefresh = useCallback(() => {
    refreshWorktrees()
      .then(() => {
        setError(null);
        setNotice('Refreshing in the background; rows update as each repository finishes.');
      })
      .catch((failure: Error) => setError(failure.message));
  }, [refreshWorktrees]);

  if (!isOpen) return null;

  return (
    <div className="worktrees-panel" data-testid="worktrees-panel">
      <div className="worktrees-panel__header">
        <span className="worktrees-panel__kicker">Worktrees</span>
        <div className="worktrees-panel__header-actions">
          <button
            type="button"
            className="worktrees-panel__action"
            data-testid="worktrees-refresh"
            onClick={requestRefresh}
          >
            Refresh
          </button>
          <button
            type="button"
            className="worktrees-panel__action"
            data-testid="worktrees-toggle-log"
            aria-pressed={showLog}
            onClick={() => setShowLog((current) => !current)}
          >
            {showLog ? 'Hide sweep log' : 'Sweep log'}
          </button>
          <button
            type="button"
            className="worktrees-panel__close"
            aria-label="Close worktrees"
            onClick={onClose}
          >
            ✕
          </button>
        </div>
      </div>

      {error && <p className="worktrees-panel__error" data-testid="worktrees-panel-error">{error}</p>}
      {notice && <p className="worktrees-panel__notice">{notice}</p>}

      {showLog && (
        <section className="worktrees-panel__log" data-testid="worktrees-panel-log">
          <h3 className="worktrees-panel__log-title">Worktrees removed</h3>
          {sweepLog.length === 0 ? (
            <p className="worktrees-panel__empty">Nothing removed yet.</p>
          ) : (
            <ul className="worktrees-panel__log-list">
              {sweepLog.map((entry) => (
                <li
                  key={entry.id}
                  className="worktrees-panel__log-row"
                  data-testid={`worktree-log-${entry.path}`}
                >
                  <span className="worktrees-panel__log-when">
                    {new Date(entry.at).toLocaleString()}
                  </span>
                  <span className="worktrees-panel__log-path" title={entry.path}>
                    {baseName(entry.path)}
                  </span>
                  <span className="worktrees-panel__log-branch">{entry.branch || '—'}</span>
                  <span className="worktrees-panel__log-action">{entry.action}</span>
                  <span className="worktrees-panel__log-reason">{entry.reason}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {loaded && grouped.length === 0 && (
        <p className="worktrees-panel__empty" data-testid="worktrees-panel-empty">
          No worktrees tracked yet. Refresh once a repository has one.
        </p>
      )}

      {grouped.map(({ mainRepo, rows }) => (
        <section className="worktrees-panel__repo" key={mainRepo}>
          <div className="worktrees-panel__repo-header">
            <span className="worktrees-panel__repo-name" title={mainRepo}>{baseName(mainRepo)}</span>
            {integrationByRepo.has(mainRepo) && (
              <span className="worktrees-panel__repo-integration">
                merges into {integrationByRepo.get(mainRepo)}
              </span>
            )}
            {runningOperationFor(gitOperations, mainRepo) && (
              <span className="worktrees-panel__refreshing" data-testid={`refreshing-${mainRepo}`}>
                refreshing…
              </span>
            )}
          </div>

          <ul className="worktrees-panel__list">
            {rows.map((worktree) => {
              const live = sessionsInWorktree(sessions, worktree.path);
              const busy = Boolean(busyPaths[worktree.path]);
              return (
                <li
                  className={`worktrees-panel__row${worktree.pinned ? ' is-pinned' : ''}`}
                  key={worktree.path}
                  data-testid={`worktree-row-${worktree.path}`}
                >
                  <div className="worktrees-panel__row-main">
                    <span className="worktrees-panel__name" title={worktree.path}>
                      {baseName(worktree.path)}
                    </span>
                    <span className="worktrees-panel__branch">{branchLabel(worktree)}</span>
                    {runningOperationFor(gitOperations, worktree.path) && (
                      <span
                        className="worktrees-panel__refreshing"
                        data-testid={`refreshing-${worktree.path}`}
                      >
                        refreshing…
                      </span>
                    )}
                  </div>

                  <div className="worktrees-panel__chips">
                    {stateChips(worktree).map((chip) => (
                      <span className="worktrees-panel__chip" key={chip}>{chip}</span>
                    ))}
                    {worktree.refresh_error && (
                      <span className="worktrees-panel__chip worktrees-panel__chip--error">
                        {worktree.refresh_error}
                      </span>
                    )}
                  </div>

                  {live.length > 0 && (
                    <div className="worktrees-panel__sessions">
                      {live.map((session) => (
                        <button
                          type="button"
                          className="worktrees-panel__session"
                          key={session.id}
                          onClick={() => onSelectSession(session.id)}
                        >
                          {session.label || session.id}
                        </button>
                      ))}
                    </div>
                  )}

                  <div className="worktrees-panel__sweep">
                    <span className="worktrees-panel__sweep-status">{sweepLabel(worktree)}</span>
                    {worktree.sweep_reason && (
                      <span className="worktrees-panel__sweep-reason">{worktree.sweep_reason}</span>
                    )}
                  </div>

                  <div className="worktrees-panel__row-actions">
                    <button
                      type="button"
                      className="worktrees-panel__action"
                      data-testid={`worktree-pin-${worktree.path}`}
                      disabled={busy}
                      onClick={() => void togglePin(worktree)}
                    >
                      {worktree.pinned ? 'Unpin' : 'Keep forever'}
                    </button>
                    {confirmDelete === worktree.path ? (
                      <>
                        <button
                          type="button"
                          className="worktrees-panel__action worktrees-panel__action--danger"
                          data-testid={`worktree-delete-confirm-${worktree.path}`}
                          disabled={busy}
                          onClick={() => void remove(worktree)}
                        >
                          {worktree.dirty ? 'Delete, losing changes' : 'Delete'}
                        </button>
                        <button
                          type="button"
                          className="worktrees-panel__action"
                          onClick={() => setConfirmDelete(null)}
                        >
                          Cancel
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        className="worktrees-panel__action"
                        data-testid={`worktree-delete-${worktree.path}`}
                        disabled={busy}
                        onClick={() => setConfirmDelete(worktree.path)}
                      >
                        Delete
                      </button>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}
