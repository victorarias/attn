// app/src/shortcuts/useShortcut.ts
import { useEffect, useRef } from 'react';
import { SHORTCUTS, ShortcutId, ShortcutDef, matchesShortcut, isChord } from './registry';
import { resolvedShortcutEntries } from './resolver';
import { enterLeader, resolvePendingThen } from './chordState';
import { matchChordLeader } from './chordDispatch';

type Handler = () => void;
const NATIVE_SHORTCUT_EVENT = 'attn:native-shortcut';

// Global registry of active handlers
const handlers = new Map<ShortcutId, Set<Handler>>();

export function triggerShortcut(id: ShortcutId): boolean {
  const shortcutHandlers = handlers.get(id);
  if (!shortcutHandlers || shortcutHandlers.size === 0) {
    return false;
  }
  for (const handler of shortcutHandlers) {
    handler();
  }
  return true;
}

/** Whether any component currently has a handler registered for this id. */
export function hasHandler(id: ShortcutId): boolean {
  const set = handlers.get(id);
  return !!set && set.size > 0;
}

// While the shortcut editor is capturing a keystroke, the global dispatcher
// must stand down so recording a combo (even an always-enabled one like ⌘Q)
// never fires its action. Registration order can't be relied on, so the
// dispatcher checks this flag directly.
let captureSuspended = false;
export function setShortcutCaptureSuspended(suspended: boolean): void {
  captureSuspended = suspended;
}

// Single global listener (installed once)
let listenerInstalled = false;

function installGlobalListener() {
  if (listenerInstalled) return;
  listenerInstalled = true;

  window.addEventListener('keydown', (e: KeyboardEvent) => {
    if (captureSuspended) return;

    // A pending leader owns the next keystroke entirely: fire its chord, refresh
    // a re-press, or cancel — but always consume so it can't fall through to a
    // single combo or leak into the terminal PTY.
    const pendingThen = resolvePendingThen(e);
    if (pendingThen.kind !== 'none') {
      e.preventDefault();
      e.stopPropagation();
      if (pendingThen.kind === 'fired') triggerShortcut(pendingThen.id);
      return;
    }

    const editableTarget = isNonTerminalEditableTarget(e.target);
    const terminalTarget = isTerminalTarget(e.target);
    // Iterate resolved bindings (defaults merged with user overrides) so rebinds
    // and unbinds take effect without reinstalling this listener.
    for (const [id, def] of resolvedShortcutEntries()) {
      if (id === 'terminal.close' && !terminalTarget) {
        continue;
      }
      if (isChord(def)) {
        continue; // chord leaders are armed in the pass below
      }
      if (matchesShortcut(e, def)) {
        if (editableTarget && def.editableTarget === 'native') {
          continue;
        }
        const shortcutHandlers = handlers.get(id);
        if (shortcutHandlers && shortcutHandlers.size > 0) {
          if (id === 'session.refreshPRs' && terminalTarget) {
            return;
          }
          e.preventDefault();
          e.stopPropagation(); // Prevent event from reaching the terminal.
          triggerShortcut(id);
          return;
        }
      }
    }

    // No single combo matched — arm a chord if this keystroke is a bound leader.
    // A bound leader is always consumed (never leaks to the PTY) even when no
    // follow action currently has a handler; it just arms nothing in that case.
    // Skip in non-terminal editable targets so a leader can't swallow a
    // keystroke meant for an input/textarea.
    if (!editableTarget) {
      const chord = matchChordLeader(e);
      if (chord) {
        const fireable = chord.candidates.filter((c) => hasHandler(c.id));
        if (fireable.length > 0) {
          enterLeader(chord.leader, fireable);
        }
        e.preventDefault();
        e.stopPropagation();
        return;
      }
    }
  }, true); // Capture phase to get events before terminal input.

  window.addEventListener(NATIVE_SHORTCUT_EVENT, (event) => {
    const shortcutId = (event as CustomEvent<unknown>).detail;
    if (
      typeof shortcutId === 'string'
      && Object.prototype.hasOwnProperty.call(SHORTCUTS, shortcutId)
    ) {
      // Honor the same editableTarget guard as the keydown path: a shortcut
      // marked editableTarget:'native' must not hijack a focused input. The
      // native menu item (which forwarded this event) already consumed the key
      // equivalent in place of the OS default — e.g. Zoom Pane replacing Redo on
      // ⇧⌘Z — so in a non-terminal editable target the correct behavior is to do
      // nothing rather than fire the action over the user's typing.
      const def = SHORTCUTS[shortcutId as ShortcutId] as ShortcutDef;
      if (
        def.editableTarget === 'native'
        && isNonTerminalEditableTarget(document.activeElement)
      ) {
        return;
      }
      triggerShortcut(shortcutId as ShortcutId);
    }
  });
}

function isTerminalTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.closest('.terminal-container, .session-terminal-workspace') !== null;
}

function isNonTerminalEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement) || target.closest('.terminal-container')) {
    return false;
  }
  return target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])') !== null;
}

/**
 * Register a handler for a keyboard shortcut.
 * Multiple components can register for the same shortcut.
 */
export function useShortcut(id: ShortcutId, handler: Handler, enabled = true): void {
  const handlerRef = useRef(handler);
  handlerRef.current = handler;

  useEffect(() => {
    installGlobalListener();

    if (!enabled) return;

    const wrappedHandler = () => handlerRef.current();

    if (!handlers.has(id)) {
      handlers.set(id, new Set());
    }
    handlers.get(id)!.add(wrappedHandler);

    return () => {
      handlers.get(id)?.delete(wrappedHandler);
    };
  }, [id, enabled]);
}
