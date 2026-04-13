import { useEffect, useRef } from 'react';

/**
 * Centralized Escape key dismiss stack.
 *
 * Rule: global Cmd-key shortcuts → shortcut registry.
 *       Modal/overlay dismiss → this hook.
 *
 * When an overlay opens, it pushes a dismiss callback onto the stack.
 * Escape calls only the top handler (LIFO), so nested overlays dismiss
 * in the right order automatically.
 *
 * Capture phase is intentional: fires before xterm.js and any element-level
 * handlers, so overlays always close regardless of what has DOM focus.
 * When the stack is non-empty, stopPropagation() prevents the event from
 * reaching background elements (xterm textarea, CodeMirror, etc.).
 * All Escape dismiss handlers — including nested sub-states — must go
 * through this hook so the LIFO ordering stays correct.
 */

const stack: Array<() => void> = [];

let installedListener: ((e: KeyboardEvent) => void) | null = null;

function ensureInstalled() {
  if (installedListener) return;
  installedListener = (e: KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    const top = stack[stack.length - 1];
    if (top) {
      e.preventDefault();
      e.stopPropagation(); // prevent xterm.js and other element handlers from also seeing it
      top();
    }
  };
  window.addEventListener('keydown', installedListener, true); // capture phase — fires before xterm.js
}

export function useEscapeStack(handler: () => void, enabled: boolean): void {
  // Stable ref so the stack entry never needs replacing when handler changes.
  const ref = useRef(handler);
  ref.current = handler;

  useEffect(() => {
    ensureInstalled();
    if (!enabled) return;
    const fn = () => ref.current();
    stack.push(fn);
    return () => {
      const i = stack.lastIndexOf(fn);
      if (i !== -1) stack.splice(i, 1);
    };
  }, [enabled]); // only re-register when open/closed, not on every handler change
}

/** Exposed for test teardown only. Do not call in production code. */
export function _resetEscapeStackForTest(): void {
  stack.length = 0;
  if (installedListener) {
    window.removeEventListener('keydown', installedListener, true);
    installedListener = null;
  }
}
