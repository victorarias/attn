import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { ptyKill } from '../pty/bridge';
import { useDaemonSocket } from './useDaemonSocket';

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

describe('useDaemonSocket PTY kill sequencing', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(true);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  it('waits for session_exited before resolving ptyKill', async () => {
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBe(1);
    });
    const ws = FakeWebSocket.instances[0];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });

    let resolved = false;
    const killPromise = ptyKill({ id: 'reload-race' }).then(() => {
      resolved = true;
    });

    await Promise.resolve();
    expect(resolved).toBe(false);

    const sent = ws.sent.map((entry) => JSON.parse(entry));
    const detachIdx = sent.findIndex((entry) => entry.cmd === 'detach_session');
    const killIdx = sent.findIndex((entry) => entry.cmd === 'kill_session');
    expect(detachIdx).toBeGreaterThanOrEqual(0);
    expect(killIdx).toBeGreaterThan(detachIdx);

    ws.emit({ event: 'session_exited', id: 'reload-race', exit_code: 0 });
    await killPromise;
    expect(resolved).toBe(true);

    unmount();
  });

  it('resolves getReviewLoopRun from show result keyed by loop_id', async () => {
    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBe(1);
    });
    const ws = FakeWebSocket.instances[0];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });

    const runPromise = result.current.getReviewLoopRun('loop-123');
    await Promise.resolve();

    ws.emit({
      event: 'review_loop_result',
      action: 'show',
      loop_id: 'loop-123',
      success: true,
      review_loop_run: {
        loop_id: 'loop-123',
        source_session_id: 'sess-1',
        repo_path: '/tmp/repo',
        status: 'running',
        resolved_prompt: 'Review this repo',
        iteration_count: 2,
        iteration_limit: 3,
        iterations: [
          {
            id: 'iter-1',
            loop_id: 'loop-123',
            iteration_number: 1,
            status: 'completed',
            started_at: '2026-03-08T00:00:00Z',
          },
        ],
        created_at: '2026-03-08T00:00:00Z',
        updated_at: '2026-03-08T00:00:00Z',
      },
    });

    await expect(runPromise).resolves.toMatchObject({
      success: true,
      state: {
        loop_id: 'loop-123',
        iterations: [{ id: 'iter-1' }],
      },
    });

    unmount();
  });

  it('rejects answerReviewLoop immediately when error result only echoes loop_id', async () => {
    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBe(1);
    });
    const ws = FakeWebSocket.instances[0];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });

    const answerPromise = result.current.answerReviewLoop('loop-123', 'interaction-1', 'Ship it');
    await Promise.resolve();

    ws.emit({
      event: 'review_loop_result',
      action: 'answer',
      loop_id: 'loop-123',
      success: false,
      error: 'interaction not found',
    });

    await expect(answerPromise).rejects.toThrow('interaction not found');

    unmount();
  });

  it('restarts an older daemon automatically on protocol mismatch in Tauri', async () => {
    vi.mocked(invoke).mockImplementation(async (cmd) => {
      if (cmd === 'restart_daemon') {
        return undefined;
      }
      return true;
    });

    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBe(1);
    });
    const ws = FakeWebSocket.instances[0];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '41',
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    await waitFor(() => {
      expect(vi.mocked(invoke)).toHaveBeenCalledWith('restart_daemon', {
        expected_protocol: '42',
        prefer_local: false,
      });
    });
    expect(ws.readyState).toBe(FakeWebSocket.CLOSED);
    expect(result.current.connectionError === null || result.current.connectionError === 'Restarting daemon...').toBe(true);

    unmount();
  });
});
