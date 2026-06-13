import { triggerShortcut, hasHandler } from '../../shortcuts/useShortcut';
import { isMacLikePlatform } from '../../shortcuts/platform';
import { matchesShortcut, ShortcutId, isChord } from '../../shortcuts/registry';
import { resolveBinding } from '../../shortcuts/resolver';
import { enterLeader, resolvePendingThen } from '../../shortcuts/chordState';
import { matchChordLeader } from '../../shortcuts/chordDispatch';

// Shortcuts intercepted on the terminal's OWN input path (Ghostty's
// InputHandler), in priority order. This is a second dispatch path separate
// from the window-level listener, so it must read bindings through the resolver
// to honor user rebinds/unbinds. ⌘W (close) is handled after the loop because
// it falls back from closing the focused pane to closing the session.
const TERMINAL_INTERCEPTS: ShortcutId[] = [
  'workspace.select1', 'workspace.select2', 'workspace.select3',
  'workspace.select4', 'workspace.select5', 'workspace.select6',
  'workspace.select7', 'workspace.select8', 'workspace.select9',
  'session.newWorkspace',
  'app.quit',
  'ui.showShortcuts',
  'session.newHorizontal',
  'terminal.find',
  'terminal.splitVertical',
  'terminal.splitHorizontal',
  'terminal.toggleZoom',
  'terminal.toggleMaximize',
];

function matchesBinding(event: KeyboardEvent, id: ShortcutId): boolean {
  const def = resolveBinding(id);
  // Chords are matched by the chord layer, not the single-combo intercepts.
  return def && !isChord(def) ? matchesShortcut(event, def) : false;
}

export function installTerminalKeyHandler(sendToPty: (data: string) => void) {
  return (event: KeyboardEvent) => {
    // A pending leader owns the next keystroke; resolve it before any PTY
    // control-sequence handling so the follow key is never emitted as input.
    // (The window listener usually handles this first in the capture phase;
    // this is the safety net for packaged-app capture-order differences.)
    if (event.type === 'keydown') {
      const pendingThen = resolvePendingThen(event);
      if (pendingThen.kind === 'fired') {
        triggerShortcut(pendingThen.id);
        return false;
      }
      if (pendingThen.kind === 'cancelled') {
        return false;
      }
    }

    if (
      event.type === 'keydown'
      && (event.key === 'Tab' || event.key === 'ISO_Left_Tab')
      && event.shiftKey
      && !event.ctrlKey
      && !event.metaKey
      && !event.altKey
    ) {
      sendToPty('\x1b[Z');
      return false;
    }
    if (
      event.type === 'keydown'
      && event.ctrlKey
      && !event.metaKey
      && !event.altKey
      && !event.shiftKey
      && event.key.toLowerCase() === 'v'
      && isMacLikePlatform()
    ) {
      // On macOS Ctrl+V is available for the agent image-paste trigger;
      // elsewhere it is the normal browser text-paste accelerator.
      sendToPty('\x16');
      return false;
    }

    if (event.type === 'keydown') {
      // Arm a chord leader before the single-combo intercepts, consuming it so
      // the leader keystroke never reaches the PTY.
      const chord = matchChordLeader(event);
      if (chord) {
        const fireable = chord.candidates.filter((c) => hasHandler(c.id));
        if (fireable.length > 0) {
          enterLeader(chord.leader, fireable);
          return false;
        }
      }
      for (const id of TERMINAL_INTERCEPTS) {
        if (matchesBinding(event, id)) {
          return !triggerShortcut(id);
        }
      }
      // ⌘W: close the focused pane, falling back to closing the session.
      if (matchesBinding(event, 'terminal.close') && triggerShortcut('terminal.close')) {
        return false;
      }
      if (matchesBinding(event, 'session.close')) {
        return !triggerShortcut('session.close');
      }
    }

    if (event.key === 'Enter' && event.shiftKey && !event.ctrlKey && !event.altKey) {
      if (event.type === 'keydown') {
        sendToPty('\n');
      }
      return false;
    }
    return true;
  };
}
