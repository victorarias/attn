import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import type { PaneRuntimeEventBinding, PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import type { GhosttyTerminalHandle } from '../GhosttyTerminal';

const { mockPtyAttach, mockPtyDetach, mockPtyResize, mockPtyWrite } = vi.hoisted(() => ({
  mockPtyAttach: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyDetach: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyResize: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyWrite: vi.fn((_request?: unknown) => Promise.resolve()),
}));

vi.mock('../../pty/bridge', () => ({
  ptyAttach: mockPtyAttach,
  ptyDetach: mockPtyDetach,
  ptyResize: mockPtyResize,
  ptyWrite: mockPtyWrite,
}));

function createTerminal(): GhosttyTerminalHandle {
  return {
    fit: vi.fn(),
    openFind: vi.fn(),
    focus: vi.fn(() => true),
    typeTextViaInput: vi.fn(() => true),
    isInputFocused: vi.fn(() => true),
    write: vi.fn(() => Promise.resolve()),
    resizeLocal: vi.fn(() => Promise.resolve()),
    reset: vi.fn(),
    scrollToTop: vi.fn(() => true),
    getText: vi.fn(() => ''),
    getSize: vi.fn(() => ({ cols: 120, rows: 40 })),
    hasMeasuredSize: vi.fn(() => true),
    getVisibleContent: vi.fn() as never,
    getVisibleStyleSummary: vi.fn() as never,
    getBlockState: vi.fn() as never,
    drain: vi.fn(() => Promise.resolve()),
  };
}

describe('useGhosttyPaneRuntime', () => {
  let binding: PaneRuntimeEventBinding | null;
  let router: PaneRuntimeEventRouter;

  beforeEach(() => {
    binding = null;
    mockPtyAttach.mockClear();
    mockPtyDetach.mockClear();
    mockPtyResize.mockClear();
    mockPtyWrite.mockClear();
    delete (window as Window & { __TEST_SESSION_INPUT_EVENTS?: unknown }).__TEST_SESSION_INPUT_EVENTS;
    router = {
      registerBinding: vi.fn((next) => {
        binding = next;
        return () => {};
      }),
    };
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('attaches with measured geometry and forwards input directly', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'pane-session',
        runtimeId: 'runtime-1',
        paneKind: 'agent',
        testSessionId: 'session-1',
      },
    ], 'pane-session', router, { current: true }));
    const terminal = createTerminal();

    await act(async () => {
      result.current.setTerminalHandle('pane-session', terminal);
      await result.current.handleTerminalReady('pane-session')(terminal);
      result.current.handleTerminalInput('pane-session')('hello');
    });

    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 120,
        rows: 40,
        shell: false,
        agent: undefined,
        policy: 'fresh_spawn',
      },
      forceResizeBeforeAttach: false,
    });
    expect(mockPtyWrite).toHaveBeenCalledWith({ id: 'runtime-1', data: 'hello' });
    expect((window as Window & {
      __TEST_SESSION_INPUT_EVENTS?: Array<{ sessionId: string; event: string; data?: string }>;
    }).__TEST_SESSION_INPUT_EVENTS).toEqual([
      { sessionId: 'session-1', event: 'connect_terminal' },
      { sessionId: 'session-1', event: 'send_to_pty', data: 'hello' },
    ]);
  });

  it('allows explicit bootstrap replay to return terminal responses to the live PTY', async () => {
    const terminal = createTerminal();
    vi.mocked(terminal.write).mockImplementation(async () => {
      result.current.handleTerminalInput('pane-session')('\u001b[1;1R');
    });
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, { current: true }));

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'data',
        id: 'runtime-1',
        source: 'attach_replay',
        suppressResponses: false,
        data: btoa('\u001b[6n'),
      });
    });

    expect(terminal.write).toHaveBeenCalled();
    expect(mockPtyWrite).toHaveBeenCalledWith({ id: 'runtime-1', data: '\u001b[1;1R' });
  });

  it('parses restore replay without returning historical terminal responses to the live PTY', async () => {
    const terminal = createTerminal();
    vi.mocked(terminal.write).mockImplementation(async (_data, options) => {
      if (!options?.suppressResponses) {
        result.current.handleTerminalInput('pane-session')('\u001b[1;1R');
      }
    });
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, { current: true }));

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'data',
        id: 'runtime-1',
        source: 'attach_replay',
        data: btoa('\u001b[6n'),
      });
    });

    expect(terminal.write).toHaveBeenCalledWith(
      expect.any(Uint8Array),
      {
        suppressResponses: true,
        yieldBefore: true,
        deferRender: true,
        historicalReplay: true,
      },
    );
    expect(mockPtyWrite).not.toHaveBeenCalled();
  });

  it('splits large historical replay into cooperative writes without losing bytes', async () => {
    const terminal = createTerminal();
    const bytes = new Uint8Array((16 * 1024) + 1);
    bytes.fill(65);
    bytes[bytes.length - 1] = 66;
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, { current: true }));

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'data',
        id: 'runtime-1',
        source: 'attach_replay',
        data: btoa(String.fromCharCode(...bytes)),
      });
    });

    expect(terminal.write).toHaveBeenCalledTimes(2);
    const firstWrite = vi.mocked(terminal.write).mock.calls[0];
    const secondWrite = vi.mocked(terminal.write).mock.calls[1];
    expect(firstWrite[0]).toBeInstanceOf(Uint8Array);
    expect(secondWrite[0]).toBeInstanceOf(Uint8Array);
    expect((firstWrite[0] as Uint8Array).byteLength).toBe(16 * 1024);
    expect((secondWrite[0] as Uint8Array)).toEqual(Uint8Array.of(66));
    expect(firstWrite[1]).toEqual({
      suppressResponses: true,
      yieldBefore: true,
      deferRender: true,
      historicalReplay: true,
    });
    expect(secondWrite[1]).toEqual({
      suppressResponses: true,
      yieldBefore: true,
      deferRender: true,
      historicalReplay: true,
    });
  });

  it('marks historical geometry changes as replay-only resizes', async () => {
    const terminal = createTerminal();
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, { current: true }));

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'local_resize',
        id: 'runtime-1',
        source: 'attach_replay',
        cols: 118,
        rows: 48,
      });
    });

    expect(terminal.resizeLocal).toHaveBeenCalledWith(
      118,
      48,
      { historicalReplay: true },
    );
  });

  it('fits an active pane after queued historical replay completes', async () => {
    const terminal = createTerminal();
    const isActiveSessionRef = { current: true };
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, isActiveSessionRef));

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({ event: 'replay_complete', id: 'runtime-1' });
      await Promise.resolve();
    });

    expect(terminal.drain).toHaveBeenCalled();
    expect(terminal.fit).toHaveBeenCalled();
  });

  it('does not fit replay completion after the workspace becomes inactive', async () => {
    let resolveDrain: (() => void) | undefined;
    const terminal = createTerminal();
    vi.mocked(terminal.drain).mockReturnValue(new Promise<void>((resolve) => {
      resolveDrain = resolve;
    }));
    const isActiveSessionRef = { current: true };
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, isActiveSessionRef));

    act(() => {
      result.current.setTerminalHandle('pane-session', terminal);
      binding?.onEvent({ event: 'replay_complete', id: 'runtime-1' });
      isActiveSessionRef.current = false;
      resolveDrain?.();
    });
    await act(async () => {
      await Promise.resolve();
    });

    expect(terminal.fit).not.toHaveBeenCalled();
  });

  it('sends only ordinary active-session resize updates after the runtime is ready', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'pane-session',
        runtimeId: 'runtime-1',
        paneKind: 'agent',
      },
    ], 'pane-session', router, { current: true }));
    const terminal = createTerminal();
    await act(async () => {
      await result.current.handleTerminalReady('pane-session')(terminal);
      result.current.handleTerminalResize('pane-session')(130, 45, { reason: 'test' });
    });

    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-1', cols: 130, rows: 45, reason: 'test' });
  });

  it('flushes the measured pane size after a fresh runtime finishes attaching', async () => {
    let resolveAttach: (() => void) | undefined;
    mockPtyAttach.mockReturnValueOnce(new Promise<void>((resolve) => {
      resolveAttach = resolve;
    }));
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'pane-session',
        runtimeId: 'runtime-1',
        paneKind: 'agent',
        agent: 'codex',
      },
    ], 'pane-session', router, { current: true }));
    const terminal = createTerminal();
    vi.mocked(terminal.getSize).mockReturnValue({ cols: 106, rows: 64 });

    let readyPromise: Promise<void>;
    act(() => {
      result.current.handleTerminalResize('pane-session')(106, 64, { reason: 'ghostty_fit' });
      readyPromise = result.current.handleTerminalReady('pane-session')(terminal);
    });

    expect(mockPtyResize).not.toHaveBeenCalled();

    await act(async () => {
      resolveAttach?.();
      await readyPromise!;
    });

    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 106,
        rows: 64,
        shell: false,
        agent: 'codex',
        policy: 'fresh_spawn',
      },
      forceResizeBeforeAttach: false,
    });
    expect(mockPtyResize).toHaveBeenCalledWith({
      id: 'runtime-1',
      cols: 106,
      rows: 64,
      reason: 'ghostty_fit',
    });
  });

  it('resizes daemon-attached runtimes after their first delivered PTY event', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
    ], 'pane-session', router, { current: true }));
    const terminal = createTerminal();

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'data',
        id: 'runtime-1',
        data: btoa('ready'),
      });
      result.current.handleTerminalResize('pane-session')(130, 45, { reason: 'attached' });
    });

    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-1', cols: 130, rows: 45, reason: 'attached' });
  });

  it('reattaches a ready runtime when its terminal model remounts', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent', agent: 'claude' },
    ], 'pane-session', router, { current: true }));
    const firstTerminal = createTerminal();
    const remountedTerminal = createTerminal();

    act(() => result.current.setTerminalHandle('pane-session', firstTerminal));
    await act(async () => {
      binding?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('ready') });
      result.current.setTerminalHandle('pane-session', null);
      await result.current.handleTerminalReady('pane-session')(remountedTerminal);
    });

    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 120,
        rows: 40,
        shell: false,
        agent: 'claude',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });
    expect(mockPtyDetach).not.toHaveBeenCalled();
  });

  it('re-attaches after queued replay is interrupted by a geometry change', async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent', agent: 'claude' },
    ], 'pane-session', router, { current: true }));
    const terminal = createTerminal();

    act(() => result.current.setTerminalHandle('pane-session', terminal));
    await act(async () => {
      binding?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('ready') });
      await result.current.handleTerminalReady('pane-session')(terminal);
    });
    mockPtyAttach.mockClear();

    // A split lands mid-replay: the terminal reports the interruption twice
    // in quick succession (burst of fits) — one debounced re-attach follows.
    act(() => {
      result.current.handleReplayInterrupted('pane-session')();
      result.current.handleReplayInterrupted('pane-session')();
    });
    expect(mockPtyAttach).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });

    expect(mockPtyAttach).toHaveBeenCalledTimes(1);
    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 120,
        rows: 40,
        shell: false,
        agent: 'claude',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });
  });

  it('detaches an attached runtime when its terminal is virtualized', async () => {
    const { result, rerender } = renderHook(
      ({ terminalsLive }) => useGhosttyPaneRuntime([
        { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
      ], 'pane-session', router, { current: true }, terminalsLive),
      { initialProps: { terminalsLive: true } },
    );
    const terminal = createTerminal();

    await act(async () => {
      await result.current.handleTerminalReady('pane-session')(terminal);
    });
    expect(mockPtyDetach).not.toHaveBeenCalled();

    rerender({ terminalsLive: false });

    expect(mockPtyDetach).toHaveBeenCalledWith({ id: 'runtime-1' });
  });

  it('invalidates an in-flight attach when its terminal is virtualized', async () => {
    let resolveAttach: (() => void) | undefined;
    mockPtyAttach.mockReturnValueOnce(new Promise<void>((resolve) => {
      resolveAttach = resolve;
    }));
    const { result, rerender } = renderHook(
      ({ terminalsLive }) => useGhosttyPaneRuntime([
        { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
      ], 'pane-session', router, { current: true }, terminalsLive),
      { initialProps: { terminalsLive: true } },
    );
    const terminal = createTerminal();

    let readyPromise: Promise<void>;
    act(() => {
      readyPromise = result.current.handleTerminalReady('pane-session')(terminal);
    });
    rerender({ terminalsLive: false });

    expect(mockPtyDetach).toHaveBeenCalledWith({ id: 'runtime-1' });

    await act(async () => {
      resolveAttach?.();
      await readyPromise!;
    });

    expect(mockPtyResize).not.toHaveBeenCalled();
    expect(terminal.write).not.toHaveBeenCalledWith(expect.stringContaining('Failed to attach PTY'));
  });

  it('waits for a canceled attach result before attaching a remounted terminal', async () => {
    let resolveFirstAttach: (() => void) | undefined;
    mockPtyAttach.mockReturnValueOnce(new Promise<void>((resolve) => {
      resolveFirstAttach = resolve;
    }));
    const { result, rerender } = renderHook(
      ({ terminalsLive }) => useGhosttyPaneRuntime([
        { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent' },
      ], 'pane-session', router, { current: true }, terminalsLive),
      { initialProps: { terminalsLive: true } },
    );
    const firstTerminal = createTerminal();
    const remountedTerminal = createTerminal();

    let firstReadyPromise: Promise<void>;
    act(() => {
      firstReadyPromise = result.current.handleTerminalReady('pane-session')(firstTerminal);
    });
    rerender({ terminalsLive: false });
    rerender({ terminalsLive: true });

    let remountedReadyPromise: Promise<void>;
    act(() => {
      remountedReadyPromise = result.current.handleTerminalReady('pane-session')(remountedTerminal);
    });
    expect(mockPtyAttach).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveFirstAttach?.();
      await firstReadyPromise!;
      await remountedReadyPromise!;
    });

    expect(mockPtyAttach).toHaveBeenCalledTimes(2);
    expect(mockPtyAttach).toHaveBeenLastCalledWith({
      args: {
        id: 'runtime-1',
        cols: 120,
        rows: 40,
        shell: false,
        agent: undefined,
        policy: 'fresh_spawn',
      },
      forceResizeBeforeAttach: false,
    });
    expect(mockPtyResize).not.toHaveBeenCalled();
  });

  it('marks attach geometry provisional and skips the pre-attach resize for unmeasured terminals', async () => {
    // A pane mounted while its session is inactive never measured its
    // container, so attaching with its construction-default size must not
    // claim PTY geometry authority (no remount_hydrate resize, and the
    // attach reconcile skips daemon_known_attach downstream).
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'pane-session', runtimeId: 'runtime-1', paneKind: 'agent', agent: 'claude' },
    ], 'pane-session', router, { current: true }));
    const firstTerminal = createTerminal();
    const remountedTerminal = createTerminal();
    vi.mocked(remountedTerminal.hasMeasuredSize).mockReturnValue(false);

    act(() => result.current.setTerminalHandle('pane-session', firstTerminal));
    await act(async () => {
      binding?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('ready') });
      result.current.setTerminalHandle('pane-session', null);
      await result.current.handleTerminalReady('pane-session')(remountedTerminal);
    });

    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 120,
        rows: 40,
        shell: false,
        agent: 'claude',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: false,
    });
  });

  it('does not send transient unusable session-pane sizes to the PTY', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'pane-session',
        runtimeId: 'runtime-1',
        paneKind: 'agent',
      },
      {
        paneId: 'pane-session-2',
        runtimeId: 'runtime-2',
        paneKind: 'agent',
      },
    ], 'pane-session', router, { current: true }));
    const firstTerminal = createTerminal();
    const secondTerminal = createTerminal();

    await act(async () => {
      await result.current.handleTerminalReady('pane-session')(firstTerminal);
      await result.current.handleTerminalReady('pane-session-2')(secondTerminal);
      result.current.handleTerminalResize('pane-session')(10, 6);
      result.current.handleTerminalResize('pane-session-2')(10, 6);
    });

    expect(mockPtyResize).not.toHaveBeenCalledWith(expect.objectContaining({ id: 'runtime-1', cols: 10, rows: 6 }));
    expect(mockPtyResize).not.toHaveBeenCalledWith(expect.objectContaining({ id: 'runtime-2', cols: 10, rows: 6 }));
  });
});
