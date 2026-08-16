import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { invoke, isTauri } from '@tauri-apps/api/core';
import { ptyAttach, ptyDetach, ptyKill, ptyReload, ptySpawn } from '../pty/bridge';
import { LOCAL_SNAPSHOT_FORMAT } from '../pty/attachPlanning';
import { AutomationActionTimeoutError, PROTOCOL_VERSION, retryTransientAttachRequest, useDaemonSocket } from './useDaemonSocket';
import { useWorkflowRunsStore } from '../store/workflowRuns';
import { useAutomationsStore } from '../store/automations';
import { TicketStatus } from '../types/generated';

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

  it('resolves or rejects ptyReload from its daemon result', async () => {
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
    const successfulReload = ptyReload({ id: 'reload-success', cols: 120, rows: 40 });
    expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
      cmd: 'reload_session',
      id: 'reload-success',
      cols: 120,
      rows: 40,
    });
    act(() => {
      ws.emit({ event: 'reload_session_result', id: 'reload-success', success: true });
    });
    await expect(successfulReload).resolves.toBeUndefined();

    const failedReload = ptyReload({ id: 'reload-failure', cols: 80, rows: 24 });
    act(() => {
      ws.emit({ event: 'reload_session_result', id: 'reload-failure', success: false, error: 'reload denied' });
    });
    await expect(failedReload).rejects.toThrow('reload denied');

    unmount();
  });

  it('forwards session_exited to onSessionExited with exit code and signal', async () => {
    const onSessionExited = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onSessionExited,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();

    // Clean voluntary exit: no signal.
    ws.emit({ event: 'session_exited', id: 'clean-exit', exit_code: 0 });
    expect(onSessionExited).toHaveBeenCalledWith({ id: 'clean-exit', exitCode: 0, signal: undefined });

    // Signal kill (e.g. reload/close): signal is forwarded so consumers can skip auto-close.
    ws.emit({ event: 'session_exited', id: 'killed', exit_code: -1, signal: 'SIGTERM' });
    expect(onSessionExited).toHaveBeenCalledWith({ id: 'killed', exitCode: -1, signal: 'SIGTERM' });

    unmount();
  });

  it('reattaches with relaunch_restore on runtime_respawned without treating it as an exit', async () => {
    const onSessionExited = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onSessionExited,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();
    ws.sent = [];

    // The daemon replaced this session's agent in place (chief assign/demote reload).
    ws.emit({ event: 'runtime_respawned', id: 'reload-sess' });

    const sent = ws.sent.map((entry) => JSON.parse(entry));
    // Re-attach with explicit relaunch_restore so the fresh worker's scrollback replays.
    expect(sent).toContainEqual({ cmd: 'attach_session', id: 'reload-sess', attach_policy: 'relaunch_restore' });
    // A reload is a runtime replacement, not a close: the session must not be torn down.
    expect(onSessionExited).not.toHaveBeenCalled();

    unmount();
  });

  it('keeps the socket connected across callback rerenders and uses the latest callback', async () => {
    const firstSessionsUpdate = vi.fn();
    const latestSessionsUpdate = vi.fn();
    const { rerender, unmount } = renderHook(
      ({ onSessionsUpdate }) => useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
      { initialProps: { onSessionsUpdate: firstSessionsUpdate } },
    );

    const ws = await waitForOpenSocket();
    rerender({ onSessionsUpdate: latestSessionsUpdate });

    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    expect(FakeWebSocket.instances).toHaveLength(1);

    act(() => {
      ws.emit({ event: 'sessions_updated', sessions: [] });
    });
    expect(firstSessionsUpdate).not.toHaveBeenCalled();
    expect(latestSessionsUpdate).toHaveBeenCalledWith([]);

    unmount();
  });

  it('advertises and handles in-app browser control', async () => {
    vi.mocked(invoke).mockResolvedValue('{"title":"Fixture"}');
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
    await waitFor(() => {
      const hello = ws.sent.map((entry) => JSON.parse(entry)).find((entry) => entry.cmd === 'client_hello');
      expect(hello?.capabilities).toContain('browser_host');
      expect(hello?.browser_host_token).toBe('{"title":"Fixture"}');
    });

    act(() => {
      ws.emit({
        event: 'browser_control_request',
        request_id: 'browser-request-1',
        workspace_id: 'workspace-1',
        tile_id: 'tile-browser',
        action: 'type',
        selector: '#query',
        text: 'browser text',
      });
    });

    await waitFor(() => {
      expect(vi.mocked(invoke)).toHaveBeenCalledWith('browser_host_control', {
        label: 'browser-workspace-1-tile-browser',
        action: 'type',
        selector: '#query',
        text: 'browser text',
      });
      const result = ws.sent.map((entry) => JSON.parse(entry)).find((entry) => entry.cmd === 'browser_control_result');
      expect(result).toEqual({
        cmd: 'browser_control_result',
        request_id: 'browser-request-1',
        success: true,
        data: '{"title":"Fixture"}',
      });
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

  it('sends force option for delete worktree requests', async () => {
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

    const promise = result.current.sendDeleteWorktree('/tmp/repo--feature', undefined, { force: true });

    await waitFor(() => {
      expect(JSON.parse(ws.sent[ws.sent.length - 1])).toMatchObject({
        cmd: 'delete_worktree',
        path: '/tmp/repo--feature',
        force: true,
      });
    });

    act(() => {
      ws.emit({
        event: 'delete_worktree_result',
        path: '/tmp/repo--feature',
        success: true,
      });
    });

    await expect(promise).resolves.toMatchObject({ success: true });

    unmount();
  });

  it('preserves forceable delete worktree failure details on rejection', async () => {
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
    const promise = result.current.sendDeleteWorktree('/tmp/repo--feature');

    act(() => {
      ws.emit({
        event: 'delete_worktree_result',
        path: '/tmp/repo--feature',
        success: false,
        error: 'contains modified or untracked files',
        forceable: true,
        reason_kind: 'dirty_worktree',
      });
    });

    await expect(promise).rejects.toMatchObject({
      message: 'contains modified or untracked files',
      forceable: true,
      reason_kind: 'dirty_worktree',
    });

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

    const install = result.current.sendInstallPlugin('git@ghe.spotify.net:victora/attn-snipe.git');
    expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
      cmd: 'install_plugin',
      source: 'git@ghe.spotify.net:victora/attn-snipe.git',
    });
    act(() => {
      ws.emit({
        event: 'plugin_action_result',
        action: 'install',
        name: 'attn-snipe',
        success: true,
      });
    });
    await expect(install).resolves.toMatchObject({ success: true, name: 'attn-snipe' });

    const installBundled = result.current.sendInstallBundledPlugin('attn-example');
    expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
      cmd: 'install_bundled_plugin',
      name: 'attn-example',
    });
    act(() => {
      ws.emit({ event: 'plugin_action_result', action: 'install_bundled', name: 'attn-example', success: true });
    });
    await expect(installBundled).resolves.toMatchObject({ success: true, name: 'attn-example' });

    const uninstall = result.current.sendUninstallPlugin('attn-example');
    expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
      cmd: 'uninstall_plugin',
      name: 'attn-example',
    });
    act(() => {
      ws.emit({ event: 'plugin_action_result', action: 'uninstall', name: 'attn-example', success: true });
    });
    await expect(uninstall).resolves.toMatchObject({ success: true, name: 'attn-example' });

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

  it('lists canonical workspace contexts by request id', async () => {
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
    const request = result.current.sendListWorkspaceContexts();
    const command = ws.sent
      .map((entry) => JSON.parse(entry))
      .find((entry) => entry.cmd === 'workspace_context_list');
    expect(command.request_id).toMatch(/^workspace_context_list:/);

    act(() => {
      ws.emit({
        event: 'workspace_context_list_result',
        request_id: command.request_id,
        success: true,
        contexts: [{
          workspace_id: 'workspace-1',
          content: '# Goal',
          revision: 2,
          updated_by_session_id: 'session-1',
          updated_at: '2026-06-09T10:00:00Z',
        }],
      });
    });

    await expect(request).resolves.toEqual([{
      workspace_id: 'workspace-1',
      content: '# Goal',
      revision: 2,
      updated_by_session_id: 'session-1',
      updated_at: '2026-06-09T10:00:00Z',
    }]);
    unmount();
  });

  it('resolves automation definitions from a correlated automation_definitions_result, ignoring a mismatched request_id', async () => {
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
    const request = result.current.listAutomationDefinitions();
    const command = ws.sent
      .map((entry) => JSON.parse(entry))
      .find((entry) => entry.cmd === 'automation_definitions_get');
    expect(command.request_id).toMatch(/^automation_definitions_get:/);

    let resolved = false;
    request.then(() => {
      resolved = true;
    });

    act(() => {
      ws.emit({
        event: 'automation_definitions_result',
        request_id: 'mismatched-request-id',
        success: true,
        definitions: [{ id: 'wrong' }],
      });
    });
    await Promise.resolve();
    expect(resolved).toBe(false);

    const definition = {
      id: 'd1',
      name: 'PR reviewer',
      enabled: true,
      revision: 1,
      trigger_type: 'manual',
      updated_at: '2026-01-01T00:00:00Z',
    };
    act(() => {
      ws.emit({
        event: 'automation_definitions_result',
        request_id: command.request_id,
        success: true,
        definitions: [definition],
      });
    });

    await expect(request).resolves.toEqual([definition]);
    unmount();
  });

  it('rejects setAutomationEnabled with the daemon error string on failure', async () => {
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
    const request = result.current.setAutomationEnabled('d1', false);
    const command = ws.sent
      .map((entry) => JSON.parse(entry))
      .find((entry) => entry.cmd === 'automation_set_enabled');
    expect(command).toMatchObject({ definition_id: 'd1', enabled: false });

    act(() => {
      ws.emit({
        event: 'automation_set_enabled_result',
        request_id: command.request_id,
        success: false,
        error: 'automation definition is disabled elsewhere',
      });
    });

    await expect(request).rejects.toThrow('automation definition is disabled elsewhere');
    unmount();
  });

  it('resolves runAutomationNow with the run summary', async () => {
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
    const request = result.current.runAutomationNow('d1', 'req-1');
    const command = ws.sent
      .map((entry) => JSON.parse(entry))
      .find((entry) => entry.cmd === 'automation_run');
    expect(command).toMatchObject({ definition_id: 'd1', request_id: 'req-1' });

    const run = {
      id: 'run-1',
      definition_id: 'd1',
      state: 'delivered',
      ticket_id: 'ticket-1',
      session_id: 'session-1',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    };
    act(() => {
      ws.emit({
        event: 'automation_run_result',
        request_id: command.request_id,
        success: true,
        run,
      });
    });

    await expect(request).resolves.toEqual(run);
    unmount();
  });

  it('rejects runAutomationNow with AutomationActionTimeoutError when no result arrives within 30s', async () => {
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

    await waitForOpenSocket();

    vi.useFakeTimers();
    let caught: unknown;
    const request = result.current.runAutomationNow('d1', 'req-timeout').catch((err) => {
      caught = err;
    });

    await vi.advanceTimersByTimeAsync(30000);
    await request;
    vi.useRealTimers();

    expect(caught).toBeInstanceOf(AutomationActionTimeoutError);
    unmount();
  });

  it('bumps useAutomationsStore changedTick on automations_changed', async () => {
    useAutomationsStore.getState().reset();
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
    const before = useAutomationsStore.getState().changedTick;

    act(() => {
      ws.emit({ event: 'automations_changed', definition_ids: ['d1'] });
    });

    expect(useAutomationsStore.getState().changedTick).toBe(before + 1);
    unmount();
  });

  it('keeps shell sessions in daemon session updates', async () => {
    const onSessionsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [
          {
            id: 'agent-1',
            label: 'Agent',
            agent: 'codex',
            directory: '/tmp/repo',
            workspace_id: 'workspace-1',
            state: 'working',
          },
          {
            id: 'shell-1',
            label: 'Shell',
            agent: 'shell',
            directory: '/tmp/repo',
            workspace_id: 'workspace-1',
            state: 'working',
          },
        ],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    expect(onSessionsUpdate).toHaveBeenLastCalledWith([
      expect.objectContaining({ id: 'agent-1', agent: 'codex' }),
      expect.objectContaining({ id: 'shell-1', agent: 'shell' }),
    ]);

    act(() => {
      ws.emit({
        event: 'session_registered',
        session: {
          id: 'shell-2',
          label: 'Shell 2',
          agent: 'shell',
          directory: '/tmp/repo',
          workspace_id: 'workspace-1',
          state: 'working',
        },
      });
    });

    expect(onSessionsUpdate).toHaveBeenLastCalledWith([
      expect.objectContaining({ id: 'agent-1', agent: 'codex' }),
      expect.objectContaining({ id: 'shell-1', agent: 'shell' }),
      expect.objectContaining({ id: 'shell-2', agent: 'shell' }),
    ]);

    unmount();
  });

  it('preserves workspace layout when a state update omits layout payload', async () => {
    const onWorkspacesUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate,
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();
    const layout = {
      workspace_id: 'workspace-1',
      active_pane_id: 'pane-1',
      layout_json: '{"type":"pane","pane_id":"pane-1"}',
      panes: [{
        workspace_id: 'workspace-1',
        pane_id: 'pane-1',
        kind: 'agent',
        runtime_id: 'session-1',
        session_id: 'session-1',
        title: 'Session 1',
      }],
    };

    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: PROTOCOL_VERSION,
        sessions: [],
        workspaces: [{
          id: 'workspace-1',
          title: 'Workspace',
          directory: '/tmp/repo',
          status: 'idle',
          muted: false,
          layout,
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    act(() => {
      ws.emit({
        event: 'workspace_state_changed',
        workspace: {
          id: 'workspace-1',
          title: 'Workspace',
          directory: '/tmp/repo',
          status: 'working',
          muted: false,
        },
      });
    });

    expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([
      expect.objectContaining({
        id: 'workspace-1',
        status: 'working',
        layout,
      }),
    ]);

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

  it('spawns daemon-known workspace runtimes before freshly attaching them', async () => {
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [],
        workspaces: [{
          id: 'workspace-sess-remote',
          title: 'Remote',
          directory: '/tmp/repo',
          status: 'idle',
          muted: false,
          layout: {
            workspace_id: 'workspace-sess-remote',
            active_pane_id: 'pane-session',
            layout_json: '',
            panes: [{
              workspace_id: 'workspace-sess-remote',
              pane_id: 'pane-shell-1',
              kind: 'agent',
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
        workspace_id: 'workspace-sess-remote',
        endpoint_id: 'ep-remote',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual(expect.objectContaining({
        cmd: 'spawn_session',
        id: 'runtime-shell-1',
      }));
    });

    act(() => {
      ws.emit({ event: 'spawn_result', id: 'runtime-shell-1', success: true });
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'attach_session', id: 'runtime-shell-1', attach_policy: 'fresh_spawn' });
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

  it('ignores an attach result after the runtime was detached', async () => {
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
    const initialState = {
      event: 'initial_state',
      protocol_version: PROTOCOL_VERSION,
      sessions: [{
        id: 'sess-canceled',
        label: 'Canceled',
        agent: 'codex',
        directory: '/tmp/repo',
        state: 'working',
      }],
      workspaces: [],
      prs: [],
      repos: [],
      authors: [],
      settings: {},
    };

    act(() => {
      ws.emit(initialState);
    });
    const attachPromise = ptyAttach({
      args: {
        id: 'sess-canceled',
        cols: 120,
        rows: 40,
        agent: 'codex',
        policy: 'same_app_remount',
      },
    });
    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
        cmd: 'attach_session',
        id: 'sess-canceled',
        attach_policy: 'same_app_remount',
      });
    });

    await ptyDetach({ id: 'sess-canceled' });
    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-canceled',
        success: true,
        cols: 120,
        rows: 40,
        running: true,
      });
    });
    await expect(attachPromise).rejects.toThrow('Attach session canceled');

    act(() => {
      ws.emit(initialState);
    });
    const attachCommands = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'attach_session' && entry.id === 'sess-canceled');
    expect(attachCommands).toHaveLength(1);
    unmount();
  });

  it('sends measured geometry only with a revive attach policy', async () => {
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
    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: PROTOCOL_VERSION,
        sessions: [{
          id: 'sess-recoverable',
          label: 'Recoverable',
          agent: 'claude',
          directory: '/tmp/repo',
          state: 'recoverable',
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
        id: 'sess-recoverable',
        cols: 113,
        rows: 37,
        agent: 'claude',
        policy: 'revive',
      },
    });
    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
        cmd: 'attach_session',
        id: 'sess-recoverable',
        attach_policy: 'revive',
        cols: 113,
        rows: 37,
      });
    });
    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-recoverable',
        success: true,
        cols: 113,
        rows: 37,
        running: true,
        revived: true,
      });
    });
    await expect(attachPromise).resolves.toBeUndefined();
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

  it('ignores workspace action results without workspace ownership', async () => {
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
    const closePromise = result.current.sendWorkspaceClosePane('workspace-1', 'pane-1');
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'workspace_layout_close_pane',
        workspace_id: 'workspace-1',
        pane_id: 'pane-1',
      });
    });

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_close_pane',
        pane_id: 'pane-1',
        success: true,
      });
    });

    const resultMarker = vi.fn();
    closePromise.then(resultMarker, resultMarker);
    await Promise.resolve();
    expect(resultMarker).not.toHaveBeenCalled();

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_close_pane',
        workspace_id: 'workspace-1',
        pane_id: 'pane-1',
        success: true,
      });
    });

    await expect(closePromise).resolves.toEqual({ success: true });
    unmount();
  });

  it('sends set_workspace_rank with neighbour ids and resolves on the action result', async () => {
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
    const rankPromise = result.current.sendSetWorkspaceRank('workspace-1', 'workspace-0', 'workspace-2');
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'set_workspace_rank',
        workspace_id: 'workspace-1',
        prev_workspace_id: 'workspace-0',
        next_workspace_id: 'workspace-2',
      });
    });

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'set_workspace_rank',
        workspace_id: 'workspace-1',
        success: true,
      });
    });

    await expect(rankPromise).resolves.toEqual({ success: true });
    unmount();
  });

  it('omits empty neighbour ids when moving a workspace to an edge', async () => {
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
    const rankPromise = result.current.sendSetWorkspaceRank('workspace-1', undefined, 'workspace-2');
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'set_workspace_rank',
        workspace_id: 'workspace-1',
        next_workspace_id: 'workspace-2',
      });
    });

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'set_workspace_rank',
        workspace_id: 'workspace-1',
        success: false,
        error: 'rank persist failed',
      });
    });

    await expect(rankPromise).rejects.toThrow('rank persist failed');
    unmount();
  });

  it('sends move_leaf_to_new_workspace and resolves on the leaf-keyed action result', async () => {
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
    const movePromise = result.current.sendWorkspaceMoveLeafToNewWorkspace('workspace-1', 'pane-7');
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'workspace_layout_move_leaf_to_new_workspace',
        source_workspace_id: 'workspace-1',
        leaf_id: 'pane-7',
        anchor_id: '',
        edge: 'left',
      });
    });

    // A result without the source workspace id (just the leaf) must not resolve it.
    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_move_leaf_to_new_workspace',
        leaf_id: 'pane-7',
        success: true,
      });
    });
    const marker = vi.fn();
    movePromise.then(marker, marker);
    await Promise.resolve();
    expect(marker).not.toHaveBeenCalled();

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_move_leaf_to_new_workspace',
        workspace_id: 'workspace-1',
        leaf_id: 'pane-7',
        success: true,
      });
    });

    await expect(movePromise).resolves.toEqual({ success: true, final_leaf_id: undefined });
    unmount();
  });

  it('correlates concurrent split resize results by split id', async () => {
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
    result.current.sendSessionSelected('session-selected');
    result.current.sendWorkspaceSelected('workspace-selected');

    const first = result.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-a', 0.3);
    const second = result.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-b', 0.7);
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'session_selected', id: 'session-selected' });
      expect(sent).toContainEqual({ cmd: 'workspace_selected', workspace_id: 'workspace-selected' });
      expect(sent).toContainEqual(expect.objectContaining({ cmd: 'workspace_layout_set_split_ratio', workspace_id: 'workspace-1', split_id: 'split-a', ratio: 0.3 }));
      expect(sent).toContainEqual(expect.objectContaining({ cmd: 'workspace_layout_set_split_ratio', workspace_id: 'workspace-1', split_id: 'split-b', ratio: 0.7 }));
    });
    const splitRequests = ws.sent.map((entry) => JSON.parse(entry)).filter((entry) => entry.cmd === 'workspace_layout_set_split_ratio');
    const splitARequest = splitRequests.find((entry) => entry.split_id === 'split-a');
    const splitBRequest = splitRequests.find((entry) => entry.split_id === 'split-b');

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_set_split_ratio',
        workspace_id: 'workspace-1',
        split_id: 'split-a',
        request_id: splitARequest.request_id,
        success: true,
      });
    });
    await expect(first).resolves.toEqual({ success: true });

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_set_split_ratio',
        workspace_id: 'workspace-1',
        split_id: 'split-b',
        request_id: splitBRequest.request_id,
        success: false,
        error: 'persist failed',
      });
    });
    await expect(second).rejects.toThrow('persist failed');
    unmount();
  });

  it('drops terminal pointer activity until the daemon is ready', async () => {
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

    result.current.sendTerminalPointerActivity('session-pointer');
    expect(ws.sent.map((entry) => JSON.parse(entry))).not.toContainEqual({
      cmd: 'terminal_pointer_activity',
      id: 'session-pointer',
    });

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
    expect(ws.sent.map((entry) => JSON.parse(entry))).not.toContainEqual({
      cmd: 'terminal_pointer_activity',
      id: 'session-pointer',
    });

    act(() => {
      result.current.sendTerminalPointerActivity('session-pointer');
    });
    expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
      cmd: 'terminal_pointer_activity',
      id: 'session-pointer',
    });

    unmount();
  });

  it('correlates overlapping resize results for the same split by request id', async () => {
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
    const first = result.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-a', 0.3);
    const second = result.current.sendWorkspaceSetSplitRatio('workspace-1', 'split-a', 0.7);
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.filter((entry) => entry.cmd === 'workspace_layout_set_split_ratio')).toHaveLength(2);
    });
    const [firstRequest, secondRequest] = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'workspace_layout_set_split_ratio');
    expect(firstRequest.request_id).not.toBe(secondRequest.request_id);

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_set_split_ratio',
        workspace_id: 'workspace-1',
        split_id: 'split-a',
        request_id: secondRequest.request_id,
        success: true,
      });
    });
    await expect(second).resolves.toEqual({ success: true });

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_set_split_ratio',
        workspace_id: 'workspace-1',
        split_id: 'split-a',
        request_id: firstRequest.request_id,
        success: false,
        error: 'first persist failed',
      });
    });
    await expect(first).rejects.toThrow('first persist failed');
    unmount();
  });

  it('correlates overlapping tile updates by request id', async () => {
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
    const first = result.current.sendWorkspaceUpdateTile('workspace-1', 'tile-browser', 'https://first.example');
    const second = result.current.sendWorkspaceUpdateTile('workspace-1', 'tile-browser', 'https://second.example');
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
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.filter((entry) => entry.cmd === 'workspace_layout_update_tile')).toHaveLength(2);
    });
    const [firstRequest, secondRequest] = ws.sent
      .map((entry) => JSON.parse(entry))
      .filter((entry) => entry.cmd === 'workspace_layout_update_tile');
    expect(firstRequest.request_id).not.toBe(secondRequest.request_id);

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_update_tile',
        workspace_id: 'workspace-1',
        tile_id: 'tile-browser',
        request_id: secondRequest.request_id,
        success: true,
      });
    });
    await expect(second).resolves.toEqual({ success: true });

    act(() => {
      ws.emit({
        event: 'workspace_layout_action_result',
        action: 'workspace_layout_update_tile',
        workspace_id: 'workspace-1',
        tile_id: 'tile-browser',
        request_id: firstRequest.request_id,
        success: false,
        error: 'first persist failed',
      });
    });
    await expect(first).rejects.toThrow('first persist failed');
    unmount();
  });

  it('prunes cached tile content when its layout leaf or workspace disappears', async () => {
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [],
        workspaces: [{
          id: 'workspace-1',
          title: 'one',
          directory: '/tmp/one',
          status: 'idle',
          muted: false,
          layout: {
            workspace_id: 'workspace-1',
            active_pane_id: 'pane-1',
            layout_json: '{"type":"split","split_id":"root","direction":"vertical","ratio":0.5,"children":[{"type":"pane","pane_id":"pane-1"},{"type":"tile","tile_id":"tile-md","tile_kind":"markdown"}]}',
            panes: [],
          },
        }],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
      ws.emit({
        event: 'workspace_tile_content',
        workspace_id: 'workspace-1',
        tile_id: 'tile-md',
        tile_kind: 'markdown',
        path: '/tmp/notes.md',
        content: '# Notes',
      });
    });
    await waitFor(() => {
      expect(result.current.tileContents['workspace-1::tile-md']?.content).toBe('# Notes');
    });

    act(() => {
      ws.emit({
        event: 'workspace_layout_updated',
        workspace_layout: {
          workspace_id: 'workspace-1',
          active_pane_id: 'pane-1',
          layout_json: '{"type":"pane","pane_id":"pane-1"}',
          panes: [],
        },
      });
    });
    await waitFor(() => {
      expect(result.current.tileContents).toEqual({});
    });

    act(() => {
      ws.emit({
        event: 'workspace_tile_content',
        workspace_id: 'workspace-1',
        tile_id: 'tile-md',
        tile_kind: 'markdown',
        path: '/tmp/notes.md',
        content: '# Notes',
      });
      ws.emit({
        event: 'workspace_unregistered',
        workspace: {
          id: 'workspace-1',
          title: 'one',
          directory: '/tmp/one',
          status: 'idle',
          muted: false,
        },
      });
    });
    await waitFor(() => {
      expect(result.current.tileContents).toEqual({});
    });
    unmount();
  });

  it('refetches persisted tile content after websocket reconnect', async () => {
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
    const workspace = {
      id: 'workspace-1',
      title: 'one',
      directory: '/tmp/one',
      status: 'idle',
      muted: false,
      layout: {
        workspace_id: 'workspace-1',
        active_pane_id: 'pane-1',
        layout_json: '{"type":"split","split_id":"root","direction":"vertical","ratio":0.5,"children":[{"type":"pane","pane_id":"pane-1"},{"type":"tile","tile_id":"tile-md","tile_kind":"markdown"}]}',
        panes: [],
      },
    };
    const initialState = {
      event: 'initial_state',
      protocol_version: PROTOCOL_VERSION,
      sessions: [],
      workspaces: [workspace],
      prs: [],
      repos: [],
      authors: [],
      settings: {},
    };
    const ws = await waitForOpenSocket();
    result.current.sendSessionSelected('session-selected');
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'session_selected', id: 'session-selected' });
    });
    act(() => {
      ws.emit(initialState);
      ws.emit({
        event: 'workspace_tile_content',
        workspace_id: 'workspace-1',
        tile_id: 'tile-md',
        tile_kind: 'markdown',
        path: '/tmp/notes.md',
        content: '# Before reconnect',
      });
    });
    await waitFor(() => {
      expect(result.current.tileContents['workspace-1::tile-md']?.content).toBe('# Before reconnect');
    });

    act(() => {
      ws.close();
    });
    await waitFor(() => {
      expect(FakeWebSocket.instances).toHaveLength(2);
    }, { timeout: 2000 });
    const reconnected = FakeWebSocket.instances[1];
    await waitFor(() => {
      expect(reconnected.readyState).toBe(FakeWebSocket.OPEN);
    });

    act(() => {
      reconnected.emit(initialState);
    });
    await waitFor(() => {
      const sent = reconnected.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({ cmd: 'session_selected', id: 'session-selected' });
      expect(sent).toContainEqual({
        cmd: 'workspace_tile_content_get',
        workspace_id: 'workspace-1',
        tile_id: 'tile-md',
      });
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [{
          id: 'sess-existing',
          label: 'attn',
          agent: 'codex',
          directory: '/tmp/repo',
          workspace_id: 'workspace-sess-existing',
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

  it('replays a fresh same-app snapshot when the mounted pane geometry differs', async () => {
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [{
          id: 'sess-existing',
          label: 'thunk',
          agent: 'codex',
          directory: '/tmp/repo',
          workspace_id: 'workspace-sess-existing',
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
        cols: 52,
        rows: 35,
        shell: false,
        agent: 'codex',
        policy: 'fresh_spawn',
      },
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent).toContainEqual({
        cmd: 'attach_session',
        id: 'sess-existing',
        attach_policy: 'same_app_remount',
      });
    });

    act(() => {
      ws.emit({
        event: 'pty_output',
        id: 'sess-existing',
        seq: 29377,
        data: btoa('live-after-snapshot'),
      });
    });

    act(() => {
      ws.emit({
        event: 'attach_result',
        id: 'sess-existing',
        success: true,
        cols: 56,
        rows: 35,
        snapshot: {
          cols: 56,
          rows: 35,
          snapshot_b64: btoa('fresh-daemon-frame'),
          format: LOCAL_SNAPSHOT_FORMAT,
        },
        last_seq: 29376,
        running: true,
      });
    });

    await expect(attachPromise).resolves.toBeUndefined();
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
    expect(ptyEvents).toContainEqual({
      event: 'local_resize',
      id: 'sess-existing',
      cols: 56,
      rows: 35,
      source: 'attach_restore',
    });
    expect(ptyEvents).toContainEqual({
      event: 'reset',
      id: 'sess-existing',
      reason: 'snapshot_restore',
    });
    expect(ptyEvents).toContainEqual({
      event: 'restore_snapshot',
      id: 'sess-existing',
      data: btoa('fresh-daemon-frame'),
    });
    expect(ptyEvents).toContainEqual({ event: 'restore_complete', id: 'sess-existing' });
    const snapshotIndex = ptyEvents.findIndex((event) => event.event === 'restore_snapshot');
    const queuedLiveIndex = ptyEvents.findIndex((event) => (
      event.event === 'data' && event.data === btoa('live-after-snapshot')
    ));
    const restoreCompleteIndex = ptyEvents.findIndex((event) => event.event === 'restore_complete');
    expect(snapshotIndex).toBeGreaterThanOrEqual(0);
    expect(queuedLiveIndex).toBeGreaterThan(snapshotIndex);
    expect(restoreCompleteIndex).toBeGreaterThan(queuedLiveIndex);
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [{
          id: 'sess-stale',
          label: 'stale',
          directory: '/tmp/repo',
          workspace_id: 'workspace-sess-stale',
          state: 'working',
          last_seen: '2026-04-09T00:00:00Z',
        }],
        workspaces: [{
          id: 'workspace-sess-stale',
          title: 'stale',
          directory: '/tmp/repo',
          status: 'working',
          muted: false,
          layout: {
            workspace_id: 'workspace-sess-stale',
            active_pane_id: 'pane-session',
            layout_json: '',
            panes: [{
              workspace_id: 'workspace-sess-stale',
              pane_id: 'pane-session',
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

  it('invalidates a closed session layout but retains the workspace until workspace_unregistered', async () => {
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [{
          id: 'sess-removed',
          label: 'removed',
          directory: '/tmp/repo',
          workspace_id: 'workspace-sess-removed',
          state: 'working',
          last_seen: '2026-04-09T00:00:00Z',
        }],
        workspaces: [{
          id: 'workspace-sess-removed',
          title: 'removed',
          directory: '/tmp/repo',
          status: 'working',
          muted: false,
          layout: {
            workspace_id: 'workspace-sess-removed',
            active_pane_id: 'pane-session',
            layout_json: '',
            panes: [{
              workspace_id: 'workspace-sess-removed',
              pane_id: 'pane-session',
              kind: 'agent',
              runtime_id: 'sess-removed',
              title: 'Agent',
              session_id: 'sess-removed',
              status: 'spawning',
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
        expect.objectContaining({
          id: 'workspace-sess-removed',
          layout: undefined,
        }),
      ]);
    });

    act(() => {
      ws.emit({
        event: 'workspace_state_changed',
        workspace: {
          id: 'workspace-sess-removed',
          title: 'removed',
          directory: '/tmp/repo',
          status: 'idle',
          muted: false,
        },
      });
    });

    await waitFor(() => {
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([
        expect.objectContaining({
          id: 'workspace-sess-removed',
          layout: undefined,
        }),
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
          muted: false,
        },
      });
    });

    await waitFor(() => {
      expect(onWorkspacesUpdate).toHaveBeenLastCalledWith([]);
    });

    unmount();
  });

  it('updates a renamed session in place without duplicating the sidebar row', async () => {
    const onSessionsUpdate = vi.fn();
    const { unmount } = renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate,
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );

    const ws = await waitForOpenSocket();
    const session = {
      id: 'sess-1',
      label: 'original',
      agent: 'shell',
      directory: '/tmp',
      workspace_id: 'workspace-sess-1',
      state: 'idle',
    };

    // First sighting registers the row; the rename arrives as another
    // session_state_changed for the same id. It must replace, not append.
    act(() => {
      ws.emit({ event: 'session_state_changed', session });
    });
    act(() => {
      ws.emit({ event: 'session_state_changed', session: { ...session, label: 'renamed' } });
    });

    const calls = onSessionsUpdate.mock.calls;
    const lastSessions = calls.length > 0 ? calls[calls.length - 1][0] : [];
    expect(lastSessions).toHaveLength(1);
    expect(lastSessions[0]).toMatchObject({ id: 'sess-1', label: 'renamed' });

    unmount();
  });

  it('resolves a chief-of-staff update from its result event', async () => {
    const onSessionsUpdate = vi.fn();
    const onWorkspacesUpdate = vi.fn();
    const onPRsUpdate = vi.fn();
    const onReposUpdate = vi.fn();
    const onAuthorsUpdate = vi.fn();
    const { result, unmount } = renderHook(() =>
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
        protocol_version: PROTOCOL_VERSION,
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });
    await waitFor(() => {
      expect(result.current.hasReceivedInitialState).toBe(true);
    });

    let updatePromise!: Promise<void>;
    act(() => {
      updatePromise = result.current.sendSetChiefOfStaff('session-1', true);
    });
    let commandSocket: FakeWebSocket | undefined;
    await waitFor(() => {
      commandSocket = FakeWebSocket.instances.find((instance) => (
        instance.sent.some((entry) => JSON.parse(entry).cmd === 'set_chief_of_staff')
      ));
      expect(commandSocket).toBeDefined();
      expect(commandSocket!.sent.map((entry) => JSON.parse(entry))).toContainEqual({
        cmd: 'set_chief_of_staff',
        session_id: 'session-1',
        chief_of_staff: true,
      });
    });

    act(() => {
      commandSocket!.emit({
        event: 'chief_of_staff_result',
        session_id: 'session-1',
        chief_of_staff: true,
        success: true,
      });
    });
    await expect(updatePromise).resolves.toBeUndefined();

    unmount();
  });

  it('queues sendSetTerminalTheme until initial_state, then flushes it fire-and-forget', async () => {
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

    // Sent before the daemon handshake completes: must queue, not drop.
    act(() => {
      result.current.sendSetTerminalTheme({
        foreground: '#d4d4d4', background: '#1e1e1e', cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });
    expect(ws.sent.map((entry) => JSON.parse(entry))).not.toContainEqual(
      expect.objectContaining({ cmd: 'set_terminal_theme' }),
    );

    act(() => {
      ws.emit({
        event: 'initial_state',
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
        cmd: 'set_terminal_theme',
        foreground: '#d4d4d4',
        background: '#1e1e1e',
        cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });

    unmount();
  });

  it('re-pushes the last terminal theme on reconnect without a new sendSetTerminalTheme call', async () => {
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
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    act(() => {
      result.current.sendSetTerminalTheme({
        foreground: '#d4d4d4', background: '#1e1e1e', cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });
    await waitFor(() => {
      expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({
        cmd: 'set_terminal_theme',
        foreground: '#d4d4d4',
        background: '#1e1e1e',
        cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });

    // Daemon restarts and drops its in-memory theme; the client reconnects.
    act(() => {
      ws.close();
    });
    await waitFor(() => {
      expect(FakeWebSocket.instances).toHaveLength(2);
    }, { timeout: 2000 });
    const reconnected = FakeWebSocket.instances[1];
    await waitFor(() => {
      expect(reconnected.readyState).toBe(FakeWebSocket.OPEN);
    });

    act(() => {
      reconnected.emit({
        event: 'initial_state',
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
      });
    });

    // No new sendSetTerminalTheme call — the re-seed must happen on its own.
    await waitFor(() => {
      expect(reconnected.sent.map((entry) => JSON.parse(entry))).toContainEqual({
        cmd: 'set_terminal_theme',
        foreground: '#d4d4d4',
        background: '#1e1e1e',
        cursor: '#d4d4d4',
        ansi_palette: Array(16).fill('#000000'),
      });
    });

    unmount();
  });

});

describe('useDaemonSocket settings refresh', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  it('requests a fresh settings snapshot', async () => {
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
    act(() => result.current.sendGetSettings());

    expect(ws.sent.map((entry) => JSON.parse(entry))).toContainEqual({ cmd: 'get_settings' });
    unmount();
  });
});

describe('useDaemonSocket workflow runs', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(true);
    vi.mocked(invoke).mockResolvedValue(true);
    useWorkflowRunsStore.getState().reset();
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
    useWorkflowRunsStore.getState().reset();
  });

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

  it('populates the store on workflow_run_updated', async () => {
    const { unmount } = renderSocket();
    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({
        event: 'workflow_run_updated',
        run: { run_id: 'wr1', status: 'running', script_path: '/x/wf.js' },
      });
    });

    await waitFor(() => {
      const run = useWorkflowRunsStore.getState().workflowRuns['wr1'];
      expect(run).toBeDefined();
      expect(run.status).toBe('running');
    });

    unmount();
  });

  it('listWorkflowRuns sends workflow_run_list and resolves + hydrates the store', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();

    const promise = result.current.listWorkflowRuns();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.some((entry) => entry.cmd === 'workflow_run_list')).toBe(true);
    });

    const run = { run_id: 'wr1', status: 'running', script_path: '/x/wf.js' };
    act(() => {
      ws.emit({
        event: 'workflow_action_result',
        action: 'list',
        success: true,
        runs: [run],
      });
    });

    await expect(promise).resolves.toMatchObject({ success: true, runs: [run] });
    expect(useWorkflowRunsStore.getState().workflowRuns['wr1']).toBeDefined();

    unmount();
  });

  it('getWorkflowRun sends workflow_run_get and resolves with the run', async () => {
    const { result, unmount } = renderSocket();
    const ws = await waitForOpenSocket();

    const promise = result.current.getWorkflowRun('wr1');

    await waitFor(() => {
      expect(JSON.parse(ws.sent[ws.sent.length - 1])).toMatchObject({
        cmd: 'workflow_run_get',
        run_id: 'wr1',
      });
    });

    const run = { run_id: 'wr1', status: 'completed', script_path: '/x/wf.js' };
    act(() => {
      ws.emit({
        event: 'workflow_action_result',
        action: 'get',
        run_id: 'wr1',
        success: true,
        run,
      });
    });

    await expect(promise).resolves.toMatchObject({ success: true, run });
    expect(useWorkflowRunsStore.getState().workflowRuns['wr1']).toBeDefined();

    unmount();
  });
});

describe('useDaemonSocket fs surface', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });
  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  function renderFsHook(extra: Record<string, unknown> = {}) {
    return renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
        ...extra,
      }),
    );
  }

  // The last command the daemon would have received, parsed (request_id correlates
  // the result event the test then emits).
  function lastSent(ws: FakeWebSocket): { cmd: string; request_id: string; [k: string]: unknown } {
    return JSON.parse(ws.sent[ws.sent.length - 1]);
  }

  it('sends fs_list and resolves entries, converting is_dir to isDir', async () => {
    const { result, unmount } = renderFsHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendFsList('knowledge');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('fs_list');
    expect(sent.path).toBe('knowledge');

    ws.emit({
      event: 'fs_list_result',
      request_id: sent.request_id,
      success: true,
      entries: [
        { path: 'knowledge/areas', name: 'areas', is_dir: true, size: 0 },
        { path: 'knowledge/index.md', name: 'index.md', is_dir: false, size: 12, modified: '2026-06-20T00:00:00Z' },
      ],
    });

    await expect(promise).resolves.toEqual([
      { path: 'knowledge/areas', name: 'areas', isDir: true, size: 0, modified: undefined },
      { path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 12, modified: '2026-06-20T00:00:00Z' },
    ]);
    unmount();
  });

  it('omits path from fs_list when listing the root', async () => {
    const { result, unmount } = renderFsHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendFsList();
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('fs_list');
    expect('path' in sent).toBe(false);

    ws.emit({ event: 'fs_list_result', request_id: sent.request_id, success: true, entries: [] });
    await expect(promise).resolves.toEqual([]);
    unmount();
  });

  it('resolves fs_read with the file content and hash', async () => {
    const { result, unmount } = renderFsHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendFsRead('notes/todo.txt');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'fs_read', path: 'notes/todo.txt' });

    ws.emit({
      event: 'fs_read_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'notes/todo.txt', content: 'buy milk', hash: 'h1' },
    });
    await expect(promise).resolves.toEqual({ path: 'notes/todo.txt', content: 'buy milk', hash: 'h1' });
    unmount();
  });

  it('rejects fs_read on a failed result', async () => {
    const { result, unmount } = renderFsHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendFsRead('gone.txt');
    await Promise.resolve();
    const sent = lastSent(ws);
    ws.emit({ event: 'fs_read_result', request_id: sent.request_id, success: false, error: 'fsdoc: gone.txt not found' });

    await expect(promise).rejects.toThrow(/not found/);
    unmount();
  });

  it('resolves fs_write, mapping current_hash to currentHash on a conflict', async () => {
    const { result, unmount } = renderFsHook();
    const ws = await waitForOpenSocket();

    // A successful (non-conflict) write.
    const ok = result.current.sendFsWrite('a.txt', 'v1');
    await Promise.resolve();
    let sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'fs_write', path: 'a.txt', content: 'v1' });
    expect('base_hash' in sent).toBe(false);
    ws.emit({
      event: 'fs_write_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'a.txt', hash: 'h2', conflict: false },
    });
    await expect(ok).resolves.toEqual({ path: 'a.txt', hash: 'h2', conflict: false, currentHash: undefined });

    // A stale-base conflict: still a resolved result, with current_hash mapped.
    const conflicting = result.current.sendFsWrite('a.txt', 'v2', 'deadbeef');
    await Promise.resolve();
    sent = lastSent(ws);
    expect(sent.base_hash).toBe('deadbeef');
    ws.emit({
      event: 'fs_write_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'a.txt', conflict: true, current_hash: 'h2' },
    });
    await expect(conflicting).resolves.toMatchObject({ conflict: true, currentHash: 'h2' });
    unmount();
  });

  it('sends and resolves fs rename and delete actions', async () => {
    const { result, unmount } = renderFsHook();
    const ws = await waitForOpenSocket();

    const rename = result.current.sendFsRename('tickets/tk/plan.md', 'tickets/tk/implementation.md');
    await Promise.resolve();
    let sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'fs_rename', path: 'tickets/tk/plan.md', new_path: 'tickets/tk/implementation.md' });
    ws.emit({ event: 'fs_rename_result', request_id: sent.request_id, success: true, result: { path: sent.path, new_path: sent.new_path } });
    await expect(rename).resolves.toEqual({ path: 'tickets/tk/plan.md', new_path: 'tickets/tk/implementation.md' });

    const deletion = result.current.sendFsDelete('tickets/tk/implementation.md');
    await Promise.resolve();
    sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'fs_delete', path: 'tickets/tk/implementation.md' });
    ws.emit({ event: 'fs_delete_result', request_id: sent.request_id, success: true, result: { path: sent.path } });
    await expect(deletion).resolves.toEqual({ path: 'tickets/tk/implementation.md' });
    unmount();
  });

  it('invokes onFsChanged with origin, paths, and root', async () => {
    const onFsChanged = vi.fn();
    const { unmount } = renderFsHook({ onFsChanged });
    const ws = await waitForOpenSocket();

    act(() => {
      ws.emit({ event: 'fs_changed', origin: 'ui', paths: ['notes/todo.txt'], root: '/Users/x/attn-notebook' });
    });
    expect(onFsChanged).toHaveBeenCalledWith('ui', ['notes/todo.txt'], '/Users/x/attn-notebook');
    unmount();
  });
});

