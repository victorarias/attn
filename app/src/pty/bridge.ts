import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';

export interface PtySpawnArgs {
  id: string;
  cwd: string;
  cols: number;
  rows: number;
  shell?: boolean;
  resume_session_id?: string | null;
  resume_picker?: boolean | null;
  fork_session?: boolean | null;
  agent?: string;
  claude_executable?: string;
  codex_executable?: string;
}

type PtyEventPayload =
  | { event: 'data'; id: string; data: string }
  | { event: 'exit'; id: string; code: number }
  | { event: 'error'; id: string; error: string }
  | { event: 'transcript'; id: string; matched: boolean };

type PtyEventHandler = (event: { payload: PtyEventPayload }) => void;

const mockListeners = new Set<PtyEventHandler>();
const mockSessions = new Set<string>();

const mockEnabled = (): boolean => {
  if (import.meta.env.VITE_MOCK_PTY === '1') return true;
  if (typeof window === 'undefined') return true;
  return !(window as unknown as { __TAURI__?: unknown }).__TAURI__;
};

const emitMock = (payload: PtyEventPayload) => {
  const event = { payload };
  if (typeof window !== 'undefined') {
    const store = (window as unknown as { __TEST_PTY_EVENTS?: PtyEventPayload[] }).__TEST_PTY_EVENTS;
    if (Array.isArray(store)) {
      store.push(payload);
    } else {
      (window as unknown as { __TEST_PTY_EVENTS?: PtyEventPayload[] }).__TEST_PTY_EVENTS = [payload];
    }
  }
  for (const handler of mockListeners) {
    handler(event);
  }
};

const encodeBase64 = (value: string) => {
  if (typeof btoa !== 'undefined') {
    return btoa(value);
  }
  return '';
};

export async function listenPtyEvents(handler: PtyEventHandler) {
  if (!mockEnabled()) {
    return listen('pty-event', handler);
  }
  mockListeners.add(handler);
  return () => {
    mockListeners.delete(handler);
  };
}

export async function ptySpawn(request: { args: PtySpawnArgs }) {
  if (!mockEnabled()) {
    return invoke<number>('pty_spawn', request);
  }
  const id = request.args.id;
  mockSessions.add(id);
  const banner = `attn mock pty: ${id}\r\n`;
  setTimeout(() => {
    emitMock({ event: 'data', id, data: encodeBase64(banner) });
  }, 30);
  return 0;
}

export async function ptyWrite(request: { id: string; data: string }) {
  if (!mockEnabled()) {
    return invoke('pty_write', request);
  }
  if (!mockSessions.has(request.id)) {
    return;
  }
  emitMock({ event: 'data', id: request.id, data: encodeBase64(request.data) });
}

export async function ptyResize(request: { id: string; cols: number; rows: number }) {
  if (!mockEnabled()) {
    return invoke('pty_resize', request);
  }
  if (!mockSessions.has(request.id)) {
    return;
  }
}

export async function ptyKill(request: { id: string }) {
  if (!mockEnabled()) {
    return invoke('pty_kill', request);
  }
  if (!mockSessions.has(request.id)) {
    return;
  }
  mockSessions.delete(request.id);
  emitMock({ event: 'exit', id: request.id, code: 0 });
}
