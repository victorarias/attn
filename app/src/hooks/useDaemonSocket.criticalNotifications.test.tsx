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
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  });
  return ws;
}

// The ambient critical surface is driven by the broadcast, not by a listed feed,
// because the panel only lists while it is open. So what the broadcast carries is
// the whole contract: get it wrong and a user who never opens the panel never
// learns that something critical is unread.
describe('useDaemonSocket critical notifications', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  async function renderWithBroadcast() {
    const onNotificationsUpdated = vi.fn();
    renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onNotificationsUpdated,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );
    const ws = await waitForOpenSocket();
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
    return { ws, onNotificationsUpdated };
  }

  it('carries the critical count and title off the broadcast', async () => {
    const { ws, onNotificationsUpdated } = await renderWithBroadcast();

    act(() => {
      ws.emit({
        event: 'notifications_updated',
        unread_count: 4,
        unread_critical_count: 2,
        critical_title: 'Plugin stopped',
      });
    });

    expect(onNotificationsUpdated).toHaveBeenCalledWith(4, { count: 2, title: 'Plugin stopped' });
  });

  it('reports the surface cleared when the last critical one is read', async () => {
    const { ws, onNotificationsUpdated } = await renderWithBroadcast();

    act(() => {
      ws.emit({
        event: 'notifications_updated',
        unread_count: 1,
        unread_critical_count: 0,
      });
    });

    expect(onNotificationsUpdated).toHaveBeenCalledWith(1, { count: 0, title: '' });
  });

  // A daemon older than the app sends neither field. That must read as "nothing
  // critical" — the surface staying down is the safe failure, and inventing a
  // count from a missing field would put up a banner nothing can clear.
  it('treats a broadcast without the severity fields as nothing critical', async () => {
    const { ws, onNotificationsUpdated } = await renderWithBroadcast();

    act(() => {
      ws.emit({ event: 'notifications_updated', unread_count: 3 });
    });

    expect(onNotificationsUpdated).toHaveBeenCalledWith(3, { count: 0, title: '' });
  });
});