describe('useDaemonSocket ticket request/result', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });
  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  function renderTicketHook() {
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

  // request_id correlates the result event the test then emits back.
  function lastSent(ws: FakeWebSocket): { cmd: string; request_id: string; [k: string]: unknown } {
    return JSON.parse(ws.sent[ws.sent.length - 1]);
  }

  it('resolves fetchTicket with the record on a matching ticket_result', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.fetchTicket('tk-1');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('get_ticket');
    expect(sent.ticket_id).toBe('tk-1');

    ws.emit({
      event: 'ticket_result',
      request_id: sent.request_id,
      success: true,
      ticket: { id: 'tk-1', title: 'Migrate' },
    });
    await expect(promise).resolves.toEqual({ id: 'tk-1', title: 'Migrate' });
    unmount();
  });

  it('rejects fetchTicket when ticket_result carries an error', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.fetchTicket('missing');
    await Promise.resolve();
    const sent = lastSent(ws);

    ws.emit({
      event: 'ticket_result',
      request_id: sent.request_id,
      success: false,
      error: 'ticket not found: missing',
    });
    await expect(promise).rejects.toThrow('ticket not found: missing');
    unmount();
  });

  it('rejects a ticket action when ticket_action_result reports failure', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendTicketChangeStatus('tk-1', TicketStatus.Blocked);
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('ticket_change_status');
    expect(sent.status).toBe('blocked');

    ws.emit({
      event: 'ticket_action_result',
      request_id: sent.request_id,
      success: false,
      error: 'mutation failed',
    });
    await expect(promise).rejects.toThrow('mutation failed');
    unmount();
  });

  it('resolves a ticket action on a successful ticket_action_result', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendTicketAddComment('tk-1', 'looks good');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('ticket_add_comment');

    ws.emit({ event: 'ticket_action_result', request_id: sent.request_id, success: true });
    await expect(promise).resolves.toBeUndefined();
    unmount();
  });

  it('sends a multi-file ticket attach and resolves its receipt', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();
    const promise = result.current.sendTicketAttach('tk-1', ['/tmp/design.md', '/tmp/rollout.md'], 'ready_for_review', 'ready');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({
      cmd: 'ticket_attach',
      ticket_id: 'tk-1',
      state: 'ready_for_review',
      comment: 'ready',
      files: [
        { source_path: '/tmp/design.md', filename: 'design.md' },
        { source_path: '/tmp/rollout.md', filename: 'rollout.md' },
      ],
    });
    const receipt = { ticket_id: 'tk-1', artifacts: [], fingerprint: 'abc', event_seq: 7, state: 'in_review', deduplicated: false };
    ws.emit({ event: 'ticket_attach_result', request_id: sent.request_id, success: true, result: receipt });
    await expect(promise).resolves.toEqual(receipt);
    unmount();
  });

  it('resolves sendTicketResume with the session to focus on a successful result', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendTicketResume('tk-1');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('ticket_resume');
    expect(sent.ticket_id).toBe('tk-1');

    ws.emit({
      event: 'ticket_resume_result',
      request_id: sent.request_id,
      success: true,
      session_id: 'sess-1',
      workspace_id: 'workspace-sess-1',
    });
    await expect(promise).resolves.toEqual({
      sessionId: 'sess-1',
      workspaceId: 'workspace-sess-1',
      alreadyRunning: false,
    });
    unmount();
  });

  it('resolves sendTicketResume with alreadyRunning when the session was still tracked', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendTicketResume('tk-1');
    await Promise.resolve();
    const sent = lastSent(ws);

    ws.emit({
      event: 'ticket_resume_result',
      request_id: sent.request_id,
      success: true,
      session_id: 'sess-1',
      already_running: true,
    });
    await expect(promise).resolves.toMatchObject({ sessionId: 'sess-1', alreadyRunning: true });
    unmount();
  });

  it('rejects sendTicketResume when ticket_resume_result reports failure', async () => {
    const { result, unmount } = renderTicketHook();
    const ws = await waitForOpenSocket();

    const promise = result.current.sendTicketResume('missing');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent.cmd).toBe('ticket_resume');

    ws.emit({
      event: 'ticket_resume_result',
      request_id: sent.request_id,
      success: false,
      error: 'ticket has no agent session to resume',
    });
    await expect(promise).rejects.toThrow('ticket has no agent session to resume');
    unmount();
  });
});

