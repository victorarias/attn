import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { Dashboard } from './Dashboard';
import type { DaemonPR } from '../hooks/useDaemonSocket';

vi.mock('../contexts/DaemonContext', () => ({
  useDaemonContext: () => ({
    sendMuteRepo: vi.fn(),
    sendMuteAuthor: vi.fn(),
    sendPRVisited: vi.fn(),
  }),
}));

// The PR filter is the daemon-facing half of the card and has its own tests;
// what home is responsible for is how it arranges what comes back, so the hook
// is stubbed with a list the test controls.
const prFilter = vi.hoisted(() => ({
  activePRs: [] as unknown[],
  needsAttention: [] as unknown[],
  reviewRequested: [] as unknown[],
  yourPRs: [] as unknown[],
}));

vi.mock('../hooks/usePRsNeedingAttention', () => ({
  usePRsNeedingAttention: () => prFilter,
}));

beforeEach(() => {
  prFilter.activePRs = [];
  prFilter.needsAttention = [];
  prFilter.reviewRequested = [];
  prFilter.yourPRs = [];
});

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

  // `home_get_state` reports which agents are still grouped by what they are
  // doing, and reads that marker to find them. Only the groups that answer that
  // question carry it — the turn band and the snoozed section share the testid
  // prefix and answer a different one.
  it('marks the state groups so a reader can tell them from the turn band', () => {
    const later = new Date(Date.now() + 60 * 60 * 1000).toISOString();
    render(
      <Dashboard
        {...props}
        sessions={[
          { id: 's1', label: 'owed', state: 'waiting_input', cwd: '/a', turnOwed: true, turnOpenedAt: '2026-07-29T09:00:00Z' },
          { id: 's2', label: 'busy', state: 'working', cwd: '/b', turnOwed: false },
          { id: 's3', label: 'deferred', state: 'working', cwd: '/c', turnSnoozedUntil: later },
        ]}
      />
    );

    const marked = Array.from(document.querySelectorAll('[data-session-group="state"] .session-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(marked).toEqual(['session-s2']);
    expect(screen.getByTestId('session-group-turns')).not.toHaveAttribute('data-session-group');
    expect(screen.getByTestId('session-group-snoozed')).not.toHaveAttribute('data-session-group');
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

// A snooze is an answer to "whose turn is it" — not yours, not yet — so a
// deferred agent's state has stopped describing anything the user asked about.
// Leaving it under Working or Idle is what these cover.
describe('Dashboard snoozed agents', () => {
  const later = new Date(Date.now() + 60 * 60 * 1000).toISOString();
  const props = {
    prs: [],
    isLoading: false,
    onSelectSession: vi.fn(),
    onNewSession: vi.fn(),
    onOpenSettings: vi.fn(),
  };

  it('takes deferred agents out of the state groups and collects them under Snoozed', () => {
    render(
      <Dashboard
        {...props}
        queueModeEnabled
        sessions={[
          { id: 's1', label: 'deferred', state: 'working', cwd: '/a', turnSnoozedUntil: later },
          { id: 's2', label: 'running', state: 'working', cwd: '/b' },
        ]}
      />
    );

    const working = screen.getByTestId('session-group-working');
    expect(working).toContainElement(screen.getByTestId('session-s2'));
    expect(screen.queryByTestId('session-s1')).not.toBeInTheDocument();

    const snoozed = screen.getByTestId('session-group-snoozed');
    expect(snoozed).toHaveTextContent('Snoozed');
    expect(snoozed).toHaveTextContent('1');
  });

  it('is collapsed until asked, then lists each wake time', () => {
    const soon = new Date(Date.now() + 30 * 60 * 1000).toISOString();
    render(
      <Dashboard
        {...props}
        queueModeEnabled
        sessions={[
          { id: 's1', label: 'wakes-later', state: 'idle', cwd: '/a', turnSnoozedUntil: later },
          { id: 's2', label: 'wakes-sooner', state: 'idle', cwd: '/b', turnSnoozedUntil: soon },
        ]}
      />
    );

    expect(screen.queryByTestId('session-s1')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('session-group-snoozed-header'));

    const rows = screen.getByTestId('session-group-snoozed');
    const names = Array.from(rows.querySelectorAll('.session-name')).map((n) => n.textContent);
    expect(names).toEqual(['wakes-sooner', 'wakes-later']);
    expect(rows.querySelectorAll('.session-wake-at')).toHaveLength(2);
  });

  it('wakes one early', () => {
    const onWakeTurn = vi.fn();
    render(
      <Dashboard
        {...props}
        queueModeEnabled
        onWakeTurn={onWakeTurn}
        sessions={[{ id: 's1', label: 'deferred', state: 'idle', cwd: '/a', turnSnoozedUntil: later }]}
      />
    );

    fireEvent.click(screen.getByTestId('session-group-snoozed-header'));
    fireEvent.click(screen.getByTestId('session-wake-s1'));
    expect(onWakeTurn).toHaveBeenCalledWith('s1');
  });

  // Every other way to end a snooze early is queue-gated, so with the
  // arrangement off this section is the deferral's only way out.
  it('still shows deferred agents, and the way to wake them, with the queue off', () => {
    const onWakeTurn = vi.fn();
    render(
      <Dashboard
        {...props}
        queueModeEnabled={false}
        onWakeTurn={onWakeTurn}
        sessions={[{ id: 's1', label: 'deferred', state: 'idle', cwd: '/a', turnSnoozedUntil: later }]}
      />
    );

    expect(screen.getByTestId('session-group-snoozed')).toBeInTheDocument();
    expect(screen.queryByTestId('session-group-idle')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('session-group-snoozed-header'));
    expect(screen.getByTestId('session-wake-s1')).toBeInTheDocument();
  });

  // The sidebar pulls the chief out of its bands entirely; home lists it with
  // the rest, so a deferred chief has to land here or it lands nowhere true.
  it('collects a deferred chief too', () => {
    render(
      <Dashboard
        {...props}
        queueModeEnabled
        sessions={[
          { id: 'chief', label: 'chief', state: 'idle', cwd: '/a', chiefOfStaff: true, turnSnoozedUntil: later },
        ]}
      />
    );

    fireEvent.click(screen.getByTestId('session-group-snoozed-header'));
    expect(screen.getByTestId('session-group-snoozed')).toContainElement(screen.getByTestId('session-chief'));
  });

  it('leaves a lapsed deadline alone', () => {
    const past = new Date(Date.now() - 60 * 1000).toISOString();
    render(
      <Dashboard
        {...props}
        queueModeEnabled
        sessions={[{ id: 's1', label: 'awake', state: 'working', cwd: '/a', turnSnoozedUntil: past }]}
      />
    );

    expect(screen.queryByTestId('session-group-snoozed')).not.toBeInTheDocument();
    expect(screen.getByTestId('session-group-working')).toContainElement(screen.getByTestId('session-s1'));
  });

  it('renders no keyboard-shortcut strip', () => {
    const { container } = render(<Dashboard {...props} sessions={[]} />);
    expect(container.querySelector('.dashboard-footer')).toBeNull();
  });
});

describe('Dashboard pull requests', () => {
  const props = {
    sessions: [],
    prs: [],
    isLoading: false,
    onSelectSession: vi.fn(),
    onNewSession: vi.fn(),
    onOpenSettings: vi.fn(),
  };

  const pr = (over: Partial<DaemonPR> & Pick<DaemonPR, 'id' | 'number' | 'repo' | 'role'>): DaemonPR => ({
    approved_by_me: false,
    author: 'someone',
    details_fetched: true,
    has_new_changes: false,
    host: 'github.com',
    last_polled: '2026-08-05T10:00:00Z',
    last_updated: '2026-08-05T10:00:00Z',
    muted: false,
    reason: 'review_needed',
    state: 'waiting',
    title: `PR ${over.number}`,
    url: `https://github.com/${over.repo}/pull/${over.number}`,
    ...over,
  } as DaemonPR);

  it('leads with the PRs you opened, flat and named by repo', () => {
    prFilter.activePRs = [
      pr({ id: 'r1', number: 10, repo: 'victorarias/attn', role: 'reviewer' as DaemonPR['role'] }),
      pr({ id: 'a1', number: 20, repo: 'victorarias/attn', role: 'author' as DaemonPR['role'], author: 'victorarias' }),
      pr({ id: 'a2', number: 21, repo: 'other/tool', role: 'author' as DaemonPR['role'], author: 'victorarias' }),
    ];
    render(<Dashboard {...props} />);

    const yours = screen.getByTestId('pr-section-yours');
    expect(yours).toHaveTextContent('Yours');
    expect(yours.querySelectorAll('[data-testid="pr-card"]')).toHaveLength(2);
    expect(Array.from(yours.querySelectorAll('.pr-repo-inline')).map((n) => n.textContent))
      .toEqual(['attn', 'tool']);
    // No repo groups on this side — the repo is on the row.
    expect(yours.querySelector('.pr-repo-group')).toBeNull();

    const review = screen.getByTestId('pr-section-review');
    expect(review.querySelectorAll('[data-testid="pr-card"]')).toHaveLength(1);
    expect(review.querySelector('.repo-name')).toHaveTextContent('attn');
    expect(review.querySelector('.pr-repo-inline')).toBeNull();
  });

  it('omits a section with nothing in it', () => {
    prFilter.activePRs = [
      pr({ id: 'r1', number: 10, repo: 'victorarias/attn', role: 'reviewer' as DaemonPR['role'] }),
    ];
    render(<Dashboard {...props} />);

    expect(screen.queryByTestId('pr-section-yours')).not.toBeInTheDocument();
    expect(screen.getByTestId('pr-section-review')).toBeInTheDocument();
  });

  it('says nothing needs attention when both sides are empty', () => {
    render(<Dashboard {...props} />);
    expect(screen.getByText('No PRs need attention')).toBeInTheDocument();
  });
});
