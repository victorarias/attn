import { useState, useEffect, useCallback } from 'react';

const STORAGE_KEY = 'uiScale';
const DEFAULT_SCALE = 1.0;
const MIN_SCALE = 0.7;
const MAX_SCALE = 1.5;
const SCALE_STEP = 0.1;

export function useUIScale() {
  const [scale, setScale] = useState<number>(() => {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) {
      const parsed = parseFloat(stored);
      if (!isNaN(parsed) && parsed >= MIN_SCALE && parsed <= MAX_SCALE) {
        return parsed;
      }
    }
    return DEFAULT_SCALE;
  });

  // Persist to localStorage
  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, scale.toString());
  }, [scale]);

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
