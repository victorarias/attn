export interface FocusableTerminalHandle<TTerminal> {
  terminal: TTerminal | null;
  focus: () => boolean;
}

export type TerminalViewportFocusResult = 'handle' | 'xterm' | 'missing';

export function focusTerminalViewport<TTerminal extends { focus: () => void }>(
  handle: FocusableTerminalHandle<TTerminal> | null | undefined,
  xterm: TTerminal | null | undefined,
): TerminalViewportFocusResult {
  if (handle?.terminal && handle.focus()) {
    return 'handle';
  }
  if (xterm) {
    xterm.focus();
    return 'xterm';
  }
  return 'missing';
}

export function scrollTerminalViewportToTop<TTerminal extends { scrollToTop: () => void }>(
  xterm: TTerminal | null | undefined,
  resetScrollPin: (term: TTerminal) => void,
): boolean {
  if (!xterm) {
    return false;
  }
  xterm.scrollToTop();
  resetScrollPin(xterm);
  return true;
}

export function resetTerminalViewport<TTerminal extends { reset: () => void }>(
  xterm: TTerminal | null | undefined,
  resetScrollPin: (term: TTerminal) => void,
): boolean {
  if (!xterm) {
    return false;
  }
  resetScrollPin(xterm);
  xterm.reset();
  return true;
}
