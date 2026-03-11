import { describe, expect, it, vi } from 'vitest';
import { createPaneRuntimeEventRouterController } from './paneRuntimeEventRouter';

describe('paneRuntimeEventRouter', () => {
  it('routes PTY events only to the matching runtime binding', () => {
    const controller = createPaneRuntimeEventRouterController();
    const paneOne = vi.fn();
    const paneTwo = vi.fn();

    controller.registerBinding({
      sessionId: 'session-1',
      paneId: 'pane-1',
      runtimeId: 'runtime-1',
      onEvent: paneOne,
    });
    controller.registerBinding({
      sessionId: 'session-2',
      paneId: 'pane-2',
      runtimeId: 'runtime-2',
      onEvent: paneTwo,
    });

    controller.handleEvent({ event: 'data', id: 'runtime-2', data: 'Zm9v' });

    expect(paneOne).not.toHaveBeenCalled();
    expect(paneTwo).toHaveBeenCalledOnce();
  });

  it('keeps the latest binding when a runtime is rebound before the stale cleanup runs', () => {
    const controller = createPaneRuntimeEventRouterController();
    const staleBinding = vi.fn();
    const freshBinding = vi.fn();

    const disposeStale = controller.registerBinding({
      sessionId: 'session-1',
      paneId: 'pane-stale',
      runtimeId: 'runtime-1',
      onEvent: staleBinding,
    });

    controller.registerBinding({
      sessionId: 'session-1',
      paneId: 'pane-fresh',
      runtimeId: 'runtime-1',
      onEvent: freshBinding,
    });

    disposeStale();
    controller.handleEvent({ event: 'data', id: 'runtime-1', data: 'YmFy' });

    expect(staleBinding).not.toHaveBeenCalled();
    expect(freshBinding).toHaveBeenCalledOnce();
  });
});
