// app/src/hooks/useLocationHistory.ts
import { useState, useEffect, useCallback } from 'react';

const STORAGE_KEY = 'attn-location-history';
const MAX_HISTORY = 20;

interface LocationEntry {
  path: string;
  label: string;
  lastUsed: number;
}

export function useLocationHistory() {
  const [history, setHistory] = useState<LocationEntry[]>([]);

  // Load from localStorage on mount
  useEffect(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY);
      if (stored) {
        setHistory(JSON.parse(stored));
      }
    } catch (e) {
      console.error('Failed to load location history:', e);
    }
  }, []);

  // Save to localStorage when history changes
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(history));
    } catch (e) {
      console.error('Failed to save location history:', e);
    }
  }, [history]);

  const addToHistory = useCallback((path: string) => {
    const label = path.split('/').pop() || path;
    setHistory((prev) => {
      const filtered = prev.filter((e) => e.path !== path);
      const newEntry: LocationEntry = { path, label, lastUsed: Date.now() };
      return [newEntry, ...filtered].slice(0, MAX_HISTORY);
    });
  }, []);

  const getRecentLocations = useCallback(() => {
    return [...history].sort((a, b) => b.lastUsed - a.lastUsed);
  }, [history]);

  return { history, addToHistory, getRecentLocations };
}
