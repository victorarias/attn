import { useState, useEffect, useCallback, useRef } from 'react';
import type { BrowseDirectoryResult } from './useDaemonSocket';
import { toDisplayPath } from '../utils/locationPickerPaths';

export interface FilesystemSuggestion {
  name: string;
  /** Display form (`~`-shortened) — what the picker shows and types back. */
  path: string;
  /** The path as the daemon resolved it, for callers that must open it. */
  absPath: string;
  isDir: boolean;
}

interface UseFilesystemSuggestionsResult {
  suggestions: FilesystemSuggestion[];
  loading: boolean;
  error: string | null;
  currentDir: string;
  /** Home directory of the machine that answered, once one has. */
  homePath: string;
}

export interface UseFilesystemSuggestionsOptions {
  homePath?: string;
  onHomePathChange?: (nextHomePath: string) => void;
  enabled?: boolean;
  /**
   * Dotless extensions (e.g. ['md']). Omitted, the listing is directories only —
   * the session picker's behavior. Supplied, matching files join the listing,
   * which is what the markdown opener's path mode needs.
   */
  extensions?: string[];
}

export function useFilesystemSuggestions(
  inputPath: string,
  endpointId: string | undefined,
  browseDirectory?: (inputPath: string, endpointId?: string, extensions?: string[]) => Promise<BrowseDirectoryResult>,
  options: UseFilesystemSuggestionsOptions = {},
): UseFilesystemSuggestionsResult {
  const { homePath, onHomePathChange, enabled = true, extensions } = options;
  const [suggestions, setSuggestions] = useState<FilesystemSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [currentDir, setCurrentDir] = useState('');
  const [resolvedHomePath, setResolvedHomePath] = useState(homePath || '');
  const debounceRef = useRef<number | null>(null);
  const requestIdRef = useRef(0);
  const previousEndpointIdRef = useRef<string | undefined>(endpointId);
  // Identity-stable list so a caller can pass an inline array literal without
  // re-running the effect on every render.
  const extensionsKey = (extensions || []).join(',');

  const fetchSuggestions = useCallback(async (path: string, targetEndpointId?: string) => {
    if (!enabled || !browseDirectory || !path || path.length < 1) {
      setSuggestions([]);
      setCurrentDir('');
      setError(null);
      setLoading(false);
      return;
    }

    const requestId = ++requestIdRef.current;
    setLoading(true);
    setError(null);

    try {
      const requested = extensionsKey ? extensionsKey.split(',') : undefined;
      const result = await browseDirectory(path, targetEndpointId, requested);
      if (requestIdRef.current !== requestId) {
        return;
      }

      const nextHomePath = result.home_path || homePath || '';
      if (nextHomePath) {
        onHomePathChange?.(nextHomePath);
        setResolvedHomePath(nextHomePath);
      }
      setCurrentDir(toDisplayPath(result.directory, nextHomePath));
      setSuggestions((result.entries || []).map((entry) => ({
        name: entry.name,
        path: toDisplayPath(entry.path, nextHomePath),
        absPath: entry.path,
        isDir: entry.is_dir,
      })));
    } catch (e) {
      if (requestIdRef.current !== requestId) {
        return;
      }
      console.error('[fs-suggestions] error:', e);
      setError(String(e));
      setSuggestions([]);
      setCurrentDir('');
    } finally {
      if (requestIdRef.current === requestId) {
        setLoading(false);
      }
    }
  }, [browseDirectory, enabled, extensionsKey, homePath, onHomePathChange]);

  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
    }

    if (!enabled) {
      requestIdRef.current += 1;
      setSuggestions([]);
      setCurrentDir('');
      setError(null);
      setLoading(false);
      return;
    }

    const endpointChanged = previousEndpointIdRef.current !== endpointId;
    previousEndpointIdRef.current = endpointId;

    if (endpointChanged) {
      requestIdRef.current += 1;
      setSuggestions([]);
      setCurrentDir('');
      setError(null);
      setLoading(Boolean(browseDirectory && inputPath && inputPath.length >= 1));
      void fetchSuggestions(inputPath, endpointId);
      return;
    }

    debounceRef.current = window.setTimeout(() => {
      void fetchSuggestions(inputPath, endpointId);
    }, 150);

    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, [enabled, endpointId, fetchSuggestions, inputPath]);

  return { suggestions, loading, error, currentDir, homePath: resolvedHomePath };
}
