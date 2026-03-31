export interface PtyPerfSnapshot {
  updatedAt: string | null;
  wsMessageCount: number;
  wsMessageBytes: number;
  wsJsonParseMs: number;
  ptyOutputCount: number;
  ptyOutputBase64Chars: number;
  ptyJsonParseMs: number;
  decodeCount: number;
  decodedBytes: number;
  decodeMs: number;
  terminalWriteCount: number;
  terminalWriteBytes: number;
  terminalWriteCallMs: number;
}

declare global {
  interface Window {
    __ATTN_PTY_PERF_DUMP?: () => PtyPerfSnapshot;
    __ATTN_PTY_PERF_CLEAR?: () => void;
  }
}

const snapshot: PtyPerfSnapshot = {
  updatedAt: null,
  wsMessageCount: 0,
  wsMessageBytes: 0,
  wsJsonParseMs: 0,
  ptyOutputCount: 0,
  ptyOutputBase64Chars: 0,
  ptyJsonParseMs: 0,
  decodeCount: 0,
  decodedBytes: 0,
  decodeMs: 0,
  terminalWriteCount: 0,
  terminalWriteBytes: 0,
  terminalWriteCallMs: 0,
};

function touch() {
  snapshot.updatedAt = new Date().toISOString();
}

export function recordWsJsonParse(messageBytes: number, durationMs: number, eventName?: string, ptyBase64Chars = 0) {
  snapshot.wsMessageCount += 1;
  snapshot.wsMessageBytes += messageBytes;
  snapshot.wsJsonParseMs += durationMs;
  if (eventName === 'pty_output') {
    snapshot.ptyOutputCount += 1;
    snapshot.ptyOutputBase64Chars += ptyBase64Chars;
    snapshot.ptyJsonParseMs += durationMs;
  }
  touch();
}

export function recordPtyDecode(decodedBytes: number, durationMs: number) {
  snapshot.decodeCount += 1;
  snapshot.decodedBytes += decodedBytes;
  snapshot.decodeMs += durationMs;
  touch();
}

export function recordTerminalWrite(bytes: number, durationMs: number) {
  snapshot.terminalWriteCount += 1;
  snapshot.terminalWriteBytes += bytes;
  snapshot.terminalWriteCallMs += durationMs;
  touch();
}

export function getPtyPerfSnapshot(): PtyPerfSnapshot {
  return { ...snapshot };
}

export function clearPtyPerfSnapshot() {
  snapshot.updatedAt = new Date().toISOString();
  snapshot.wsMessageCount = 0;
  snapshot.wsMessageBytes = 0;
  snapshot.wsJsonParseMs = 0;
  snapshot.ptyOutputCount = 0;
  snapshot.ptyOutputBase64Chars = 0;
  snapshot.ptyJsonParseMs = 0;
  snapshot.decodeCount = 0;
  snapshot.decodedBytes = 0;
  snapshot.decodeMs = 0;
  snapshot.terminalWriteCount = 0;
  snapshot.terminalWriteBytes = 0;
  snapshot.terminalWriteCallMs = 0;
}

if (typeof window !== 'undefined') {
  window.__ATTN_PTY_PERF_DUMP = () => getPtyPerfSnapshot();
  window.__ATTN_PTY_PERF_CLEAR = () => clearPtyPerfSnapshot();
}
