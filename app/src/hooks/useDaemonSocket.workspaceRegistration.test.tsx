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

// Registering and unregistering a workspace park a promise that only the daemon
// can settle. A registration the daemon refuses — a forwarded remote one that
// failed, say — comes back as `command_error`, which carries no correlation id,
// so the socket has to match it by command name. Without that the caller waits
// out the full ten seconds and reports a timeout, hiding what the daemon said.
describe('useDaemonSocket workspace registration errors', () => {
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

  it('rejects a pending registration with the daemon error instead of timing out', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    let registration!: Promise<void>;
    act(() => {
      registration = result.current.sendRegisterWorkspace('workspace-1', 'Workspace', '/tmp/repo');
    });
    const settled = registration.then(
      () => 'resolved',
      (err: Error) => err.message,
    );

    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry)).some((entry) => entry.cmd === 'register_workspace')).toBe(true);
    });

    act(() => {
      ws.emit({
        event: 'command_error',
        cmd: 'register_workspace',
        error: 'remote endpoint refused the workspace',
      });
    });

    // Drain the ten-second registration timeout too: if the command_error branch
    // is ever dropped, this test fails on the timeout's own wording rather than
    // hanging on a promise nobody settles.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });

    await expect(settled).resolves.toBe('remote endpoint refused the workspace');

    unmount();
  });

  it('rejects a pending unregistration with the daemon error instead of timing out', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    let unregistration!: Promise<void>;
    act(() => {
      unregistration = result.current.sendUnregisterWorkspace('workspace-1');
    });
    const settled = unregistration.then(
      () => 'resolved',
      (err: Error) => err.message,
    );

    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry)).some((entry) => entry.cmd === 'unregister_workspace')).toBe(true);
    });

    act(() => {
      ws.emit({
        event: 'command_error',
        cmd: 'unregister_workspace',
        error: 'workspace is still in use',
      });
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });

    await expect(settled).resolves.toBe('workspace is still in use');

    unmount();
  });
});
