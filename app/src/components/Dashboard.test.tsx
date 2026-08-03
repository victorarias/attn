import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Dashboard } from './Dashboard';

vi.mock('../contexts/DaemonContext', () => ({
  useDaemonContext: () => ({
    sendMuteRepo: vi.fn(),
    sendMuteAuthor: vi.fn(),
    sendPRVisited: vi.fn(),
  }),
}));

vi.mock('../hooks/usePRsNeedingAttention', () => ({
  usePRsNeedingAttention: () => ({
    activePRs: [],
    needsAttention: [],
    reviewRequested: [],
    yourPRs: [],
  }),
}));

vi.mock('@tauri-apps/plugin-opener', () => ({
  openUrl: vi.fn(),
}));

describe('Dashboard sessions', () => {
  it('shows pending approval sessions on home screen', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'conductor-bot', state: 'working', cwd: '/repo/a' },
          { id: 's2', label: 'review-bot', state: 'pending_approval', cwd: '/repo/b' },
          { id: 's3', label: 'fix-bot', state: 'pending_approval', cwd: '/repo/c' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByTestId('session-group-working')).toBeInTheDocument();
    expect(screen.getByTestId('session-group-pending')).toBeInTheDocument();
    expect(screen.getByTestId('session-s1')).toBeInTheDocument();
    expect(screen.getByTestId('session-s2')).toBeInTheDocument();
    expect(screen.getByTestId('session-s3')).toBeInTheDocument();
  });

  it('groups scheduled sessions in their own section', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'loop-bot', state: 'scheduled', cwd: '/repo/a' },
          { id: 's2', label: 'busy-bot', state: 'working', cwd: '/repo/b' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByTestId('session-group-scheduled')).toBeInTheDocument();
    const scheduled = screen.getByTestId('session-s1');
    expect(scheduled).toBeInTheDocument();
    expect(scheduled).toHaveAttribute('data-state', 'scheduled');
  });

  it('renders recoverable sessions in their own section', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'restartable-bot', state: 'recoverable', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByTestId('session-group-recoverable')).toBeInTheDocument();
    const recoverable = screen.getByTestId('session-s1');
    expect(recoverable).toBeVisible();
    expect(recoverable).toHaveAttribute('data-state', 'recoverable');
  });

  it('renders endpoint badges for remote sessions', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'remote-bot', state: 'working', cwd: '/repo/a', endpointName: 'gpu-box', endpointStatus: 'connected' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByText('gpu-box')).toBeInTheDocument();
  });

  it('marks the chief-of-staff session', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getAllByLabelText('Chief of staff')).toHaveLength(2);
  });

  it('renders the chief session summary and navigates to it', () => {
    const onSelectSession = vi.fn();
    render(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'waiting_input', cwd: '/repo/a', chiefOfStaff: true },
          { id: 'worker-1', label: 'parser-worker', state: 'working', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={onSelectSession}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    const summary = screen.getByTestId('chief-session-summary');
    expect(summary).toBeInTheDocument();
    expect(screen.getByText('Chief session')).toBeInTheDocument();
    expect(screen.getByText('Session: waiting input')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /planner/i }));
    expect(onSelectSession).toHaveBeenCalledWith('chief-1');
  });

  it('prompts to assign a chief when none is set', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'worker', state: 'working', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByText('Assign a session as chief to track delegated work.')).toBeInTheDocument();
    expect(screen.queryByTestId('chief-session-summary')).not.toBeInTheDocument();
  });
});

