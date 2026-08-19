import { describe, expect, it, vi, afterEach } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { SettingsProvider } from '../contexts/SettingsContext';
import { useGardenScale } from './useGardenScale';

function wrapperWith(settings: Record<string, string>, setSetting = vi.fn()) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <SettingsProvider settings={settings} setSetting={setSetting}>
        {children}
      </SettingsProvider>
    );
  };
}

const cssVar = () => document.documentElement.style.getPropertyValue('--garden-scale');

afterEach(() => {
  document.documentElement.style.removeProperty('--garden-scale');
});

describe('useGardenScale', () => {
  it('matches the app by default: no override, no CSS variable', () => {
    const { result } = renderHook(() => useGardenScale(1.2), {
      wrapper: wrapperWith({}),
    });

    expect(result.current.scale).toBeNull();
    expect(result.current.effectiveScale).toBe(1.2);
    expect(cssVar()).toBe('');
  });

  it('initializes from a stored gardenScale setting and applies the CSS variable', () => {
    const { result } = renderHook(() => useGardenScale(1), {
      wrapper: wrapperWith({ gardenScale: '1.3' }),
    });

    expect(result.current.scale).toBe(1.3);
    expect(result.current.effectiveScale).toBe(1.3);
    expect(cssVar()).toBe('1.3');
  });

  it('steps from the current app scale when first leaving match-app, and persists', () => {
    const setSetting = vi.fn();
    const { result } = renderHook(() => useGardenScale(1.1), {
      wrapper: wrapperWith({}, setSetting),
    });

    act(() => result.current.increaseScale());

    expect(result.current.scale).toBe(1.2);
    expect(cssVar()).toBe('1.2');
    expect(setSetting).toHaveBeenCalledWith('gardenScale', '1.2');
  });

  it('clamps at the bounds', () => {
    const { result } = renderHook(() => useGardenScale(1), {
      wrapper: wrapperWith({ gardenScale: '1.5' }),
    });

    act(() => result.current.increaseScale());
    expect(result.current.scale).toBe(1.5);

    const low = renderHook(() => useGardenScale(1), {
      wrapper: wrapperWith({ gardenScale: '0.7' }),
    });
    act(() => low.result.current.decreaseScale());
    expect(low.result.current.scale).toBe(0.7);
  });

  it('match app clears the override, removes the CSS variable, and persists an empty value', () => {
    const setSetting = vi.fn();
    const { result } = renderHook(() => useGardenScale(1), {
      wrapper: wrapperWith({ gardenScale: '1.3' }, setSetting),
    });
    expect(cssVar()).toBe('1.3');

    act(() => result.current.matchApp());

    expect(result.current.scale).toBeNull();
    expect(cssVar()).toBe('');
    expect(setSetting).toHaveBeenCalledWith('gardenScale', '');
  });

  it('does not persist the value it just synced from settings', () => {
    const setSetting = vi.fn();
    renderHook(() => useGardenScale(1), {
      wrapper: wrapperWith({ gardenScale: '1.3' }, setSetting),
    });

    expect(setSetting).not.toHaveBeenCalled();
  });
});
