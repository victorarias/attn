import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SessionsPanel } from './SessionsPanel';
import type { ReopenVerdictView } from './SessionsPanel';
import type { SessionLedgerPage } from '../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry } from '../types/generated';
import { SessionState } from '../types/generated';

function closedEntry(id: string, overrides: Partial<SessionLedgerEntry> = {}): SessionLedgerEntry {
  return {
    id,
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${id}`,
    last_seen: '2026-09-05T09:00:00Z',
    closed_at: '2026-09-05T10:00:00Z',
    closed_by: 'user',
    close_reason: 'work finished',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    ...overrides,
  };
}

function liveEntry(id: string): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${id}`,
    last_seen: '2026-09-05T13:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    id,
  };
}

const REOPEN: ReopenVerdictView['actions'] = [{ id: 'reopen', label: 'Reopen' }];
const RECREATE: ReopenVerdictView['actions'] = [
  { id: 'recreate_worktree_and_reopen', label: 'Recreate the worktree' },
];

const now = () => new Date('2026-09-05T14:30:00Z');

function panel(page: SessionLedgerPage, props: Record<string, unknown> = {}) {
  const list = vi.fn(async () => page);
  const view = render(
    <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} {...props} />,
  );
  const rerender = (next: Record<string, unknown>) => view.rerender(
    <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} {...props} {...next} />,
  );
  return { rerender };
}

describe('SessionsPanel reopen verdicts', () => {
  it('asks for a verdict only for the closed rows on screen', async () => {
    const onRequestVerdict = vi.fn();
    panel(
      { entries: [closedEntry('s1'), liveEntry('s2')], omitted: 0 },
      { onRequestVerdict },
    );

    await waitFor(() => expect(onRequestVerdict).toHaveBeenCalled());
    expect(onRequestVerdict.mock.calls).toEqual([['s1']]);
  });

  it('shows a verdict as refreshing while a git check refines it, then in place', async () => {
    const verdict: ReopenVerdictView = {
      refreshing: true,
      summary: 'the worktree was there when it closed',
      reopenable: true,
      actions: REOPEN,
    };
    const { rerender } = panel(
      { entries: [closedEntry('s1')], omitted: 0 },
      { verdicts: { s1: verdict }, onRequestVerdict: vi.fn() },
    );

    await waitFor(() => expect(screen.getByText('refreshing…')).toBeTruthy());
    // The action is offered while the check runs; waiting for git would be the delay.
    expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy();

    rerender({ verdicts: { s1: { ...verdict, refreshing: false, summary: 'the worktree is still there' } } });
    expect(screen.queryByText('refreshing…')).toBeNull();
    expect(screen.getByText('the worktree is still there')).toBeTruthy();
  });

  it('holds an action taken mid-check and runs it against the verdict that lands', async () => {
    const onReopen = vi.fn();
    const refreshing: ReopenVerdictView = {
      refreshing: true, summary: 'checking the worktree', reopenable: true, actions: REOPEN,
    };
    const { rerender } = panel(
      { entries: [closedEntry('s1')], omitted: 0 },
      { verdicts: { s1: refreshing }, onRequestVerdict: vi.fn(), onReopen },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    expect(onReopen).not.toHaveBeenCalled();
    expect(screen.getByText('waiting for the check…')).toBeTruthy();

    rerender({ verdicts: { s1: { ...refreshing, refreshing: false, summary: 'the worktree is still there' } } });
    await waitFor(() => expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]));
  });

  it('refuses an action the fresh verdict no longer offers, and says why', async () => {
    const onReopen = vi.fn();
    const refreshing: ReopenVerdictView = {
      refreshing: true, summary: 'checking the worktree', reopenable: true, actions: REOPEN,
    };
    const { rerender } = panel(
      { entries: [closedEntry('s1')], omitted: 0 },
      { verdicts: { s1: refreshing }, onRequestVerdict: vi.fn(), onReopen },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));

    rerender({
      verdicts: {
        s1: { refreshing: false, summary: 'the worktree is gone', reopenable: false, actions: RECREATE },
      },
    });

    await waitFor(() => expect(
      screen.getByText('The check finished and that is no longer possible: the worktree is gone'),
    ).toBeTruthy());
    expect(onReopen).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Recreate the worktree' })).toBeTruthy();
  });

  it('runs an action straight away once the verdict has settled', async () => {
    const onReopen = vi.fn();
    panel(
      { entries: [closedEntry('s1')], omitted: 0 },
      {
        verdicts: {
          s1: { refreshing: false, summary: 'the worktree is still there', reopenable: true, actions: REOPEN },
        },
        onRequestVerdict: vi.fn(),
        onReopen,
      },
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Reopen' }));
    expect(onReopen.mock.calls).toEqual([['s1', 'reopen']]);
  });

  it('leaves the reopen column blank when nothing is checking', async () => {
    panel({ entries: [closedEntry('s1')], omitted: 0 });

    await waitFor(() => expect(screen.getByText('run s1')).toBeTruthy());
    expect(screen.queryByText('checking…')).toBeNull();
  });
});

describe('SessionsPanel live closes', () => {
  it('replaces a live row in place when the daemon says it closed', async () => {
    const { rerender } = panel({ entries: [liveEntry('s1')], omitted: 0 });

    await waitFor(() => expect(screen.getByRole('button', { name: 'Focus' })).toBeTruthy());
    rerender({ closeNotice: { entry: closedEntry('s1'), nonce: 1 } });

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Focus' })).toBeNull());
    expect(screen.getByText('closed')).toBeTruthy();
    expect(screen.getByText(/closed by you: work finished/)).toBeTruthy();
  });
});
