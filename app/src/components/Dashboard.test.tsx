import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Dashboard } from './Dashboard';

const daemonStoreState = vi.hoisted(() => ({
  chiefOfStaffDispatches: [] as Array<Record<string, unknown>>,
}));

vi.mock('../contexts/DaemonContext', () => ({
  useDaemonContext: () => ({
    sendMuteRepo: vi.fn(),
    sendMuteAuthor: vi.fn(),
    sendPRVisited: vi.fn(),
  }),
}));

vi.mock('../store/daemonSessions', () => ({
  useDaemonStore: () => ({
    chiefOfStaffDispatches: daemonStoreState.chiefOfStaffDispatches,
    repoStates: [],
    authorStates: [],
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
  beforeEach(() => {
    daemonStoreState.chiefOfStaffDispatches = [];
  });

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

  it('shows tracked dispatch status and latest report with session navigation', () => {
    daemonStoreState.chiefOfStaffDispatches = [{
      id: 'dispatch-1',
      chief_session_id: 'chief-1',
      session_id: 'worker-1',
      workspace_id: 'workspace-1',
      brief: 'Investigate the parser.',
      label: 'Parser investigation',
      agent: 'codex',
      directory: '/repo/a',
      status: 'working',
      status_since: '2026-06-09T10:00:00Z',
      latest_report: 'Root cause found; implementing the fix.',
      reported_at: '2026-06-09T10:10:00Z',
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:10:00Z',
    }];
    const onSelectSession = vi.fn();
    render(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
          { id: 'worker-1', label: 'parser-worker', state: 'waiting_input', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={onSelectSession}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    const dispatch = screen.getByTestId('chief-dispatch-dispatch-1');
    expect(dispatch).toHaveAttribute('data-state', 'waiting_input');
    expect(screen.getByText('Root cause found; implementing the fix.')).toBeInTheDocument();
    fireEvent.click(dispatch);
    expect(onSelectSession).toHaveBeenCalledWith('worker-1');
  });
});
