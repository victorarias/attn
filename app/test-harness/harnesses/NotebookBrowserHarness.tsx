/**
 * NotebookBrowser Test Harness
 *
 * Renders the whole notebook modal (sidebar + single live-editor document pane)
 * with mocked daemon functions, in a real browser, so the redesigned layout and the
 * CodeMirror live editor can be exercised and eyeballed together. Edits are recorded
 * via the writeNotebook mock so the autosave path is observable.
 */
import { useCallback, useEffect } from 'react';
import { NotebookBrowser } from '../../src/components/NotebookBrowser';
import type {
  NotebookEntry,
  NotebookReadResult,
  NotebookSendToChiefResult,
  NotebookTask,
  NotebookWriteResult,
} from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';

const ENTRIES: NotebookEntry[] = [
  { path: 'knowledge/index.md', type: 'note', title: 'Knowledge index', size: 10 },
  { path: 'journal/2026-06-20.md', type: 'journal', title: '2026-06-20', size: 20 },
  { path: 'knowledge/areas/foo.md', type: 'note', title: 'Foo decision', size: 30 },
];

const CONTENT: Record<string, string> = {
  'knowledge/index.md': `# Knowledge index

The distilled map of the notebook. A paragraph with **bold**, *italic*,
\`inline code\`, and a [wiki link](/knowledge/areas/foo.md) to another note.

## Sections

- areas — long-lived responsibilities
- resources — reference material
`,
};

export function NotebookBrowserHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const listNotebook = useCallback(async () => ENTRIES, []);
  const readNotebook = useCallback(async (path: string): Promise<NotebookReadResult> => {
    return { path, content: CONTENT[path] ?? `# ${path}\n\nSample note body.`, hash: 'h1' };
  }, []);
  const backlinksNotebook = useCallback(async (): Promise<NotebookEntry[]> => [
    { path: 'journal/2026-06-20.md', type: 'journal', title: '2026-06-20', size: 20 },
  ], []);
  const writeNotebook = useCallback(async (path: string, content: string, baseHash?: string): Promise<NotebookWriteResult> => {
    window.__HARNESS__.recordCall('writeNotebook', [path, content, baseHash]);
    return { path, hash: 'h2', conflict: false };
  }, []);
  const sendToChief = useCallback(async (selection: string, sourcePath?: string): Promise<NotebookSendToChiefResult> => {
    window.__HARNESS__.recordCall('sendToChief', [selection, sourcePath]);
    return { path: 'inbox.md', nudged: false };
  }, []);
  const listTasks = useCallback(async (): Promise<NotebookTask[]> => [], []);
  const retryTask = useCallback(async (): Promise<NotebookTask | null> => null, []);

  useEffect(() => {
    setTriggerRerender(() => {});
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <NotebookBrowser
      isOpen
      onClose={() => window.__HARNESS__.recordCall('close', [])}
      listNotebook={listNotebook}
      readNotebook={readNotebook}
      backlinksNotebook={backlinksNotebook}
      writeNotebook={writeNotebook}
      sendToChief={sendToChief}
      changeSignal={0}
      listTasks={listTasks}
      retryTask={retryTask}
      taskChangeSignal={0}
    />
  );
}
