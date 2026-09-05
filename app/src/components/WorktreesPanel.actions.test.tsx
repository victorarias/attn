import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { WorktreesPanel, type WorktreesPanelProps } from './WorktreesPanel';
import { useWorktreeStore } from '../store/worktrees';
import type { Worktree } from '../types/generated';

const worktree = (over: Partial<Worktree> = {}): Worktree => ({
  path: '/projects/attn--feat-one',
  branch: 'feat/one',
  main_repo: '/projects/attn',
  observed_at: '2026-09-01T10:00:00Z',
  sweep_status: 'scheduled',
  sweep_reason: 'merged and clean; idle 3 of 14 days',
  ...over,
});

const props = (over: Partial<WorktreesPanelProps> = {}): WorktreesPanelProps => ({
  isOpen: true,
  onClose: vi.fn(),
  listWorktrees: vi.fn().mockResolvedValue({
    worktrees: [worktree()],
    repositories: [{ main_repo: '/projects/attn' }],
    omitted: 0,
  }),
  getSweepLog: vi.fn().mockResolvedValue({ entries: [], omitted: 0 }),
  setKeep: vi.fn().mockImplementation((path: string, keep: boolean) =>
    Promise.resolve(worktree({ path, pinned: keep, sweep_status: keep ? 'pinned' : 'scheduled' }))),
  refreshWorktrees: vi.fn().mockResolvedValue(true),
  deleteWorktree: vi.fn().mockResolvedValue(undefined),
  sessions: [],
  gitOperations: {},
  onSelectSession: vi.fn(),
  ...over,
});

describe('WorktreesPanel actions', () => {
  beforeEach(() => {
    useWorktreeStore.getState().clear();
  });

  it('pins a worktree and offers the way back out', async () => {
    const setKeep = vi.fn().mockImplementation((path: string, keep: boolean) =>
      Promise.resolve(worktree({ path, pinned: keep, sweep_status: keep ? 'pinned' : 'scheduled' })));
    render(<WorktreesPanel {...props({ setKeep })} />);

    fireEvent.click(await screen.findByText('Keep forever'));
    await waitFor(() => expect(screen.getByText('Unpin')).toBeTruthy());
    expect(setKeep).toHaveBeenCalledWith('/projects/attn--feat-one', true);
    expect(screen.getByText('kept forever')).toBeTruthy();

    fireEvent.click(screen.getByText('Unpin'));
    await waitFor(() => expect(screen.getByText('Keep forever')).toBeTruthy());
    expect(setKeep).toHaveBeenLastCalledWith('/projects/attn--feat-one', false);
  });

  it('asks before deleting, then drops the row on the daemon\u2019s receipt', async () => {
    const deleteWorktree = vi.fn().mockResolvedValue(undefined);
    render(<WorktreesPanel {...props({ deleteWorktree })} />);

    fireEvent.click(await screen.findByText('Delete'));
    expect(deleteWorktree).not.toHaveBeenCalled();

    fireEvent.click(screen.getByText('Cancel'));
    expect(screen.getByText('Delete')).toBeTruthy();

    fireEvent.click(screen.getByText('Delete'));
    fireEvent.click(screen.getAllByText('Delete')[1] ?? screen.getByText('Delete'));
    await waitFor(() => expect(deleteWorktree).toHaveBeenCalledWith('/projects/attn--feat-one', false));

    expect(screen.queryByText('attn--feat-one')).toBeTruthy();
    useWorktreeStore.getState().swept({
      id: 'entry-1',
      path: '/projects/attn--feat-one',
      main_repo: '/projects/attn',
      branch: 'feat/one',
      action: 'deleted',
      reason: 'at your request',
      at: '2026-09-05T10:00:00Z',
    });
    await waitFor(() => expect(screen.queryByText('attn--feat-one')).toBeNull());
  });

  it('says a dirty worktree loses changes and forces the delete', async () => {
    const deleteWorktree = vi.fn().mockResolvedValue(undefined);
    const listWorktrees = vi.fn().mockResolvedValue({
      worktrees: [worktree({ dirty: true, dirty_files: 2, sweep_status: 'kept_dirty' })],
      repositories: [{ main_repo: '/projects/attn' }],
      omitted: 0,
    });
    render(<WorktreesPanel {...props({ deleteWorktree, listWorktrees })} />);

    fireEvent.click(await screen.findByText('Delete'));
    fireEvent.click(screen.getByText('Delete, losing changes'));
    await waitFor(() => expect(deleteWorktree).toHaveBeenCalledWith('/projects/attn--feat-one', true));
  });

  it('surfaces a refused delete rather than dropping the row', async () => {
    const deleteWorktree = vi.fn().mockRejectedValue(new Error('worktree has uncommitted changes'));
    render(<WorktreesPanel {...props({ deleteWorktree })} />);

    fireEvent.click(await screen.findByText('Delete'));
    fireEvent.click(screen.getAllByText('Delete')[1] ?? screen.getByText('Delete'));
    await waitFor(() => {
      expect(screen.getByTestId('worktrees-panel-error').textContent).toContain('uncommitted changes');
    });
    expect(screen.getByText('attn--feat-one')).toBeTruthy();
  });

  it('asks the daemon to refresh in the background and says so', async () => {
    const refreshWorktrees = vi.fn().mockResolvedValue(true);
    render(<WorktreesPanel {...props({ refreshWorktrees })} />);

    fireEvent.click(await screen.findByText('Refresh'));
    await waitFor(() => expect(refreshWorktrees).toHaveBeenCalled());
    expect(screen.getByText(/Refreshing in the background/)).toBeTruthy();
  });

  it('reads the sweep log only when it is opened', async () => {
    const getSweepLog = vi.fn().mockResolvedValue({
      entries: [{
        id: 'entry-1', path: '/projects/attn--feat-gone', main_repo: '/projects/attn',
        branch: 'feat/gone', action: 'removed', reason: 'merged (ancestor) and clean, idle 19 days',
        at: '2026-09-02T09:00:00Z',
      }],
      omitted: 0,
    });
    render(<WorktreesPanel {...props({ getSweepLog })} />);

    await screen.findByText('attn--feat-one');
    expect(getSweepLog).not.toHaveBeenCalled();

    fireEvent.click(screen.getByText('Sweep log'));
    await waitFor(() => expect(screen.getByText('attn--feat-gone')).toBeTruthy());
    expect(screen.getByText('merged (ancestor) and clean, idle 19 days')).toBeTruthy();
  });

  it('says who removed each worktree, the sweep or the user', async () => {
    const getSweepLog = vi.fn().mockResolvedValue({
      entries: [
        {
          id: 'entry-1', path: '/projects/attn--feat-gone', main_repo: '/projects/attn',
          branch: 'feat/gone', action: 'removed', reason: 'merged (ancestor) and clean, idle 19 days',
          at: '2026-09-02T09:00:00Z',
        },
        {
          id: 'entry-2', path: '/projects/attn--feat-byhand', main_repo: '/projects/attn',
          branch: 'feat/byhand', action: 'deleted', reason: 'at your request',
          at: '2026-09-02T10:00:00Z',
        },
      ],
      omitted: 0,
    });
    render(<WorktreesPanel {...props({ getSweepLog })} />);

    fireEvent.click(await screen.findByText('Sweep log'));
    await waitFor(() => expect(screen.getByText('attn--feat-byhand')).toBeTruthy());
    expect(screen.getByText('removed')).toBeTruthy();
    expect(screen.getByText('deleted')).toBeTruthy();
    expect(screen.getByText('at your request')).toBeTruthy();
  });
});
