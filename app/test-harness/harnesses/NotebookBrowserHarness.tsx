/**
 * NotebookBrowser Test Harness
 *
 * Renders the whole notebook modal (lazy filesystem tree + single live-editor
 * document pane) with mocked daemon functions, in a real browser, so the fs-backed
 * layout and the CodeMirror live editor can be exercised and eyeballed together.
 * Edits are recorded via the writeFile mock so the autosave path is observable.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
// Pull in the app's design tokens (--color-*, --accent) so the harness renders with
// the real theme — without this the editor/modal fall back to undefined variables and
// the screenshot isn't color-representative.
import '../../src/App.css';
import { NotebookBrowser } from '../../src/components/NotebookBrowser';
import type {
  FsEntry,
  FsExistsResult,
  FsReadAssetResult,
  FsReadResult,
  FsWriteResult,
  NotebookEntry,
  NotebookSendToChiefResult,
} from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';

// A valid, minimal 1x1 transparent PNG — the "resolvable" asset the readAsset mock
// returns for assets/tiny.png. Any other path is treated as missing.
const TINY_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

// A small fixture filesystem: a PARA-shaped notebook root with nested folders, a
// markdown index, a plain text file, and a binary file (placeholder). listDir
// returns the immediate children of a directory ('' = root).
const TREE: Record<string, FsEntry[]> = {
  '': [
    { path: 'journal', name: 'journal', isDir: true, size: 0 },
    { path: 'knowledge', name: 'knowledge', isDir: true, size: 0 },
    { path: 'notes.txt', name: 'notes.txt', isDir: false, size: 64 },
    { path: 'cover.png', name: 'cover.png', isDir: false, size: 4096 },
    // A dedicated fixture note for fence/blockquote/hr live-preview e2e coverage — kept
    // out of listFiles/TREE offsets that other tests already depend on (e.g. the Cmd+P
    // finder's fixed option count and index.md's heading/scroll offsets).
    { path: 'fences.md', name: 'fences.md', isDir: false, size: 96 },
    { path: 'images.md', name: 'images.md', isDir: false, size: 64 },
  ],
  journal: [{ path: 'journal/2026-06-20.md', name: '2026-06-20.md', isDir: false, size: 20 }],
  knowledge: [
    { path: 'knowledge/index.md', name: 'index.md', isDir: false, size: 128 },
    { path: 'knowledge/areas', name: 'areas', isDir: true, size: 0 },
  ],
  'knowledge/areas': [{ path: 'knowledge/areas/foo.md', name: 'foo.md', isDir: false, size: 30 }],
};

// A deliberately long body so the second/third headings sit well below the fold —
// the outline-jump e2e can then assert a real scroll when it clicks a lower heading.
const INDEX_FILLER = Array.from(
  { length: 30 },
  (_, i) => `Line ${i + 1}: distilled context the agent keeps around for later recall.`,
).join('\n');

const CONTENT: Record<string, string> = {
  'knowledge/index.md': `# Knowledge index

The distilled map of the notebook. A paragraph with **bold**, *italic*,
\`inline code\`, and a [wiki link](/knowledge/areas/foo.md) to another note.

${INDEX_FILLER}

## Sections

- areas — long-lived responsibilities
- resources — reference material

### Subsection detail

${INDEX_FILLER}
`,
  'images.md': `# Images

![tiny](assets/tiny.png)

![gone](assets/missing.png)
`,
  'notes.txt': 'Plain text scratch file.\nNo markdown affordances here — just edit and autosave.\n',
  'fences.md': `# Fences

A fenced code block, a blockquote, and a horizontal rule.

\`\`\`javascript
const x = 1; // note
\`\`\`

> quoted line

---

After the rule.

| col a | col b |
| ----- | ----: |
| one   | two   |
`,
};

// Drive an external file change the way the daemon's fs_changed would: override a
// file's bytes/hash, then bump the changeSignal so the open browser re-reads it. Kept
// off the typed HarnessAPI (a fixed shape); the e2e casts window to reach it.
interface NotebookHarnessControls {
  // Stage new bytes for `path` (so the next read returns them) and bump changeSignal.
  // Omit content to fire a no-op fs_changed (the open file re-reads unchanged).
  fsChanged: (path?: string, content?: string, hash?: string) => void;
  // The bytes a path currently reads as, so the e2e can derive a minimally-edited
  // version (an agent appending a line) rather than guessing the fixture body.
  getContent: (path: string) => string;
}
declare global {
  interface Window {
    __NB_HARNESS__?: NotebookHarnessControls;
  }
}

export function NotebookBrowserHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [changeSignal, setChangeSignal] = useState(0);
  // Per-path byte/hash overrides the e2e stages to simulate an on-disk change.
  const overridesRef = useRef<Record<string, { content: string; hash: string }>>({});

  const listDir = useCallback(async (path: string): Promise<FsEntry[]> => TREE[path] ?? [], []);
  const readFile = useCallback(async (path: string): Promise<FsReadResult> => {
    const override = overridesRef.current[path];
    if (override) return { path, content: override.content, hash: override.hash };
    return { path, content: CONTENT[path] ?? `# ${path}\n\nSample body.`, hash: 'h1' };
  }, []);
  const backlinksNotebook = useCallback(async (): Promise<NotebookEntry[]> => [
    { path: 'journal/2026-06-20.md', type: 'journal', title: '2026-06-20', size: 20 },
  ], []);
  const writeFile = useCallback(async (path: string, content: string, baseHash?: string): Promise<FsWriteResult> => {
    window.__HARNESS__.recordCall('writeFile', [path, content, baseHash]);
    return { path, hash: 'h2', conflict: false };
  }, []);
  const existsFile = useCallback(async (path: string): Promise<FsExistsResult> => {
    // Everything in the fixture tree exists; the index's wiki link resolves, so no
    // broken-link flag fires here (broken links have their own dedicated harness).
    return { path, exists: true };
  }, []);
  // Serves the live editor's inline image widget: assets/tiny.png resolves to a real
  // (tiny) PNG; anything else — e.g. assets/missing.png — rejects, so the widget
  // renders its broken placeholder.
  const readAsset = useCallback(async (path: string): Promise<FsReadAssetResult> => {
    window.__HARNESS__.recordCall('readAsset', [path]);
    if (path === 'assets/tiny.png') {
      return { path, mimeType: 'image/png', dataBase64: TINY_PNG_BASE64 };
    }
    throw new Error(`asset not found: ${path}`);
  }, []);
  const sendToChief = useCallback(async (selection: string, sourcePath?: string): Promise<NotebookSendToChiefResult> => {
    window.__HARNESS__.recordCall('sendToChief', [selection, sourcePath]);
    return { path: 'inbox.md', nudged: false };
  }, []);
  // The whole vault (recursive, .md only) for the Cmd+P finder — the same shape
  // notebook_list returns. Titles let the finder rank/label by heading.
  const listFiles = useCallback(async (): Promise<NotebookEntry[]> => [
    { path: 'knowledge/index.md', type: 'note', title: 'Knowledge index', size: 128 },
    { path: 'journal/2026-06-20.md', type: 'journal', title: '2026-06-20', size: 20 },
    { path: 'knowledge/areas/foo.md', type: 'note', title: 'Foo area', size: 30 },
  ], []);

  useEffect(() => {
    // The app defaults to the dark theme (useTheme DEFAULT_PREFERENCE); force it here
    // so the harness is deterministic regardless of the runner's OS color scheme.
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => {});
    window.__NB_HARNESS__ = {
      fsChanged: (path?: string, content?: string, hash?: string) => {
        if (path && content !== undefined) {
          overridesRef.current[path] = { content, hash: hash ?? `h-${content.length}` };
        }
        setChangeSignal((n) => n + 1);
      },
      getContent: (path: string) =>
        overridesRef.current[path]?.content ?? CONTENT[path] ?? `# ${path}\n\nSample body.`,
    };
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
      existsFile={existsFile}
      readAsset={readAsset}
      sendToChief={sendToChief}
      listFiles={listFiles}
      changeSignal={changeSignal}
      chiefActive
    />
  );
}
