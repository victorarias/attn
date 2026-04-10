import { act, render, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createElement, useLayoutEffect } from 'react';
import { installTerminalKeyHandler, usePaneRuntimeBinder } from './usePaneRuntimeBinder';
import type { PaneRuntimeEventBinding, PaneRuntimeEventRouter } from './paneRuntimeEventRouter';

const { mockTriggerShortcut, mockIsMacLikePlatform, mockPtyAttach, mockPtySpawn, mockPtyResize, mockPtyWrite } = vi.hoisted(() => ({
  mockTriggerShortcut: vi.fn(() => false),
  mockIsMacLikePlatform: vi.fn(() => true),
  mockPtyAttach: vi.fn(() => Promise.resolve()),
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
  ptyAttach: mockPtyAttach,
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

function createMockCell(chars = ' ') {
  return {
    getChars: () => chars,
    getWidth: () => 1,
    isBold: () => 0,
    isItalic: () => 0,
    isUnderline: () => 0,
    isBlink: () => 0,
    isInverse: () => 0,
    getFgColor: () => 0,
    getBgColor: () => 0,
    isFgRGB: () => false,
    isFgPalette: () => false,
    isBgRGB: () => false,
    isBgPalette: () => false,
  };
}

function createMockLine(text: string, cols: number) {
  return {
    translateToString: () => text.padEnd(cols, ' '),
    getCell: (index: number) => createMockCell(index < text.length ? text[index] : (index < cols ? ' ' : '')),
  };
}

function createMockXterm(options?: {
  manualWriteCallbacks?: boolean;
  visibleText?: string;
  cols?: number;
  rows?: number;
  dataDuringWrite?: string[];
}) {
  const writeParsedHandlers = new Set<() => void>();
  const dataHandlers = new Set<(data: string) => void>();
  const pendingWriteCallbacks: Array<() => void> = [];
  const cols = options?.cols ?? 80;
  const rows = options?.rows ?? 24;
  const visibleText = options?.visibleText;
  const emitData = (data: string) => {
    for (const handler of Array.from(dataHandlers)) {
      handler(data);
    }
  };
  const completeWrite = (callback?: () => void) => {
    callback?.();
    for (const handler of Array.from(writeParsedHandlers)) {
      handler();
    }
  };
  return {
    cols,
    rows,
    write: vi.fn((_data: string | Uint8Array, callback?: () => void) => {
      const finishWrite = () => {
        for (const data of options?.dataDuringWrite ?? []) {
          emitData(data);
        }
        completeWrite(callback);
      };
      if (!callback) {
        return;
      }
      if (options?.manualWriteCallbacks) {
        pendingWriteCallbacks.push(finishWrite);
        return;
      }
      queueMicrotask(finishWrite);
    }),
    reset: vi.fn(),
    focus: vi.fn(),
    onData: vi.fn((handler: (data: string) => void) => {
      dataHandlers.add(handler);
      return {
        dispose: () => {
          dataHandlers.delete(handler);
        },
      };
    }),
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
        length: visibleText ? 1 : 0,
        viewportY: 0,
        getNullCell: vi.fn(() => createMockCell(' ')),
        getLine: vi.fn((index: number) => (visibleText && index === 0 ? createMockLine(visibleText, cols) : null)),
      },
    },
    __fireWriteParsed: () => {
      for (const handler of Array.from(writeParsedHandlers)) {
        handler();
      }
    },
    __emitData: emitData,
    __flushPendingWrites: () => {
      while (pendingWriteCallbacks.length > 0) {
        pendingWriteCallbacks.shift()?.();
      }
    },
  };
}

describe('installTerminalKeyHandler', () => {
  beforeEach(() => {
    mockTriggerShortcut.mockReset().mockReturnValue(false);
    mockIsMacLikePlatform.mockReset().mockReturnValue(true);
    mockPtyAttach.mockReset().mockResolvedValue(undefined);
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
    mockPtyAttach.mockReset().mockResolvedValue(undefined);
    mockPtySpawn.mockReset().mockResolvedValue(undefined);
    mockPtyResize.mockReset().mockResolvedValue(undefined);
    mockPtyWrite.mockReset().mockResolvedValue(undefined);
    window.localStorage.removeItem('attn:terminal-runtime-trace');
    (window as Window & { __ATTN_TERMINAL_RUNTIME_EVENTS?: unknown[] }).__ATTN_TERMINAL_RUNTIME_EVENTS = [];
  });

  it('replays queued PTY output after a pane remounts', async () => {
    vi.useFakeTimers();
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
      await vi.advanceTimersByTimeAsync(100);
      await result.current.drainPaneTerminal('pane-1');
      await Promise.resolve();
    });

    expect(xterm.write).toHaveBeenCalledWith(new TextEncoder().encode('queued output'), expect.any(Function));
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

    expect(xterm.write).toHaveBeenCalledWith(new TextEncoder().encode(' live output'), expect.any(Function));
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

    expect(xterm.write).toHaveBeenCalledWith(new TextEncoder().encode('queued output'), expect.any(Function));
    // Reset fence is not even enqueued until the preceding write callback fires.
    expect(xterm.write).not.toHaveBeenCalledWith(new Uint8Array(0), expect.any(Function));
    expect(xterm.reset).not.toHaveBeenCalled();

    await act(async () => {
      (xterm as any).__flushPendingWrites();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(xterm.write).toHaveBeenCalledWith(new Uint8Array(0), expect.any(Function));
    expect(xterm.reset).not.toHaveBeenCalled();

    await act(async () => {
      (xterm as any).__flushPendingWrites();
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(xterm.reset).toHaveBeenCalledOnce();
  });

  it('preserves early terminal input before the pane map effect hydrates', async () => {
    vi.useFakeTimers();
    const bindings = new Map<string, PaneRuntimeEventBinding>();
    const eventRouter = createMockEventRouter(bindings);
    const xterm = createMockXterm();

    function Child({ binder }: { binder: ReturnType<typeof usePaneRuntimeBinder> }) {
      useLayoutEffect(() => {
        binder.handleTerminalReady('pane-1')(xterm as any);
        const onDataCalls = xterm.onData.mock.calls as Array<[((data: string) => void)?]>;
        const latestOnDataCall = onDataCalls[onDataCalls.length - 1];
        const sendToPty = latestOnDataCall?.[0] as ((data: string) => void) | undefined;
        sendToPty?.('\u001bc');
      }, [binder]);
      return null;
    }

    function Harness() {
      const binder = usePaneRuntimeBinder([
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
      ], 'pane-1', eventRouter);
      return createElement(Child, { binder });
    }

    await act(async () => {
      render(createElement(Harness));
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtyWrite).toHaveBeenCalledWith({
      id: 'runtime-1',
      data: '\u001bc',
      source: 'user',
    });
  });

  it('drops terminal query responses triggered while attach replay is being restored', async () => {
    window.localStorage.setItem('attn:terminal-runtime-trace', '1');
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

    const xterm = createMockXterm({ dataDuringWrite: ['\u001b[1;1R'] });
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await Promise.resolve();
    });

    mockPtyWrite.mockClear();

    act(() => {
      bindings.get('runtime-1')?.onEvent({
        event: 'data',
        id: 'runtime-1',
        data: btoa('replay output'),
        source: 'attach_replay',
      });
    });

    await act(async () => {
      await result.current.drainPaneTerminal('pane-1');
      await Promise.resolve();
    });

    expect(mockPtyWrite).not.toHaveBeenCalled();
    const runtimeEvents = (window as Window & {
      __ATTN_TERMINAL_RUNTIME_EVENTS?: Array<{ event?: string; runtimeId?: string; details?: Record<string, unknown> }>;
    }).__ATTN_TERMINAL_RUNTIME_EVENTS || [];
    expect(runtimeEvents).toContainEqual(expect.objectContaining({
      event: 'terminal.reply.suppressed',
      runtimeId: 'runtime-1',
      details: expect.objectContaining({
        replyKinds: ['cpr'],
        cprResponses: [{ row: 1, col: 1 }],
        recognizedTerminalReply: true,
      }),
    }));

    act(() => {
      (xterm as any).__emitData('x');
    });

    expect(mockPtyWrite).toHaveBeenCalledWith({
      id: 'runtime-1',
      data: 'x',
      source: 'user',
    });
  });

  it('still forwards terminal responses generated by ordinary live output', async () => {
    window.localStorage.setItem('attn:terminal-runtime-trace', '1');
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

    const xterm = createMockXterm({ dataDuringWrite: ['\u001b[1;1R'] });
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await Promise.resolve();
    });

    mockPtyWrite.mockClear();

    act(() => {
      bindings.get('runtime-1')?.onEvent({
        event: 'data',
        id: 'runtime-1',
        data: btoa('live output'),
        seq: 12,
      });
    });

    await act(async () => {
      await result.current.drainPaneTerminal('pane-1');
      await Promise.resolve();
    });

    expect(mockPtyWrite).toHaveBeenCalledWith({
      id: 'runtime-1',
      data: '\u001b[1;1R',
      source: 'user',
    });
    const runtimeEvents = (window as Window & {
      __ATTN_TERMINAL_RUNTIME_EVENTS?: Array<{ event?: string; runtimeId?: string; details?: Record<string, unknown> }>;
    }).__ATTN_TERMINAL_RUNTIME_EVENTS || [];
    expect(runtimeEvents).toContainEqual(expect.objectContaining({
      event: 'terminal.reply.forwarded',
      runtimeId: 'runtime-1',
      details: expect.objectContaining({
        replyKinds: ['cpr'],
        cprResponses: [{ row: 1, col: 1 }],
        recognizedTerminalReply: true,
      }),
    }));
  });

  it('waits for xterm write callbacks before drain resolves', async () => {
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
    });

    let drained = false;
    const drainPromise = result.current.drainPaneTerminal('pane-1').then((value) => {
      drained = value;
      return value;
    });

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(drained).toBe(false);

    await act(async () => {
      (xterm as any).__flushPendingWrites();
      await Promise.resolve();
    });

    await expect(drainPromise).resolves.toBe(true);
  });

  it('ensures the runtime from the settled resize when terminal ready is missed', async () => {
    vi.useFakeTimers();
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
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtyResize).not.toHaveBeenCalled();
    expect(mockPtySpawn).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cwd: '/tmp/repo',
        cols: 140,
        rows: 43,
        shell: true,
      },
    });
    expect(mockPtySpawn).toHaveBeenCalledTimes(1);
  });

  it('does not schedule runtime ensure from init unless a remount hydrate is pending', async () => {
    vi.useFakeTimers();
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
      await vi.advanceTimersByTimeAsync(200);
      await Promise.resolve();
    });

    expect(mockPtySpawn).not.toHaveBeenCalled();
    expect(mockPtyAttach).not.toHaveBeenCalled();
    expect(mockPtyResize).not.toHaveBeenCalled();
  });

  it('coalesces rapid runtime resizes after ensure and sends only the settled size', async () => {
    vi.useFakeTimers();
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
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    mockPtyResize.mockClear();

    await act(async () => {
      result.current.handleTerminalResize('pane-1')(132, 41, { reason: 'resize_both' });
      result.current.handleTerminalResize('pane-1')(140, 43, { reason: 'resize_both' });
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtyResize).toHaveBeenCalledTimes(1);
    expect(mockPtyResize).toHaveBeenCalledWith({
      id: 'runtime-1',
      cols: 140,
      rows: 43,
      reason: 'resize_both',
    });
  });

  it('only sends the settled resize after widening an already-running main runtime', async () => {
    vi.useFakeTimers();
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
          shell: false,
        }),
      },
    ], 'pane-1', eventRouter));

    const xterm = createMockXterm();

    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    mockPtyResize.mockClear();

    await act(async () => {
      result.current.handleTerminalResize('pane-1')(140, 43, { reason: 'fit' });
      await vi.advanceTimersByTimeAsync(240);
      await Promise.resolve();
    });

    expect(mockPtyResize).toHaveBeenCalledTimes(1);
    expect(mockPtyResize).toHaveBeenCalledWith({
      id: 'runtime-1',
      cols: 140,
      rows: 43,
      reason: 'fit',
    });
  });

  it('does not schedule a deferred redraw after resize even if fresh output arrives later', async () => {
    vi.useFakeTimers();
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
          shell: false,
        }),
      },
    ], 'pane-1', eventRouter));

    const xterm = createMockXterm();

    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    mockPtyResize.mockClear();

    await act(async () => {
      result.current.handleTerminalResize('pane-1')(140, 43, { reason: 'fit' });
      await vi.advanceTimersByTimeAsync(100);
      bindings.get('runtime-1')?.onEvent({ event: 'data', id: 'runtime-1', data: btoa('repaint') });
      await result.current.drainPaneTerminal('pane-1');
      await vi.advanceTimersByTimeAsync(160);
      await Promise.resolve();
    });

    expect(mockPtyResize).toHaveBeenCalledTimes(1);
    expect(mockPtyResize).toHaveBeenCalledWith({
      id: 'runtime-1',
      cols: 140,
      rows: 43,
      reason: 'fit',
    });
  });

  it('hydrates a remounted xterm for an already-running runtime', async () => {
    vi.useFakeTimers();
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
          shell: false,
        }),
      },
    ], 'pane-1', eventRouter));

    const firstXterm = createMockXterm();
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(firstXterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtySpawn).toHaveBeenCalledTimes(1);
    mockPtyAttach.mockClear();
    mockPtyResize.mockClear();

    await act(async () => {
      result.current.setTerminalHandle('pane-1', null);
      await vi.advanceTimersByTimeAsync(0);
      await Promise.resolve();
    });

    const remountedXterm = createMockXterm();
    remountedXterm.cols = 132;
    remountedXterm.rows = 41;

    await act(async () => {
      result.current.handleTerminalReady('pane-1')(remountedXterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 132,
        rows: 41,
        shell: false,
        reason: 'ready',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });
    expect(mockPtyResize).not.toHaveBeenCalled();
    expect(mockPtySpawn).toHaveBeenCalledTimes(1);
  });

  it('does not hydrate when a pane is rewired without first detaching', async () => {
    vi.useFakeTimers();
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
          shell: false,
        }),
      },
    ], 'pane-1', eventRouter));

    const firstXterm = createMockXterm();
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(firstXterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    mockPtyAttach.mockClear();
    mockPtyResize.mockClear();

    const rewiredXterm = createMockXterm();
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(rewiredXterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtyAttach).not.toHaveBeenCalled();
    expect(mockPtyResize).not.toHaveBeenCalled();
  });

  it('skips same-size PTY geometry work once runtime geometry is already committed', async () => {
    vi.useFakeTimers();
    window.localStorage.setItem('attn:terminal-runtime-trace', '1');
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
          shell: false,
        }),
      },
    ], 'pane-1', eventRouter));

    const xterm = createMockXterm();
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(xterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    mockPtySpawn.mockClear();
    mockPtyAttach.mockClear();
    mockPtyResize.mockClear();
    (window as Window & { __ATTN_TERMINAL_RUNTIME_EVENTS?: unknown[] }).__ATTN_TERMINAL_RUNTIME_EVENTS = [];

    act(() => {
      result.current.handleTerminalResize('pane-1')(80, 24, { reason: 'fit' });
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(mockPtySpawn).not.toHaveBeenCalled();
    expect(mockPtyAttach).not.toHaveBeenCalled();
    expect(mockPtyResize).not.toHaveBeenCalled();

    const runtimeEvents = (window as Window & {
      __ATTN_TERMINAL_RUNTIME_EVENTS?: Array<{ event?: string; runtimeId?: string; details?: Record<string, unknown> }>;
    }).__ATTN_TERMINAL_RUNTIME_EVENTS || [];
    expect(runtimeEvents).toContainEqual(expect.objectContaining({
      event: 'pty.geometry.skipped_same_size',
      runtimeId: 'runtime-1',
      details: expect.objectContaining({
        source: 'resize',
        reason: 'fit',
        cols: 80,
        rows: 24,
      }),
    }));
  });

  it('hydrates a remounted xterm even when terminal ready is missed', async () => {
    vi.useFakeTimers();
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
          shell: false,
        }),
      },
    ], 'pane-1', eventRouter));

    const firstXterm = createMockXterm();
    await act(async () => {
      result.current.handleTerminalReady('pane-1')(firstXterm as any);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    mockPtyAttach.mockClear();
    mockPtyResize.mockClear();

    await act(async () => {
      result.current.setTerminalHandle('pane-1', null);
      await vi.advanceTimersByTimeAsync(0);
      await Promise.resolve();
    });

    const fitHandle = {
      fit: vi.fn(),
      focus: vi.fn(() => true),
      terminal: null,
    };
    act(() => {
      result.current.setTerminalHandle('pane-1', fitHandle as any);
    });

    const remountedXterm = createMockXterm();
    remountedXterm.cols = 132;
    remountedXterm.rows = 41;

    await act(async () => {
      result.current.handleTerminalInit('pane-1')(remountedXterm as any);
      await vi.advanceTimersByTimeAsync(16);
      await vi.advanceTimersByTimeAsync(100);
      await Promise.resolve();
    });

    expect(fitHandle.fit).toHaveBeenCalledTimes(1);
    expect(mockPtyAttach).toHaveBeenCalledWith({
      args: {
        id: 'runtime-1',
        cols: 132,
        rows: 41,
        shell: false,
        reason: 'init_hydrate',
        policy: 'same_app_remount',
      },
      forceResizeBeforeAttach: true,
    });
    expect(mockPtyResize).not.toHaveBeenCalled();
  });
});
