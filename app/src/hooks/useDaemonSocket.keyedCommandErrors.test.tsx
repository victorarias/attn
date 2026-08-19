import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  sent: string[] = [];

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.(new Event('open'));
    });
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

async function waitForOpenSocket(): Promise<FakeWebSocket> {
  await waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  });
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  expect(ws).toBeDefined();
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  });
  return ws;
}

function renderSocket() {
  return renderHook(() =>
    useDaemonSocket({
      onSessionsUpdate: vi.fn(),
      onWorkspacesUpdate: vi.fn(),
      onPRsUpdate: vi.fn(),
      onReposUpdate: vi.fn(),
      onAuthorsUpdate: vi.fn(),
      wsUrl: 'ws://localhost:9999/ws',
    }),
  );
}

function emitInitialState(ws: FakeWebSocket) {
  act(() => {
    ws.emit({
      event: 'initial_state',
      protocol_version: PROTOCOL_VERSION,
      sessions: [],
      workspaces: [],
      prs: [],
      repos: [],
      authors: [],
      settings: {},
    });
  });
}

// A keyed request parks its promise under `<cmd>:<id>`, so a `command_error`
// — which carries no correlation id — has to be matched by command name with
// that suffix allowed. Without it the caller waits out its own timeout and
// reports "timed out" instead of what the daemon said: a live remote session
// reloaded against a parked endpoint showed "Reload session timed out" while
// the daemon had already answered with the parked reason.
describe('useDaemonSocket keyed command errors', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(false);
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  it('rejects a pending session rename with the parked endpoint reason instead of timing out', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    let rename!: Promise<void>;
    act(() => {
      rename = result.current.sendRenameSession('session-1', 'new name');
    });
    const settled = rename.then(
      () => 'resolved',
      (err: Error) => err.message,
    );

    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry)).some((entry) => entry.cmd === 'rename_session')).toBe(true);
    });

    act(() => {
      ws.emit({
        event: 'command_error',
        cmd: 'rename_session',
        error: 'endpoint gpu-box is parked: remote binary (abc1234) differs from this client (def5678) — click Sync to update',
      });
    });

    // Drain the request's own timeout: if the match is ever dropped, this fails
    // on the timeout's wording rather than hanging on a promise nobody settles.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });

    await expect(settled).resolves.toBe(
      'endpoint gpu-box is parked: remote binary (abc1234) differs from this client (def5678) — click Sync to update',
    );

    unmount();
  });

  it('rejects a pending workspace rename with the daemon error instead of timing out', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    let rename!: Promise<void>;
    act(() => {
      rename = result.current.sendRenameWorkspace('workspace-1', 'new name');
    });
    const settled = rename.then(
      () => 'resolved',
      (err: Error) => err.message,
    );

    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry)).some((entry) => entry.cmd === 'rename_workspace')).toBe(true);
    });

    act(() => {
      ws.emit({
        event: 'command_error',
        cmd: 'rename_workspace',
        error: 'endpoint gpu-box is parked: remote binary differs from this client — click Sync to update',
      });
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });

    await expect(settled).resolves.toBe(
      'endpoint gpu-box is parked: remote binary differs from this client — click Sync to update',
    );

    unmount();
  });
});
