import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { explainEviction, PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';

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

// The daemon hangs up on a client it cannot keep fed, and the close status
// saying so cannot outrun the backlog that caused the hangup. The reason
// therefore arrives on the *next* connection, addressed to the client id the
// app repeats across reconnects — so the app has to send that id and has to
// surface what comes back, or the user is left with an unexplained blink.
describe('useDaemonSocket eviction notice', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    vi.useRealTimers();
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  it('identifies itself with a client id that survives reconnects', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    const hello = ws.sent.map((entry) => JSON.parse(entry)).find((entry) => entry.cmd === 'client_hello');
    expect(hello?.client_id).toBeTruthy();

    // Same app process, new socket — which is exactly the shape of an eviction:
    // the id has to match or the daemon cannot recognise the client it dropped.
    act(() => {
      ws.close();
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
    });
    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBeGreaterThan(1);
    });
    const reconnected = await waitForOpenSocket();
    await waitFor(() => {
      expect(reconnected.sent.length).toBeGreaterThan(0);
    });
    const secondHello = reconnected.sent
      .map((entry) => JSON.parse(entry))
      .find((entry) => entry.cmd === 'client_hello');
    expect(secondHello?.client_id).toBe(hello?.client_id);

    unmount();
  });

  it('surfaces the explanation, once, and then lets it be cleared', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();
    emitInitialState(ws);

    expect(result.current.disconnectExplanation).toBeNull();

    act(() => {
      ws.emit({
        event: 'client_eviction_notice',
        evicted_at: '2026-08-09T12:00:00Z',
        reason: 'client too slow',
        undelivered_messages: 3,
      });
    });

    await waitFor(() => {
      expect(result.current.disconnectExplanation).toContain('fell behind on updates');
    });

    act(() => {
      result.current.clearDisconnectExplanation();
    });
    await waitFor(() => {
      expect(result.current.disconnectExplanation).toBeNull();
    });

    unmount();
  });
});

describe('explainEviction', () => {
  it('names the cause in the user\'s terms, not the protocol\'s', () => {
    const message = explainEviction('client too slow', '2026-08-09T12:00:00Z');
    expect(message).toContain('Reconnected');
    expect(message).toContain('fell behind on updates');
    expect(message).not.toContain('client too slow');
  });

  it('passes an unfamiliar reason through rather than inventing one', () => {
    expect(explainEviction('command buffer overflow', '2026-08-09T12:00:00Z')).toContain(
      'command buffer overflow',
    );
  });

  it('drops the timestamp rather than printing a broken one', () => {
    const message = explainEviction('client too slow', 'not-a-date');
    expect(message).not.toContain('Invalid');
    expect(message).toContain('fell behind on updates');
  });
});
