import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAnnotationSend, type AnnotationSendResult } from './useAnnotationSend';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('useAnnotationSend', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('guards re-entry, reports sending and sent, then expires sent', async () => {
    const pending = deferred<AnnotationSendResult>();
    const send = vi.fn(() => pending.promise);
    const { result } = renderHook(() => useAnnotationSend({
      send,
      shortcutId: 'terminal.sendAnnotations',
      enabled: true,
      sentClearMs: 2200,
    }));

    act(() => {
      result.current.send();
      result.current.send();
    });
    expect(send).toHaveBeenCalledTimes(1);
    expect(result.current.outcome).toEqual({ kind: 'sending' });

    await act(async () => pending.resolve({ kind: 'sent' }));
    expect(result.current.outcome).toEqual({ kind: 'sent' });
    act(() => vi.advanceTimersByTime(2199));
    expect(result.current.outcome).toEqual({ kind: 'sent' });
    act(() => vi.advanceTimersByTime(1));
    expect(result.current.outcome).toBeNull();
  });

  it('keeps skipped distinct and persistent', async () => {
    const { result } = renderHook(() => useAnnotationSend({
      send: async () => ({ kind: 'skipped' as const }),
      shortcutId: 'terminal.sendAnnotations',
      enabled: true,
      sentClearMs: 2200,
    }));

    act(() => result.current.send());
    await act(async () => {});
    expect(result.current.outcome).toEqual({ kind: 'skipped' });
    act(() => vi.runAllTimers());
    expect(result.current.outcome).toEqual({ kind: 'skipped' });
  });

  it('turns a rejected send into a persistent error', async () => {
    const { result } = renderHook(() => useAnnotationSend({
      send: async () => { throw new Error('delivery failed'); },
      shortcutId: 'terminal.sendAnnotations',
      enabled: true,
      sentClearMs: 2200,
    }));

    act(() => result.current.send());
    await act(async () => {});
    expect(result.current.outcome).toEqual({ kind: 'error', message: 'delivery failed' });
    act(() => vi.runAllTimers());
    expect(result.current.outcome).toEqual({ kind: 'error', message: 'delivery failed' });
  });

  it('preserves delivered-with-clear-warning as a persistent outcome', async () => {
    const { result } = renderHook(() => useAnnotationSend({
      send: async () => ({ kind: 'warning' as const, message: 'draft clear failed' }),
      shortcutId: 'markdown.sendAnnotations',
      enabled: true,
      sentClearMs: 4000,
    }));

    act(() => result.current.send());
    await act(async () => {});
    expect(result.current.outcome).toEqual({ kind: 'warning', message: 'draft clear failed' });
    act(() => vi.runAllTimers());
    expect(result.current.outcome).toEqual({ kind: 'warning', message: 'draft clear failed' });
  });
});
