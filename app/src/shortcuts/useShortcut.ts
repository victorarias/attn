// app/src/shortcuts/useShortcut.ts
import { useEffect, useRef } from 'react';
import { SHORTCUTS, ShortcutDef, ShortcutId, matchesShortcut } from './registry';

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

// Single global listener (installed once)
let listenerInstalled = false;

function installGlobalListener() {
  if (listenerInstalled) return;
  listenerInstalled = true;

  window.addEventListener('keydown', (e: KeyboardEvent) => {
    const editableTarget = isNonTerminalEditableTarget(e.target);
    const terminalTarget = isTerminalTarget(e.target);
    for (const [id, def] of Object.entries(SHORTCUTS)) {
      if (id === 'terminal.close' && !terminalTarget) {
        continue;
      }
      if (matchesShortcut(e, def)) {
        if (editableTarget && (def as ShortcutDef).editableTarget === 'native') {
          continue;
        }
        const shortcutHandlers = handlers.get(id as ShortcutId);
        if (shortcutHandlers && shortcutHandlers.size > 0) {
          if (id === 'session.refreshPRs' && terminalTarget) {
            return;
          }
          e.preventDefault();
          e.stopPropagation(); // Prevent event from reaching the terminal.
          triggerShortcut(id as ShortcutId);
          return;
        }
      }
    }
  }, true); // Capture phase to get events before terminal input.

  window.addEventListener(NATIVE_SHORTCUT_EVENT, (event) => {
    const shortcutId = (event as CustomEvent<unknown>).detail;
    if (
      typeof shortcutId === 'string'
      && Object.prototype.hasOwnProperty.call(SHORTCUTS, shortcutId)
    ) {
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
