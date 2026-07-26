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

function sidebarData(sessions: TestSession[]) {
  const workspaces = buildWorkspaceViewModels(
    [
      { id: 'ws-a', title: 'alpha', directory: '/repo/a', rank: 'a' },
      { id: 'ws-b', title: 'beta', directory: '/repo/b', rank: 'b' },
    ],
    sessions,
  );
  return {
    workspaces,
    visualOrder: workspaces,
    visualIndexByWorkspaceId: new Map(workspaces.map((workspace, index) => [workspace.id, index])),
  };
}

function renderSidebar(sessions: TestSession[], queueMode: boolean, overrides = {}) {
  const data = sidebarData(sessions);
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

  it('lists owed turns oldest first, with the chief anchored above them', () => {
    const { container } = renderSidebar(sessions, true);

    const rows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(rows).toEqual(['queue-chief-chief', 'queue-turn-older', 'queue-turn-newer']);
  });

  it('keeps the workspace tree complete and unchanged — the queue only adds rows', () => {
    const off = renderSidebar(sessions, false);
    const treeOff = Array.from(off.container.querySelectorAll('.session-list [data-testid^="sidebar-session-"]'))
      .map((row) => row.getAttribute('data-testid'));
    off.unmount();

    const on = renderSidebar(sessions, true);
    const treeOn = Array.from(on.container.querySelectorAll('.session-list [data-testid^="sidebar-session-"]'))
      .map((row) => row.getAttribute('data-testid'));

    expect(treeOn).toEqual(treeOff);
    expect(treeOn).toContain('sidebar-session-older');
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
