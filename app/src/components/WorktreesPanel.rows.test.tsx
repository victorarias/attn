import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { WorktreesPanel, type WorktreesPanelProps } from './WorktreesPanel';
import { useWorktreeStore } from '../store/worktrees';
import type { GitOperation, Worktree } from '../types/generated';

const worktree = (over: Partial<Worktree> = {}): Worktree => ({
  path: '/projects/attn--feat-one',
  branch: 'feat/one',
  main_repo: '/projects/attn',
  observed_at: '2026-09-01T10:00:00Z',
  sweep_status: 'scheduled',
  sweep_reason: 'merged and clean; idle 3 of 14 days',
  sweep_at: '2026-09-15T10:00:00Z',
  merged_signal: 'ancestor',
  ...over,
});

const props = (over: Partial<WorktreesPanelProps> = {}): WorktreesPanelProps => ({
  isOpen: true,
  onClose: vi.fn(),
  listWorktrees: vi.fn().mockResolvedValue({
    worktrees: [worktree()],
    repositories: [{ main_repo: '/projects/attn', integration_branch: 'origin/next', integration_source: 'pull_requests' }],
    omitted: 0,
  }),
  getSweepLog: vi.fn().mockResolvedValue({ entries: [], omitted: 0 }),
  setKeep: vi.fn(),
  refreshWorktrees: vi.fn().mockResolvedValue(true),
  deleteWorktree: vi.fn(),
  sessions: [],
  gitOperations: {},
  onSelectSession: vi.fn(),
  ...over,
});

describe('WorktreesPanel rows', () => {
  beforeEach(() => {
    useWorktreeStore.getState().clear();
  });

  it('fetches once and groups rows under their repository and integration branch', async () => {
    const listWorktrees = vi.fn().mockResolvedValue({
      worktrees: [worktree(), worktree({ path: '/projects/attn--feat-two', branch: 'feat/two' })],
      repositories: [{ main_repo: '/projects/attn', integration_branch: 'origin/next', integration_source: 'pull_requests' }],
      omitted: 0,
    });
    render(<WorktreesPanel {...props({ listWorktrees })} />);

    await screen.findByText('attn--feat-one');
    expect(screen.getByText('attn--feat-two')).toBeTruthy();
    expect(screen.getByText('merges into origin/next')).toBeTruthy();
    expect(listWorktrees).toHaveBeenCalledTimes(1);
  });

  it('says what the sweep decided and why, with the date it becomes eligible', async () => {
    render(<WorktreesPanel {...props()} />);

    await screen.findByText('attn--feat-one');
    expect(screen.getByText(/removing on/)).toBeTruthy();
    expect(screen.getByText('merged and clean; idle 3 of 14 days')).toBeTruthy();
    expect(screen.getByText('merged · ancestor')).toBeTruthy();
  });

  it('shows every kept state as its own chip', async () => {
    const listWorktrees = vi.fn().mockResolvedValue({
      worktrees: [worktree({
        dirty: true, dirty_files: 3, stashes: 2, unpushed: 1, prunable: true,
        sweep_status: 'kept_dirty', sweep_reason: '3 uncommitted or untracked file(s)',
      })],
      repositories: [{ main_repo: '/projects/attn' }],
      omitted: 0,
    });
    render(<WorktreesPanel {...props({ listWorktrees })} />);

    await screen.findByText('attn--feat-one');
    for (const chip of ['stale', 'dirty 3', '2 stashed', '1 ahead']) {
      expect(screen.getByText(chip)).toBeTruthy();
    }
    expect(screen.getByText('kept dirty')).toBeTruthy();
  });

  it('marks a row refreshing while a git operation is running against its path', async () => {
    const operation: GitOperation = {
      id: 'op-1', kind: 'refresh_worktree', path: '/projects/attn--feat-one',
      started_at: '2026-09-05T10:00:00Z', status: 'running',
    } as GitOperation;
    render(<WorktreesPanel {...props({ gitOperations: { 'op-1': operation } })} />);

    await screen.findByText('attn--feat-one');
    expect(screen.getByTestId('refreshing-/projects/attn--feat-one')).toBeTruthy();
  });

  it('names a live session in the worktree and selects it on click', async () => {
    const onSelectSession = vi.fn();
    render(<WorktreesPanel {...props({
      onSelectSession,
      sessions: [{ id: 'sess-1', label: 'one', directory: '/projects/attn--feat-one/app' }],
    })} />);

    const button = await screen.findByText('one');
    button.click();
    expect(onSelectSession).toHaveBeenCalledWith('sess-1');
  });

  it('surfaces a failed fetch instead of rendering an empty list', async () => {
    const listWorktrees = vi.fn().mockRejectedValue(new Error('WebSocket not connected'));
    render(<WorktreesPanel {...props({ listWorktrees })} />);

    await waitFor(() => {
      expect(screen.getByTestId('worktrees-panel-error').textContent).toContain('WebSocket not connected');
    });
  });
});
