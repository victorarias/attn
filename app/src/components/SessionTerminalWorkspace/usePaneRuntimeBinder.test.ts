import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { installTerminalKeyHandler, usePaneRuntimeBinder } from './usePaneRuntimeBinder';
import type { PaneRuntimeEventBinding, PaneRuntimeEventRouter } from './paneRuntimeEventRouter';

const { mockTriggerShortcut, mockIsMacLikePlatform, mockPtySpawn, mockPtyResize, mockPtyWrite } = vi.hoisted(() => ({
  mockTriggerShortcut: vi.fn(() => false),
  mockIsMacLikePlatform: vi.fn(() => true),
  mockPtySpawn: vi.fn(() => Promise.resolve()),
  mockPtyResize: vi.fn(() => Promise.resolve()),
  mockPtyWrite: vi.fn(() => Promise.resolve()),
}));

vi.mock('../../shortcuts/useShortcut', () => ({
  triggerShortcut: mockTriggerShortcut,
}));

vi.mock('../../shortcuts/platform', () => ({
  isMacLikePlatform: mockIsMacLikePlatform,
}));

vi.mock('../../pty/bridge', () => ({
  ptySpawn: mockPtySpawn,
  ptyResize: mockPtyResize,
  ptyWrite: mockPtyWrite,
}));

function createMockEventRouter(bindings: Map<string, PaneRuntimeEventBinding>): PaneRuntimeEventRouter {
  return {
    registerBinding: (binding) => {
      bindings.set(binding.runtimeId, binding);
      return () => {
        bindings.delete(binding.runtimeId);
      };
    },
  };
}

function createMockXterm(options?: { manualWriteCallbacks?: boolean }) {
  const writeParsedHandlers = new Set<() => void>();
  const pendingWriteCallbacks: Array<() => void> = [];
  const completeWrite = (callback?: () => void) => {
    callback?.();
    for (const handler of Array.from(writeParsedHandlers)) {
      handler();
    }
  };
  return {
    cols: 80,
    rows: 24,
    write: vi.fn((_data: string | Uint8Array, callback?: () => void) => {
      if (!callback) {
        return;
      }
      if (options?.manualWriteCallbacks) {
        pendingWriteCallbacks.push(callback);
        return;
      }
      queueMicrotask(() => completeWrite(callback));
    }),
    reset: vi.fn(),
    focus: vi.fn(),
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    onWriteParsed: vi.fn((handler: () => void) => {
      writeParsedHandlers.add(handler);
      return {
        dispose: () => {
          writeParsedHandlers.delete(handler);
        },
      };
    }),
    attachCustomKeyEventHandler: vi.fn(),
    buffer: {
      active: {
        length: 0,
        getLine: vi.fn(() => null),
      },
    },
    __fireWriteParsed: () => {
      for (const handler of Array.from(writeParsedHandlers)) {
        handler();
      }
    },
    __flushPendingWrites: () => {
      while (pendingWriteCallbacks.length > 0) {
        completeWrite(pendingWriteCallbacks.shift());
      }
    },
  };
}

describe('installTerminalKeyHandler', () => {
  beforeEach(() => {
    mockTriggerShortcut.mockReset().mockReturnValue(false);
    mockIsMacLikePlatform.mockReset().mockReturnValue(true);
    mockPtySpawn.mockReset().mockResolvedValue(undefined);
    mockPtyResize.mockReset().mockResolvedValue(undefined);
    mockPtyWrite.mockReset().mockResolvedValue(undefined);
  });

  it('swallows cmd+w even when no pane close shortcut is registered', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', { key: 'w', metaKey: true });

    expect(handler(event)).toBe(false);
    expect(mockTriggerShortcut).toHaveBeenCalledWith('terminal.close');
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('leaves ctrl+w alone on macOS so shells can erase the previous word', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', { key: 'w', ctrlKey: true });

    expect(handler(event)).toBe(true);
    expect(mockTriggerShortcut).not.toHaveBeenCalled();
    expect(sendToPty).not.toHaveBeenCalled();
  });

  it('routes cmd+shift+z to the zoom shortcut', () => {
    const sendToPty = vi.fn();
    const handler = installTerminalKeyHandler(sendToPty);
    const event = new KeyboardEvent('keydown', { key: 'z', metaKey: true, shiftKey: true });

    handler(event);

    expect(mockTriggerShortcut).toHaveBeenCalledWith('terminal.toggleZoom');
    expect(sendToPty).not.toHaveBeenCalled();
  });
});

