// app/src/shortcuts/chordDispatch.ts
// Shared chord-leader lookup for the two dispatch paths (window listener and
// the terminal's Ghostty input handler). Reads bindings through the resolver so
// rebinds/unbinds take effect everywhere at once.

import { Combo, matchesShortcut, isChord } from './registry';
import { resolvedShortcutEntries } from './resolver';
import { ChordCandidate } from './chordState';

/**
 * If `e` matches the leader of any bound chord, return that leader plus every
 * chord that shares it — so one leader can fan out to several follow keys
 * (⌘K D, ⌘K G). Returns null when no chord leader matches.
 */
export function matchChordLeader(
  e: KeyboardEvent,
): { leader: Combo; candidates: ChordCandidate[] } | null {
  let leader: Combo | null = null;
  const candidates: ChordCandidate[] = [];
  for (const [id, def] of resolvedShortcutEntries()) {
    if (isChord(def) && matchesShortcut(e, def.leader)) {
      leader = def.leader;
      candidates.push({ id, then: def.then });
    }
  }
  return leader ? { leader, candidates } : null;
}
