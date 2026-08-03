import { describe, expect, it } from 'vitest';
import {
  advanceAfterTurnClosed,
  buildQueueBands,
  headOfQueue,
  oldestWantedTurn,
  type QueueBandSession,
} from './queueBands';
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

  describe('snoozed', () => {
    // Fixed clocks: a snooze is a comparison against now, and a test whose
    // deadlines drift with the wall clock is a test that expires.
    const now = Date.parse('2026-07-26T12:00:00Z');
    const laterToday = '2026-07-26T13:00:00Z';
    const laterStill = '2026-07-26T18:00:00Z';

    it('takes a deferred agent out of both bands into its own list', () => {
      const bands = buildQueueBands(views([
        { id: 'owed', label: 'owed', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
        { id: 'quiet', label: 'quiet', workspaceId: 'ws-a' },
        { id: 'deferred', label: 'deferred', workspaceId: 'ws-b', turnSnoozedUntil: laterToday },
      ]), now);

      expect(bands.turns.map((row) => row.session.id)).toEqual(['owed']);
      expect(bands.settled.map((row) => row.session.id)).toEqual(['quiet']);
      expect(bands.snoozed.map((row) => row.session.id)).toEqual(['deferred']);
    });

    it('orders by when each comes back, soonest first', () => {
      const bands = buildQueueBands(views([
        { id: 'late', label: 'late', workspaceId: 'ws-a', turnSnoozedUntil: laterStill },
        { id: 'soon', label: 'soon', workspaceId: 'ws-b', turnSnoozedUntil: laterToday },
      ]), now);

      expect(bands.snoozed.map((row) => row.session.id)).toEqual(['soon', 'late']);
    });

    it('returns a lapsed deadline to the settled band', () => {
      // The daemon strips a lapsed deadline from the broadcast, but a client
      // holding a snapshot across the wake must not park the row for as long as
      // it takes the next one to land.
      const bands = buildQueueBands(views([
        { id: 'woken', label: 'woken', workspaceId: 'ws-a', turnSnoozedUntil: '2026-07-26T11:00:00Z' },
      ]), now);

      expect(bands.snoozed).toEqual([]);
      expect(bands.settled.map((row) => row.session.id)).toEqual(['woken']);
    });

    it('keeps a snoozed agent out of the turns band even if a snapshot still says owed', () => {
      // The daemon settles as it snoozes, so the two never both hold. The order
      // of the checks is what stops a snapshot taken mid-broadcast from drawing
      // a deferred agent as a turn the user owes.
      const bands = buildQueueBands(views([
        { id: 'both', label: 'both', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z', turnSnoozedUntil: laterToday },
      ]), now);

      expect(bands.turns).toEqual([]);
      expect(bands.snoozed.map((row) => row.session.id)).toEqual(['both']);
    });

    it('leaves a pinned workspace out of the snoozed list too', () => {
      // Pinned and muted are in the tree, not the bands, whatever else is true
      // of them.
      const pinnedViews = buildWorkspaceViewModels(
        [{ id: 'ws-p', title: 'P', directory: '/repo/p', rank: 'a', pinned: true }],
        [{ id: 'pinned', label: 'pinned', workspaceId: 'ws-p', turnSnoozedUntil: laterToday }],
      );

      expect(buildQueueBands(pinnedViews, now).snoozed).toEqual([]);
    });
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

describe('advanceAfterTurnClosed', () => {
  function owed(id: string, hour: number, workspaceId = 'ws-a'): QueueBandSession {
    return { id, label: id, workspaceId, turnOwed: true, turnOpenedAt: `2026-07-26T0${hour}:00:00Z` };
  }

  it('moves on to the next turn in queue order when the watched turn closes', () => {
    const queue = [owed('watched', 0), owed('next', 1), owed('after', 2)];
    const before = buildQueueBands(views(queue));
    const after = buildQueueBands(views([{ ...queue[0], turnOwed: false }, queue[1], queue[2]]));

    const advance = advanceAfterTurnClosed(before.turns, after, 'watched');

    expect(advance).toEqual({ to: 'session', row: expect.objectContaining({ workspaceTitle: 'A' }) });
    expect(advance?.to === 'session' && advance.row.session.id).toBe('next');
  });

  it('reads the position from the earlier snapshot, where the closed row still is', () => {
    // The row is already out of the turns band by the time this runs, so a
    // decision made from the new bands alone would have no position to continue
    // from and would restart at the top of the queue.
    const queue = [owed('oldest', 0), owed('watched', 1), owed('newest', 2)];
    const before = buildQueueBands(views(queue));
    const after = buildQueueBands(views([queue[0], { ...queue[1], turnOwed: false }, queue[2]]));

    const advance = advanceAfterTurnClosed(before.turns, after, 'watched');

    expect(advance?.to === 'session' && advance.row.session.id).toBe('newest');
  });

  it('wraps to the top when the bottom row is the one that closed', () => {
    // Queue order is not attention order: the rows above are still owed, so the
    // bottom row moves on to the oldest rather than falling off the end.
    const queue = [owed('oldest', 0), owed('middle', 1), owed('watched', 2)];
    const before = buildQueueBands(views(queue));
    const after = buildQueueBands(views([queue[0], queue[1], { ...queue[2], turnOwed: false }]));

    const advance = advanceAfterTurnClosed(before.turns, after, 'watched');

    expect(advance?.to === 'session' && advance.row.session.id).toBe('oldest');
  });

  it('skips a successor that settled in the same broadcast', () => {
    // One update can close several turns — an auto-settle countdown and a settle
    // from another client landing together. The successor was owed in the old
    // snapshot and is not owed in this one, so landing on it would hand the user
    // a second agent that is already finished with them.
    const queue = [owed('watched', 0), owed('alsoSettled', 1), owed('after', 2)];
    const before = buildQueueBands(views(queue));
    const after = buildQueueBands(views([
      { ...queue[0], turnOwed: false },
      { ...queue[1], turnOwed: false },
      queue[2],
    ]));

    const advance = advanceAfterTurnClosed(before.turns, after, 'watched');

    expect(advance?.to === 'session' && advance.row.session.id).toBe('after');
  });

  it('wraps past a coalesced settle rather than falling through to home', () => {
    // The only row still owed sits above the one that closed, and the row below
    // it went with it. Eligibility is read from the new band, so the wrap has to
    // keep going rather than stop at the first old-snapshot successor.
    const queue = [owed('stillOwed', 0), owed('watched', 1), owed('alsoSettled', 2)];
    const before = buildQueueBands(views(queue));
    const after = buildQueueBands(views([
      queue[0],
      { ...queue[1], turnOwed: false },
      { ...queue[2], turnOwed: false },
    ]));

    const advance = advanceAfterTurnClosed(before.turns, after, 'watched');

    expect(advance?.to === 'session' && advance.row.session.id).toBe('stillOwed');
  });

  it('lands on a turn that opened in the same broadcast that closed this one', () => {
    // The arrival has no position in the old snapshot, so the scan cannot find
    // it. Home is reached from the current band being empty, never from the old
    // one running out — otherwise a queue that is not empty sends the user home.
    const watched = owed('watched', 0);
    const before = buildQueueBands(views([watched]));
    const after = buildQueueBands(views([{ ...watched, turnOwed: false }, owed('arrival', 1)]));

    const advance = advanceAfterTurnClosed(before.turns, after, 'watched');

    expect(advance?.to === 'session' && advance.row.session.id).toBe('arrival');
  });

  it('goes home when the closed turn was the last one owed', () => {
    // Staying would leave the user on the one agent guaranteed to be finished
    // with them.
    const only = owed('watched', 0);
    const before = buildQueueBands(views([only]));
    const after = buildQueueBands(views([{ ...only, turnOwed: false }]));

    expect(advanceAfterTurnClosed(before.turns, after, 'watched')).toEqual({ to: 'dashboard' });
  });

  it('stays put while the turn is still owed', () => {
    const queue = [owed('watched', 0), owed('next', 1)];
    const bands = buildQueueBands(views(queue));

    expect(advanceAfterTurnClosed(bands.turns, bands, 'watched')).toBeNull();
  });

  it('stays put when the watched agent never owed the turn that closed', () => {
    // Someone else's turn closing is not a reason to move the user.
    const queue = [owed('elsewhere', 0)];
    const before = buildQueueBands(views(queue));
    const after = buildQueueBands(views([{ ...queue[0], turnOwed: false }, { id: 'watched', label: 'watched', workspaceId: 'ws-a' }]));

    expect(advanceAfterTurnClosed(before.turns, after, 'watched')).toBeNull();
  });

  it('stays put when the row left the queue by being pinned rather than settled', () => {
    // Pinning clears turn_owed too, but it means "keep this in view" — being
    // carried off to another agent is the opposite of what was asked for. The
    // row lands in no band at all, which is what tells the two apart.
    const watched = owed('watched', 0);
    const before = buildQueueBands(views([watched, owed('next', 1, 'ws-b')]));
    const after = buildQueueBands(buildWorkspaceViewModels(
      [{ id: 'ws-a', title: 'A', directory: '/repo/a', rank: 'a', pinned: true }, workspaces[1]],
      [{ ...watched, turnOwed: false }, owed('next', 1, 'ws-b')],
    ));

    expect(after.turns.map((row) => row.session.id)).toEqual(['next']);
    expect(after.settled).toEqual([]);
    expect(advanceAfterTurnClosed(before.turns, after, 'watched')).toBeNull();
  });

  it('stays put when the watched agent is gone from the bands entirely', () => {
    // A closed session, or a muted workspace: nothing settled, so nothing to
    // move on from.
    const watched = owed('watched', 0);
    const before = buildQueueBands(views([watched, owed('next', 1)]));
    const after = buildQueueBands(views([owed('next', 1)]));

    expect(advanceAfterTurnClosed(before.turns, after, 'watched')).toBeNull();
  });

  it('moves on when the watched turn closed by being snoozed', () => {
    // A snooze closes the turn on the agent the user is looking at, exactly like
    // a settle — so leaving them parked in the agent they just deferred is the
    // bookkeeping this exists to remove.
    const watched = owed('watched', 0);
    const before = buildQueueBands(views([watched, owed('next', 1)]));
    const after = buildQueueBands(views([
      { ...watched, turnOwed: false, turnSnoozedUntil: '2026-07-26T20:00:00Z' },
      owed('next', 1),
    ]), Date.parse('2026-07-26T12:00:00Z'));

    expect(after.snoozed.map((row) => row.session.id)).toEqual(['watched']);
    expect(advanceAfterTurnClosed(before.turns, after, 'watched')).toEqual({
      to: 'session',
      row: expect.objectContaining({ session: expect.objectContaining({ id: 'next' }) }),
    });
  });

  it('goes home when the snoozed turn was the last one owed', () => {
    const only = owed('watched', 0);
    const before = buildQueueBands(views([only]));
    const after = buildQueueBands(views([
      { ...only, turnOwed: false, turnSnoozedUntil: '2026-07-26T20:00:00Z' },
    ]), Date.parse('2026-07-26T12:00:00Z'));

    expect(advanceAfterTurnClosed(before.turns, after, 'watched')).toEqual({ to: 'dashboard' });
  });

  it('stays put with no agent selected, or before any bands exist', () => {
    const bands = buildQueueBands(views([owed('a', 0)]));

    expect(advanceAfterTurnClosed(bands.turns, bands, null)).toBeNull();
    expect(advanceAfterTurnClosed(bands.turns, null, 'a')).toBeNull();
  });
});

describe('headOfQueue', () => {
  it('is the turn owed longest, not the first one listed by the workspace tree', () => {
    // Home waits on this while the queue is empty, so it must answer with the
    // same row the band and ⌘J lead with — the oldest turn, wherever it lives.
    const bands = buildQueueBands(views([
      { id: 'newer', label: 'newer', workspaceId: 'ws-a', turnOwed: true, turnOpenedAt: '2026-07-26T12:00:00Z' },
      { id: 'older', label: 'older', workspaceId: 'ws-b', turnOwed: true, turnOpenedAt: '2026-07-26T09:00:00Z' },
    ]));

    expect(headOfQueue(bands)?.session.id).toBe('older');
  });

  it('is null while nothing is owed, and with no bands at all', () => {
    const settled = buildQueueBands(views([
      { id: 'a', label: 'a', workspaceId: 'ws-a', turnOwed: false },
    ]));

    expect(headOfQueue(settled)).toBeNull();
    expect(headOfQueue(null)).toBeNull();
  });
});
