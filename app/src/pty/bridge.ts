import { isTauri } from '@tauri-apps/api/core';

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
  label?: string;
  claude_executable?: string;
  codex_executable?: string;
}

export type PtyEventPayload =
  | { event: 'data'; id: string; data: string }
  | { event: 'exit'; id: string; code: number; signal?: string }
  | { event: 'error'; id: string; error: string }
  | { event: 'transcript'; id: string; matched: boolean }
  | { event: 'reset'; id: string; reason: string };

type PtyEventHandler = (event: { payload: PtyEventPayload }) => void;

export interface PtyBackend {
  spawn: (args: PtySpawnArgs) => Promise<void>;
  write: (id: string, data: string) => Promise<void>;
  resize: (id: string, cols: number, rows: number) => Promise<void>;
  kill: (id: string) => Promise<void>;
}

const listeners = new Set<PtyEventHandler>();
const mockSessions = new Set<string>();

let backend: PtyBackend | null = null;

const mockEnabled = (): boolean => {
  if (import.meta.env.VITE_FORCE_REAL_PTY === '1') return false;
  if (import.meta.env.VITE_MOCK_PTY === '1') return true;
  if (typeof window === 'undefined') return true;
  return !isTauri();
};

export function setPtyBackend(next: PtyBackend | null) {
  backend = next;
}

export function emitPtyEvent(payload: PtyEventPayload) {
  const event = { payload };
  if (typeof window !== 'undefined') {
    const store = (window as unknown as { __TEST_PTY_EVENTS?: PtyEventPayload[] }).__TEST_PTY_EVENTS;
    if (Array.isArray(store)) {
      store.push(payload);
    } else {
      (window as unknown as { __TEST_PTY_EVENTS?: PtyEventPayload[] }).__TEST_PTY_EVENTS = [payload];
    }
  }
  for (const handler of listeners) {
    handler(event);
  }
}

const encodeBase64 = (value: string) => {
  if (typeof btoa !== 'undefined') {
    return btoa(value);
  }
  return '';
};

export async function listenPtyEvents(handler: PtyEventHandler) {
  listeners.add(handler);
  return () => {
    listeners.delete(handler);
  };
}

export async function ptySpawn(request: { args: PtySpawnArgs }) {
  if (mockEnabled()) {
    const id = request.args.id;
    mockSessions.add(id);
    const banner = `attn mock pty: ${id}\r\n`;
    setTimeout(() => {
      emitPtyEvent({ event: 'data', id, data: encodeBase64(banner) });
    }, 30);
    return;
  }
  if (!backend) {
    throw new Error('PTY backend is not configured');
  }
  await backend.spawn(request.args);
}

export async function ptyWrite(request: { id: string; data: string }) {
  if (mockEnabled()) {
    if (!mockSessions.has(request.id)) {
      return;
    }
    emitPtyEvent({ event: 'data', id: request.id, data: encodeBase64(request.data) });
    return;
  }
  if (!backend) {
    throw new Error('PTY backend is not configured');
  }
  await backend.write(request.id, request.data);
}

export async function ptyResize(request: { id: string; cols: number; rows: number }) {
  if (mockEnabled()) {
    if (!mockSessions.has(request.id)) {
      return;
    }
    return;
  }
  if (!backend) {
    throw new Error('PTY backend is not configured');
  }
  await backend.resize(request.id, request.cols, request.rows);
}

export async function ptyKill(request: { id: string }) {
  if (mockEnabled()) {
    if (!mockSessions.has(request.id)) {
      return;
    }
    mockSessions.delete(request.id);
    emitPtyEvent({ event: 'exit', id: request.id, code: 0 });
    return;
  }
  if (!backend) {
    throw new Error('PTY backend is not configured');
  }
  await backend.kill(request.id);
}
