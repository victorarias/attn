export interface TerminalPerfSnapshot {
  terminalName: string;
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
