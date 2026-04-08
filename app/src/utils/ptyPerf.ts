export interface PtyPerfEventSample {
  at: string;
  kind: 'ws_event' | 'command';
  event: string | null;
  command: string | null;
  source: string | null;
  runtimeId: string | null;
  seq: number | null;
  base64Chars: number;
  dataBytes: number;
}

export interface PtyPerfSnapshot {
  updatedAt: string | null;
  lastEventAt: string | null;
  lastEventName: string | null;
  lastEventRuntimeId: string | null;
  lastEventSeq: number | null;
  wsMessageCount: number;
  wsMessageBytes: number;
  wsJsonParseMs: number;
  ptyOutputCount: number;
  ptyOutputBase64Chars: number;
  ptyJsonParseMs: number;
  lastPtyOutputAt: string | null;
  lastPtyOutputRuntimeId: string | null;
  lastPtyOutputSeq: number | null;
  commandCount: number;
  lastCommandAt: string | null;
  lastCommandName: string | null;
  lastCommandRuntimeId: string | null;
  ptyInputCount: number;
  ptyInputBytes: number;
  lastPtyInputAt: string | null;
  lastPtyInputRuntimeId: string | null;
  decodeCount: number;
  decodedBytes: number;
  decodeMs: number;
  terminalWriteCount: number;
  terminalWriteBytes: number;
  terminalWriteCallMs: number;
  listenerErrorCount: number;
  lastListenerErrorAt: string | null;
  lastListenerError: {
    event: string | null;
    runtimeId: string | null;
    message: string | null;
  } | null;
  recentEvents: PtyPerfEventSample[];
}

declare global {
  interface Window {
    __ATTN_PTY_PERF_DUMP?: () => PtyPerfSnapshot;
    __ATTN_PTY_PERF_CLEAR?: () => void;
  }
}

const snapshot: PtyPerfSnapshot = {
  updatedAt: null,
  lastEventAt: null,
  lastEventName: null,
  lastEventRuntimeId: null,
  lastEventSeq: null,
  wsMessageCount: 0,
  wsMessageBytes: 0,
  wsJsonParseMs: 0,
  ptyOutputCount: 0,
  ptyOutputBase64Chars: 0,
  ptyJsonParseMs: 0,
  lastPtyOutputAt: null,
  lastPtyOutputRuntimeId: null,
  lastPtyOutputSeq: null,
  commandCount: 0,
  lastCommandAt: null,
  lastCommandName: null,
  lastCommandRuntimeId: null,
  ptyInputCount: 0,
  ptyInputBytes: 0,
  lastPtyInputAt: null,
  lastPtyInputRuntimeId: null,
  decodeCount: 0,
  decodedBytes: 0,
  decodeMs: 0,
  terminalWriteCount: 0,
  terminalWriteBytes: 0,
  terminalWriteCallMs: 0,
  listenerErrorCount: 0,
  lastListenerErrorAt: null,
  lastListenerError: null,
  recentEvents: [],
};

const MAX_RECENT_PTY_EVENTS = 64;

function touch() {
  snapshot.updatedAt = new Date().toISOString();
}

export function recordWsJsonParse(
  messageBytes: number,
  durationMs: number,
  eventName?: string,
  ptyBase64Chars = 0,
  metadata?: { runtimeId?: string | null; seq?: number | null },
) {
  snapshot.wsMessageCount += 1;
  snapshot.wsMessageBytes += messageBytes;
  snapshot.wsJsonParseMs += durationMs;
  snapshot.lastEventAt = new Date().toISOString();
  snapshot.lastEventName = eventName || null;
  snapshot.lastEventRuntimeId = metadata?.runtimeId ?? null;
  snapshot.lastEventSeq = typeof metadata?.seq === 'number' ? metadata.seq : null;
  if (eventName === 'pty_output') {
    snapshot.ptyOutputCount += 1;
    snapshot.ptyOutputBase64Chars += ptyBase64Chars;
    snapshot.ptyJsonParseMs += durationMs;
    snapshot.lastPtyOutputAt = snapshot.lastEventAt;
    snapshot.lastPtyOutputRuntimeId = snapshot.lastEventRuntimeId;
    snapshot.lastPtyOutputSeq = snapshot.lastEventSeq;
  }
  snapshot.recentEvents.push({
    at: snapshot.lastEventAt ?? new Date().toISOString(),
    kind: 'ws_event',
    event: snapshot.lastEventName,
    command: null,
    source: null,
    runtimeId: snapshot.lastEventRuntimeId,
    seq: snapshot.lastEventSeq,
    base64Chars: ptyBase64Chars,
    dataBytes: 0,
  });
  if (snapshot.recentEvents.length > MAX_RECENT_PTY_EVENTS) {
    snapshot.recentEvents.splice(0, snapshot.recentEvents.length - MAX_RECENT_PTY_EVENTS);
  }
  touch();
}

