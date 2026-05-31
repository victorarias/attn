// Codex (and other inline-rendering TUIs) cannot recover from a SIGWINCH at
// e.g. 10×6 — their UI gets re-anchored at the small size and never recovers
// when the pane grows back. Agent panes suppress SIGWINCH below these
// dimensions at the resize-send point. Plain shell panes don't have this
// fragility and a legitimately small zsh (e.g. 14 cols from a 3-way split)
// renders fine, so this floor is agent-pane-only.
//
// Transient layout-state filtering (panel animations, grid track resolving)
// is handled separately by the terminal surface's layout measurement.
export const MIN_USABLE_TERMINAL_COLS = 20;
export const MIN_USABLE_TERMINAL_ROWS = 10;

export function isSuspiciousTerminalSize(cols: number, rows: number): boolean {
  return cols <= MIN_USABLE_TERMINAL_COLS || rows <= MIN_USABLE_TERMINAL_ROWS;
}

// Geometry captured at each resize, consumed by terminal perf snapshots
// (see terminalPerf.ts).
export interface ResizeDiagnostics {
  containerWidth: number;
  containerHeight: number;
  availableWidth: number;
  availableHeight: number;
  cellWidth: number;
  cellHeight: number;
  cellSource: 'renderer' | 'measured';
  dpr: number;
}
