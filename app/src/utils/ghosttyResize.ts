import type { GhosttyTerminal } from 'ghostty-web';

const DEC_WRAPAROUND_MODE = 7;
const DISABLE_WRAPAROUND = '\x1b[?7l';
const ENABLE_WRAPAROUND = '\x1b[?7h';
export const WORKSPACE_RESIZE_COALESCE_MS = 250;

export interface TerminalDimensions {
  cols: number;
  rows: number;
}

export interface ResizeCoalescer {
  submit: (dimensions: TerminalDimensions, coalesce: boolean) => void;
  cancel: () => void;
}

export function createResizeCoalescer(
  apply: (dimensions: TerminalDimensions) => void,
  intervalMs = WORKSPACE_RESIZE_COALESCE_MS,
): ResizeCoalescer {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let pending: TerminalDimensions | null = null;
  let lastAppliedAt: number | null = null;

  const cancelTimer = () => {
    if (timer == null) return;
    clearTimeout(timer);
    timer = null;
  };

  const applyNow = (dimensions: TerminalDimensions) => {
    lastAppliedAt = Date.now();
    apply(dimensions);
  };

  const flushPending = () => {
    timer = null;
    const dimensions = pending;
    pending = null;
    if (dimensions) applyNow(dimensions);
  };

  return {
    submit(dimensions, coalesce) {
      pending = dimensions;
      if (!coalesce) {
        cancelTimer();
        pending = null;
        applyNow(dimensions);
        return;
      }

      const elapsed = lastAppliedAt == null ? intervalMs : Date.now() - lastAppliedAt;
      if (elapsed >= intervalMs) {
        cancelTimer();
        flushPending();
        return;
      }
      if (timer == null) {
        timer = setTimeout(flushPending, intervalMs - elapsed);
      }
    },
    cancel() {
      cancelTimer();
      pending = null;
    },
  };
}

export function resizeGhosttyWithoutReflow(
  terminal: Pick<GhosttyTerminal, 'getMode' | 'resize' | 'write'>,
  cols: number,
  rows: number,
): void {
  const wraparoundEnabled = terminal.getMode(DEC_WRAPAROUND_MODE);
  if (!wraparoundEnabled) {
    terminal.resize(cols, rows);
    return;
  }

  // Ghostty reflows the entire primary-screen history when DEC wraparound is
  // enabled. Temporarily disabling it selects Ghostty's no-reflow resize path;
  // restore the producer's mode before any later output is parsed.
  terminal.write(DISABLE_WRAPAROUND);
  try {
    terminal.resize(cols, rows);
  } finally {
    terminal.write(ENABLE_WRAPAROUND);
  }
}
