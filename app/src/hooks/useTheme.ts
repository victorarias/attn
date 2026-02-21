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

function resolveTheme(preference: ThemePreference): ResolvedTheme {
  if (preference === 'system') return getSystemTheme();
  return preference;
}

export function useTheme() {
  const { settings, setSetting } = useSettings();
  const initializedFromSettings = useRef(false);

  const [preference, setPreference] = useState<ThemePreference>(DEFAULT_PREFERENCE);

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

  const resolved = resolveTheme(preference);

  // Apply data-theme attribute to <html>
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', resolved);
  }, [resolved]);

  // Listen for system theme changes when preference is 'system'
  useEffect(() => {
    if (preference !== 'system') return;

    const mediaQuery = window.matchMedia('(prefers-color-scheme: light)');
    const handleChange = () => {
      document.documentElement.setAttribute('data-theme', getSystemTheme());
    };
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
