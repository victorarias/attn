import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

// macOS-only matcher: meta is the accelerator.
vi.mock('./platform', () => ({
  isMacLikePlatform: () => true,
  isAccelKeyPressed: (e: KeyboardEvent) => e.metaKey,
}));

import {
  enterLeader,
  cancelLeader,
  isLeaderPending,
  resolvePendingThen,
  getChordSnapshot,
  subscribeChord,
  LEADER_TIMEOUT_MS,
} from './chordState';

function key(init: KeyboardEventInit): KeyboardEvent {
  return new KeyboardEvent('keydown', init);
}

const LEADER = { key: 'k', meta: true };

beforeEach(() => {
  vi.useFakeTimers();
  cancelLeader();
});

afterEach(() => {
  cancelLeader();
  vi.useRealTimers();
});

describe('chordState', () => {
  it('reports no pending leader and a null snapshot at rest', () => {
    expect(isLeaderPending()).toBe(false);
    expect(getChordSnapshot().leader).toBeNull();
    expect(resolvePendingThen(key({ key: 'd' }))).toEqual({ kind: 'none' });
  });

  it('arms a leader and exposes it via the snapshot', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(isLeaderPending()).toBe(true);
    expect(getChordSnapshot().leader).toEqual(LEADER);
  });

  it('fires the matching candidate on the follow key and clears the leader', () => {
    enterLeader(LEADER, [
      { id: 'dock.attention', then: { key: 'd' } },
      { id: 'view.toggleGrid', then: { key: 'g' } },
    ]);
    expect(resolvePendingThen(key({ key: 'g' }))).toEqual({ kind: 'fired', id: 'view.toggleGrid' });
    expect(isLeaderPending()).toBe(false);
    expect(getChordSnapshot().leader).toBeNull();
  });

  it('cancels (and consumes) on a non-matching key', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(resolvePendingThen(key({ key: 'x' }))).toEqual({ kind: 'cancelled' });
    expect(isLeaderPending()).toBe(false);
  });

  it('cancels on Escape', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(resolvePendingThen(key({ key: 'Escape' }))).toEqual({ kind: 'cancelled' });
    expect(isLeaderPending()).toBe(false);
  });

  it('keeps the leader pending on a lone modifier keydown', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(resolvePendingThen(key({ key: 'Shift', shiftKey: true }))).toEqual({ kind: 'none' });
    expect(isLeaderPending()).toBe(true);
    // The real follow key still fires afterward.
    expect(resolvePendingThen(key({ key: 'd' }))).toEqual({ kind: 'fired', id: 'dock.attention' });
  });

  it('matches a follow key that itself carries modifiers', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd', meta: true } }]);
    expect(resolvePendingThen(key({ key: 'd' }))).toEqual({ kind: 'cancelled' });
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd', meta: true } }]);
    expect(resolvePendingThen(key({ key: 'd', metaKey: true }))).toEqual({ kind: 'fired', id: 'dock.attention' });
  });

  it('re-arms (does not cancel) when the leader is pressed again', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(resolvePendingThen(key({ key: 'k', metaKey: true }))).toEqual({ kind: 'rearmed' });
    expect(isLeaderPending()).toBe(true);
    // The follow key still fires after a re-arm.
    expect(resolvePendingThen(key({ key: 'd' }))).toEqual({ kind: 'fired', id: 'dock.attention' });
  });

  it('keeps the leader pending on OS auto-repeat instead of self-cancelling', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(resolvePendingThen(key({ key: 'k', metaKey: true, repeat: true }))).toEqual({ kind: 'rearmed' });
    expect(isLeaderPending()).toBe(true);
  });

  it('does not re-render the HUD when re-armed with the same leader', () => {
    const sub = vi.fn();
    const unsubscribe = subscribeChord(sub);
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(sub).toHaveBeenCalledTimes(1);
    // Same leader object -> snapshot unchanged -> no extra notification.
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(sub).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it('auto-cancels after the timeout', () => {
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    vi.advanceTimersByTime(LEADER_TIMEOUT_MS + 1);
    expect(isLeaderPending()).toBe(false);
    expect(getChordSnapshot().leader).toBeNull();
  });

  it('notifies subscribers when the armed leader changes', () => {
    const sub = vi.fn();
    const unsubscribe = subscribeChord(sub);
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(sub).toHaveBeenCalledTimes(1);
    cancelLeader();
    expect(sub).toHaveBeenCalledTimes(2);
    unsubscribe();
    enterLeader(LEADER, [{ id: 'dock.attention', then: { key: 'd' } }]);
    expect(sub).toHaveBeenCalledTimes(2);
  });
});
