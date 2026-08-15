import { StrictMode } from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { Sidebar } from './Sidebar';
import { WAKE_ARM_TIMEOUT_MS } from './CrewWake';
import { buildQueueBands, formatTurnAge } from '../utils/queueBands';
import { buildWorkspaceViewModels } from '../utils/workspaceViewModels';

interface TestSession {
  id: string;
  label: string;
  state: 'working' | 'waiting_input' | 'idle';
  workspaceId: string;
  chiefOfStaff?: boolean;
  turnOwed?: boolean;
  turnOpenedAt?: string;
  turnSnoozedUntil?: string;
  pinnedAt?: string;
  parentSessionId?: string;
  crewMember?: string;
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

  it('leaves the tree alone while the arrangement is off, pins and satellites included', () => {
    // Both new fields are queue vocabulary. The tree predates the queue and is
    // the arrangement people who never turn it on live in, so a session that
    // carries either one has to draw exactly where it always did.
    const tagged: TestSession[] = [
      ...sessions,
      { id: 'held', label: 'held', state: 'working', workspaceId: 'ws-b', pinnedAt: '2026-07-26T12:00:00Z' },
      { id: 'shell', label: 'shell', state: 'idle', workspaceId: 'ws-b', parentSessionId: 'older' },
    ];
    renderSidebar(tagged, false);

    for (const id of ['chief', 'newer', 'older', 'settled', 'held', 'shell']) {
      expect(screen.getByTestId(`sidebar-session-${id}`)).toBeTruthy();
    }
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

    fireEvent.click(screen.getByTestId('queue-select-older'));
    expect(onSelectSession).toHaveBeenCalledWith('older');
  });

  it('hands the agent over from the keyboard, so the queue is not mouse-only', () => {
    // The row opens through a real button rather than a click handler on the
    // row div, which is what makes it reachable by Tab and pressed by Enter or
    // Space without the component handling either key itself.
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onSelectSession });

    const open = screen.getByTestId('queue-select-older');
    expect(open.tagName).toBe('BUTTON');
    expect(open.getAttribute('aria-label')).toBe('Open older');

