// app/src/shortcuts/useShortcut.ts
import { useEffect, useRef } from 'react';
import { SHORTCUTS, ShortcutId, matchesShortcut } from './registry';

type Handler = () => void;

// Global registry of active handlers
const handlers = new Map<ShortcutId, Set<Handler>>();

// Single global listener (installed once)
let listenerInstalled = false;

function installGlobalListener() {
  if (listenerInstalled) return;
  listenerInstalled = true;

  window.addEventListener('keydown', (e: KeyboardEvent) => {
    for (const [id, def] of Object.entries(SHORTCUTS)) {
      if (matchesShortcut(e, def)) {
        const shortcutHandlers = handlers.get(id as ShortcutId);
        if (shortcutHandlers && shortcutHandlers.size > 0) {
          e.preventDefault();
          // Call all registered handlers for this shortcut
          for (const handler of shortcutHandlers) {
            handler();
          }
          return;
        }
      }
    }
  }, true); // capture phase to get events before xterm
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
