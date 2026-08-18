import { useState, useEffect, useCallback, useRef } from 'react';
import { useSettings } from '../contexts/SettingsContext';

const SETTINGS_KEY = 'gardenScale';
const MIN_SCALE = 0.7;
const MAX_SCALE = 1.5;
const SCALE_STEP = 0.1;

function clamp(value: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, Math.round(value * 10) / 10));
}

/**
 * Font scale for the garden surfaces, independent of the app-wide uiScale.
 * `null` means "match app": no override is stored and the `--garden-scale` CSS
 * variable falls back to `var(--ui-scale)`.
 *
 * `appScale` is the current uiScale, used as the starting point when the user
 * first steps away from "match app".
 */
export function useGardenScale(appScale: number) {
  const { settings, setSetting } = useSettings();
  const initializedFromSettings = useRef(false);

  const [scale, setScale] = useState<number | null>(null);

  // Sync from daemon settings when they arrive. Persistence happens in the
  // action callbacks below, so a synced value is never echoed back.
  useEffect(() => {
    if (settings[SETTINGS_KEY] && !initializedFromSettings.current) {
      const parsed = parseFloat(settings[SETTINGS_KEY]);
      if (!isNaN(parsed) && parsed >= MIN_SCALE && parsed <= MAX_SCALE) {
        setScale(parsed);
        initializedFromSettings.current = true;
      }
    }
  }, [settings]);

  // Apply the CSS variable to the document root; removing it lets the
  // stylesheet fallback (var(--ui-scale)) take over.
  useEffect(() => {
    if (scale === null) {
      document.documentElement.style.removeProperty('--garden-scale');
    } else {
      document.documentElement.style.setProperty('--garden-scale', scale.toString());
    }
  }, [scale]);

  const applyScale = useCallback(
    (next: number | null) => {
      setScale(next);
      setSetting(SETTINGS_KEY, next === null ? '' : next.toString());
    },
    [setSetting],
  );

  const increaseScale = useCallback(() => {
    applyScale(clamp((scale ?? appScale) + SCALE_STEP));
  }, [applyScale, scale, appScale]);

  const decreaseScale = useCallback(() => {
    applyScale(clamp((scale ?? appScale) - SCALE_STEP));
  }, [applyScale, scale, appScale]);

  const matchApp = useCallback(() => {
    applyScale(null);
  }, [applyScale]);

  return {
    /** The stored override, or null when the garden matches the app scale. */
    scale,
    /** What the garden actually renders at right now. */
    effectiveScale: scale ?? appScale,
    increaseScale,
    decreaseScale,
    matchApp,
  };
}
