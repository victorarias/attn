import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clearTerminalRuntimeLog,
  isTerminalRuntimeTraceEnabled,
  recordTerminalRuntimeLog,
  setTerminalRuntimeTraceEnabled,
  type TerminalRuntimeLogEvent,
} from './terminalRuntimeLog';

describe('terminalRuntimeLog', () => {
  beforeEach(() => {
    setTerminalRuntimeTraceEnabled(false);
    clearTerminalRuntimeLog();
    window.localStorage.removeItem('attn:terminal-runtime-trace');
  });

  it('skips disabled runtime trace events without evaluating lazy details', () => {
    const details = vi.fn(() => ({ rows: 46 }));

    recordTerminalRuntimeLog({
      category: 'terminal',
      message: 'ignored trace event',
      details,
    });

    expect(isTerminalRuntimeTraceEnabled()).toBe(false);
    expect(details).not.toHaveBeenCalled();
    const dump = (window as Window & {
      __ATTN_TERMINAL_RUNTIME_DUMP?: () => TerminalRuntimeLogEvent[];
    }).__ATTN_TERMINAL_RUNTIME_DUMP?.();
    expect(dump).toEqual([]);
  });

  it('records enabled runtime trace events and resolves lazy details once', () => {
    setTerminalRuntimeTraceEnabled(true);
    clearTerminalRuntimeLog();
    const details = vi.fn(() => ({ rows: 46 }));

    recordTerminalRuntimeLog({
      category: 'terminal',
      message: 'recorded trace event',
      details,
    });

    const events = (window as Window & {
      __ATTN_TERMINAL_RUNTIME_DUMP?: () => TerminalRuntimeLogEvent[];
    }).__ATTN_TERMINAL_RUNTIME_DUMP?.() || [];
    expect(isTerminalRuntimeTraceEnabled()).toBe(true);
    expect(details).toHaveBeenCalledTimes(1);
    expect(events).toHaveLength(1);
    expect(events[0]?.details).toEqual({ rows: 46 });
  });
});
