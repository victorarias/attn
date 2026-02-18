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

