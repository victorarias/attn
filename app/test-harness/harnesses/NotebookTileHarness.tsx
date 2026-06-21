/**
 * NotebookTile Test Harness
 *
 * Renders the `tile` shape of the Notebook (a NotebookTile fed by a mocked
 * NotebookSurfaceProvider) inside a width-controllable frame, so an e2e can verify
 * the tile renders the live surface and folds its rail/tree responsively as the
 * frame narrows. The frame width is set via window.__setTileWidth(px).
 */
import { useCallback, useMemo, useState, useEffect } from 'react';
// Pull in the app's design tokens so the surface renders with the real theme.
import '../../src/App.css';
import { NotebookSurfaceProvider, type NotebookSurfaceDaemon } from '../../src/contexts/NotebookSurfaceContext';
import { NotebookTile } from '../../src/components/notebook/NotebookTile';
import type { FsEntry, FsExistsResult, FsReadResult, FsWriteResult, NotebookEntry, NotebookSendToChiefResult, NotebookTask } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';

const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'journal', name: 'journal', isDir: true, size: 0 },
    { path: 'knowledge', name: 'knowledge', isDir: true, size: 0 },
    { path: 'notes.txt', name: 'notes.txt', isDir: false, size: 64 },
  ],
  journal: [{ path: 'journal/2026-06-20.md', name: '2026-06-20.md', isDir: false, size: 20 }],
  knowledge: [{ path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 128 }],
};

const CONTENT: Record<string, string> = {
  'knowledge/index.md': `# Knowledge index

The distilled map of the notebook, with a [wiki link](/journal/2026-06-20.md).

## Sections

- areas — long-lived responsibilities
`,
};

declare global {
  interface Window {
    __setTileWidth?: (px: number) => void;
  }
}

export function NotebookTileHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [width, setWidth] = useState(1100);

  const listDir = useCallback(async (path: string): Promise<FsEntry[]> => TREE[path] ?? [], []);
  const readFile = useCallback(async (path: string): Promise<FsReadResult> => ({
    path,
    content: CONTENT[path] ?? `# ${path}\n\nSample body.`,
    hash: 'h1',
  }), []);
  const writeFile = useCallback(async (path: string, content: string, baseHash?: string): Promise<FsWriteResult> => {
    window.__HARNESS__.recordCall('writeFile', [path, content, baseHash]);
    return { path, hash: 'h2', conflict: false };
  }, []);
  const existsFile = useCallback(async (path: string): Promise<FsExistsResult> => ({ path, exists: true }), []);
  const backlinksNotebook = useCallback(async (): Promise<NotebookEntry[]> => [], []);
  const sendToChief = useCallback(async (selection: string, sourcePath?: string): Promise<NotebookSendToChiefResult> => {
    window.__HARNESS__.recordCall('sendToChief', [selection, sourcePath]);
    return { path: 'inbox.md', nudged: false };
  }, []);
  const listTasks = useCallback(async (): Promise<NotebookTask[]> => [], []);
  const retryTask = useCallback(async (): Promise<NotebookTask | null> => null, []);

  const daemon = useMemo<NotebookSurfaceDaemon>(() => ({
    listDir,
    readFile,
    writeFile,
    existsFile,
    backlinksNotebook,
    sendToChief,
    listTasks,
    retryTask,
    changeSignal: 0,
    taskChangeSignal: 0,
  }), [listDir, readFile, writeFile, existsFile, backlinksNotebook, sendToChief, listTasks, retryTask]);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', 'dark');
    window.__setTileWidth = (px: number) => setWidth(px);
    setTriggerRerender(() => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ position: 'fixed', inset: 0, display: 'flex', background: 'var(--color-bg-app)' }}>
      <div style={{ width, height: '100%', overflow: 'hidden' }} data-testid="notebook-tile-frame">
        <NotebookSurfaceProvider value={daemon}>
          <NotebookTile
            initialPath="knowledge/index.md"
            onOpenFile={(path) => window.__HARNESS__.recordCall('openFile', [path])}
          />
        </NotebookSurfaceProvider>
      </div>
    </div>
  );
}
