// app/src/shortcuts/chordState.ts
// Module-level state machine for an armed leader key. After a leader fires
// (e.g. ⌘K), the next keystroke is owned by this machine: it either fires a
// matching chord or cancels — but is always consumed so it can never leak to
// the terminal PTY. Both dispatch paths (the window listener and the terminal's
// Ghostty input handler) share this one singleton, and the HUD subscribes to it.

import { Combo, ShortcutId, matchesShortcut } from './registry';

export const LEADER_TIMEOUT_MS = 600;

export interface ChordCandidate {
  id: ShortcutId;
  then: Combo;
}

interface PendingLeader {
  leader: Combo;
  candidates: ChordCandidate[];
  timer: ReturnType<typeof setTimeout>;
}

let pending: PendingLeader | null = null;

// A stable snapshot reference for useSyncExternalStore — only replaced when the
// armed leader actually changes, so the HUD doesn't re-render on every read.
let snapshot: { leader: Combo | null } = { leader: null };
const subscribers = new Set<() => void>();

function publish(leader: Combo | null): void {
  snapshot = { leader };
  for (const cb of subscribers) cb();
}

export function subscribeChord(cb: () => void): () => void {
  subscribers.add(cb);
  return () => {
    subscribers.delete(cb);
  };
}

export function getChordSnapshot(): { leader: Combo | null } {
  return snapshot;
}

export function isLeaderPending(): boolean {
  return pending !== null;
}

/** Arm a leader. Replaces any currently-armed leader and (re)starts the timeout. */
export function enterLeader(leader: Combo, candidates: ChordCandidate[]): void {
  if (pending) clearTimeout(pending.timer);
  const timer = setTimeout(cancelLeader, LEADER_TIMEOUT_MS);
  pending = { leader, candidates, timer };
  publish(leader);
}

export function cancelLeader(): void {
  if (!pending) return;
  clearTimeout(pending.timer);
  pending = null;
  publish(null);
}

// Lone modifier keydowns shouldn't cancel a pending leader — the user is still
// reaching for the follow key.
const MODIFIER_KEYS = new Set([
  'Meta', 'Control', 'Shift', 'Alt', 'AltGraph', 'OS', 'Hyper', 'Super',
  'CapsLock', 'Fn', 'FnLock', 'NumLock', 'ScrollLock', 'Dead',
]);

export type ThenResult =
  // No leader pending, or a lone modifier while pending: the caller proceeds
  // with normal single-combo handling (and pending, if any, survives).
  | { kind: 'none' }
  | { kind: 'fired'; id: ShortcutId }
  | { kind: 'cancelled' };

/**
 * Resolve the follow keystroke after a leader. A matching candidate fires and
 * clears the leader; Escape or any non-matching, non-modifier key cancels. Both
 * 'fired' and 'cancelled' mean the caller must consume the event so the follow
 * key never reaches the PTY.
 */
export function resolvePendingThen(e: KeyboardEvent): ThenResult {
  if (!pending) return { kind: 'none' };
  if (MODIFIER_KEYS.has(e.key)) return { kind: 'none' };
  if (e.key === 'Escape') {
    cancelLeader();
    return { kind: 'cancelled' };
  }
  for (const c of pending.candidates) {
    if (matchesShortcut(e, c.then)) {
      cancelLeader();
      return { kind: 'fired', id: c.id };
    }
  }
  cancelLeader();
  return { kind: 'cancelled' };
}
