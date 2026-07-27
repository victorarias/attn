import { fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { formatTurnAge } from './QueueBands';
import { buildQueueBands } from '../utils/queueBands';
import { buildWorkspaceViewModels } from '../utils/workspaceViewModels';

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  workspaceId: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
}

const baseProps = {
  selectedId: null,
  selectedWorkspaceId: null,
  collapsed: false,
  headerActions: [],
  onSelectSession: () => {},
  onSelectWorkspace: () => {},
  onNewSession: () => {},
  onCloseSession: () => {},
  onReloadSession: () => {},
  onGoToDashboard: () => {},
  onToggleCollapse: () => {},
};

type WorkspaceFlags = { pinned?: boolean; muted?: boolean };

function sidebarData(sessions: TestSession[], flags: Record<string, WorkspaceFlags> = {}) {
  const workspaces = buildWorkspaceViewModels(
    [
      { id: 'ws-a', title: 'alpha', directory: '/repo/a', rank: 'a', ...flags['ws-a'] },
      { id: 'ws-b', title: 'beta', directory: '/repo/b', rank: 'b', ...flags['ws-b'] },
    ],
    sessions,
  );
  return {
    workspaces,
    visualOrder: workspaces,
    visualIndexByWorkspaceId: new Map(workspaces.map((workspace, index) => [workspace.id, index])),
  };
}

function renderSidebar(
  sessions: TestSession[],
  queueMode: boolean,
  overrides = {},
  workspaceFlags: Record<string, WorkspaceFlags> = {},
) {
  const data = sidebarData(sessions, workspaceFlags);
  return render(
    <Sidebar
      {...baseProps}
      {...data}
      {...overrides}
      queue={queueMode ? buildQueueBands(data.workspaces) : null}
    />
  );
}

const sessions: TestSession[] = [
  { id: 'chief', label: 'chief', state: 'idle', workspaceId: 'ws-a', chiefOfStaff: true },
  { id: 'newer', label: 'newer', state: 'waiting_input', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T11:00:00Z' },
  { id: 'older', label: 'older', state: 'working', workspaceId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
  { id: 'settled', label: 'settled', state: 'waiting_input', workspaceId: 'ws-b' },
];

describe('the queue arrangement', () => {
  it('renders nothing extra while the arrangement is off', () => {
    renderSidebar(sessions, false);
    expect(screen.queryByTestId('sidebar-queue')).toBeNull();
  });

  it('lists owed turns oldest first, then the settled rest, with the chief anchored above both', () => {
    const { container } = renderSidebar(sessions, true);

    const rows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(rows).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer', 'queue-settled-settled']);
  });

  it('draws each agent exactly once — the bands replace the tree, they do not sit on top of it', () => {
    // The duplication this replaces made a row look like it moved when only one
    // of an agent's two copies did.
    const { container } = renderSidebar(sessions, true);

    const tree = Array.from(container.querySelectorAll('.session-list [data-testid^="sidebar-session-"]'))
      .map((row) => row.getAttribute('data-testid'));
    expect(tree).toEqual([]);
    expect(container.querySelectorAll(
      '[data-testid="queue-turn-older"], [data-testid="sidebar-session-older"]',
    )).toHaveLength(1);
  });

  it('keeps pinned workspaces as groups, and their agents out of both bands', () => {
    // Pinned is the way out of the queue, so a pinned agent must not be in it —
    // and the group has to survive, because it is where you go and get that work.
    const { container } = renderSidebar(sessions, true, {}, { 'ws-b': { pinned: true } });

    const bandRows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(bandRows).toEqual(['queue-chief-chief', 'queue-turn-newer']);

    const tree = Array.from(container.querySelectorAll('.session-list [data-testid^="sidebar-session-"]'))
      .map((row) => row.getAttribute('data-testid'));
    expect(tree).toEqual(['sidebar-session-older', 'sidebar-session-settled']);
  });

  it('shows the live state of a queued agent, because being queued no longer means stopped', () => {
    renderSidebar(sessions, true);
    expect(screen.getByTestId('queue-turn-older').getAttribute('data-state')).toBe('working');
  });

  it('hands the agent over on click', () => {
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onSelectSession });

    fireEvent.click(screen.getByTestId('queue-turn-older'));
    expect(onSelectSession).toHaveBeenCalledWith('older');
  });

  it('settles a row without selecting it', () => {
    const onSettleTurn = vi.fn();
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onSettleTurn, onSelectSession });

    fireEvent.click(screen.getByTestId('queue-settle-older'));
    expect(onSettleTurn).toHaveBeenCalledWith('older');
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('pins a row\'s workspace without selecting it, from either band', () => {
    // The workspace group header owns pin in the tree, and queue mode does not
    // draw the group for these workspaces. Without this the queue would be a
    // one-way door: pinning is how an agent leaves it for good.
    const onPinWorkspace = vi.fn();
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onPinWorkspace, onSelectSession });

    fireEvent.click(screen.getByTestId('queue-pin-older'));
    expect(onPinWorkspace).toHaveBeenCalledWith('ws-b', true);

    fireEvent.click(screen.getByTestId('queue-pin-settled'));
    expect(onPinWorkspace).toHaveBeenLastCalledWith('ws-b', true);
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('offers no settle affordance on a settled row, which has nothing to discharge', () => {
    renderSidebar(sessions, true);
    expect(screen.getByTestId('queue-settled-settled')).toBeTruthy();
    expect(screen.queryByTestId('queue-settle-settled')).toBeNull();
  });

  it('offers no settle affordance on the chief, which never queues', () => {
    renderSidebar(sessions, true);
    expect(screen.queryByTestId('queue-settle-chief')).toBeNull();
  });

  it('says so when nothing is owed', () => {
    renderSidebar([sessions[0], sessions[3]], true);
    expect(screen.getByTestId('queue-empty')).toBeInTheDocument();
  });

  it('follows turn_owed rather than state for the collapsed rail badge', () => {
    // `settled` is waiting_input but owes nothing; `older` is working and does.
    const owedOnly: TestSession[] = [
      { id: 'settled', label: 'settled', state: 'waiting_input', workspaceId: 'ws-a' },
      { id: 'owed', label: 'owed', state: 'working', workspaceId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
    ];

    const off = renderSidebar(owedOnly, false, { collapsed: true });
    expect(off.container.querySelectorAll('.mini-badge')).toHaveLength(1);
    expect(off.container.querySelector('.session-icon .mini-badge')).toBeTruthy();
    off.unmount();

    const on = renderSidebar(owedOnly, true, { collapsed: true });
    const badgedTitles = Array.from(on.container.querySelectorAll('.session-icon'))
      .filter((icon) => icon.querySelector('.mini-badge'))
      .map((icon) => icon.getAttribute('title'));
    expect(badgedTitles).toHaveLength(1);
    expect(badgedTitles[0]).toContain('beta');
  });
});

describe('formatTurnAge', () => {
  const opened = Date.parse('2026-07-26T10:00:00Z');

  it('reads how long the turn has been owed', () => {
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 30_000)).toBe('now');
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 12 * 60_000)).toBe('12m');
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 3 * 3600_000)).toBe('3h');
    expect(formatTurnAge('2026-07-26T10:00:00Z', opened + 50 * 3600_000)).toBe('2d');
  });

  it('is empty when there is no stamp', () => {
    expect(formatTurnAge(undefined, opened)).toBe('');
    expect(formatTurnAge('not a date', opened)).toBe('');
  });
});