export function recordPtyCommand(
  command: string,
  runtimeId?: string | null,
  dataBytes = 0,
  source?: string | null,
  metadata?: { seq?: number | null },
) {
  const recordedAt = new Date().toISOString();
  snapshot.commandCount += 1;
  snapshot.lastCommandAt = recordedAt;
  snapshot.lastCommandName = command || null;
  snapshot.lastCommandRuntimeId = runtimeId ?? null;
  if (command === 'pty_input') {
    snapshot.ptyInputCount += 1;
    snapshot.ptyInputBytes += Math.max(0, dataBytes);
    snapshot.lastPtyInputAt = recordedAt;
    snapshot.lastPtyInputRuntimeId = runtimeId ?? null;
  }
  snapshot.recentEvents.push({
    at: recordedAt,
    kind: 'command',
    event: null,
    command: command || null,
    source: source ?? null,
    runtimeId: runtimeId ?? null,
    seq: typeof metadata?.seq === 'number' ? metadata.seq : null,
    base64Chars: 0,
    dataBytes: Math.max(0, dataBytes),
  });
  if (snapshot.recentEvents.length > MAX_RECENT_PTY_EVENTS) {
    snapshot.recentEvents.splice(0, snapshot.recentEvents.length - MAX_RECENT_PTY_EVENTS);
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

export function recordPtyListenerError(eventName: string, runtimeId: string | null | undefined, error: unknown) {
  snapshot.listenerErrorCount += 1;
  snapshot.lastListenerErrorAt = new Date().toISOString();
  snapshot.lastListenerError = {
    event: eventName || null,
    runtimeId: runtimeId ?? null,
    message: error instanceof Error ? error.message : String(error),
  };
  touch();
}

export function getPtyPerfSnapshot(): PtyPerfSnapshot {
  return {
    ...snapshot,
    recentEvents: snapshot.recentEvents.map((event) => ({ ...event })),
  };
}

export function clearPtyPerfSnapshot() {
  snapshot.updatedAt = new Date().toISOString();
  snapshot.lastEventAt = null;
  snapshot.lastEventName = null;
  snapshot.lastEventRuntimeId = null;
  snapshot.lastEventSeq = null;
  snapshot.wsMessageCount = 0;
  snapshot.wsMessageBytes = 0;
  snapshot.wsJsonParseMs = 0;
  snapshot.ptyOutputCount = 0;
  snapshot.ptyOutputBase64Chars = 0;
  snapshot.ptyJsonParseMs = 0;
  snapshot.lastPtyOutputAt = null;
  snapshot.lastPtyOutputRuntimeId = null;
  snapshot.lastPtyOutputSeq = null;
  snapshot.commandCount = 0;
  snapshot.lastCommandAt = null;
  snapshot.lastCommandName = null;
  snapshot.lastCommandRuntimeId = null;
  snapshot.ptyInputCount = 0;
  snapshot.ptyInputBytes = 0;
  snapshot.lastPtyInputAt = null;
  snapshot.lastPtyInputRuntimeId = null;
  snapshot.decodeCount = 0;
  snapshot.decodedBytes = 0;
  snapshot.decodeMs = 0;
  snapshot.terminalWriteCount = 0;
  snapshot.terminalWriteBytes = 0;
  snapshot.terminalWriteCallMs = 0;
  snapshot.listenerErrorCount = 0;
  snapshot.lastListenerErrorAt = null;
  snapshot.lastListenerError = null;
  snapshot.recentEvents = [];
}

if (typeof window !== 'undefined') {
  window.__ATTN_PTY_PERF_DUMP = () => getPtyPerfSnapshot();
  window.__ATTN_PTY_PERF_CLEAR = () => clearPtyPerfSnapshot();
}
