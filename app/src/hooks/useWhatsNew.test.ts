// app/src/hooks/useWhatsNew.test.ts
import { describe, it, expect, beforeEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useWhatsNew, WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY as STORAGE_KEY } from './useWhatsNew';

describe('useWhatsNew', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('opens on first run when nothing has been seen', () => {
    const { result } = renderHook(() => useWhatsNew());
    expect(result.current.isOpen).toBe(true);
  });

  it('opens when a previous, older release was seen', () => {
    window.localStorage.setItem(STORAGE_KEY, 'something-older');
    const { result } = renderHook(() => useWhatsNew());
    expect(result.current.isOpen).toBe(true);
  });

  it('stays closed when the current release was already seen', () => {
    window.localStorage.setItem(STORAGE_KEY, WHATS_NEW_ID);
    const { result } = renderHook(() => useWhatsNew());
    expect(result.current.isOpen).toBe(false);
  });

  it('dismiss closes the modal and remembers the current release', () => {
    const { result } = renderHook(() => useWhatsNew());
    expect(result.current.isOpen).toBe(true);

    act(() => result.current.dismiss());

    expect(result.current.isOpen).toBe(false);
    expect(window.localStorage.getItem(STORAGE_KEY)).toBe(WHATS_NEW_ID);

    // A fresh mount no longer auto-opens.
    const second = renderHook(() => useWhatsNew());
    expect(second.result.current.isOpen).toBe(false);
  });

  it('open re-shows the modal on demand', () => {
    window.localStorage.setItem(STORAGE_KEY, WHATS_NEW_ID);
    const { result } = renderHook(() => useWhatsNew());
    expect(result.current.isOpen).toBe(false);

    act(() => result.current.open());
    expect(result.current.isOpen).toBe(true);
  });
});