    open.focus();
    expect(document.activeElement).toBe(open);
  });

  it('settles a row without selecting it', () => {
    const onSettleTurn = vi.fn();
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onSettleTurn, onSelectSession });

    fireEvent.click(screen.getByTestId('queue-settle-older'));
    expect(onSettleTurn).toHaveBeenCalledWith('older');
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('pins the row\'s own agent without selecting it, from either band', () => {
    // A gesture aimed at a row acts on that row. `older` and `settled` share a
    // workspace, so the old behavior — pin the workspace — would have taken the
    // sibling out too, which is the friction this replaced.
    const onPinSession = vi.fn();
    const onSelectSession = vi.fn();
    renderSidebar(sessions, true, { onPinSession, onSelectSession });

    fireEvent.click(screen.getByTestId('queue-pin-older'));
    expect(onPinSession).toHaveBeenCalledWith('older', true);

    fireEvent.click(screen.getByTestId('queue-pin-settled'));
    expect(onPinSession).toHaveBeenLastCalledWith('settled', true);
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('draws a pinned agent in its own band below settled, and unpins it there', () => {
    // Pinning has to be reversible from where the row lands, or it is a one-way
    // door out of the queue.
    const onPinSession = vi.fn();
    const onSelectSession = vi.fn();
    const withPin: TestSession[] = [
      ...sessions,
      { id: 'held', label: 'held', state: 'working', workspaceId: 'ws-b', pinnedAt: '2026-07-26T12:00:00Z' },
    ];
    const { container } = renderSidebar(withPin, true, { onPinSession, onSelectSession });

    const bandRows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(bandRows).toContain('queue-pinned-held');
    expect(bandRows.indexOf('queue-pinned-held')).toBeGreaterThan(bandRows.indexOf('queue-settled-settled'));

    // Neither settle nor snooze: both answer "whose turn is it", and a pinned
    // agent has stepped out of that question.
    expect(screen.queryByTestId('queue-settle-held')).toBeNull();
    expect(screen.queryByTestId('queue-snooze-held')).toBeNull();

    fireEvent.click(screen.getByTestId('queue-unpin-held'));
    expect(onPinSession).toHaveBeenCalledWith('held', false);
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('gives a shell no row of its own while it sits beside its agent', () => {
    const withShell: TestSession[] = [
      ...sessions,
      { id: 'shell', label: 'shell', state: 'idle', workspaceId: 'ws-b', parentSessionId: 'older' },
      { id: 'orphan', label: 'orphan', state: 'idle', workspaceId: 'ws-b' },
    ];
    const { container } = renderSidebar(withShell, true);

    const bandRows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(bandRows).not.toContain('queue-settled-shell');
    // A shell with no agent to be reached through keeps its row: the queue
    // reorders, it never hides.
    expect(bandRows).toContain('queue-settled-orphan');
  });

  it('draws the chief once when its own workspace stays in the tree', () => {
    // The chief keeps its anchored slot whatever its workspace is, and pin and
    // mute are both reachable for it, so the surviving group must not draw it
    // again.
    const pinned = renderSidebar(sessions, true, {}, { 'ws-a': { pinned: true } });
    expect(pinned.getByTestId('queue-chief-chief')).toBeTruthy();
    expect(pinned.queryByTestId('sidebar-session-chief')).toBeNull();
    pinned.unmount();

    const data = sidebarData(sessions, { 'ws-a': { muted: true } });
    const muted = render(
      <Sidebar
        {...baseProps}
        {...data}
        workspaces={data.workspaces.filter((workspace) => !workspace.muted)}
        mutedWorkspaces={data.workspaces.filter((workspace) => workspace.muted)}
        mutedExpanded
        queue={buildQueueBands(data.workspaces)}
      />
    );
    expect(muted.getByTestId('queue-chief-chief')).toBeTruthy();
    expect(muted.queryByTestId('sidebar-session-chief')).toBeNull();
    expect(muted.getByTestId('sidebar-muted-workspace-ws-a')).toBeTruthy();
  });

  it('keeps the per-session menu reachable from every band', () => {
    // Chief of staff, close and reload live on this menu, which the workspace
    // tree row owns when the queue is off. Queue mode stops drawing that row.
    renderSidebar(sessions, true);
    for (const id of ['chief', 'older', 'settled']) {
      expect(screen.getByTestId(`session-actions-${id}`)).toBeTruthy();
    }
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

describe('snoozing from the sidebar', () => {
  // A real clock, because buildQueueBands compares the deadline against now.
  const inAnHour = () => new Date(Date.now() + 3600_000).toISOString();

  it('offers snooze on an owed turn and on a settled row alike', () => {
    // Deferring a run *before* it finishes — so the turn it would open never
    // opens — is the case the verb exists for, and that agent is settled.
    const onOpenSnooze = vi.fn();
    renderSidebar(sessions, true, { onOpenSnooze });

    fireEvent.click(screen.getByTestId('queue-snooze-older'));
    expect(onOpenSnooze).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: 'older' }),
      expect.anything(),
    );

    fireEvent.click(screen.getByTestId('queue-snooze-settled'));
    expect(onOpenSnooze).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: 'settled' }),
      expect.anything(),
    );
  });

  it('does not offer snooze on the chief, which never queues', () => {
    renderSidebar(sessions, true, { onOpenSnooze: vi.fn() });
    expect(screen.queryByTestId('queue-snooze-chief')).toBeNull();
  });

  it('takes a deferred agent out of the bands into its own collapsed section', () => {
    const deferred: TestSession[] = [
      ...sessions,
      { id: 'later', label: 'later', state: 'idle', workspaceId: 'ws-a', turnSnoozedUntil: inAnHour() },
    ];
    const { container } = renderSidebar(deferred, true, { onWakeTurn: vi.fn() });

    const bandRows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(bandRows).not.toContain('queue-settled-later');
    expect(bandRows).not.toContain('queue-turn-later');

    // Collapsed by default: a snooze surfaces itself when it wakes, so the
    // section is for checking on a promise rather than standing room.
    expect(screen.getByTestId('snoozed-section-header').textContent).toContain('Snoozed (1)');
    expect(screen.queryByTestId('queue-snoozed-later')).toBeNull();
  });

  it('shows when each deferred agent comes back, and wakes it without selecting it', () => {
    const onWakeTurn = vi.fn();
    const onSelectSession = vi.fn();
    const deferred: TestSession[] = [
      { id: 'later', label: 'later', state: 'idle', workspaceId: 'ws-a', turnSnoozedUntil: inAnHour() },
    ];
    renderSidebar(deferred, true, { onWakeTurn, onSelectSession });

    fireEvent.click(screen.getByTestId('snoozed-section-header'));
    const row = screen.getByTestId('queue-snoozed-later');
    expect(row.querySelector('.queue-row-wake-at')?.textContent).toBeTruthy();
    // Already closed: waking is the undo, not a second way to dismiss.
    expect(screen.queryByTestId('queue-settle-later')).toBeNull();

    fireEvent.click(screen.getByTestId('queue-wake-later'));
    expect(onWakeTurn).toHaveBeenCalledWith('later');
    expect(onSelectSession).not.toHaveBeenCalled();
  });

  it('sits above the muted workspaces', () => {
    // *Not yet* is nearer to your attention than *not ever*.
    const deferred: TestSession[] = [
      { id: 'later', label: 'later', state: 'idle', workspaceId: 'ws-a', turnSnoozedUntil: inAnHour() },
      { id: 'quiet', label: 'quiet', state: 'idle', workspaceId: 'ws-b' },
    ];
    const data = sidebarData(deferred, { 'ws-b': { muted: true } });
    const { container } = render(
      <Sidebar
        {...baseProps}
        {...data}
        workspaces={data.workspaces.filter((workspace) => !workspace.muted)}
        mutedWorkspaces={data.workspaces.filter((workspace) => workspace.muted)}
        onWakeTurn={vi.fn()}
        queue={buildQueueBands(data.workspaces)}
      />
    );

    const sections = Array.from(container.querySelectorAll('.muted-sessions-header'))
      .map((header) => header.textContent?.trim());
    expect(sections).toEqual(['▸Snoozed (1)', '▸Muted Workspaces (1)']);
  });

  it('draws no section at all while nothing is deferred', () => {
    renderSidebar(sessions, true, { onWakeTurn: vi.fn() });
    expect(screen.queryByTestId('sidebar-snoozed')).toBeNull();
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

describe('the crew in the sidebar', () => {
  const roster = [{ id: 'alder' }, { id: 'keel' }, { id: 'trellis' }];

  function renderCrew(
    crewSessions: TestSession[],
    overrides: Record<string, unknown> = {},
    crew: { id: string; binding_session?: string }[] = roster,
  ) {
    return renderSidebar([...sessions, ...crewSessions], true, { crew, ...overrides });
  }

  it('draws every member, awake or asleep, at the top of the pinned band', () => {
    const { container } = renderCrew([
      { id: 'sess-keel', label: 'keel of the day', state: 'working', workspaceId: 'ws-a', crewMember: 'keel' },
    ]);

    const rows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(rows.filter((id) => id?.startsWith('queue-crew-')))
      .toEqual(['queue-crew-alder', 'queue-crew-keel', 'queue-crew-trellis']);
    // Inside the pinned region, above the pins themselves.
    const headers = Array.from(container.querySelectorAll('.queue-bands > *'))
      .map((node) => node.textContent);
    expect(headers.some((text) => text?.startsWith('Pinned'))).toBe(true);

    // Awake vs asleep is visible without reading a label.
    expect(screen.getByTestId('queue-crew-keel').getAttribute('data-crew-state')).toBe('awake');
    expect(screen.getByTestId('queue-crew-alder').getAttribute('data-crew-state')).toBe('asleep');
    // Pin-shaped, and distinct from an ordinary pinned session.
    expect(screen.getByTestId('queue-crew-alder').className).toContain('queue-row--crew');
  });

  it('shows an awake member exactly once, under its own row', () => {
    const { container } = renderCrew([
      { id: 'sess-keel', label: 'keel of the day', state: 'working', workspaceId: 'ws-a', crewMember: 'keel', turnOwed: true, turnOpenedAt: '2026-07-26T08:00:00Z' },
    ]);

    const rows = Array.from(container.querySelectorAll('.queue-bands .queue-row'))
      .map((row) => row.getAttribute('data-testid'));
    expect(rows.filter((id) => id?.includes('keel'))).toEqual(['queue-crew-keel']);
  });

  it('wakes a sleeping member on the second click, asks an awake one to sleep, and focuses its day', () => {
    const onWakeCrewMember = vi.fn();
    const onSleepCrewMember = vi.fn();
    const onSelectSession = vi.fn();
    renderCrew(
      [{ id: 'sess-keel', label: 'keel of the day', state: 'working', workspaceId: 'ws-a', crewMember: 'keel' }] as TestSession[],
      { onWakeCrewMember, onSleepCrewMember, onSelectSession },
    );

    fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
    expect(onWakeCrewMember).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
    expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);

    fireEvent.click(screen.getByTestId('queue-crew-sleep-keel'));
    expect(onSleepCrewMember.mock.calls).toEqual([['keel']]);
    expect(onSelectSession).not.toHaveBeenCalled();

    // Focusing an awake member is not consequential and stays one click.
    fireEvent.click(screen.getByTestId('queue-crew-select-keel'));
    expect(onSelectSession.mock.calls).toEqual([['sess-keel']]);

    // Each act exists only on the side of the day where it makes sense.
    expect(screen.queryByTestId('queue-crew-wake-keel')).toBeNull();
    expect(screen.queryByTestId('queue-crew-sleep-trellis')).toBeNull();
    expect(screen.getByTestId('queue-crew-sleep-keel').getAttribute('aria-label')).toBe('Ask Keel to sleep');
  });

  it('writes a sleeping member as a name while the id stays the address', () => {
    renderCrew([], { onWakeCrewMember: vi.fn() });
    const row = screen.getByTestId('queue-crew-trellis');
    // The row reads Trellis; every hook into it — test id, data attribute, the
    // id handed back on wake — is the lowercase id.
    expect(row.textContent).toContain('Trellis');
    expect(row.textContent).not.toContain('trellis');
    expect(row.getAttribute('data-crew-member')).toBe('trellis');
    expect(screen.getByTestId('queue-crew-wake-trellis').getAttribute('aria-label')).toBe('Wake Trellis');
  });

  it('keeps the band when the crew is the only thing in it', () => {
    // Without a crew the pinned band is absent, so the members had nowhere to
    // render before this.
    renderCrew([]);
    expect(screen.getByTestId('queue-crew-alder')).toBeTruthy();
  });

  it('still draws a bound session whose member left the roster', () => {
    // Dropping the row would hide a running agent.
    renderCrew(
      [{ id: 'sess-ghost', label: 'ghost', state: 'working', workspaceId: 'ws-a', crewMember: 'sable' }] as TestSession[],
      {},
      roster,
    );
    expect(screen.getByTestId('queue-crew-sable').getAttribute('data-crew-state')).toBe('awake');
  });

  it('renders no crew rows while the queue arrangement is off', () => {
    renderSidebar(sessions, false, { crew: roster });
    expect(screen.queryByTestId('queue-crew-alder')).toBeNull();
  });

  describe('arming a wake', () => {
    // Waking starts a day of a durable identity and cannot be un-rung, so it
    // takes two deliberate clicks and an unconfirmed arm must never wake
    // anyone. Every disarm route is a separate test below.

    function armed(member: string) {
      return screen.getByTestId(`queue-crew-${member}`).getAttribute('data-crew-wake');
    }

    it('says what the second click does, and says it without the animation', () => {
      renderCrew([], { onWakeCrewMember: vi.fn() });
      const button = screen.getByTestId('queue-crew-wake-trellis');
      expect(armed('trellis')).toBeNull();

      fireEvent.click(button);

      expect(armed('trellis')).toBe('armed');
      expect(button.getAttribute('aria-label')).toBe('Wake Trellis — click again to confirm');
      // A word, not only a drawing: the motion is the delight, the text is the
      // contract, and one of them survives prefers-reduced-motion untouched.
      expect(screen.getByTestId('queue-crew-trellis').textContent).toContain('confirm');
      // The fill button covers the whole row and is the other confirm target,
      // so it has to make the same promise.
      expect(screen.getByTestId('queue-crew-select-trellis').getAttribute('aria-label'))
        .toBe('Wake Trellis — click again to confirm');
    });

    it('arms on the row and confirms on the sun, because they are one gesture', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-select-trellis'));
      expect(onWakeCrewMember).not.toHaveBeenCalled();
      expect(armed('trellis')).toBe('armed');

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
      expect(armed('trellis')).toBe('breaking');
    });

    it('is not a target while it is flaring', () => {
      // The flare lasts long enough to click into. Re-arming a row whose wake
      // is already sent would put a second wake one click away.
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));

      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
      expect(armed('trellis')).toBe('breaking');
    });

    it('stands down when the next click lands somewhere else', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.pointerDown(document.body);
      expect(armed('trellis')).toBeNull();

      // Disarmed for real: the next click arms again rather than waking.
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('arms one member at a time', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      // Reaching for another member's row is a click outside this one.
      fireEvent.pointerDown(screen.getByTestId('queue-crew-wake-alder'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-alder'));

      expect(armed('trellis')).toBeNull();
      expect(armed('alder')).toBe('armed');
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('arms one member at a time for the keyboard too', () => {
      // A keyboard user reaches the next row by focusing it, never by clicking
      // outside this one, so focus leaving the row has to disarm it as well.
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.focusIn(screen.getByTestId('queue-crew-wake-alder'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-alder'));

      expect(armed('trellis')).toBeNull();
      expect(armed('alder')).toBe('armed');
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('keeps the arm while focus moves inside its own row', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-select-trellis'));
      fireEvent.focusIn(screen.getByTestId('queue-crew-wake-trellis'));

      expect(armed('trellis')).toBe('armed');
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
    });

    it('stands down on Escape', () => {
      const onWakeCrewMember = vi.fn();
      renderCrew([], { onWakeCrewMember });

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.keyDown(document, { key: 'Escape' });

      expect(armed('trellis')).toBeNull();
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      expect(onWakeCrewMember).not.toHaveBeenCalled();
    });

    it('wakes once even where React runs the click path twice', () => {
      // StrictMode double-invokes state updaters, so sending the wake from
      // inside one would spend two days on the one click this earns.
      const onWakeCrewMember = vi.fn();
      const data = sidebarData(sessions);
      render(
        <StrictMode>
          <Sidebar
            {...baseProps}
            {...data}
            crew={roster}
            onWakeCrewMember={onWakeCrewMember}
            queue={buildQueueBands(data.workspaces)}
          />
        </StrictMode>,
      );

      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
      fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));

      expect(onWakeCrewMember.mock.calls).toEqual([['trellis']]);
    });

    it('stands down on its own, and wakes nobody doing it', () => {
      vi.useFakeTimers();
      try {
        const onWakeCrewMember = vi.fn();
        renderCrew([], { onWakeCrewMember });

        fireEvent.click(screen.getByTestId('queue-crew-wake-trellis'));
        expect(armed('trellis')).toBe('armed');

        act(() => {
          vi.advanceTimersByTime(WAKE_ARM_TIMEOUT_MS - 1);
        });
        expect(armed('trellis')).toBe('armed');

        act(() => {
          vi.advanceTimersByTime(1);
        });
        expect(armed('trellis')).toBeNull();
        expect(onWakeCrewMember).not.toHaveBeenCalled();
      } finally {
        vi.useRealTimers();
      }
    });
  });
});
