import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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
    expect(screen.getByText('Chief session')).toBeInTheDocument();
    expect(screen.getByText('Delegated work')).toBeInTheDocument();
    expect(screen.getByText('codex agent')).toBeInTheDocument();
    expect(screen.getByText('Agent status: waiting input')).toBeInTheDocument();
    expect(screen.getByText('Task')).toBeInTheDocument();
    expect(screen.getByText('Latest update')).toBeInTheDocument();
    expect(screen.getByText('Root cause found; implementing the fix.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /parser-worker/i }));
    expect(onSelectSession).toHaveBeenCalledWith('worker-1');
  });

  it('shows unread delegated messages and explicitly wakes an idle worker', async () => {
    daemonStoreState.chiefOfStaffDispatches = [{
      id: 'dispatch-mail',
      chief_session_id: 'chief-1',
      session_id: 'worker-1',
      workspace_id: 'workspace-1',
      brief: 'Investigate the parser.',
      label: 'Parser investigation',
      agent: 'codex',
      directory: '/repo/a',
      status: 'idle',
      status_since: '2026-06-09T10:00:00Z',
      unread_message_count: 2,
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:00:00Z',
    }];
    const onWakeDispatch = vi.fn().mockResolvedValue(undefined);
    render(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
          { id: 'worker-1', label: 'parser-worker', state: 'idle', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onWakeDispatch={onWakeDispatch}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByText('2 unread')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Wake agent' }));
    await waitFor(() => {
      expect(onWakeDispatch).toHaveBeenCalledWith('chief-1', 'dispatch-mail');
    });
  });

  it('does not offer wake while a delegated worker is active', () => {
    daemonStoreState.chiefOfStaffDispatches = [{
      id: 'dispatch-working-mail',
      chief_session_id: 'chief-1',
      session_id: 'worker-1',
      workspace_id: 'workspace-1',
      brief: 'Investigate the parser.',
      label: 'Parser investigation',
      agent: 'codex',
      directory: '/repo/a',
      status: 'working',
      status_since: '2026-06-09T10:00:00Z',
      unread_message_count: 1,
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:00:00Z',
    }];
    render(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
          { id: 'worker-1', label: 'parser-worker', state: 'working', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onWakeDispatch={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    expect(screen.getByText('1 unread')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Wake agent' })).not.toBeInTheDocument();
  });

  it('hides a dispatch after its delegated session is closed', () => {
    daemonStoreState.chiefOfStaffDispatches = [{
      id: 'dispatch-closed',
      chief_session_id: 'chief-1',
      session_id: 'worker-closed',
      workspace_id: 'workspace-1',
      brief: 'Investigate the parser.',
      label: 'Parser investigation',
      agent: 'codex',
      directory: '/repo/a',
      status: 'closed',
      status_since: '',
      latest_report: 'Investigation complete.',
      reported_at: '2026-06-09T10:10:00Z',
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:10:00Z',
    }];

    const props = {
      prs: [],
      isLoading: false,
      onSelectSession: vi.fn(),
      onNewSession: vi.fn(),
      onOpenSettings: vi.fn(),
    };
    const { rerender } = render(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
          { id: 'worker-closed', label: 'parser-worker', state: 'idle', cwd: '/repo/a' },
        ]}
        {...props}
      />
    );

    expect(screen.getByTestId('chief-dispatch-dispatch-closed')).toBeInTheDocument();

    rerender(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
        ]}
        {...props}
      />
    );

    expect(screen.queryByTestId('chief-dispatch-dispatch-closed')).not.toBeInTheDocument();
    expect(screen.getByText('No delegated work yet.')).toBeInTheDocument();
  });

  it('renders actionable structured coordination without showing the full brief', () => {
    daemonStoreState.chiefOfStaffDispatches = [{
      id: 'dispatch-structured',
      chief_session_id: 'chief-1',
      session_id: 'worker-1',
      workspace_id: 'workspace-1',
      brief: 'Long delegation brief that should not appear in the concise structured view.',
      label: 'Freshness gate',
      agent: 'codex',
      directory: '/repo/a',
      status: 'idle',
      status_since: '2026-06-09T10:00:00Z',
      latest_report: 'Human narrative with additional implementation detail.',
      reported_at: new Date(Date.now() - 120_000).toISOString(),
      concise_summary: 'Core freshness gate implemented locally',
      actionable: true,
      structured_report: {
        report_type: 'blocker',
        summary: 'Core freshness gate implemented locally',
        work_state: 'needs_input',
        next_actor: 'team',
        next_action: 'Decide the AisNoOperationV1 event contract',
        request: {
          question: 'Should the worker emit AisNoOperationV1?',
          expected_responder: 'team',
          status: 'pending',
        },
        reported_at: '2026-06-09T10:02:00Z',
      },
      created_at: '2026-06-09T10:00:00Z',
      updated_at: '2026-06-09T10:02:00Z',
    }];
    render(
      <Dashboard
        sessions={[
          { id: 'chief-1', label: 'planner', state: 'working', cwd: '/repo/a', chiefOfStaff: true },
          { id: 'worker-1', label: 'freshness-worker', state: 'idle', cwd: '/repo/a' },
        ]}
        prs={[]}
        isLoading={false}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onOpenSettings={vi.fn()}
      />
    );

    const dispatch = screen.getByTestId('chief-dispatch-dispatch-structured');
    expect(dispatch).toHaveAttribute('data-state', 'idle');
    expect(dispatch).toHaveAttribute('data-actionable', 'true');
    expect(screen.getByText('Core freshness gate implemented locally')).toBeInTheDocument();
    expect(screen.getByText('Work: needs input')).toBeInTheDocument();
    expect(screen.getByText('Action needed')).toBeInTheDocument();
    expect(screen.getByText('Next: team')).toBeInTheDocument();
    expect(screen.getByText('Decide the AisNoOperationV1 event contract')).toBeInTheDocument();
    expect(screen.getByText('Should the worker emit AisNoOperationV1?')).toBeInTheDocument();
    expect(screen.getByText(/Reported .* ago/)).toBeInTheDocument();
    expect(screen.queryByText(/Long delegation brief/)).not.toBeInTheDocument();
  });
});
