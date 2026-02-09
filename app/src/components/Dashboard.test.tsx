import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Dashboard } from './Dashboard';

vi.mock('../contexts/DaemonContext', () => ({
  useDaemonContext: () => ({
    sendMuteRepo: vi.fn(),
    sendMuteAuthor: vi.fn(),
    sendPRVisited: vi.fn(),
  }),
}));

vi.mock('../store/daemonSessions', () => ({
  useDaemonStore: () => ({
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
        settings={{}}
        onSelectSession={vi.fn()}
        onNewSession={vi.fn()}
        onSetSetting={vi.fn()}
      />
    );

    expect(screen.getByTestId('session-group-working')).toBeInTheDocument();
    expect(screen.getByTestId('session-group-pending')).toBeInTheDocument();
    expect(screen.getByTestId('session-s1')).toBeInTheDocument();
    expect(screen.getByTestId('session-s2')).toBeInTheDocument();
    expect(screen.getByTestId('session-s3')).toBeInTheDocument();
  });
});
