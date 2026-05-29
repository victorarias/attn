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

  it('stores daemon git operation lifecycle events by operation id', async () => {
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
        event: 'git_operation_started',
        operation: {
          id: 'op-1',
          kind: 'delete_worktree',
          status: 'running',
          path: '/tmp/repo/.worktrees/feature-a',
          started_at: '2026-05-16T16:00:00Z',
        },
      });
    });

    await waitFor(() => {
      expect(result.current.gitOperations['op-1']).toMatchObject({
        kind: 'delete_worktree',
        status: 'running',
        path: '/tmp/repo/.worktrees/feature-a',
      });
    });

    act(() => {
      ws.emit({
        event: 'git_operation_finished',
        operation: {
          id: 'op-1',
          kind: 'delete_worktree',
          status: 'succeeded',
          path: '/tmp/repo/.worktrees/feature-a',
          started_at: '2026-05-16T16:00:00Z',
          finished_at: '2026-05-16T16:00:05Z',
          duration_ms: 5000,
        },
      });
    });

    await waitFor(() => {
      expect(result.current.gitOperations['op-1']).toMatchObject({
        status: 'succeeded',
        duration_ms: 5000,
      });
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

  it('re-runs daemon ensure automatically on protocol mismatch in Tauri', async () => {
    vi.mocked(invoke).mockImplementation(async (cmd) => {
      if (cmd === 'ensure_daemon') {
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
      expect(vi.mocked(invoke)).toHaveBeenCalledWith('ensure_daemon');
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

  it('resolves plugin list and priority actions from daemon events', async () => {
    const onPluginsUpdate = vi.fn();
    const { result, unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onPluginsUpdate,
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();
    const listPlugins = result.current.sendListPlugins();

    act(() => {
      ws.emit({
        event: 'plugins_updated',
        plugins: [{
          name: 'services-pilot-worktrees',
          version: '0.1.0',
          dir: '/tmp/services-pilot-worktrees',
          priority: 25,
          connected: true,
          running: true,
        }],
      });
    });

    await expect(listPlugins).resolves.toEqual({
      plugins: [{
        name: 'services-pilot-worktrees',
        version: '0.1.0',
        dir: '/tmp/services-pilot-worktrees',
        priority: 25,
        connected: true,
        running: true,
      }],
      issues: [],
    });
    expect(onPluginsUpdate).toHaveBeenCalledWith([{
      name: 'services-pilot-worktrees',
      version: '0.1.0',
      dir: '/tmp/services-pilot-worktrees',
      priority: 25,
      connected: true,
      running: true,
    }], []);

    const setPriority = result.current.sendSetPluginPriority('services-pilot-worktrees', 50);
    act(() => {
      ws.emit({
        event: 'plugin_action_result',
        action: 'set_priority',
        name: 'services-pilot-worktrees',
        success: true,
      });
    });

    await expect(setPriority).resolves.toMatchObject({
      success: true,
      name: 'services-pilot-worktrees',
    });
    unmount();
  });

  it('reports daemon-discovered GitHub hosts from snapshots and refresh events', async () => {
    const onGitHubHostsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onGitHubHostsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();
    act(() => {
      ws.emit({
        event: 'initial_state',
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        github_hosts: ['github.com'],
        settings: {},
      });
      ws.emit({
        event: 'github_hosts_updated',
        github_hosts: ['ghe.example.test', 'github.com'],
      });
    });

    expect(onGitHubHostsUpdate).toHaveBeenNthCalledWith(1, ['github.com']);
    expect(onGitHubHostsUpdate).toHaveBeenNthCalledWith(2, ['ghe.example.test', 'github.com']);
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
    (window as Window & { __TEST_PTY_EVENTS?: Array<{ event: string; id: string; cols?: number; rows?: number; source?: string }> }).__TEST_PTY_EVENTS = [];
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
        protocol_version: '65',
        sessions: [],
        workspaces: [{
          id: 'workspace-sess-remote',
          title: 'Remote',
          directory: '/tmp/repo',
          status: 'idle',
          layout: {
            workspace_id: 'workspace-sess-remote',
            active_pane_id: 'main',
            layout_json: '',
            panes: [{
              pane_id: 'pane-shell-1',
              kind: 'shell',
              runtime_id: 'runtime-shell-1',
              title: 'Shell 1',
            }],
          },
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

  it('preserves daemon geometry while relaunch restores before workspace layout settles', async () => {
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
        protocol_version: '65',
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

    const sent = ws.sent.map((entry) => JSON.parse(entry));
    expect(sent).not.toContainEqual({ cmd: 'pty_resize', id: 'sess-existing', cols: 58, rows: 46 });
    expect((window as Window & { __TEST_PTY_EVENTS?: unknown[] }).__TEST_PTY_EVENTS).toContainEqual({
      event: 'local_resize',
      id: 'sess-existing',
      cols: 80,
      rows: 24,
      source: 'attach_replay',
    });

    unmount();
  });

  it('includes the owning workspace when spawning a new agent session', async () => {
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

    const ws = await waitForOpenSocket();
    const spawnPromise = ptySpawn({
      args: {
        id: 'sess-new',
        cwd: '/tmp/repo',
        workspace_id: 'workspace-sess-new',
        agent: 'claude',
        cols: 80,
        rows: 24,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'spawn_session',
        id: 'sess-new',
        cwd: '/tmp/repo',
        workspace_id: 'workspace-sess-new',
        agent: 'claude',
        cols: 80,
        rows: 24,
      });
    });

    act(() => {
      ws.emit({ event: 'spawn_result', id: 'sess-new', success: true });
    });
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'sess-new', attach_policy: 'fresh_spawn' });
    });
    act(() => {
      ws.emit({ event: 'attach_result', id: 'sess-new', success: true, cols: 80, rows: 24, running: true });
    });
    await expect(spawnPromise).resolves.toBeUndefined();
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
        protocol_version: '65',
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

  it('replays geometry-aware segmented raw history for relaunching a daemon-known Codex session', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    (window as Window & {
      __TEST_PTY_EVENTS?: Array<{
        event: string;
        id: string;
        data?: string;
        cols?: number;
        rows?: number;
        source?: string;
        reason?: string;
      }>;
    }).__TEST_PTY_EVENTS = [];
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
        protocol_version: '65',
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
        replay_segments: [
          { cols: 118, rows: 48, data: 'd2lkZS1oaXN0b3J5' },
          { cols: 58, rows: 46, data: 'bmFycm93LXRhaWw=' },
        ],
        screen_cols: 58,
        screen_rows: 46,
        running: true,
      });
    });

    await expect(spawnPromise).resolves.toBeUndefined();

    const ptyEvents = (window as Window & {
      __TEST_PTY_EVENTS?: Array<{
        event: string;
        id: string;
        data?: string;
        cols?: number;
        rows?: number;
        source?: string;
        reason?: string;
      }>;
    }).__TEST_PTY_EVENTS || [];
    expect(ptyEvents).toContainEqual({ event: 'reset', id: 'sess-existing', reason: 'reattach' });
    expect(ptyEvents).toContainEqual({ event: 'local_resize', id: 'sess-existing', cols: 118, rows: 48, source: 'attach_replay' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'd2lkZS1oaXN0b3J5', source: 'attach_replay', suppressResponses: true });
    expect(ptyEvents).toContainEqual({ event: 'local_resize', id: 'sess-existing', cols: 58, rows: 46, source: 'attach_replay' });
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'bmFycm93LXRhaWw=', source: 'attach_replay', suppressResponses: true });
    expect(ptyEvents.some((event) => event.event === 'data' && event.id === 'sess-existing' && event.data === 'c25hcHNob3Qtd2lucw==')).toBe(false);

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
        protocol_version: '65',
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
    expect(ptyEvents).toContainEqual({ event: 'data', id: 'sess-existing', data: 'cmF3LXJlcGxheQ==', source: 'attach_replay', suppressResponses: true });

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
        protocol_version: '65',
        sessions: [],
        workspaces: [{
          id: 'workspace-sess-remote',
          title: 'Remote',
          directory: '/tmp/repo',
          status: 'idle',
          layout: {
            workspace_id: 'workspace-sess-remote',
            active_pane_id: 'main',
            layout_json: '',
            panes: [{
              pane_id: 'pane-shell-1',
              kind: 'shell',
              runtime_id: 'runtime-shell-1',
              title: 'Shell 1',
            }],
          },
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

  it('retains workspaces when one agent session disappears', async () => {
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
        protocol_version: '65',
        sessions: [{
          id: 'sess-stale',
          label: 'stale',
          directory: '/tmp/repo',
          state: 'working',
          last_seen: '2026-04-09T00:00:00Z',
        }],
        workspaces: [{
          id: 'workspace-sess-stale',
          title: 'stale',
          directory: '/tmp/repo',
          status: 'working',
          layout: {
            workspace_id: 'workspace-sess-stale',
            active_pane_id: 'main',
            layout_json: '',
            panes: [{
              pane_id: 'main',
              kind: 'agent',
              title: 'Agent',
              session_id: 'sess-stale',
            }],
          },
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
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([
        expect.objectContaining({ id: 'workspace-sess-stale' }),
      ]);
    });

    unmount();
  });

  it('removes a workspace only after workspace_unregistered', async () => {
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
        protocol_version: '65',
        sessions: [{
          id: 'sess-removed',
          label: 'removed',
          directory: '/tmp/repo',
          state: 'working',
          last_seen: '2026-04-09T00:00:00Z',
        }],
        workspaces: [{
          id: 'workspace-sess-removed',
          title: 'removed',
          directory: '/tmp/repo',
          status: 'working',
          layout: {
            workspace_id: 'workspace-sess-removed',
            active_pane_id: 'main',
            layout_json: '',
            panes: [{
              pane_id: 'main',
              kind: 'agent',
              title: 'Agent',
              session_id: 'sess-removed',
            }],
          },
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
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([
        expect.objectContaining({ id: 'workspace-sess-removed' }),
      ]);
    });

    act(() => {
      ws.emit({
        event: 'workspace_unregistered',
        workspace: {
          id: 'workspace-sess-removed',
          title: 'removed',
          directory: '/tmp/repo',
          status: 'idle',
        },
      });
    });

    await waitFor(() => {
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([]);
    });

    unmount();
  });

});
