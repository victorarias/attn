import { useState, useEffect, useCallback, useRef } from 'react';
import { useSettings } from '../contexts/SettingsContext';

export type ThemePreference = 'dark' | 'light' | 'system';
export type ResolvedTheme = 'dark' | 'light';

const SETTINGS_KEY = 'theme';
const DEFAULT_PREFERENCE: ThemePreference = 'dark';

function isValidPreference(value: string): value is ThemePreference {
  return value === 'dark' || value === 'light' || value === 'system';
}

function getSystemTheme(): ResolvedTheme {
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

export function useTheme() {
  const { settings, setSetting } = useSettings();
  const initializedFromSettings = useRef(false);

  const [preference, setPreference] = useState<ThemePreference>(DEFAULT_PREFERENCE);
  // resolved is state so OS theme changes trigger terminal re-renders.
  const [resolved, setResolved] = useState<ResolvedTheme>('dark');

  // Sync from daemon settings when they arrive
  useEffect(() => {
    if (settings[SETTINGS_KEY] && !initializedFromSettings.current) {
      const value = settings[SETTINGS_KEY];
      if (isValidPreference(value)) {
        setPreference(value);
        initializedFromSettings.current = true;
      }
    }
  }, [settings]);

  // Persist to daemon settings when preference changes (skip initial sync)
  const lastSavedPreference = useRef<ThemePreference | null>(null);
  useEffect(() => {
    if (lastSavedPreference.current !== null && preference !== lastSavedPreference.current) {
      setSetting(SETTINGS_KEY, preference);
    }
    lastSavedPreference.current = preference;
  }, [preference, setSetting]);

  // Apply data-theme attribute and update resolved theme
  // - "system": remove data-theme, let CSS @media handle it; resolve the terminal theme via matchMedia.
  // - "dark"/"light": set data-theme explicitly
  useEffect(() => {
    if (preference === 'system') {
      document.documentElement.removeAttribute('data-theme');
      setResolved(getSystemTheme());
    } else {
      document.documentElement.setAttribute('data-theme', preference);
      setResolved(preference);
    }
  }, [preference]);

  // Listen for OS theme changes (only matters for terminal rendering when preference is "system";
  // CSS variables update automatically via @media query)
  useEffect(() => {
    if (preference !== 'system') return;

    const mediaQuery = window.matchMedia('(prefers-color-scheme: light)');
    const handleChange = () => setResolved(getSystemTheme());
    mediaQuery.addEventListener('change', handleChange);
    return () => mediaQuery.removeEventListener('change', handleChange);
  }, [preference]);

  const setTheme = useCallback((newPreference: ThemePreference) => {
    setPreference(newPreference);
  }, []);

  return {
    preference,
    resolved,
    setTheme,
  };
}
