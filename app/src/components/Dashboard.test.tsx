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

  it('shows review loop state on session rows', () => {
    render(
      <Dashboard
        sessions={[
          { id: 's1', label: 'review-bot', state: 'working', cwd: '/repo/a', reviewLoopStatus: 'running' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByLabelText('Review loop running')).toBeInTheDocument();
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
