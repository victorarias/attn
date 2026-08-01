import { describe, expect, it } from 'vitest';
import { buildQueueBands, nextTurnAfterSettle, oldestWantedTurn, type QueueBandSession } from './queueBands';
import { buildWorkspaceViewModels } from './workspaceViewModels';

const workspaces = [
  { id: 'ws-a', title: 'A', directory: '/repo/a', rank: 'a' },
  { id: 'ws-b', title: 'B', directory: '/repo/b', rank: 'b' },
];

function views(sessions: QueueBandSession[]) {
  return buildWorkspaceViewModels(workspaces, sessions);
}

describe('buildQueueBands', () => {
  it('lists the oldest turn first, across workspaces', () => {
    const bands = buildQueueBands(views([
      { id: 'newest', label: 'newest', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T12:00:00Z' },
      { id: 'oldest', label: 'oldest', workspaceId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'middle', label: 'middle', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
    ]));

    expect(bands.turns.map((row) => row.session.id)).toEqual(['oldest', 'middle', 'newest']);
    expect(bands.turns.map((row) => row.workspaceTitle)).toEqual(['B', 'A', 'A']);
  });

  it('reads turn_owed rather than deriving it from state', () => {
    // The daemon applies exclusions the client cannot see, so a session that
    // looks like it wants attention but is not owed must not appear.
    const bands = buildQueueBands(views([
      { id: 'waiting-but-settled', label: 'a', workspaceId: 'ws-a', state: 'waiting_input', turnOwed: false },
      { id: 'working-but-owed', label: 'b', workspaceId: 'ws-a', state: 'working', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
    ]));

    expect(bands.turns.map((row) => row.session.id)).toEqual(['working-but-owed']);
  });

  it('puts a new arrival at the bottom and leaves the rows above untouched', () => {
    const existing: QueueBandSession[] = [
      { id: 'first', label: 'first', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'second', label: 'second', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
    ];
    const before = buildQueueBands(views(existing));
    const after = buildQueueBands(views([
      ...existing,
      { id: 'arrival', label: 'arrival', workspaceId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T11:00:00Z' },
    ]));

    expect(after.turns.map((row) => row.session.id))
      .toEqual([...before.turns.map((row) => row.session.id), 'arrival']);
  });

  it('does not move a row when its state changes', () => {
    const session = (state: string): QueueBandSession => ({
      id: 'steered', label: 'steered', workspaceId: 'ws-a', state, turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z',
    });
    const others: QueueBandSession[] = [
      { id: 'older', label: 'older', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'newer', label: 'newer', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T11:00:00Z' },
    ];

    const waiting = buildQueueBands(views([...others, session('waiting_input')]));
    const working = buildQueueBands(views([...others, session('working')]));

    expect(waiting.turns.map((row) => row.session.id)).toEqual(['older', 'steered', 'newer']);
    expect(working.turns.map((row) => row.session.id)).toEqual(['older', 'steered', 'newer']);
  });

  it('settling a row moves only the rows below it', () => {
    const all: QueueBandSession[] = [
      { id: 'a', label: 'a', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'b', label: 'b', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
      { id: 'c', label: 'c', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T11:00:00Z' },
    ];
    const settled = all.map((session) => (session.id === 'b' ? { ...session, turnOwed: false } : session));

    expect(buildQueueBands(views(settled)).turns.map((row) => row.session.id)).toEqual(['a', 'c']);
  });

  it('anchors the chief and never queues it', () => {
    const bands = buildQueueBands(views([
      { id: 'chief', label: 'chief', workspaceId: 'ws-a', chiefOfStaff: true, turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'agent', label: 'agent', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
    ]));

    expect(bands.chief?.session.id).toBe('chief');
    expect(bands.turns.map((row) => row.session.id)).toEqual(['agent']);
  });

  it('is empty when nothing is owed', () => {
    const bands = buildQueueBands(views([
      { id: 'a', label: 'a', workspaceId: 'ws-a', state: 'working' },
    ]));

    expect(bands.chief).toBeNull();
    expect(bands.turns).toEqual([]);
  });

  it('puts everything not owed into settled, so no agent is in both bands', () => {
    const bands = buildQueueBands(views([
      { id: 'owed', label: 'owed', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'quiet', label: 'quiet', workspaceId: 'ws-a' },
      { id: 'busy', label: 'busy', workspaceId: 'ws-b', state: 'working' },
    ]));

    expect(bands.turns.map((row) => row.session.id)).toEqual(['owed']);
    expect(bands.settled.map((row) => row.session.id)).toEqual(['quiet', 'busy']);
  });

  it('settling moves a row from one band to the other rather than out of the sidebar', () => {
    const session: QueueBandSession = {
      id: 'a', label: 'a', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z',
    };
    const before = buildQueueBands(views([session]));
    const after = buildQueueBands(views([{ ...session, turnOwed: false }]));

    expect(before.turns.map((row) => row.session.id)).toEqual(['a']);
    expect(before.settled).toEqual([]);
    expect(after.turns).toEqual([]);
    expect(after.settled.map((row) => row.session.id)).toEqual(['a']);
  });

  it('keeps pinned and muted workspaces out of both bands — they stay in the tree', () => {
    // Pinning is the way out of the queue entirely, and muting is its absolute
    // sibling. Either one landing in Settled would put it back in the list it
    // was taken out of.
    const pinnedAndMuted = [
      { id: 'ws-a', title: 'A', directory: '/repo/a', rank: 'a', pinned: true },
      { id: 'ws-b', title: 'B', directory: '/repo/b', rank: 'b', muted: true },
    ];
    const bands = buildQueueBands(buildWorkspaceViewModels(pinnedAndMuted, [
      { id: 'pinned-owed', label: 'a', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'muted-quiet', label: 'b', workspaceId: 'ws-b' },
    ]));

    expect(bands.turns).toEqual([]);
    expect(bands.settled).toEqual([]);
  });

  it('anchors the chief even when its workspace is pinned or muted', () => {
    // The chief is the seat you always want to reach, not a piece of work you
    // filed away, so neither pin nor mute takes its slot from it.
    for (const flag of [{ pinned: true }, { muted: true }]) {
      const bands = buildQueueBands(buildWorkspaceViewModels(
        [{ id: 'ws-a', title: 'A', directory: '/repo/a', rank: 'a', ...flag }],
        [{ id: 'chief', label: 'chief', workspaceId: 'ws-a', chiefOfStaff: true }],
      ));

      expect(bands.chief?.session.id).toBe('chief');
    }
  });

  it('leaves the workspace tree untouched — it is not an output of the queue', () => {
    const sessions: QueueBandSession[] = [
      { id: 'a', label: 'a', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'b', label: 'b', workspaceId: 'ws-b' },
    ];
    const tree = views(sessions);
    buildQueueBands(tree);

    expect(tree.map((workspace) => workspace.sessions.map((session) => session.id))).toEqual([['a'], ['b']]);
  });
});

describe('oldestWantedTurn', () => {
  const wantsOwed = (session: QueueBandSession) => Boolean(session.turnOwed);

  it('lands on the turn owed longest, not the first in list order', () => {
    // The list arrives in workspace order; ⌘J must follow the queue's order.
    const target = oldestWantedTurn([
      { id: 'newest', label: 'newest', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T12:00:00Z' },
      { id: 'oldest', label: 'oldest', workspaceId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
      { id: 'middle', label: 'middle', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
    ], wantsOwed);

    expect(target?.id).toBe('oldest');
  });

  it('skips sessions that do not want the user, whatever their stamp says', () => {
    // A settled turn keeps its turnOpenedAt until the next turn opens; being
    // old is not being owed.
    const target = oldestWantedTurn([
      { id: 'settled-old', label: 'a', workspaceId: 'ws-a', turnOwed: false, turnOpenedAt: '2026-07-26T08:00:00Z' },
      { id: 'owed', label: 'b', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T10:00:00Z' },
    ], wantsOwed);

    expect(target?.id).toBe('owed');
  });

  it('is null when nothing wants the user', () => {
    expect(oldestWantedTurn([
      { id: 'quiet', label: 'quiet', workspaceId: 'ws-a' },
    ], wantsOwed)).toBeNull();
    expect(oldestWantedTurn([], wantsOwed)).toBeNull();
  });
});

describe('nextTurnAfterSettle', () => {
  function turnsFor(ids: string[]) {
    return buildQueueBands(views(ids.map((id, index) => ({
      id,
      label: id,
      workspaceId: 'ws-a',
      turnOwed: true,
      turnOpenedAt: `2026-07-26T0${index}:00:00Z`,
    })))).turns;
  }

  it('lands on the row after the one settled, in queue order', () => {
    const turns = turnsFor(['oldest', 'middle', 'newest']);

    expect(nextTurnAfterSettle(turns, 'oldest')?.session.id).toBe('middle');
    expect(nextTurnAfterSettle(turns, 'middle')?.session.id).toBe('newest');
  });

  it('wraps to the top when the last row is settled', () => {
    // Queue order is not attention order: the rows above are still owed, so the
    // bottom row moves on to the oldest rather than falling off the end.
    const turns = turnsFor(['oldest', 'middle', 'newest']);

    expect(nextTurnAfterSettle(turns, 'newest')?.session.id).toBe('oldest');
  });

  it('never lands on the row just settled', () => {
    // The band is read before the settle, so the settled row is still in it.
    expect(nextTurnAfterSettle(turnsFor(['only']), 'only')).toBeNull();
    expect(nextTurnAfterSettle([], 'anything')).toBeNull();
    expect(nextTurnAfterSettle(turnsFor(['only']), null)?.session.id).toBe('only');
  });

  it('starts at the top for a session the band does not hold', () => {
    // Already settled, pinned, muted, or the chief: no position to move on from.
    const turns = turnsFor(['oldest', 'newest']);

    expect(nextTurnAfterSettle(turns, 'elsewhere')?.session.id).toBe('oldest');
  });
});
