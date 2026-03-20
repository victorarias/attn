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

function createMockXterm() {
  return {
    cols: 80,
    rows: 24,
    write: vi.fn(),
    reset: vi.fn(),
    focus: vi.fn(),
    onData: vi.fn(() => ({ dispose: vi.fn() })),
    attachCustomKeyEventHandler: vi.fn(),
    buffer: {
      active: {
        length: 0,
        getLine: vi.fn(() => null),
      },
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

    expect(xterm.write).toHaveBeenNthCalledWith(2, new TextEncoder().encode(' live output'));
  });
});
