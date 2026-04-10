import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  isPaneRuntimeDebugEnabled,
  type PaneRuntimeDebugEvent,
  recordPaneRuntimeDebugEvent,
} from './paneRuntimeDebug';

describe('paneRuntimeDebug', () => {
  beforeEach(() => {
    vi.spyOn(console, 'log').mockImplementation(() => {});
    (window as Window & {
      __ATTN_PANE_DEBUG_CLEAR?: () => void;
      __ATTN_PANE_DEBUG_ENABLE?: (enabled: boolean) => void;
    }).__ATTN_PANE_DEBUG_ENABLE?.(false);
    (window as Window & {
      __ATTN_PANE_DEBUG_CLEAR?: () => void;
    }).__ATTN_PANE_DEBUG_CLEAR?.();
    window.localStorage.removeItem('attn:pane-runtime-debug');
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('skips disabled pane debug events without evaluating lazy details', () => {
    const details = vi.fn(() => ({ activeTag: 'DIV' }));

    recordPaneRuntimeDebugEvent({
      scope: 'workspace',
      message: 'ignored event',
      details,
    });

    expect(isPaneRuntimeDebugEnabled()).toBe(false);
    expect(details).not.toHaveBeenCalled();
    const dump = (window as Window & {
      __ATTN_PANE_DEBUG_DUMP?: () => PaneRuntimeDebugEvent[];
    }).__ATTN_PANE_DEBUG_DUMP?.();
    expect(dump).toEqual([]);
  });

  it('records enabled pane debug events and resolves lazy details once', () => {
    (window as Window & {
      __ATTN_PANE_DEBUG_ENABLE?: (enabled: boolean) => void;
    }).__ATTN_PANE_DEBUG_ENABLE?.(true);
    const details = vi.fn(() => ({ activeTag: 'BUTTON' }));

    recordPaneRuntimeDebugEvent({
      scope: 'workspace',
      message: 'recorded event',
      details,
    });

    const events = (window as Window & {
      __ATTN_PANE_DEBUG_DUMP?: () => PaneRuntimeDebugEvent[];
    }).__ATTN_PANE_DEBUG_DUMP?.() || [];
    expect(isPaneRuntimeDebugEnabled()).toBe(true);
    expect(details).toHaveBeenCalledTimes(1);
    expect(events).toHaveLength(1);
    expect(events[0]?.details).toEqual({ activeTag: 'BUTTON' });
  });
});
