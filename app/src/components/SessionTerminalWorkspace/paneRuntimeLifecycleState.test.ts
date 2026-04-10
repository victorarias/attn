import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPaneRuntimeLifecycleRegistry } from './paneRuntimeLifecycleState';

describe('paneRuntimeLifecycleState', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('initializes and reuses pane write state', () => {
    const registry = createPaneRuntimeLifecycleRegistry();

    const first = registry.getWriteState('pane-1');
    const second = registry.getWriteState('pane-1');

    expect(second).toBe(first);
    expect(first.writeCount).toBe(0);
    expect(first.totalWriteCount).toBe(0);
  });

  it('caps queued terminal events and clears them after take', () => {
    const registry = createPaneRuntimeLifecycleRegistry();

    registry.appendPendingTerminalEvent('pane-1', { event: 'data', id: 'runtime-1', data: 'a' }, 2);
    registry.appendPendingTerminalEvent('pane-1', { event: 'data', id: 'runtime-1', data: 'b' }, 2);
    registry.appendPendingTerminalEvent('pane-1', { event: 'data', id: 'runtime-1', data: 'c' }, 2);

    expect(registry.takePendingTerminalEvents('pane-1')).toEqual([
      { event: 'data', id: 'runtime-1', data: 'b' },
      { event: 'data', id: 'runtime-1', data: 'c' },
    ]);
    expect(registry.takePendingTerminalEvents('pane-1')).toEqual([]);
  });

  it('replaces and clears pane input subscriptions', () => {
    const registry = createPaneRuntimeLifecycleRegistry();
    const first = { dispose: vi.fn() };
    const second = { dispose: vi.fn() };

    registry.replaceInputSubscription('pane-1', first);
    registry.replaceInputSubscription('pane-1', second);
    registry.clearInputSubscription('pane-1');

    expect(first.dispose).toHaveBeenCalledTimes(1);
    expect(second.dispose).toHaveBeenCalledTimes(1);
    expect(registry.get('pane-1')?.inputSubscription).toBeUndefined();
  });

  it('suppresses pane input only while the wrapped task is running', async () => {
    const registry = createPaneRuntimeLifecycleRegistry();
    let resolveTask: (() => void) | null = null;
    const task = new Promise<void>((resolve) => {
      resolveTask = resolve;
    });

    const wrapped = registry.runWithInputSuppressed('pane-1', async () => {
      expect(registry.isInputSuppressed('pane-1')).toBe(true);
      await task;
    });

    expect(registry.isInputSuppressed('pane-1')).toBe(true);

    resolveTask?.();
    await wrapped;

    expect(registry.isInputSuppressed('pane-1')).toBe(false);
  });

  it('disposes timers and subscriptions when removing pane lifecycle state', () => {
    const registry = createPaneRuntimeLifecycleRegistry();
    const clearTimeoutSpy = vi.spyOn(window, 'clearTimeout');
    const subscription = { dispose: vi.fn() };
    const state = registry.ensure('pane-1');
    state.pendingGeometryTimerId = 11;
    state.pendingUnmountCleanupId = 12;
    state.inputSubscription = subscription;

    registry.dispose('pane-1');

    expect(clearTimeoutSpy).toHaveBeenCalledWith(11);
    expect(clearTimeoutSpy).toHaveBeenCalledWith(12);
    expect(subscription.dispose).toHaveBeenCalledTimes(1);
    expect(registry.get('pane-1')).toBeUndefined();
  });
});
