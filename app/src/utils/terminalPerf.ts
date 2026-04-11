import type { ResizeDiagnostics } from './terminalDebug';

export interface TerminalPerfElementMetrics {
  width: number;
  height: number;
}

export interface TerminalPerfStartupSnapshot {
  initialContainer: TerminalPerfElementMetrics | null;
  initialCols: number | null;
  initialRows: number | null;
  firstObservedContainer: TerminalPerfElementMetrics | null;
  firstReadySource: 'resize_observer' | 'font_change' | 'fit_fallback' | null;
  firstReadyAt: number | null;
  firstReadyCols: number | null;
  firstReadyRows: number | null;
  fontEffectAppliedBeforeReady: boolean;
  skippedInitialFontEffect: boolean;
}

export interface TerminalPerfResizeSnapshot {
  at: number;
  trigger: string;
  cols: number;
  rows: number;
  prevCols: number;
  prevRows: number;
  diagnostics: ResizeDiagnostics;
}

export interface TerminalPerfSnapshot {
  terminalName: string;
  sessionId: string | null;
  paneId: string | null;
  runtimeId: string | null;
  paneKind: 'main' | 'shell' | null;
  isActivePane: boolean | null;
  isActiveSession: boolean | null;
  cols: number;
  rows: number;
  bufferLength: number;
  baseY: number;
  viewportY: number;
  scrollbackLimit: number;
  renderer: 'webgl' | 'dom';
  visible: boolean;
  writeQueueChunks: number;
  writeQueueBytes: number;
  renderCount: number;
  writeParsedCount: number;
  lastRenderAt: number;
  lastWriteParsedAt: number;
  lastRenderRange: { start: number; end: number } | null;
  ready: boolean;
  startup: TerminalPerfStartupSnapshot;
  lastResize: TerminalPerfResizeSnapshot | null;
  dom: {
    container: TerminalPerfElementMetrics | null;
    xterm: TerminalPerfElementMetrics | null;
    xtermScreen: TerminalPerfElementMetrics | null;
    canvas: TerminalPerfElementMetrics | null;
  };
}

declare global {
  interface Window {
    __ATTN_TERMINAL_PERF_DUMP?: () => TerminalPerfSnapshot[];
  }
}

const terminalPerfGetters = new Map<string, () => TerminalPerfSnapshot | null>();

export function registerTerminalPerfGetter(
  id: string,
  getSnapshot: () => TerminalPerfSnapshot | null,
): () => void {
  terminalPerfGetters.set(id, getSnapshot);
  syncWindowTerminalPerfDump();
  return () => {
    terminalPerfGetters.delete(id);
    syncWindowTerminalPerfDump();
  };
}

export function getTerminalPerfSnapshot(): TerminalPerfSnapshot[] {
  const snapshots: TerminalPerfSnapshot[] = [];
  for (const getter of terminalPerfGetters.values()) {
    try {
      const snapshot = getter();
      if (snapshot) {
        snapshots.push(snapshot);
      }
    } catch {
      // Ignore individual terminal snapshot failures.
    }
  }
  snapshots.sort((a, b) => a.terminalName.localeCompare(b.terminalName));
  return snapshots;
}

function syncWindowTerminalPerfDump() {
  if (typeof window === 'undefined') {
    return;
  }
  window.__ATTN_TERMINAL_PERF_DUMP = () => getTerminalPerfSnapshot();
}

syncWindowTerminalPerfDump();
