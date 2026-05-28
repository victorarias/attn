import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import type { PaneRuntimeEventBinding, PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import type { GhosttyTerminalHandle } from '../GhosttyTerminal';

const { mockPtyAttach, mockPtyResize, mockPtySpawn, mockPtyWrite } = vi.hoisted(() => ({
  mockPtyAttach: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyResize: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtySpawn: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyWrite: vi.fn((_request?: unknown) => Promise.resolve()),
}));

vi.mock('../../pty/bridge', () => ({
  ptyAttach: mockPtyAttach,
  ptyResize: mockPtyResize,
  ptySpawn: mockPtySpawn,
  ptyWrite: mockPtyWrite,
}));

function createTerminal(): GhosttyTerminalHandle {
  return {
    fit: vi.fn(),
    focus: vi.fn(() => true),
    typeTextViaInput: vi.fn(() => true),
    isInputFocused: vi.fn(() => true),
    write: vi.fn(() => Promise.resolve()),
    resizeLocal: vi.fn(() => Promise.resolve()),
    reset: vi.fn(),
    scrollToTop: vi.fn(() => true),
    getText: vi.fn(() => ''),
    getSize: vi.fn(() => ({ cols: 120, rows: 40 })),
    getVisibleContent: vi.fn() as never,
    getVisibleStyleSummary: vi.fn() as never,
    drain: vi.fn(() => Promise.resolve()),
  };
}

describe('useGhosttyPaneRuntime', () => {
  let binding: PaneRuntimeEventBinding | null;
  let router: PaneRuntimeEventRouter;

  beforeEach(() => {
    binding = null;
    mockPtyAttach.mockClear();
    mockPtyResize.mockClear();
    mockPtySpawn.mockClear();
    mockPtyWrite.mockClear();
    delete (window as Window & { __TEST_SESSION_INPUT_EVENTS?: unknown }).__TEST_SESSION_INPUT_EVENTS;
    router = {
      registerBinding: vi.fn((next) => {
        binding = next;
        return () => {};
      }),
    };
  });

  it('spawns with measured geometry and forwards input directly', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'main',
        runtimeId: 'runtime-1',
        paneKind: 'main',
        testSessionId: 'session-1',
        getSpawnArgs: (size) => ({ id: 'runtime-1', cwd: '/tmp', ...size }),
      },
    ], 'main', router, { current: true }));
    const terminal = createTerminal();

    await act(async () => {
      result.current.setTerminalHandle('main', terminal);
      await result.current.handleTerminalReady('main')(terminal);
      result.current.handleTerminalInput('main')('hello');
    });

    expect(mockPtySpawn).toHaveBeenCalledWith({ args: { id: 'runtime-1', cwd: '/tmp', cols: 120, rows: 40 } });
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
      result.current.handleTerminalInput('main')('\u001b[1;1R');
    });
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'main', runtimeId: 'runtime-1', paneKind: 'main', getSpawnArgs: () => null },
    ], 'main', router, { current: true }));

    act(() => result.current.setTerminalHandle('main', terminal));
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
        result.current.handleTerminalInput('main')('\u001b[1;1R');
      }
    });
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'main', runtimeId: 'runtime-1', paneKind: 'main', getSpawnArgs: () => null },
    ], 'main', router, { current: true }));

    act(() => result.current.setTerminalHandle('main', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'data',
        id: 'runtime-1',
        source: 'attach_replay',
        data: btoa('\u001b[6n'),
      });
    });

    expect(terminal.write).toHaveBeenCalledWith(expect.any(Uint8Array), { suppressResponses: true });
    expect(mockPtyWrite).not.toHaveBeenCalled();
  });

  it('sends only ordinary active-session resize updates after the runtime is ready', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'main',
        runtimeId: 'runtime-1',
        paneKind: 'main',
        getSpawnArgs: (size) => ({ id: 'runtime-1', cwd: '/tmp', ...size }),
      },
    ], 'main', router, { current: true }));
    const terminal = createTerminal();
    await act(async () => {
      await result.current.handleTerminalReady('main')(terminal);
      result.current.handleTerminalResize('main')(130, 45, { reason: 'test' });
    });

    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-1', cols: 130, rows: 45, reason: 'test' });
  });

  it('resizes daemon-attached runtimes after their first delivered PTY event', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'main', runtimeId: 'runtime-1', paneKind: 'main', getSpawnArgs: () => null },
    ], 'main', router, { current: true }));
    const terminal = createTerminal();

    act(() => result.current.setTerminalHandle('main', terminal));
    await act(async () => {
      binding?.onEvent({
        event: 'data',
        id: 'runtime-1',
        data: btoa('ready'),
      });
      result.current.handleTerminalResize('main')(130, 45, { reason: 'attached' });
    });

    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-1', cols: 130, rows: 45, reason: 'attached' });
  });

  it('reattaches a ready runtime when its terminal model remounts', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'main', runtimeId: 'runtime-1', paneKind: 'main', agent: 'claude', getSpawnArgs: () => null },
    ], 'main', router, { current: true }));
    const firstTerminal = createTerminal();
    const remountedTerminal = createTerminal();

    act(() => result.current.setTerminalHandle('main', firstTerminal));
    await act(async () => {
      binding?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('ready') });
      result.current.setTerminalHandle('main', null);
      await result.current.handleTerminalReady('main')(remountedTerminal);
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
    });
  });

  it('does not send transient unusable main-pane sizes to the PTY', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'main',
        runtimeId: 'runtime-1',
        paneKind: 'main',
        getSpawnArgs: (size) => ({ id: 'runtime-1', cwd: '/tmp', ...size }),
      },
      {
        paneId: 'shell',
        runtimeId: 'runtime-2',
        paneKind: 'shell',
        getSpawnArgs: (size) => ({ id: 'runtime-2', cwd: '/tmp', shell: true, ...size }),
      },
    ], 'main', router, { current: true }));
    const mainTerminal = createTerminal();
    const shellTerminal = createTerminal();

    await act(async () => {
      await result.current.handleTerminalReady('main')(mainTerminal);
      await result.current.handleTerminalReady('shell')(shellTerminal);
      result.current.handleTerminalResize('main')(10, 6);
      result.current.handleTerminalResize('shell')(10, 6);
    });

    expect(mockPtyResize).not.toHaveBeenCalledWith(expect.objectContaining({ id: 'runtime-1', cols: 10, rows: 6 }));
    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-2', cols: 10, rows: 6, reason: 'ghostty_fit' });
  });
});