describe('Dashboard in queue mode', () => {
  const props = {
    prs: [],
    isLoading: false,
    queueModeEnabled: true,
    onSelectSession: vi.fn(),
    onNewSession: vi.fn(),
    onOpenSettings: vi.fn(),
  };

  it('leads with the turns owed, oldest first, and keeps them out of the state groups', () => {
    render(
      <Dashboard
        {...props}
        sessions={[
          { id: 's1', label: 'newer', state: 'waiting_input', cwd: '/a', turnOwed: true, turnOpenedAt: '2026-07-29T10:05:00Z' },
          { id: 's2', label: 'older', state: 'pending_approval', cwd: '/b', turnOwed: true, turnOpenedAt: '2026-07-29T09:00:00Z' },
          { id: 's3', label: 'settled-but-waiting', state: 'waiting_input', cwd: '/c', turnOwed: false },
        ]}
      />
    );

    const turns = screen.getByTestId('session-group-turns');
    expect(turns).toBeInTheDocument();
    const names = Array.from(turns.querySelectorAll('.session-name')).map((n) => n.textContent);
    expect(names).toEqual(['older', 'newer']);

    // The settled agent is still in waiting_input, so the state group exists —
    // but it holds only the agent whose turn is closed.
    const waiting = screen.getByTestId('session-group-waiting');
    expect(waiting).toContainElement(screen.getByTestId('session-s3'));
    expect(waiting).not.toContainElement(screen.getByTestId('session-s1'));
    expect(screen.getByTestId('session-group-settled')).toBeInTheDocument();
    expect(screen.queryByTestId('all-settled')).not.toBeInTheDocument();
  });

  it('announces all settled with what is still running once nothing is owed', () => {
    render(
      <Dashboard
        {...props}
        sessions={[
          { id: 's1', label: 'busy', state: 'working', cwd: '/a', turnOwed: false },
          { id: 's2', label: 'busy-too', state: 'working', cwd: '/b', turnOwed: false },
          { id: 's3', label: 'later', state: 'scheduled', cwd: '/c', turnOwed: false },
        ]}
      />
    );

    expect(screen.getByTestId('all-settled')).toBeInTheDocument();
    expect(screen.getByText('2 working · 1 scheduled')).toBeInTheDocument();
    expect(screen.queryByTestId('session-group-turns')).not.toBeInTheDocument();
  });

  it('says nothing is running when everything settled is parked', () => {
    render(
      <Dashboard
        {...props}
        sessions={[{ id: 's1', label: 'parked', state: 'idle', cwd: '/a', turnOwed: false }]}
      />
    );

    expect(screen.getByText('Nothing is running.')).toBeInTheDocument();
  });

  it('leaves the chief out of the turns, matching the sidebar band', () => {
    render(
      <Dashboard
        {...props}
        sessions={[
          { id: 'chief', label: 'chief', state: 'waiting_input', cwd: '/a', chiefOfStaff: true, turnOwed: true, turnOpenedAt: '2026-07-29T09:00:00Z' },
        ]}
      />
    );

    expect(screen.queryByTestId('session-group-turns')).not.toBeInTheDocument();
    expect(screen.getByTestId('all-settled')).toBeInTheDocument();
  });

  it('keeps the plain state grouping when the queue is off', () => {
    render(
      <Dashboard
        {...props}
        queueModeEnabled={false}
        sessions={[
          { id: 's1', label: 'asking', state: 'waiting_input', cwd: '/a', turnOwed: true, turnOpenedAt: '2026-07-29T09:00:00Z' },
        ]}
      />
    );

    expect(screen.queryByTestId('session-group-turns')).not.toBeInTheDocument();
    expect(screen.queryByTestId('session-group-settled')).not.toBeInTheDocument();
    expect(screen.queryByTestId('all-settled')).not.toBeInTheDocument();
    expect(screen.getByTestId('session-group-waiting')).toContainElement(screen.getByTestId('session-s1'));
  });

  // The wait has to be visible and reversible from the screen it happens on:
  // being moved to another agent without having been told is the surprise the
  // switch exists to remove, and an armed latch with no way off is a one-way
  // door.
  describe('the follow-next-turn switch', () => {
    const settled = [{ id: 's1', label: 'busy', state: 'working' as const, cwd: '/a', turnOwed: false }];

    it('shows what the wait is set to and turns it off from the banner', () => {
      const onToggleFollowNextTurn = vi.fn();
      render(
        <Dashboard
          {...props}
          sessions={settled}
          followNextTurn
          onToggleFollowNextTurn={onToggleFollowNextTurn}
        />
      );

      const box = screen.getByTestId('follow-next-turn').querySelector('input') as HTMLInputElement;
      expect(box.checked).toBe(true);
      fireEvent.click(box);
      expect(onToggleFollowNextTurn).toHaveBeenCalledTimes(1);
    });

    it('is off, and still offered, when the user walked home themselves', () => {
      render(
        <Dashboard {...props} sessions={settled} onToggleFollowNextTurn={vi.fn()} />
      );

      const box = screen.getByTestId('follow-next-turn').querySelector('input') as HTMLInputElement;
      expect(box.checked).toBe(false);
    });

    it('is absent while a turn is owed, since there is nothing to wait for', () => {
      render(
        <Dashboard
          {...props}
          sessions={[{ id: 's1', label: 'asking', state: 'waiting_input', cwd: '/a', turnOwed: true, turnOpenedAt: '2026-07-29T09:00:00Z' }]}
          followNextTurn
          onToggleFollowNextTurn={vi.fn()}
        />
      );

      expect(screen.queryByTestId('follow-next-turn')).not.toBeInTheDocument();
    });
  });
});
