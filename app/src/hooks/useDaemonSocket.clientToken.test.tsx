import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { useDaemonSocket } from './useDaemonSocket';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  binaryType = 'blob';
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

// The daemon's WebSocket port carries no file permissions, so the app proves
// itself with the profile's client token. Two halves matter to the user: the
// hello carries it, and a refusal ends the reconnect loop showing what the
// daemon said — which names the file to read — instead of blinking forever.
describe('useDaemonSocket client token', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockImplementation(async (cmd: string) =>
      cmd === 'get_client_token' ? 'profile-token' : '',
    );
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.restoreAllMocks();
  });

  it('presents the profile token in client_hello', async () => {
    const { unmount } = renderSocket();
    const ws = await waitForOpenSocket();

    await waitFor(() => {
      const hello = ws.sent.map((entry) => JSON.parse(entry)).find((entry) => entry.cmd === 'client_hello');
      expect(hello?.client_token).toBe('profile-token');
    });

    unmount();
  });

  it('stops reconnecting and surfaces the daemon message when the token is refused', async () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'command_error',
        cmd: 'client_hello',
        success: false,
        error_code: 'unauthorized_client',
        error: 'client_hello refused: read /tmp/.attn-dev/client-token',
      });
      ws.close();
    });

    await waitFor(() => {
      expect(result.current.connectionError).toContain('/tmp/.attn-dev/client-token');
    });
    // Retrying cannot help until someone changes the token, and a reconnect loop
    // would bury the message that says how. The close handler decides this
    // synchronously and says so — asserting on the socket count instead would
    // pass on its own, since the backoff has not elapsed by here either way.
    expect(errors.mock.calls.flat().join(' ')).toContain('Circuit open, not retrying');
    expect(FakeWebSocket.instances).toHaveLength(1);

    unmount();
  });
});
