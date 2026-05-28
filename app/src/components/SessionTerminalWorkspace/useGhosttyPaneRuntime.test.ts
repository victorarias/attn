import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useGhosttyPaneRuntime } from './useGhosttyPaneRuntime';
import type { PaneRuntimeEventBinding, PaneRuntimeEventRouter } from './paneRuntimeEventRouter';
import type { GhosttyTerminalHandle } from '../GhosttyTerminal';

const { mockPtyResize, mockPtySpawn, mockPtyWrite } = vi.hoisted(() => ({
  mockPtyResize: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtySpawn: vi.fn((_request?: unknown) => Promise.resolve()),
  mockPtyWrite: vi.fn((_request?: unknown) => Promise.resolve()),
}));

vi.mock('../../pty/bridge', () => ({
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
    resizeLocal: vi.fn(),
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

  it('writes attach replay normally and forwards any response generated while parsing it', async () => {
    const terminal = createTerminal();
    vi.mocked(terminal.write).mockImplementation(async () => {
      result.current.handleTerminalInput('main')('\u001b[1;1R');
    });
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      { paneId: 'main', runtimeId: 'runtime-1', getSpawnArgs: () => null },
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

    expect(terminal.write).toHaveBeenCalled();
    expect(mockPtyWrite).toHaveBeenCalledWith({ id: 'runtime-1', data: '\u001b[1;1R' });
  });

  it('sends only ordinary active-session resize updates after the runtime is ready', async () => {
    const { result } = renderHook(() => useGhosttyPaneRuntime([
      {
        paneId: 'main',
        runtimeId: 'runtime-1',
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
      { paneId: 'main', runtimeId: 'runtime-1', getSpawnArgs: () => null },
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
});
