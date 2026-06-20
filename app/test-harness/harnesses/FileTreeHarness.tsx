/**
 * FileTree Test Harness
 *
 * Renders the lazy filesystem tree in a real browser against an in-memory fixture
 * tree, so the lazy expand/collapse, selection highlight, and theming can be
 * exercised and eyeballed. listDir resolves from the fixture with a small delay so
 * the "Loading…" state is observable; every call is recorded for assertions.
 */
import { useCallback, useEffect, useState } from 'react';
// Pull in the app's design tokens (--color-*, --accent) so the tree renders with the
// real theme rather than undefined variables.
import '../../src/App.css';
import { FileTree } from '../../src/components/notebook/FileTree';
import type { FsEntry } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';

// A small fixture filesystem: a PARA-shaped notebook root with a couple of nested
// folders and files. listDir returns the immediate children of a directory.
const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'areas', name: 'areas', isDir: true, size: 0 },
    { path: 'resources', name: 'resources', isDir: true, size: 0 },
    { path: 'index.md', name: 'index.md', isDir: false, size: 128, modified: '2026-06-20T09:00:00Z' },
    { path: 'README', name: 'README', isDir: false, size: 64 },
  ],
  areas: [
    { path: 'areas/hiring', name: 'hiring', isDir: true, size: 0 },
    { path: 'areas/roadmap.md', name: 'roadmap.md', isDir: false, size: 240 },
  ],
  'areas/hiring': [{ path: 'areas/hiring/pipeline.md', name: 'pipeline.md', isDir: false, size: 96 }],
  resources: [
    { path: 'resources/links.md', name: 'links.md', isDir: false, size: 48 },
    { path: 'resources/diagram.png', name: 'diagram.png', isDir: false, size: 4096 },
  ],
};

export function FileTreeHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [selectedPath, setSelectedPath] = useState<string | null>('index.md');

  const listDir = useCallback(async (path: string): Promise<FsEntry[]> => {
    window.__HARNESS__.recordCall('listDir', [path]);
    // Small delay so the lazy "Loading…" state is visible during expansion.
    await new Promise((resolve) => setTimeout(resolve, 120));
    return TREE[path] ?? [];
  }, []);

  const onSelectFile = useCallback((path: string) => {
    window.__HARNESS__.recordCall('onSelectFile', [path]);
    setSelectedPath(path);
  }, []);

  useEffect(() => {
    // The app defaults to the dark theme; force it so the harness is deterministic
    // regardless of the runner's OS color scheme.
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ width: 320, padding: 12, background: 'var(--color-bg-app)', minHeight: '100vh' }}>
      <FileTree listDir={listDir} selectedPath={selectedPath} onSelectFile={onSelectFile} />
    </div>
  );
}
