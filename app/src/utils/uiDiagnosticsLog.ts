// Bounded, disk-backed diagnostics for failures that affect the whole app
// shell. Terminal rendering has its own richer stream; this file answers the
// layer above it: did React render, did the DOM stay visible, did the event loop
// stall, or did a native browser child WebView remain over the app?
import { isTauri } from '@tauri-apps/api/core';

const DEBUG_DIR = 'debug';
const FILE = `${DEBUG_DIR}/ui-diagnostics.jsonl`;
const FILE_SIZE_CAP_BYTES = 4 * 1024 * 1024;
const RING_LIMIT = 300;
const SWITCH_PROBE_DELAYS_MS = [0, 250, 1500];
const HEARTBEAT_INTERVAL_MS = 30_000;
const EVENT_LOOP_STALL_MS = 5_000;

export interface UiDiagEvent {
  at: number;
  kind: string;
  [key: string]: unknown;
}

declare global {
  interface Window {
    __ATTN_UI_DIAG?: UiDiagEvent[];
    __ATTN_UI_DIAG_DUMP?: () => UiDiagEvent[];
    __ATTN_UI_DIAG_FILE?: string;
  }
}

const ring: UiDiagEvent[] = [];
let writeChain: Promise<void> = Promise.resolve();
let bytes = 0;
let sizeSeeded = false;
let installed = false;
let switchGeneration = 0;

function exposeGlobals(): void {
  window.__ATTN_UI_DIAG = ring;
  window.__ATTN_UI_DIAG_DUMP = () => [...ring];
  window.__ATTN_UI_DIAG_FILE = `$APPLOCALDATA/${FILE}`;
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

async function append(line: string): Promise<void> {
  if (!isTauri()) return;
  // Some unit tests mock `isTauri()` so browser-host calls take their native
  // branch without installing Tauri's real invoke bridge. Avoid noisy failed
  // filesystem calls in that partial environment.
  const tauriInternals = (window as unknown as { __TAURI_INTERNALS__?: { invoke?: unknown } }).__TAURI_INTERNALS__;
  if (typeof tauriInternals?.invoke !== 'function') return;
  try {
    const { mkdir, stat, writeTextFile, BaseDirectory } = await import('@tauri-apps/plugin-fs');
    await mkdir(DEBUG_DIR, { baseDir: BaseDirectory.AppLocalData, recursive: true });
    if (!sizeSeeded) {
      try {
        bytes = (await stat(FILE, { baseDir: BaseDirectory.AppLocalData })).size;
      } catch {
        bytes = 0;
      }
      sizeSeeded = true;
    }
    const reset = bytes > FILE_SIZE_CAP_BYTES;
    const payload = reset
      ? `${JSON.stringify({ at: Date.now(), kind: 'rotate' })}\n${line}`
      : line;
    await writeTextFile(FILE, payload, {
      baseDir: BaseDirectory.AppLocalData,
      append: !reset,
      create: true,
    });
    bytes = reset ? byteLength(payload) : bytes + byteLength(payload);
  } catch (error) {
    console.warn('[UiDiag] write failed:', error);
  }
}

export function recordUiDiag(event: Omit<UiDiagEvent, 'at'>): void {
  if (typeof window === 'undefined') return;
  exposeGlobals();
  const entry = { ...event, at: Date.now() } as UiDiagEvent;
  if (ring.length >= RING_LIMIT) ring.shift();
  ring.push(entry);
  const line = `${JSON.stringify(entry)}\n`;
  writeChain = writeChain.catch(() => {}).then(() => append(line));
}

function errorDetail(value: unknown): { message: string; stack?: string } {
  if (value instanceof Error) {
    return { message: value.message, stack: value.stack };
  }
  if (typeof value === 'string') return { message: value };
  try {
    return { message: JSON.stringify(value) };
  } catch {
    return { message: String(value) };
  }
}

function elementSummary(element: Element | null): Record<string, unknown> | null {
  if (!element) return null;
  const html = element as HTMLElement;
  const rect = html.getBoundingClientRect();
  const style = getComputedStyle(html);
  return {
    tag: element.tagName.toLowerCase(),
    id: html.id || undefined,
    classes: typeof html.className === 'string' ? html.className.slice(0, 240) : undefined,
    rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
    display: style.display,
    visibility: style.visibility,
    opacity: style.opacity,
    background: style.backgroundColor,
  };
}

export function captureUiSnapshot(): Record<string, unknown> {
  const root = document.getElementById('root');
  const app = document.querySelector('.app');
  const activeWrappers = [...document.querySelectorAll('.terminal-wrapper.active')];
  const center = document.elementFromPoint(window.innerWidth / 2, window.innerHeight / 2);
  return {
    visibilityState: document.visibilityState,
    window: { width: window.innerWidth, height: window.innerHeight, devicePixelRatio: window.devicePixelRatio },
    root: elementSummary(root),
    rootChildren: root?.childElementCount ?? 0,
    app: elementSummary(app),
    activeWrapperCount: activeWrappers.length,
    activeWrappers: activeWrappers.slice(0, 4).map(elementSummary),
    center: elementSummary(center),
    activeElement: elementSummary(document.activeElement),
    browserHostOwners: [...document.querySelectorAll('[data-browser-host-owner]')].slice(0, 8).map(elementSummary),
  };
}

export function probeUiAfterSwitch(context: {
  sessionId: string | null;
  workspaceId: string | null;
  view: string;
}): void {
  const generation = ++switchGeneration;
  recordUiDiag({ kind: 'session_switch', generation, ...context });
  for (const delayMs of SWITCH_PROBE_DELAYS_MS) {
    window.setTimeout(() => {
      if (generation !== switchGeneration) return;
      recordUiDiag({
        kind: 'switch_probe',
        generation,
        delayMs,
        ...context,
        snapshot: captureUiSnapshot(),
      });
    }, delayMs);
  }
}

export function installUiDiagnostics(): void {
  if (installed || typeof window === 'undefined') return;
  installed = true;
  exposeGlobals();
  recordUiDiag({ kind: 'boot', href: window.location.pathname, visibilityState: document.visibilityState });

  window.addEventListener('error', (event) => {
    recordUiDiag({
      kind: 'window_error',
      ...errorDetail(event.error ?? event.message),
      filename: event.filename,
      line: event.lineno,
      column: event.colno,
      snapshot: captureUiSnapshot(),
    });
  });
  window.addEventListener('unhandledrejection', (event) => {
    recordUiDiag({ kind: 'unhandled_rejection', ...errorDetail(event.reason), snapshot: captureUiSnapshot() });
  });
  document.addEventListener('visibilitychange', () => {
    recordUiDiag({ kind: 'visibility_change', visibilityState: document.visibilityState });
  });

  let expectedAt = Date.now() + HEARTBEAT_INTERVAL_MS;
  window.setInterval(() => {
    const now = Date.now();
    const lateByMs = Math.max(0, now - expectedAt);
    recordUiDiag({
      kind: lateByMs >= EVENT_LOOP_STALL_MS ? 'event_loop_stall' : 'heartbeat',
      lateByMs,
      visibilityState: document.visibilityState,
    });
    expectedAt = now + HEARTBEAT_INTERVAL_MS;
  }, HEARTBEAT_INTERVAL_MS);
}

export function recordReactError(error: unknown, componentStack?: string): void {
  recordUiDiag({
    kind: 'react_error',
    ...errorDetail(error),
    componentStack,
    snapshot: captureUiSnapshot(),
  });
}
