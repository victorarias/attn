/**
 * LiveMarkdownEditor Test Harness
 *
 * Renders the CodeMirror-backed live-preview editor in a real browser (CM can't
 * mount under happy-dom), so its rendering and interactions can be exercised and
 * eyeballed. Exposes window.__HARNESS__ controls: the current value is recorded on
 * every change, and link-follow / selection callbacks are recorded too.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
// Pull in the app's design tokens so the transparent editor renders over the real
// dark pane (otherwise it sits on the browser's default white page, which is exactly
// the kind of theme mismatch this editor is supposed to avoid).
import '../../src/App.css';
// The notebook layout CSS bounds the editor's height (.notebook-browser-live-editor is
// a flex column whose react-codemirror child is flex:1, so .cm-scroller actually
// overflows and scrolls) — exactly the chain the real document pane provides.
import '../../src/components/NotebookBrowser.css';
import { LiveMarkdownEditor, type LiveMarkdownEditorHandle, type LiveSelection } from '../../src/components/notebook/LiveMarkdownEditor';
import type { HarnessProps } from '../types';

const SAMPLE = `# Notebook heading

A paragraph with **bold**, *italic*, \`inline code\`, and a
[wiki link](/knowledge/areas/foo.md) to another note.

## Second heading

- a list item
- another item

## Tasks

- [ ] an open task
- [x] a done task

## Code

\`\`\`ts
const answer = 42;
\`\`\`
`;

// A document tall enough to scroll, so the scroll-preservation test has somewhere to
// scroll to. Each line is uniquely numbered so an external edit can target one.
const LONG = `# Long note\n\n${Array.from({ length: 80 }, (_, i) => `Paragraph line number ${i + 1} of the long note.`).join('\n\n')}\n`;

// Editing-harness controls the spec drives directly (kept off the typed HarnessAPI,
// which is a fixed shape). applyExternal pushes content through the scroll-preserving
// minimal-edit handle; swapValue replaces the controlled `value` wholesale (the old
// full-document-replace path) so the spec can contrast the two.
interface EditorHarnessControls {
  applyExternal: (next: string) => void;
  swapValue: (next: string) => void;
}
declare global {
  interface Window {
    __EDITOR_HARNESS__?: EditorHarnessControls;
  }
}

export function LiveMarkdownEditorHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const initial = params.get('empty') === '1' ? '' : params.get('long') === '1' ? LONG : SAMPLE;
  const [value, setValue] = useState(initial);
  const [, force] = useState(0);
  const editorRef = useRef<LiveMarkdownEditorHandle>(null);

  const handleChange = useCallback((next: string) => {
    window.__HARNESS__.recordCall('change', [next]);
    setValue(next);
  }, []);

  const handleFollowLink = useCallback((href: string) => {
    window.__HARNESS__.recordCall('followLink', [href]);
  }, []);

  const handleSelectionChange = useCallback((selection: LiveSelection | null) => {
    window.__HARNESS__.recordCall('selectionChange', [selection]);
  }, []);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => force((n) => n + 1));
    window.__EDITOR_HARNESS__ = {
      applyExternal: (next: string) => editorRef.current?.applyExternalContent(next),
      swapValue: (next: string) => setValue(next),
    };
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', padding: 24, background: 'var(--color-bg-app)' }}>
      <div
        className="notebook-browser-live-editor"
        style={{ border: '1px solid var(--color-border)', borderRadius: 8 }}
      >
        <LiveMarkdownEditor
          ref={editorRef}
          value={value}
          onChange={handleChange}
          onFollowLink={handleFollowLink}
          onSelectionChange={handleSelectionChange}
          ariaLabel="Note"
        />
      </div>
    </div>
  );
}
