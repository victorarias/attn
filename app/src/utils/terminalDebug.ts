const TERMINAL_DEBUG_STORAGE_KEY = 'attn:terminal-debug';
const SUSPICIOUS_COLS_THRESHOLD = 20;
const SUSPICIOUS_ROWS_THRESHOLD = 10;

export function isTerminalDebugEnabled(): boolean {
  try {
    return window.localStorage.getItem(TERMINAL_DEBUG_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

export function isSuspiciousTerminalSize(cols: number, rows: number): boolean {
  return cols <= SUSPICIOUS_COLS_THRESHOLD || rows <= SUSPICIOUS_ROWS_THRESHOLD;
}

// --- Resize diagnostics ---

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

// --- Resize event ring buffer ---

export interface ResizeEvent {
  timestamp: number;
  terminalName: string;
  trigger: string;
  fontSize: number;
  cols: number;
  rows: number;
  prevCols: number;
  prevRows: number;
  isVisible: boolean;
  diagnostics: ResizeDiagnostics;
}

const RING_BUFFER_SIZE = 50;
const resizeBuffers = new Map<string, ResizeEvent[]>();

export function recordResizeEvent(event: ResizeEvent): void {
  let buffer = resizeBuffers.get(event.terminalName);
  if (!buffer) {
    buffer = [];
    resizeBuffers.set(event.terminalName, buffer);
  }
  buffer.push(event);
  if (buffer.length > RING_BUFFER_SIZE) {
    buffer.shift();
  }
}

export function getAllResizeEvents(): ResizeEvent[] {
  const all: ResizeEvent[] = [];
  for (const buffer of resizeBuffers.values()) {
    all.push(...buffer);
  }
  all.sort((a, b) => a.timestamp - b.timestamp);
  return all;
}

export function formatResizeLog(): string {
  const events = getAllResizeEvents();
  if (events.length === 0) return 'No resize events recorded.';

  const lines: string[] = [];
  lines.push(`Terminal Resize Debug Log (${events.length} events)`);
  lines.push(`Captured at: ${new Date().toISOString()}`);
  lines.push(`Window: ${window.innerWidth}×${window.innerHeight} dpr:${window.devicePixelRatio}`);
  lines.push('='.repeat(120));

  for (const e of events) {
    const time = new Date(e.timestamp).toISOString().slice(11, 23);
    const d = e.diagnostics;
    lines.push(
      `[${time}] ${e.terminalName.padEnd(20)} ${e.trigger.padEnd(16)}` +
      ` ${String(e.prevCols).padStart(3)}×${String(e.prevRows).padStart(3)}` +
      ` → ${String(e.cols).padStart(3)}×${String(e.rows).padStart(3)}` +
      ` | font:${e.fontSize}` +
      ` cell:${d.cellWidth.toFixed(1)}×${d.cellHeight.toFixed(1)}(${d.cellSource})` +
      ` | ctr:${d.containerWidth.toFixed(0)}×${d.containerHeight.toFixed(0)}` +
      ` avail:${d.availableWidth.toFixed(0)}×${d.availableHeight.toFixed(0)}` +
      ` | dpr:${d.dpr} vis:${e.isVisible}`
    );
  }

  return lines.join('\n');
}

export function clearResizeLog(): void {
  resizeBuffers.clear();
}

// Expose on window for console access even without debug overlay
if (typeof window !== 'undefined') {
  (window as any).__attnResizeLog = formatResizeLog;
  (window as any).__attnResizeClear = clearResizeLog;
}
