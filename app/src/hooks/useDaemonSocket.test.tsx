import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { ptyAttach, ptyKill, ptySpawn } from '../pty/bridge';
import { retryTransientAttachRequest, useDaemonSocket } from './useDaemonSocket';

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

describe('useDaemonSocket PTY kill sequencing', () => {
  let originalWebSocket: typeof WebSocket;
  let originalSetTimeout: typeof globalThis.setTimeout;
  let originalClearTimeout: typeof globalThis.clearTimeout;
  let pendingTimeouts: Set<ReturnType<typeof globalThis.setTimeout>>;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    originalSetTimeout = globalThis.setTimeout;
    originalClearTimeout = globalThis.clearTimeout;
    pendingTimeouts = new Set();
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    globalThis.setTimeout = ((handler: TimerHandler, timeout?: number, ...args: any[]) => {
      let timeoutId: ReturnType<typeof globalThis.setTimeout>;
      timeoutId = originalSetTimeout((...callbackArgs: any[]) => {
        pendingTimeouts.delete(timeoutId);
        if (typeof handler === 'function') {
          handler(...callbackArgs);
        }
      }, timeout, ...args);
      pendingTimeouts.add(timeoutId);
      return timeoutId;
    }) as typeof globalThis.setTimeout;
    globalThis.clearTimeout = ((timeoutId?: ReturnType<typeof globalThis.setTimeout>) => {
      if (timeoutId !== undefined) {
        pendingTimeouts.delete(timeoutId);
      }
      return originalClearTimeout(timeoutId);
    }) as typeof globalThis.clearTimeout;
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(true);
  });

  afterEach(() => {
    for (const timeoutId of pendingTimeouts) {
      originalClearTimeout(timeoutId);
    }
    pendingTimeouts.clear();
    globalThis.setTimeout = originalSetTimeout;
    globalThis.clearTimeout = originalClearTimeout;
    globalThis.WebSocket = originalWebSocket;
    vi.useRealTimers();
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
      expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
    });
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
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

    const ws = await waitForOpenSocket();

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

    const ws = await waitForOpenSocket();

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

    const ws = await waitForOpenSocket();

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
        expected_protocol: '50',
        prefer_local: false,
      });
    });
    expect(ws.readyState).toBe(FakeWebSocket.CLOSED);
    expect(result.current.connectionError === null || result.current.connectionError === 'Restarting daemon...').toBe(true);

    unmount();
  });

  it('serializes endpoint actions so concurrent updates do not collide', async () => {
    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onEndpointsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    const first = result.current.sendUpdateEndpoint('ep-1', { enabled: false });
    await expect(result.current.sendUpdateEndpoint('ep-2', { enabled: false })).rejects.toThrow(
      'Another endpoint action is already in progress',
    );

    act(() => {
      ws.emit({
        event: 'endpoint_action_result',
        action: 'update',
        endpoint_id: 'ep-1',
        success: true,
      });
    });

    await expect(first).resolves.toMatchObject({ success: true, endpoint_id: 'ep-1' });
    unmount();
  });

  it('retries transient worker attach failures after respawn', async () => {
    const waits: number[] = [];
    const attach = vi.fn()
      .mockRejectedValueOnce(new Error('dial unix /Users/test/.attn/workers/d-test/sock/ABC.sock: connect: no such file or directory'))
      .mockResolvedValueOnce({ success: true });

    await expect(
      retryTransientAttachRequest(() => attach(), {
        timeoutMs: 500,
        delayMs: 25,
        wait: async (delayMs) => {
          waits.push(delayMs);
        },
      }),
    ).resolves.toEqual({ success: true });

    expect(attach).toHaveBeenCalledTimes(2);
    expect(waits).toEqual([25]);
  });

  it('attaches daemon-known workspace runtimes before attempting to spawn them', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [],
        workspaces: [{
          session_id: 'sess-remote',
          active_pane_id: 'main',
          layout_json: '',
          panes: [{
            pane_id: 'pane-shell-1',
            kind: 'shell',
            runtime_id: 'runtime-shell-1',
            title: 'Shell 1',
          }],
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'runtime-shell-1',
        cwd: '/tmp/repo',
        endpoint_id: 'ep-remote',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'runtime-shell-1', attach_policy: 'relaunch_restore' });
    });

    const sent = ws.sent.map((entry) => JSON.parse(entry));
    expect(sent.find((entry) => entry.cmd === 'spawn_session' && entry.id === 'runtime-shell-1')).toBeUndefined();

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'runtime-shell-1',
        success: true,
        cols: 80,
        rows: 24,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();
    unmount();
  });

  it('claims visible geometry after attaching a daemon-known session with stale replay dimensions', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-existing',
        cwd: '/tmp/repo',
        agent: 'codex',
        cols: 58,
        rows: 46,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 80,
        rows: 24,
        screen_cols: 80,
        screen_rows: 24,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 46 });
    });

    unmount();
  });

  it('hydrates a remounted runtime by resizing before re-attaching', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-existing',
        cols: 58,
        rows: 46,
        shell: false,
        reason: 'remount_attach',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 46 });
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'same_app_remount' });
    });

    const sent = ws.sent.map((entry) => JSON.parse(entry));
    const resizeIndex = sent.findIndex((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
    const attachIndex = sent.findIndex((entry) => entry.cmd === 'attach_session' && entry.id === 'sess-existing');
    expect(resizeIndex).toBeGreaterThanOrEqual(0);
    expect(attachIndex).toBeGreaterThan(resizeIndex);

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 58,
        rows: 46,
        screen_cols: 58,
        screen_rows: 46,
        running: true,
      });
    });

    await expect(attachPromise).resolves.toBeUndefined();
    unmount();
  });

  it('skips stale replay and only requests authoritative geometry when remounted attach geometry mismatches', async () => {
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS = [];
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-existing',
        cols: 58,
        rows: 46,
        shell: false,
        reason: 'remount_attach',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 46 });
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'same_app_remount' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 37,
        rows: 46,
        screen_cols: 37,
        screen_rows: 46,
        screen_snapshot: 'c3RhbGU=',
        running: true,
      });
    });

    await expect(attachPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'sess-existing' && event.data === 'c3RhbGU=')).toBe(false);
    expect(ptyEvents.some((event) => event.event === 'reset' && event.id === 'sess-existing')).toBe(false);

    await waitFor(() => {
      const resizes = ws.sent
        .map((entry) => JSON.parse(entry))
        .filter((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
      expect(resizes.filter((entry) => entry.cols === 58 && entry.rows === 46).length).toBeGreaterThanOrEqual(2);
      expect(resizes).not.toContainEqual({ cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 45 });
    });

    unmount();
  });

  it('skips matching replay and does not request extra PTY work when remounted geometry already matches', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS = [];
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const attachPromise = ptyAttach({
      args: {
        id: 'sess-existing',
        cols: 58,
        rows: 46,
        shell: false,
        reason: 'remount_attach',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'same_app_remount' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 58,
        rows: 46,
        screen_cols: 58,
        screen_rows: 46,
        screen_snapshot: 'bWF0Y2g=',
        running: true,
      });
    });

    await expect(attachPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'sess-existing' && event.data === 'bWF0Y2g=')).toBe(false);
    expect(ptyEvents.some((event) => event.event === 'reset' && event.id === 'sess-existing')).toBe(false);

    await waitFor(() => {
      const resizes = ws.sent
        .map((entry) => JSON.parse(entry))
        .filter((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
      expect(resizes).toEqual([
        { cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 46 },
      ]);
    });

    unmount();
  });

  it('prefers raw scrollback over screen snapshot when relaunching a daemon-known Codex session', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS = [];
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-existing',
        cwd: '/tmp/repo',
        agent: 'codex',
        cols: 58,
        rows: 46,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 58,
        rows: 46,
        scrollback: 'cmF3LXdpbnM=',
        screen_cols: 58,
        screen_rows: 46,
        screen_snapshot: 'bWF0Y2g=',
        screen_snapshot_fresh: true,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string; source?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({ event: 'reset', id: 'sess-existing', reason: 'reattach' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'cmF3LXdpbnM=', source: 'attach_replay' });
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'sess-existing' && event.data === 'bWF0Y2g=')).toBe(false);

    const resizes = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
    expect(resizes).toEqual([]);

    unmount();
  });

  it('replays the fresh screen snapshot when relaunching a daemon-known Codex session without raw scrollback', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string; source?: string; reason?: string }> }).__TEST_PTY_EVENTS = [];
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-existing',
        cwd: '/tmp/repo',
        agent: 'codex',
        cols: 58,
        rows: 46,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 58,
        rows: 46,
        screen_cols: 58,
        screen_rows: 46,
        screen_snapshot: 'c25hcHNob3Qtd2lucw==',
        screen_snapshot_fresh: true,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string; source?: string; reason?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({ event: 'reset', id: 'sess-existing', reason: 'snapshot_restore' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'c25hcHNob3Qtd2lucw==', source: 'attach_replay' });
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'sess-existing' && event.data === 'cmF3LXdpbnM=')).toBe(false);

    unmount();
  });

  it('keeps screen snapshot replay when relaunching a daemon-known Claude session', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS = [];
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'claude',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-existing',
        cwd: '/tmp/repo',
        agent: 'claude',
        cols: 58,
        rows: 46,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 58,
        rows: 46,
        scrollback: 'Y2xhdWRlLXJhdy1mYWxsYmFjaw==',
        screen_cols: 58,
        screen_rows: 46,
        screen_snapshot: 'Y2xhdWRlLXNuYXBzaG90',
        screen_snapshot_fresh: true,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string; source?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({ event: 'reset', id: 'sess-existing', reason: 'snapshot_restore' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'Y2xhdWRlLXNuYXBzaG90', source: 'attach_replay' });
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'sess-existing' && event.data === 'Y2xhdWRlLXJhdy1mYWxsYmFjaw==')).toBe(false);

    const resizes = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
    expect(resizes).toEqual([]);

    unmount();
  });

  it('keeps screen snapshot replay for daemon-known shell relaunches', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS = [];
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [],
        workspaces: [{
          session_id: 'sess-remote',
          active_pane_id: 'main',
          layout_json: '',
          panes: [{
            pane_id: 'pane-shell-1',
            kind: 'shell',
            runtime_id: 'runtime-shell-1',
            title: 'Shell 1',
          }],
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'runtime-shell-1',
        cwd: '/tmp/repo',
        endpoint_id: 'ep-remote',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'runtime-shell-1', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'runtime-shell-1',
        success: true,
        cols: 80,
        rows: 24,
        scrollback: 'c2hlbGwtcmF3LWZhbGxiYWNr',
        screen_cols: 80,
        screen_rows: 24,
        screen_snapshot: 'c2hlbGwtc25hcHNob3Q=',
        screen_snapshot_fresh: true,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string; source?: string; reason?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({ event: 'reset', id: 'runtime-shell-1', reason: 'snapshot_restore' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'runtime-shell-1', data: 'c2hlbGwtc25hcHNob3Q=', source: 'attach_replay' });
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'runtime-shell-1' && event.data === 'c2hlbGwtcmF3LWZhbGxiYWNr')).toBe(false);

    const resizes = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'pty_resize' && entry.id === 'runtime-shell-1');
    expect(resizes).toEqual([]);

    unmount();
  });

  it('applies raw scrollback replay when attaching a daemon-known non-shell session', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string }> }).__TEST_PTY_EVENTS = [];
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          state: 'idle',
          state_since: '2026-04-08T00:00:00Z',
          state_updated_at: '2026-04-08T00:00:00Z',
          last_seen: '2026-04-08T00:00:00Z',
        }],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-existing',
        cwd: '/tmp/repo',
        agent: 'codex',
        cols: 58,
        rows: 46,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-existing', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 58,
        rows: 46,
        scrollback: 'cmF3LXJlcGxheQ==',
        scrollback_truncated: true,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; data?: string; source?: string }> }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({ event: 'reset', id: 'sess-existing', reason: 'reattach' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'cmF3LXJlcGxheQ==', source: 'attach_replay' });

    const resizes = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'pty_resize' && entry.id === 'sess-existing');
    expect(resizes).toEqual([]);

    unmount();
  });

  it('re-spawns remote workspace runtimes on the correct endpoint after attach failure', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate,
        onReposUpdate,
        onAuthorsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [],
        workspaces: [{
          session_id: 'sess-remote',
          active_pane_id: 'main',
          layout_json: '',
          panes: [{
            pane_id: 'pane-shell-1',
            kind: 'shell',
            runtime_id: 'runtime-shell-1',
            title: 'Shell 1',
          }],
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    const spawnPromise = ptySpawn({
      args: {
        id: 'runtime-shell-1',
        cwd: '/tmp/repo',
        endpoint_id: 'ep-remote',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'runtime-shell-1', attach_policy: 'relaunch_restore' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'runtime-shell-1',
        success: false,
        error: 'session not found',
      });
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'spawn_session',
        id: 'runtime-shell-1',
        cwd: '/tmp/repo',
        endpoint_id: 'ep-remote',
        agent: 'shell',
        cols: 80,
        rows: 24,
      });
    });

    act(() => {
      ws.emit({
        event: 'spawn_result',
        id: 'runtime-shell-1',
        success: true,
      });
    });

    await waitFor(() => {
      const attachCommands = ws.sent
        .map((entry) => JSON.parse(entry))
        .filter((entry) => entry.cmd === 'attach_session' && entry.id === 'runtime-shell-1');
      expect(attachCommands).toHaveLength(2);
      expect(attachCommands).toContainEqual({ cmd: 'attach_session', id: 'runtime-shell-1', attach_policy: 'relaunch_restore' });
      expect(attachCommands).toContainEqual({ cmd: 'attach_session', id: 'runtime-shell-1', attach_policy: 'fresh_spawn' });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'runtime-shell-1',
        success: true,
        cols: 80,
        rows: 24,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();
    unmount();
  });

  it('prunes stale workspaces when sessions_updated removes a session', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-stale',
          label: 'stale',
          directory: '/tmp/repo',
          state: 'working',
          last_seen: '2026-04-09T00:00:00Z',
        }],
        workspaces: [{
          session_id: 'sess-stale',
          active_pane_id: 'main',
          layout_json: '',
          panes: [{
            pane_id: 'main',
            kind: 'main',
            title: 'Session',
          }],
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    act(() => {
      ws.emit({
        event: 'sessions_updated',
        sessions: [],
      });
    });

    await waitFor(() => {
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([]);
    });

    unmount();
  });

  it('ignores late workspace updates for sessions that were already removed', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate,
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: '50',
        sessions: [{
          id: 'sess-removed',
          label: 'removed',
          directory: '/tmp/repo',
          state: 'working',
          last_seen: '2026-04-09T00:00:00Z',
        }],
        workspaces: [{
          session_id: 'sess-removed',
          active_pane_id: 'main',
          layout_json: '',
          panes: [{
            pane_id: 'main',
            kind: 'main',
            title: 'Session',
          }],
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    act(() => {
      ws.emit({
        event: 'session_unregistered',
        session: {
          id: 'sess-removed',
          label: 'removed',
          directory: '/tmp/repo',
          state: 'working',
        },
      });
    });

    await waitFor(() => {
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([]);
    });

    act(() => {
      ws.emit({
        event: 'workspace_updated',
        workspace: {
          session_id: 'sess-removed',
          active_pane_id: 'main',
          layout_json: '',
          panes: [{
            pane_id: 'main',
            kind: 'main',
            title: 'Ghost',
          }],
        },
      });
    });

    await waitFor(() => {
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([]);
    });

    unmount();
  });

});
