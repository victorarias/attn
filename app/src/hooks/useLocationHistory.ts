// app/src/hooks/useLocationHistory.ts
import { useState, useEffect, useCallback, useRef } from 'react';
import {
  readTextFile,
  writeTextFile,
  exists,
  mkdir,
  BaseDirectory,
} from '@tauri-apps/plugin-fs';

const HISTORY_FILE = 'location-history.json';
const MAX_HISTORY = 20;

interface LocationEntry {
  path: string;
  label: string;
  lastUsed: number;
}

export function useLocationHistory() {
  const [history, setHistory] = useState<LocationEntry[]>([]);
  const [loaded, setLoaded] = useState(false);
  const saveTimeoutRef = useRef<number | null>(null);

  // Load from file on mount and clean up stale entries
  useEffect(() => {
    async function loadHistory() {
      try {
        // Ensure app data directory exists
        const dirExists = await exists('', { baseDir: BaseDirectory.AppData });
        if (!dirExists) {
          await mkdir('', { baseDir: BaseDirectory.AppData, recursive: true });
        }

        const fileExists = await exists(HISTORY_FILE, { baseDir: BaseDirectory.AppData });
        if (fileExists) {
          const content = await readTextFile(HISTORY_FILE, { baseDir: BaseDirectory.AppData });
          const parsed = JSON.parse(content);
          if (Array.isArray(parsed)) {
            // Filter out entries where the path no longer exists
            const validEntries: LocationEntry[] = [];
            for (const entry of parsed) {
              try {
                // Check if the path exists - filter out deleted directories
                const pathExists = await exists(entry.path);
                if (pathExists) {
                  validEntries.push(entry);
                } else {
                  console.log('[LocationHistory] Filtering out non-existent path:', entry.path);
                }
              } catch (err) {
                // If we can't check, DON'T keep the entry - safer to remove it
                // This prevents deleted worktrees from lingering if exists() fails
                console.warn('[LocationHistory] Could not check path, removing:', entry.path, err);
              }
            }
            setHistory(validEntries);
          }
        }
      } catch (e) {
        console.error('Failed to load location history:', e);
      } finally {
        setLoaded(true);
      }
    }
    loadHistory();
  }, []);

  // Save to file when history changes (debounced)
  useEffect(() => {
    if (!loaded) return; // Don't save before initial load

    if (saveTimeoutRef.current) {
      clearTimeout(saveTimeoutRef.current);
    }

    saveTimeoutRef.current = window.setTimeout(async () => {
      try {
        await writeTextFile(HISTORY_FILE, JSON.stringify(history, null, 2), {
          baseDir: BaseDirectory.AppData,
        });
      } catch (e) {
        console.error('Failed to save location history:', e);
      }
    }, 500);

    return () => {
      if (saveTimeoutRef.current) {
        clearTimeout(saveTimeoutRef.current);
      }
    };
  }, [history, loaded]);

  const addToHistory = useCallback((path: string) => {
    const label = path.split('/').pop() || path;
    setHistory((prev) => {
      const filtered = prev.filter((e) => e.path !== path);
      const newEntry: LocationEntry = { path, label, lastUsed: Date.now() };
      return [newEntry, ...filtered].slice(0, MAX_HISTORY);
    });
  }, []);

  const removeFromHistory = useCallback((path: string) => {
    setHistory((prev) => {
      const newHistory = prev.filter((e) => e.path !== path);
      // Save immediately when removing (don't wait for debounce)
      // This ensures the file is updated before another component reads it
      writeTextFile(HISTORY_FILE, JSON.stringify(newHistory, null, 2), {
        baseDir: BaseDirectory.AppData,
      }).catch((e) => console.error('Failed to save location history after removal:', e));
      return newHistory;
    });
  }, []);

  const getRecentLocations = useCallback(() => {
    return [...history].sort((a, b) => b.lastUsed - a.lastUsed);
  }, [history]);

  return { history, addToHistory, removeFromHistory, getRecentLocations };
}
