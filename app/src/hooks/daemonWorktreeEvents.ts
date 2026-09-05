import type {
  Worktree,
  WorktreeListResult,
  WorktreeSweepEntry,
  WorktreeSweepLogResult,
} from '../types/generated';
import { type PendingRequests, settlePendingRequest } from './daemonPendingRequests';

interface WorktreeDaemonEvent {
  event?: string;
  request_id?: unknown;
  success?: boolean;
  error?: string;
  worktrees?: Worktree[];
  sweep_entry?: WorktreeSweepEntry;
  worktree_list_result?: WorktreeListResult;
  worktree_sweep_log_result?: WorktreeSweepLogResult;
}

export interface WorktreeEventCallbacks {
  onWorktreeState?: (worktree: Worktree) => void;
  onWorktreeSwept?: (entry: WorktreeSweepEntry) => void;
}

export function handleWorktreeDaemonEvent(
  event: WorktreeDaemonEvent,
  pending: PendingRequests,
  callbacks: WorktreeEventCallbacks,
): boolean {
  switch (event.event) {
    case 'worktree_list_result':
      settlePendingRequest(
        pending,
        'worktree_list',
        event,
        (settled) => settled.worktree_list_result,
        'Listing worktrees failed',
      );
      return true;

    case 'worktree_sweep_log_result':
      settlePendingRequest(
        pending,
        'worktree_sweep_log',
        event,
        (settled) => settled.worktree_sweep_log_result,
        'Reading the sweep log failed',
      );
      return true;

    case 'worktree_keep_result':
      settlePendingRequest(
        pending,
        'worktree_keep',
        event,
        (settled) => settled.worktrees?.[0],
        'Changing the keep pin failed',
      );
      return true;

    case 'worktree_refresh_result':
      settlePendingRequest(
        pending,
        'worktree_refresh',
        event,
        // The daemon answers false when it has no job queue to enqueue onto, and
        // that is a failure the caller must see rather than a quiet no-op.
        (settled) => (settled.success ? true : undefined),
        'The daemon has no job queue running, so no refresh was queued',
      );
      return true;

    // Both pushes are unsolicited: the background sweep moves rows nobody asked about.
    case 'worktree_state_changed':
      for (const worktree of event.worktrees ?? []) {
        callbacks.onWorktreeState?.(worktree);
      }
      return true;

    case 'worktree_swept':
      if (event.sweep_entry) {
        callbacks.onWorktreeSwept?.(event.sweep_entry);
      }
      return true;

    default:
      return false;
  }
}
