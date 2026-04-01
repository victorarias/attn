import { isTauri } from '@tauri-apps/api/core';

const TERMINAL_RUNTIME_LOG_DIR = 'debug';
const TERMINAL_RUNTIME_LOG_FILE = `${TERMINAL_RUNTIME_LOG_DIR}/terminal-runtime.jsonl`;
const MAX_TERMINAL_RUNTIME_EVENTS = 500;

export interface TerminalRuntimeLogEvent {
  at: string;
  category: string;
  sessionId?: string;
  paneId?: string;
  runtimeId?: string;
  debugName?: string;
  message: string;
  details?: Record<string, unknown>;
}

declare global {
  interface Window {
    __ATTN_TERMINAL_RUNTIME_EVENTS?: TerminalRuntimeLogEvent[];
    __ATTN_TERMINAL_RUNTIME_DUMP?: () => TerminalRuntimeLogEvent[];
    __ATTN_TERMINAL_RUNTIME_CLEAR?: () => void;
    __ATTN_TERMINAL_RUNTIME_FILE?: string;
  }
}

let fileWriteChain: Promise<void> = Promise.resolve();

async function appendRuntimeLogToFile(entry: TerminalRuntimeLogEvent) {
  if (!isTauri()) {
    return;
  }
  try {
    const { mkdir, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(TERMINAL_RUNTIME_LOG_DIR, {
      baseDir: BaseDirectory.AppLocalData,
      recursive: true,
    });
    await writeTextFile(
      TERMINAL_RUNTIME_LOG_FILE,
      `${JSON.stringify(entry)}\n`,
      { baseDir: BaseDirectory.AppLocalData, append: true, create: true },
    );
  } catch (error) {
    console.warn('[TerminalRuntimeLog] Failed to append runtime event:', error);
  }
}

async function clearRuntimeLogFile() {
  if (!isTauri()) {
    return;
  }
  try {
    const { mkdir, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(TERMINAL_RUNTIME_LOG_DIR, {
      baseDir: BaseDirectory.AppLocalData,
      recursive: true,
    });
    await writeTextFile(
      TERMINAL_RUNTIME_LOG_FILE,
      '',
      { baseDir: BaseDirectory.AppLocalData, create: true },
    );
  } catch (error) {
    console.warn('[TerminalRuntimeLog] Failed to clear runtime log file:', error);
  }
}

function enqueueRuntimeLogFileWrite(entry: TerminalRuntimeLogEvent) {
  fileWriteChain = fileWriteChain
    .catch(() => {})
    .then(() => appendRuntimeLogToFile(entry));
}

function ensureGlobals() {
  if (typeof window === 'undefined') {
    return;
  }
  window.__ATTN_TERMINAL_RUNTIME_FILE = `$APPLOCALDATA/${TERMINAL_RUNTIME_LOG_FILE}`;
  if (!window.__ATTN_TERMINAL_RUNTIME_EVENTS) {
    window.__ATTN_TERMINAL_RUNTIME_EVENTS = [];
  }
  if (!window.__ATTN_TERMINAL_RUNTIME_DUMP) {
    window.__ATTN_TERMINAL_RUNTIME_DUMP = () => [...(window.__ATTN_TERMINAL_RUNTIME_EVENTS || [])];
  }
  if (!window.__ATTN_TERMINAL_RUNTIME_CLEAR) {
    window.__ATTN_TERMINAL_RUNTIME_CLEAR = () => {
      window.__ATTN_TERMINAL_RUNTIME_EVENTS = [];
      void clearRuntimeLogFile();
    };
  }
}

export function recordTerminalRuntimeLog(event: Omit<TerminalRuntimeLogEvent, 'at'>) {
  if (typeof window === 'undefined') {
    return;
  }
  ensureGlobals();
  const entry: TerminalRuntimeLogEvent = {
    at: new Date().toISOString(),
    ...event,
  };
  const events = window.__ATTN_TERMINAL_RUNTIME_EVENTS || [];
  events.push(entry);
  if (events.length > MAX_TERMINAL_RUNTIME_EVENTS) {
    events.splice(0, events.length - MAX_TERMINAL_RUNTIME_EVENTS);
  }
  window.__ATTN_TERMINAL_RUNTIME_EVENTS = events;
  enqueueRuntimeLogFileWrite(entry);
}

ensureGlobals();