// The notebook and markdown-annotation events moved out of useDaemonSocket.ts
// into daemonNotebookEvents.ts and daemonMarkdownAnnotationEvents.ts. Nothing
// covered them at the time, so the move was unfalsifiable; these exercise the
// two correlation schemes through the hook's public surface.
describe('useDaemonSocket notebook and annotation events', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
  });
  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  // Take the socket this render created, by index. Grabbing the newest instance
  // races with a reconnect leaked from an earlier test and hands back a socket
  // the hook under test never writes to.
  async function renderAndOpen(extra: Record<string, unknown> = {}) {
    const before = FakeWebSocket.instances.length;
    const rendered = renderNotebookHook(extra);
    await waitFor(() => {
      expect(FakeWebSocket.instances.length).toBeGreaterThan(before);
    });
    const ws = FakeWebSocket.instances[before];
    await waitFor(() => {
      expect(ws.readyState).toBe(FakeWebSocket.OPEN);
    });
    return { ...rendered, ws };
  }

  function renderNotebookHook(extra: Record<string, unknown> = {}) {
    return renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        wsUrl: 'ws://localhost:9999/ws',
        ...extra,
      }),
    );
  }

  function lastSent(ws: FakeWebSocket): { cmd: string; request_id: string; [k: string]: unknown } {
    return JSON.parse(ws.sent[ws.sent.length - 1]);
  }

  it('resolves session_messages_get with the annotatable window', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionMessagesGet('session-1');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'session_messages_get', session_id: 'session-1' });

    ws.emit({
      event: 'session_messages_get_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      status: 'ready',
      messages: [
        { key: 'turn-1', markdown: 'The first answer.' },
        { key: 'turn-2', markdown: 'The second answer.' },
      ],
      truncated: true,
    });
    await expect(promise).resolves.toEqual({
      messages: [
        { key: 'turn-1', markdown: 'The first answer.' },
        { key: 'turn-2', markdown: 'The second answer.' },
      ],
      status: 'ready',
      detail: undefined,
      truncated: true,
    });
    unmount();
  });

  it('resolves session_messages_get with an empty window rather than rejecting', async () => {
    // A session with no annotatable prose yet is a success with nothing to
    // annotate. Rejecting would report a failure that did not happen.
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionMessagesGet('session-1');
    await Promise.resolve();
    ws.emit({
      event: 'session_messages_get_result',
      request_id: lastSent(ws).request_id,
      session_id: 'session-1',
      success: true,
      status: 'ready',
      messages: [],
      truncated: false,
    });

    await expect(promise).resolves.toEqual({ messages: [], status: 'ready', detail: undefined, truncated: false });
    unmount();
  });

  it('routes message-window invalidations only to that session’s subscribers', async () => {
    const { result, unmount, ws } = await renderAndOpen();
    const first = vi.fn();
    const second = vi.fn();
    const unsubscribe = result.current.subscribeSessionMessagesChanged('session-1', first);
    result.current.subscribeSessionMessagesChanged('session-2', second);

    ws.emit({ event: 'session_messages_changed', session_id: 'session-1' });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).not.toHaveBeenCalled();

    unsubscribe();
    ws.emit({ event: 'session_messages_changed', session_id: 'session-1' });
    expect(first).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('revalidates subscribed message windows when the socket reconnects', async () => {
    const { result, unmount, ws } = await renderAndOpen();
    const listener = vi.fn();
    result.current.subscribeSessionMessagesChanged('session-1', listener);
    const before = FakeWebSocket.instances.length;

    act(() => {
      ws.close();
    });

    await waitFor(() => expect(FakeWebSocket.instances.length).toBeGreaterThan(before), { timeout: 2500 });
    const replacement = FakeWebSocket.instances[before];
    await waitFor(() => expect(replacement.readyState).toBe(FakeWebSocket.OPEN));
    expect(replacement).not.toBe(ws);
    await waitFor(() => expect(listener).toHaveBeenCalledTimes(1));
    unmount();
  });

  it('rejects session_messages_get when the daemon reports a failure', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionMessagesGet('session-1');
    await Promise.resolve();
    ws.emit({
      event: 'session_messages_get_result',
      request_id: lastSent(ws).request_id,
      session_id: 'session-1',
      success: false,
      error: 'no transcript for session session-1',
    });

    await expect(promise).rejects.toThrow('no transcript for session session-1');
    unmount();
  });

  it('decodes an emoji-only old-format session annotation to its quick-label id', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsGet('session-1');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'session_annotations_get', session_id: 'session-1' });

    ws.emit({
      event: 'session_annotations_get_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      annotations: [{
        id: 'anno-1',
        message_key: 'turn-1',
        start: 4,
        end: 10,
        quote: 'parser',
        emoji: '❓',
        comment: 'why this?',
      }],
      generation: 7,
    });

    // The wire is snake_case and the store is not; the boundary is here, so a
    // stored annotation arrives usable rather than half-translated.
    await expect(promise).resolves.toEqual({
      annotations: [{
        id: 'anno-1',
        messageKey: 'turn-1',
        start: 4,
        end: 10,
        quote: 'parser',
        quickLabelId: 'clarify-this',
        comment: 'why this?',
      }],
      // A get that carried no note reads as an empty one: the panel's box has
      // to be settable either way, and there is nothing for "absent" to mean
      // that "" does not already say.
      note: '',
      generation: 7,
    });
    unmount();
  });

  it('carries the draft note back with the marks', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsGet('session-1');
    await Promise.resolve();
    const sent = lastSent(ws);

    ws.emit({
      event: 'session_annotations_get_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      annotations: [],
      note: 'Split this into two PRs.',
      generation: 3,
    });

    await expect(promise).resolves.toEqual({
      annotations: [],
      note: 'Split this into two PRs.',
      generation: 3,
    });
    unmount();
  });

  it('sends a save as the whole list under its generation', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsSave('session-1', [{
      id: 'anno-1',
      messageKey: 'turn-1',
      start: 4,
      end: 10,
      quote: 'parser',
      quickLabelId: 'clarify-this',
      comment: 'why this?',
    }], 'and land the retry wrapper as is', 3);
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({
      cmd: 'session_annotations_save',
      session_id: 'session-1',
      note: 'and land the retry wrapper as is',
      generation: 3,
      annotations: [{
        id: 'anno-1',
        message_key: 'turn-1',
        start: 4,
        end: 10,
        quote: 'parser',
        quick_label_id: 'clarify-this',
        comment: 'why this?',
      }],
    });

    ws.emit({
      event: 'session_annotations_save_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      generation: 3,
    });
    await expect(promise).resolves.toEqual({ stale: false });
    unmount();
  });

  it('resolves a stale save rather than rejecting it', async () => {
    // Losing to a newer write is a routine outcome the caller handles by
    // re-hydrating; surfacing it as an error would put a race in front of the
    // user as a failure.
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsSave('session-1', [], '', 2);
    await Promise.resolve();
    ws.emit({
      event: 'session_annotations_save_result',
      request_id: lastSent(ws).request_id,
      session_id: 'session-1',
      success: false,
      stale: true,
      generation: 9,
    });

    await expect(promise).resolves.toEqual({ stale: true });
    unmount();
  });

  it('rejects a save the daemon failed for any other reason', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsSave('session-1', [], '', 2);
    await Promise.resolve();
    ws.emit({
      event: 'session_annotations_save_result',
      request_id: lastSent(ws).request_id,
      session_id: 'session-1',
      success: false,
      error: 'database is locked',
      generation: 0,
    });

    await expect(promise).rejects.toThrow('database is locked');
    unmount();
  });

  it('resolves session_annotations_clear with the tombstone the daemon settled on', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsClear('session-1', 5);
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({
      cmd: 'session_annotations_clear',
      session_id: 'session-1',
      generation: 5,
    });

    ws.emit({
      event: 'session_annotations_clear_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      generation: 5,
    });
    await expect(promise).resolves.toEqual({ generation: 5 });
    unmount();
  });

  it('resolves session_annotations_submit with the delivered status', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsSubmit('session-1', 'Feedback on your last message.');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({
      cmd: 'session_annotations_submit',
      session_id: 'session-1',
      text: 'Feedback on your last message.',
    });

    ws.emit({
      event: 'session_annotations_submit_result',
      request_id: sent.request_id,
      session_id: 'session-1',
      success: true,
      status: 'delivered',
    });
    await expect(promise).resolves.toEqual({ status: 'delivered' });
    unmount();
  });

  it('resolves a submit the daemon refused for a pending approval', async () => {
    // The caller has to tell "nothing was sent, keep the marks" apart from a
    // broken socket, so the skip arrives as a status rather than a rejection.
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsSubmit('session-1', 'feedback');
    await Promise.resolve();
    ws.emit({
      event: 'session_annotations_submit_result',
      request_id: lastSent(ws).request_id,
      session_id: 'session-1',
      success: false,
      status: 'skipped_pending_approval',
    });

    await expect(promise).resolves.toEqual({ status: 'skipped_pending_approval' });
    unmount();
  });

  it('rejects a submit the daemon failed to deliver', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendSessionAnnotationsSubmit('session-1', 'feedback');
    await Promise.resolve();
    ws.emit({
      event: 'session_annotations_submit_result',
      request_id: lastSent(ws).request_id,
      session_id: 'session-1',
      success: false,
      status: 'error',
      error: 'pty write failed',
    });

    await expect(promise).rejects.toThrow('pty write failed');
    unmount();
  });

  it('resolves notebook_read with the daemon result', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendNotebookRead('journal/2026-08-01.md');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'notebook_read', path: 'journal/2026-08-01.md' });

    ws.emit({
      event: 'notebook_read_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'journal/2026-08-01.md', content: 'today', hash: 'h1' },
    });
    await expect(promise).resolves.toEqual({ path: 'journal/2026-08-01.md', content: 'today', hash: 'h1' });
    unmount();
  });

  it('rejects notebook_read when the daemon reports success without a result', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendNotebookRead('gone.md');
    await Promise.resolve();
    ws.emit({ event: 'notebook_read_result', request_id: lastSent(ws).request_id, success: true });

    await expect(promise).rejects.toThrow('Notebook read failed');
    unmount();
  });

  it('resolves notebook_write on a conflict rather than rejecting', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.sendNotebookWrite('a.md', 'v2', 'h1');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'notebook_write', path: 'a.md' });

    ws.emit({
      event: 'notebook_write_result',
      request_id: sent.request_id,
      success: true,
      result: { path: 'a.md', hash: 'h2', conflict: true, current_hash: 'h3' },
    });
    await expect(promise).resolves.toEqual({ path: 'a.md', hash: 'h2', conflict: true, currentHash: 'h3' });
    unmount();
  });

  it('resolves notebook_list and notebook_backlinks from their own pending keys', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const list = result.current.sendNotebookList('journal');
    await Promise.resolve();
    const listSent = lastSent(ws);
    const backlinks = result.current.sendNotebookBacklinks('journal/a.md');
    await Promise.resolve();
    const backlinksSent = lastSent(ws);
    expect(listSent.cmd).toBe('notebook_list');
    expect(backlinksSent.cmd).toBe('notebook_backlinks');

    // Answer out of order: each result must find its own waiter.
    ws.emit({ event: 'notebook_backlinks_result', request_id: backlinksSent.request_id, success: true, entries: [{ path: 'b.md' }] });
    ws.emit({ event: 'notebook_list_result', request_id: listSent.request_id, success: true, entries: [{ path: 'a.md' }] });

    await expect(backlinks).resolves.toEqual([{ path: 'b.md' }]);
    await expect(list).resolves.toEqual([{ path: 'a.md' }]);
    unmount();
  });

  it('resolves markdown annotation get with annotations and generation', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.getMarkdownAnnotations('doc.md', 'ws-1');
    await Promise.resolve();
    const sent = lastSent(ws);
    expect(sent).toMatchObject({ cmd: 'markdown_annotations_get', path: 'doc.md', workspace_id: 'ws-1' });

    ws.emit({
      event: 'markdown_annotations_get_result',
      request_id: sent.request_id,
      workspace_id: 'ws-1',
      path: 'doc.md',
      success: true,
      annotations: [{ id: 'a1' }],
      generation: 7,
    });
    await expect(promise).resolves.toEqual({ annotations: [{ id: 'a1' }], generation: 7 });
    unmount();
  });

  it('treats a stale save as a resolved outcome, not an error', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const promise = result.current.saveMarkdownAnnotations('doc.md', 'ws-1', [], 3);
    await Promise.resolve();
    const sent = lastSent(ws);

    ws.emit({
      event: 'markdown_annotations_save_result',
      request_id: sent.request_id,
      workspace_id: 'ws-1',
      path: 'doc.md',
      success: false,
      stale: true,
    });
    await expect(promise).resolves.toEqual({ stale: true });
    unmount();
  });

  it('drops an annotation result whose request was superseded', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    // `get` deliberately shares one in-flight round-trip per document, so
    // supersede is exercised through `save`, which does replace its waiter.
    const first = result.current.saveMarkdownAnnotations('doc.md', 'ws-1', [], 1);
    await Promise.resolve();
    const firstSent = lastSent(ws);
    const second = result.current.saveMarkdownAnnotations('doc.md', 'ws-1', [], 2);
    await Promise.resolve();
    const secondSent = lastSent(ws);
    expect(firstSent.request_id).not.toBe(secondSent.request_id);
    await expect(first).rejects.toThrow('Superseded by a newer request');

    // The late answer to the superseded request must not settle the live one.
    ws.emit({
      event: 'markdown_annotations_save_result',
      request_id: firstSent.request_id,
      workspace_id: 'ws-1',
      path: 'doc.md',
      success: false,
      stale: true,
    });
    ws.emit({
      event: 'markdown_annotations_save_result',
      request_id: secondSent.request_id,
      workspace_id: 'ws-1',
      path: 'doc.md',
      success: true,
    });
    await expect(second).resolves.toEqual({ stale: false });
    unmount();
  });

  it('shares one in-flight round-trip when the same document is hydrated twice', async () => {
    const { result, unmount, ws } = await renderAndOpen();

    const first = result.current.getMarkdownAnnotations('doc.md', 'ws-1');
    await Promise.resolve();
    const sent = lastSent(ws);
    const second = result.current.getMarkdownAnnotations('doc.md', 'ws-1');
    await Promise.resolve();
    // No second command: a rejected hydrate would lock that tile's saves.
    expect(lastSent(ws).request_id).toBe(sent.request_id);

    ws.emit({
      event: 'markdown_annotations_get_result',
      request_id: sent.request_id,
      workspace_id: 'ws-1',
      path: 'doc.md',
      success: true,
      annotations: [{ id: 'a1' }],
      generation: 4,
    });
    await expect(first).resolves.toEqual({ annotations: [{ id: 'a1' }], generation: 4 });
    await expect(second).resolves.toEqual({ annotations: [{ id: 'a1' }], generation: 4 });
    unmount();
  });
});
