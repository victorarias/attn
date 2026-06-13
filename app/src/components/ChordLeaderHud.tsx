// app/src/components/ChordLeaderHud.tsx
// A small heads-up indicator shown while a leader key is armed and attn is
// waiting for the follow key (e.g. after ⌘K). Presentation only — it never
// listens for keys; it just subscribes to the shared chord state.

import { useSyncExternalStore } from 'react';
import { subscribeChord, getChordSnapshot } from '../shortcuts/chordState';
import { shortcutTokens } from '../shortcuts/formatShortcut';
import { KeyCombo } from './Keycap';
import './ChordLeaderHud.css';

export function ChordLeaderHud() {
  const snapshot = useSyncExternalStore(subscribeChord, getChordSnapshot, getChordSnapshot);
  if (!snapshot.leader) return null;
  return (
    <div className="chord-leader-hud" role="status" data-testid="chord-leader-hud">
      <KeyCombo tokens={shortcutTokens(snapshot.leader)} />
      <span className="chord-leader-hud-then">then…</span>
    </div>
  );
}
