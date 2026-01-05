import { useState, useEffect, useCallback, useRef } from 'react';
import { useSettings } from '../contexts/SettingsContext';

const SETTINGS_KEY = 'uiScale';
const DEFAULT_SCALE = 1.0;
const MIN_SCALE = 0.7;
const MAX_SCALE = 1.5;
const SCALE_STEP = 0.1;

export function useUIScale() {
  const { settings, setSetting } = useSettings();
  const initializedFromSettings = useRef(false);

  const [scale, setScale] = useState<number>(DEFAULT_SCALE);

  // Sync from daemon settings when they arrive
  useEffect(() => {
    if (settings[SETTINGS_KEY] && !initializedFromSettings.current) {
      const parsed = parseFloat(settings[SETTINGS_KEY]);
      if (!isNaN(parsed) && parsed >= MIN_SCALE && parsed <= MAX_SCALE) {
        setScale(parsed);
        initializedFromSettings.current = true;
      }
    }
  }, [settings]);

  // Persist to daemon settings when scale changes (skip initial sync)
  const lastSavedScale = useRef<number | null>(null);
  useEffect(() => {
    // Don't save if this is the initial value from settings
    if (lastSavedScale.current !== null && scale !== lastSavedScale.current) {
      setSetting(SETTINGS_KEY, scale.toString());
    }
    lastSavedScale.current = scale;
  }, [scale, setSetting]);

  // Apply CSS variable to document root
  useEffect(() => {
    document.documentElement.style.setProperty('--ui-scale', scale.toString());
  }, [scale]);

  const increaseScale = useCallback(() => {
    setScale(prev => Math.min(MAX_SCALE, Math.round((prev + SCALE_STEP) * 10) / 10));
  }, []);

  const decreaseScale = useCallback(() => {
    setScale(prev => Math.max(MIN_SCALE, Math.round((prev - SCALE_STEP) * 10) / 10));
  }, []);

  const resetScale = useCallback(() => {
    setScale(DEFAULT_SCALE);
  }, []);

  return {
    scale,
    increaseScale,
    decreaseScale,
    resetScale,
  };
}
