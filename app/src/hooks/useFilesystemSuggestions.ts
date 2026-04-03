import { useState, useEffect, useCallback, useRef } from 'react';
import type { BrowseDirectoryResult } from './useDaemonSocket';

interface FilesystemSuggestion {
  name: string;
  path: string;
}

interface UseFilesystemSuggestionsResult {
  suggestions: FilesystemSuggestion[];
  loading: boolean;
  error: string | null;
  currentDir: string;
  homePath: string;
}

function contractPath(path: string, homePath: string): string {
  if (!path || !homePath) {
    return path;
  }
  if (path === homePath) {
    return '~';
  }
  if (path.startsWith(homePath + '/')) {
    return '~' + path.slice(homePath.length);
  }
  return path;
}

export function useFilesystemSuggestions(
  inputPath: string,
  endpointId: string | undefined,
  browseDirectory?: (inputPath: string, endpointId?: string) => Promise<BrowseDirectoryResult>,
): UseFilesystemSuggestionsResult {
  const [suggestions, setSuggestions] = useState<FilesystemSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [currentDir, setCurrentDir] = useState('');
  const [homePath, setHomePath] = useState('');
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

      const nextHomePath = result.home_path || '';
      if (nextHomePath) {
        setHomePath(nextHomePath);
      }
      setCurrentDir(contractPath(result.directory, nextHomePath));
      setSuggestions((result.entries || []).map((entry) => ({
        name: entry.name,
        path: contractPath(entry.path, nextHomePath) + '/',
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
  }, [browseDirectory]);

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

  return { suggestions, loading, error, currentDir, homePath };
}
