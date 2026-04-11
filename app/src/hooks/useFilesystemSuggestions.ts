import { useState, useEffect, useCallback, useRef } from 'react';
import type { BrowseDirectoryResult } from './useDaemonSocket';
import { toDisplayPath } from '../utils/locationPickerPaths';

interface FilesystemSuggestion {
  name: string;
  path: string;
}

interface UseFilesystemSuggestionsResult {
  suggestions: FilesystemSuggestion[];
  loading: boolean;
  error: string | null;
  currentDir: string;
}

export function useFilesystemSuggestions(
  inputPath: string,
  endpointId: string | undefined,
  browseDirectory?: (inputPath: string, endpointId?: string) => Promise<BrowseDirectoryResult>,
  homePath?: string,
  onHomePathChange?: (nextHomePath: string) => void,
): UseFilesystemSuggestionsResult {
  const [suggestions, setSuggestions] = useState<FilesystemSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [currentDir, setCurrentDir] = useState('');
  const debounceRef = useRef<number | null>(null);
  const requestIdRef = useRef(0);
  const previousEndpointIdRef = useRef<string | undefined>(endpointId);

  const fetchSuggestions = useCallback(async (path: string, targetEndpointId?: string) => {
    if (!browseDirectory || !path || path.length < 1) {
      setSuggestions([]);
      setCurrentDir('');
      return;
    }

    const requestId = ++requestIdRef.current;
    setLoading(true);
    setError(null);

    try {
      const result = await browseDirectory(path, targetEndpointId);
      if (requestIdRef.current !== requestId) {
        return;
      }

      const nextHomePath = result.home_path || homePath || '';
      if (nextHomePath) {
        onHomePathChange?.(nextHomePath);
      }
      setCurrentDir(toDisplayPath(result.directory, nextHomePath));
      setSuggestions((result.entries || []).map((entry) => ({
        name: entry.name,
        path: toDisplayPath(entry.path, nextHomePath),
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
  }, [browseDirectory, homePath, onHomePathChange]);

  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
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
  }, [endpointId, fetchSuggestions, inputPath]);

  return { suggestions, loading, error, currentDir };
}
