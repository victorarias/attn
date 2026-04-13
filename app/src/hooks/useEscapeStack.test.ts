import { renderHook, fireEvent } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useEscapeStack, _resetEscapeStackForTest } from './useEscapeStack';

afterEach(() => {
  _resetEscapeStackForTest();
});

describe('useEscapeStack', () => {
  it('calls the handler when Escape is pressed', () => {
    const handler = vi.fn();
    renderHook(() => useEscapeStack(handler, true));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('does not call the handler when disabled', () => {
    const handler = vi.fn();
    renderHook(() => useEscapeStack(handler, false));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('ignores non-Escape keys', () => {
    const handler = vi.fn();
    renderHook(() => useEscapeStack(handler, true));

    fireEvent.keyDown(window, { key: 'Enter' });
    fireEvent.keyDown(window, { key: 'ArrowDown' });
    expect(handler).not.toHaveBeenCalled();
  });

  it('calls only the top handler (LIFO)', () => {
    const first = vi.fn();
    const second = vi.fn();
    renderHook(() => useEscapeStack(first, true));
    renderHook(() => useEscapeStack(second, true));

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(second).toHaveBeenCalledTimes(1);
    expect(first).not.toHaveBeenCalled();
  });

  it('falls through to the previous handler after the top is removed', () => {
    const first = vi.fn();
    const second = vi.fn();
    renderHook(() => useEscapeStack(first, true));
    const { unmount } = renderHook(() => useEscapeStack(second, true));

    unmount();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).not.toHaveBeenCalled();
  });

  it('always calls the latest handler reference', () => {
    let count = 0;
    const getHandler = () => () => { count++; };

    const { rerender } = renderHook(({ h }) => useEscapeStack(h, true), {
      initialProps: { h: getHandler() },
    });
    rerender({ h: getHandler() });
    rerender({ h: getHandler() });

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(count).toBe(1); // called exactly once, not three times
  });
});
