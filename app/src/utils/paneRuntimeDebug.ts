import { isTauri } from '@tauri-apps/api/core';

const PANE_RUNTIME_DEBUG_STORAGE_KEY = 'attn:pane-runtime-debug';
const MAX_PANE_RUNTIME_DEBUG_EVENTS = 500;
const PANE_RUNTIME_DEBUG_DIR = 'debug';
const PANE_RUNTIME_DEBUG_FILE = `${PANE_RUNTIME_DEBUG_DIR}/pane-runtime-debug.jsonl`;

export interface PaneRuntimeDebugEvent {
  at: string;
  scope: string;
  sessionId?: string;
  paneId?: string;
  runtimeId?: string;
  message: string;
  details?: Record<string, unknown>;
}

declare global {
  interface Window {
    __ATTN_PANE_DEBUG_EVENTS?: PaneRuntimeDebugEvent[];
    __ATTN_PANE_DEBUG_DUMP?: () => PaneRuntimeDebugEvent[];
    __ATTN_PANE_DEBUG_CLEAR?: () => void;
    __ATTN_PANE_DEBUG_ENABLE?: (enabled: boolean) => void;
    __ATTN_PANE_DEBUG_FILE?: string;
  }
}

let fileWriteChain: Promise<void> = Promise.resolve();
type PaneRuntimeDebugEventDetails =
  | Record<string, unknown>
  | (() => Record<string, unknown> | undefined);
type PaneRuntimeDebugEventInput =
  Omit<PaneRuntimeDebugEvent, 'at' | 'details'>
  & { details?: PaneRuntimeDebugEventDetails };

async function appendDebugEventToFile(entry: PaneRuntimeDebugEvent) {
  if (!isTauri() || !isPaneRuntimeDebugEnabled()) {
    return;
  }
  try {
    const { mkdir, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(PANE_RUNTIME_DEBUG_DIR, {
      baseDir: BaseDirectory.AppLocalData,
      recursive: true,
    });
    await writeTextFile(
      PANE_RUNTIME_DEBUG_FILE,
      `${JSON.stringify(entry)}\n`,
      { baseDir: BaseDirectory.AppLocalData, append: true, create: true },
    );
  } catch (error) {
    console.warn('[PaneDebug] Failed to append debug event to file:', error);
  }
}

function enqueueDebugEventFileWrite(entry: PaneRuntimeDebugEvent) {
  fileWriteChain = fileWriteChain
    .catch(() => {})
    .then(() => appendDebugEventToFile(entry));
}

async function clearDebugFile() {
  if (!isTauri()) {
    return;
  }
  try {
    const { mkdir, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(PANE_RUNTIME_DEBUG_DIR, {
      baseDir: BaseDirectory.AppLocalData,
      recursive: true,
    });
    await writeTextFile(
      PANE_RUNTIME_DEBUG_FILE,
      '',
      { baseDir: BaseDirectory.AppLocalData, create: true },
    );
  } catch (error) {
    console.warn('[PaneDebug] Failed to clear debug file:', error);
  }
}

function ensureGlobals() {
  if (typeof window === 'undefined') {
    return;
  }
  window.__ATTN_PANE_DEBUG_FILE = `$APPLOCALDATA/${PANE_RUNTIME_DEBUG_FILE}`;
  if (!window.__ATTN_PANE_DEBUG_EVENTS) {
    window.__ATTN_PANE_DEBUG_EVENTS = [];
  }
  if (!window.__ATTN_PANE_DEBUG_DUMP) {
    window.__ATTN_PANE_DEBUG_DUMP = () => [...(window.__ATTN_PANE_DEBUG_EVENTS || [])];
  }
  if (!window.__ATTN_PANE_DEBUG_CLEAR) {
    window.__ATTN_PANE_DEBUG_CLEAR = () => {
      window.__ATTN_PANE_DEBUG_EVENTS = [];
      void clearDebugFile();
    };
  }
  if (!window.__ATTN_PANE_DEBUG_ENABLE) {
    window.__ATTN_PANE_DEBUG_ENABLE = (enabled: boolean) => {
      try {
        if (enabled) {
          window.localStorage.setItem(PANE_RUNTIME_DEBUG_STORAGE_KEY, '1');
        } else {
          window.localStorage.removeItem(PANE_RUNTIME_DEBUG_STORAGE_KEY);
        }
        if (enabled) {
          window.__ATTN_PANE_DEBUG_EVENTS = [];
          void clearDebugFile();
        }
      } catch {
        // Ignore localStorage errors in constrained environments.
      }
    };
  }
}

export function isPaneRuntimeDebugEnabled(): boolean {
  if (typeof window === 'undefined') {
    return false;
  }
  try {
    return window.localStorage.getItem(PANE_RUNTIME_DEBUG_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

export function recordPaneRuntimeDebugEvent(event: PaneRuntimeDebugEventInput) {
  if (typeof window === 'undefined') {
    return;
  }
  if (!isPaneRuntimeDebugEnabled()) {
    return;
  }
  ensureGlobals();
  const details = resolvePaneRuntimeDebugDetails(event.details);
  const entry: PaneRuntimeDebugEvent = {
    at: new Date().toISOString(),
    ...event,
    details,
  };
  const events = window.__ATTN_PANE_DEBUG_EVENTS || [];
  events.push(entry);
  if (events.length > MAX_PANE_RUNTIME_DEBUG_EVENTS) {
    events.splice(0, events.length - MAX_PANE_RUNTIME_DEBUG_EVENTS);
  }
  window.__ATTN_PANE_DEBUG_EVENTS = events;
  enqueueDebugEventFileWrite(entry);

  const prefix = `[PaneDebug:${event.scope}] ${event.message}`;
  if (details) {
    console.log(prefix, {
      sessionId: event.sessionId,
      paneId: event.paneId,
      runtimeId: event.runtimeId,
      ...details,
    });
  } else {
    console.log(prefix, {
      sessionId: event.sessionId,
      paneId: event.paneId,
      runtimeId: event.runtimeId,
    });
  }
}

function resolvePaneRuntimeDebugDetails(details: PaneRuntimeDebugEventDetails | undefined) {
  if (typeof details === 'function') {
    return details();
  }
  return details;
}

export function activeElementSummary(): Record<string, unknown> {
  if (typeof document === 'undefined') {
    return {};
  }
  const active = document.activeElement as HTMLElement | null;
  if (!active) {
    return { activeTag: null };
  }
  return {
    activeTag: active.tagName,
    activeRole: active.getAttribute('role'),
    activeAriaLabel: active.getAttribute('aria-label'),
    activeClassName: active.className,
    activeText: active.textContent?.slice(0, 60) || '',
  };
}

ensureGlobals();
