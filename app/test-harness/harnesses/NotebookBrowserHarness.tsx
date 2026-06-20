/**
 * NotebookBrowser Test Harness
 *
 * Renders the whole notebook modal (lazy filesystem tree + single live-editor
 * document pane) with mocked daemon functions, in a real browser, so the fs-backed
 * layout and the CodeMirror live editor can be exercised and eyeballed together.
 * Edits are recorded via the writeFile mock so the autosave path is observable.
 */
import { useCallback, useEffect } from 'react';
// Pull in the app's design tokens (--color-*, --accent) so the harness renders with
// the real theme — without this the editor/modal fall back to undefined variables and
// the screenshot isn't color-representative.
import '../../src/App.css';
import { NotebookBrowser } from '../../src/components/NotebookBrowser';
import type {
  FsEntry,
  FsReadResult,
  FsWriteResult,
  NotebookEntry,
  NotebookSendToChiefResult,
  NotebookTask,
} from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';

// A small fixture filesystem: a PARA-shaped notebook root with nested folders, a
// markdown index, a plain text file, and a binary file (placeholder). listDir
// returns the immediate children of a directory ('' = root).
const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'journal', name: 'journal', isDir: true, size: 0 },
    { path: 'knowledge', name: 'knowledge', isDir: true, size: 0 },
    { path: 'notes.txt', name: 'notes.txt', isDir: false, size: 64 },
    { path: 'cover.png', name: 'cover.png', isDir: false, size: 4096 },
  ],
  journal: [{ path: 'journal/2026-06-20.md', name: '2026-06-20.md', isDir: false, size: 20 }],
  knowledge: [
    { path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 128 },
    { path: 'knowledge/areas', name: 'areas', isDir: true, size: 0 },
  ],
  'knowledge/areas': [{ path: 'knowledge/areas/foo.md', name: 'foo.md', isDir: false, size: 30 }],
};

const CONTENT: Record<string, string> = {
  'knowledge/index.md': `# Knowledge index

The distilled map of the notebook. A paragraph with **bold**, *italic*,
\`inline code\`, and a [wiki link](/knowledge/areas/foo.md) to another note.

## Sections

- areas — long-lived responsibilities
- resources — reference material
`,
  'notes.txt': 'Plain text scratch file.\nNo markdown affordances here — just edit and autosave.\n',
};

export function NotebookBrowserHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const listDir = useCallback(async (path: string): Promise<FsEntry[]> => TREE[path] ?? [], []);
  const readFile = useCallback(async (path: string): Promise<FsReadResult> => {
    return { path, content: CONTENT[path] ?? `# ${path}\n\nSample body.`, hash: 'h1' };
  }, []);
  const backlinksNotebook = useCallback(async (): Promise<NotebookEntry[]> => [
    { path: 'journal/2026-06-20.md', type: 'journal', title: '2026-06-20', size: 20 },
  ], []);
  const writeFile = useCallback(async (path: string, content: string, baseHash?: string): Promise<FsWriteResult> => {
    window.__HARNESS__.recordCall('writeFile', [path, content, baseHash]);
    return { path, hash: 'h2', conflict: false };
  }, []);
  const sendToChief = useCallback(async (selection: string, sourcePath?: string): Promise<NotebookSendToChiefResult> => {
    window.__HARNESS__.recordCall('sendToChief', [selection, sourcePath]);
    return { path: 'inbox.md', nudged: false };
  }, []);
  const listTasks = useCallback(async (): Promise<NotebookTask[]> => [], []);
  const retryTask = useCallback(async (): Promise<NotebookTask | null> => null, []);

  useEffect(() => {
    // The app defaults to the dark theme (useTheme DEFAULT_PREFERENCE); force it here
    // so the harness is deterministic regardless of the runner's OS color scheme.
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <NotebookBrowser
      isOpen
      onClose={() => window.__HARNESS__.recordCall('close', [])}
      listDir={listDir}
      readFile={readFile}
      backlinksNotebook={backlinksNotebook}
      writeFile={writeFile}
      sendToChief={sendToChief}
      changeSignal={0}
      listTasks={listTasks}
      retryTask={retryTask}
      taskChangeSignal={0}
    />
  );
}
