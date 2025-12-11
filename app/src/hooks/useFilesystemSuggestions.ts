// app/src/hooks/useFilesystemSuggestions.ts
import { useState, useEffect, useCallback, useRef } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { homeDir } from '@tauri-apps/api/path';

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

export function useFilesystemSuggestions(inputPath: string): UseFilesystemSuggestionsResult {
  const [suggestions, setSuggestions] = useState<FilesystemSuggestion[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [currentDir, setCurrentDir] = useState('');
  const [homePath, setHomePath] = useState('/Users');
  const debounceRef = useRef<number | null>(null);

  // Get home directory on mount
  useEffect(() => {
    homeDir().then((dir) => {
      setHomePath(dir.replace(/\/$/, ''));
    }).catch(() => {});
  }, []);

  const fetchSuggestions = useCallback(async (path: string) => {
    if (!path || path.length < 1) {
      setSuggestions([]);
      setCurrentDir('');
      return;
    }

    // Parse input to determine directory to query
    let dirToQuery: string;
    let prefix: string;

    // Expand ~ to home directory
    let expandedPath: string;
    if (path === '~') {
      expandedPath = homePath;
    } else if (path.startsWith('~/')) {
      expandedPath = homePath + path.slice(1); // ~/foo -> /Users/x/foo
    } else if (path.startsWith('~')) {
      expandedPath = homePath + '/' + path.slice(1); // ~foo -> /Users/x/foo
    } else {
      expandedPath = path;
    }

    if (expandedPath.endsWith('/')) {
      // User typed "/Users/" - query that directory
      dirToQuery = expandedPath;
      prefix = '';
    } else {
      // User typed "/Users/jo" - query parent, filter by "jo"
      const lastSlash = expandedPath.lastIndexOf('/');
      if (lastSlash === -1) {
        setSuggestions([]);
        return;
      }
      dirToQuery = expandedPath.slice(0, lastSlash + 1) || '/';
      prefix = expandedPath.slice(lastSlash + 1).toLowerCase();
    }

    setCurrentDir(dirToQuery.replace(homePath, '~'));
    setLoading(true);
    setError(null);

    try {
      // Pass prefix to Rust side so filtering happens before truncation
      const dirs = await invoke<string[]>('list_directory', {
        path: dirToQuery,
        prefix: prefix || null,
      });

      setSuggestions(dirs.map(name => ({
        name,
        path: dirToQuery + name,
      })));
    } catch (e) {
      console.error('[fs-suggestions] error:', e);
      setError(String(e));
      setSuggestions([]);
    } finally {
      setLoading(false);
    }
  }, [homePath]);

  // Debounced fetch
  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
    }

    debounceRef.current = window.setTimeout(() => {
      fetchSuggestions(inputPath);
    }, 150);

    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, [inputPath, fetchSuggestions]);

  return { suggestions, loading, error, currentDir };
}
