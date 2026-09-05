import { describe, expect, it, vi } from 'vitest';
import { handleWorktreeDaemonEvent } from './daemonWorktreeEvents';
import { pendingRequestKey, type PendingRequests } from './daemonPendingRequests';

function pendingFor(kind: string, requestId: string) {
  const pending: PendingRequests = new Map();
  const resolve = vi.fn();
  const reject = vi.fn();
  pending.set(pendingRequestKey(kind, requestId), { resolve, reject });
  return { pending, resolve, reject };
}

describe('handleWorktreeDaemonEvent', () => {
  it('settles the list only for the request that asked', () => {
    const { pending, resolve } = pendingFor('worktree_list', 'req-1');
    const result = { worktrees: [], repositories: [], omitted: 0 };

    handleWorktreeDaemonEvent(
      { event: 'worktree_list_result', request_id: 'req-2', success: true, worktree_list_result: result },
      pending, {},
    );
    expect(resolve).not.toHaveBeenCalled();
    expect(pending.size).toBe(1);

    handleWorktreeDaemonEvent(
      { event: 'worktree_list_result', request_id: 'req-1', success: true, worktree_list_result: result },
      pending, {},
    );
    expect(resolve).toHaveBeenCalledWith(result);
  });

  it('rejects a refused keep with the daemon’s own words', () => {
    const { pending, reject } = pendingFor('worktree_keep', 'req-1');

    handleWorktreeDaemonEvent(
      { event: 'worktree_keep_result', request_id: 'req-1', success: false, error: 'no worktree at that path' },
      pending, {},
    );
    expect(reject.mock.calls[0][0].message).toBe('no worktree at that path');
  });

  it('treats a refresh the daemon could not queue as a failure the caller sees', () => {
    const { pending, reject } = pendingFor('worktree_refresh', 'req-1');

    handleWorktreeDaemonEvent(
      { event: 'worktree_refresh_result', request_id: 'req-1', success: false },
      pending, {},
    );
    expect(reject.mock.calls[0][0].message).toContain('no job queue');
  });

  it('hands unsolicited state and sweep pushes to the callbacks', () => {
    const onWorktreeState = vi.fn();
    const onWorktreeSwept = vi.fn();
    const pending: PendingRequests = new Map();

    const worktree = { path: '/repo--one', branch: 'feat/one', main_repo: '/repo' };
    expect(handleWorktreeDaemonEvent(
      { event: 'worktree_state_changed', worktrees: [worktree] },
      pending, { onWorktreeState, onWorktreeSwept },
    )).toBe(true);
    expect(onWorktreeState).toHaveBeenCalledWith(worktree);

    const entry = { id: 'e-1', path: '/repo--one', main_repo: '/repo', action: 'removed', at: '2026-09-05T10:00:00Z' };
    handleWorktreeDaemonEvent(
      { event: 'worktree_swept', sweep_entry: entry },
      pending, { onWorktreeState, onWorktreeSwept },
    );
    expect(onWorktreeSwept).toHaveBeenCalledWith(entry);
  });

  it('leaves events it does not own to the rest of the chain', () => {
    expect(handleWorktreeDaemonEvent({ event: 'session_state_changed' }, new Map(), {})).toBe(false);
  });
});