describe('usePaneRuntimeBinder', () => {
  beforeEach(() => {
    mockPtySpawn.mockReset().mockResolvedValue(undefined);
  });

  it('replays queued PTY output after a pane remounts', async () => {
    const bindings = new Map<string, PaneRuntimeEventBinding>();
    const eventRouter = createMockEventRouter(bindings);
    const { result } = renderHook(() => usePaneRuntimeBinder([
      {
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        testSessionId: 'session-1',
        getSpawnArgs: ({ cols, rows }) => ({
          id: 'runtime-1',
          cwd: '/tmp/repo',
          cols,
          rows,
          shell: true,
        }),
      },
    ], 'pane-1', eventRouter));

    const queuedBinding = bindings.get('runtime-1');
    expect(queuedBinding).toBeDefined();

    act(() => {
      queuedBinding?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('queued output') });
    });

    const xterm = createMockXterm();

    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await result.current.drainPaneTerminal('pane-1');
      await Promise.resolve();
    });

    expect(xterm.write).toHaveBeenCalledWith(new TextEncoder().encode('queued output'));
    expect(mockPtySpawn).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cwd: '/tmp/repo',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });

    act(() => {
      bindings.get('runtime-1')?.onEvent({ event: 'data', id: 'runtime-1', data: btoa(' live output') });
    });

    await act(async () => {
      await result.current.drainPaneTerminal('pane-1');
      await Promise.resolve();
    });

    expect(xterm.write).toHaveBeenCalledWith(new TextEncoder().encode(' live output'));
  });

  it('keeps reset ordered behind pending terminal writes', async () => {
    const bindings = new Map<string, PaneRuntimeEventBinding>();
    const eventRouter = createMockEventRouter(bindings);
    const { result } = renderHook(() => usePaneRuntimeBinder([
      {
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        testSessionId: 'session-1',
        getSpawnArgs: () => null,
      },
    ], 'pane-1', eventRouter));

    const xterm = createMockXterm({ manualWriteCallbacks: true });
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await Promise.resolve();
    });

    act(() => {
      bindings.get('runtime-1')?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('queued output') });
      bindings.get('runtime-1')?.onEvent({ event: 'reset', id: 'runtime-1', reason: 'test' });
    });

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(xterm.write).toHaveBeenCalledWith(new TextEncoder().encode('queued output'));
    // Reset fence write is queued with a callback — reset waits for it
    expect(xterm.write).toHaveBeenCalledWith(new Uint8Array(0), expect.any(Function));
    expect(xterm.reset).not.toHaveBeenCalled();

    await act(async () => {
      (xterm as any).__flushPendingWrites();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(xterm.reset).toHaveBeenCalledOnce();
  });

  it('ensures the runtime from the first resize when terminal ready is missed', async () => {
    const bindings = new Map<string, PaneRuntimeEventBinding>();
    const eventRouter = createMockEventRouter(bindings);
    const { result } = renderHook(() => usePaneRuntimeBinder([
      {
        paneId: 'pane-1',
        runtimeId: 'runtime-1',
        testSessionId: 'session-1',
        getSpawnArgs: ({ cols, rows }) => ({
          id: 'runtime-1',
          cwd: '/tmp/repo',
          cols,
          rows,
          shell: true,
        }),
      },
    ], 'pane-1', eventRouter));

    const xterm = createMockXterm();

    await act(async () => {
      result.current.handleTerminalInit('pane-1')(xterm as any);
      result.current.handleTerminalResize('pane-1')(132, 41);
      result.current.handleTerminalResize('pane-1')(140, 43);
      await Promise.resolve();
    });

    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-1', cols: 132, rows: 41 });
    expect(mockPtyResize).toHaveBeenCalledWith({ id: 'runtime-1', cols: 140, rows: 43 });
    expect(mockPtySpawn).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cwd: '/tmp/repo',
        cols: 80,
        rows: 24,
        shell: true,
      },
    });
    expect(mockPtySpawn).toHaveBeenCalledTimes(1);
  });
});
