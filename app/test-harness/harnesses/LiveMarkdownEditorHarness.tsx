/**
 * LiveMarkdownEditor Test Harness
 *
 * Renders the CodeMirror-backed live-preview editor in a real browser (CM can't
 * mount under happy-dom), so its rendering and interactions can be exercised and
 * eyeballed. Exposes window.__HARNESS__ controls: the current value is recorded on
 * every change, and link-follow / selection callbacks are recorded too.
 */
import { useCallback, useEffect, useState } from 'react';
// Pull in the app's design tokens so the transparent editor renders over the real
// dark pane (otherwise it sits on the browser's default white page, which is exactly
// the kind of theme mismatch this editor is supposed to avoid).
import '../../src/App.css';
import { LiveMarkdownEditor, type LiveSelection } from '../../src/components/notebook/LiveMarkdownEditor';
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

export function LiveMarkdownEditorHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const [value, setValue] = useState(params.get('empty') === '1' ? '' : SAMPLE);
  const [, force] = useState(0);

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
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', padding: 24, background: 'var(--color-bg-app)' }}>
      <div style={{ flex: 1, minHeight: 0, border: '1px solid var(--color-border)', borderRadius: 8 }}>
        <LiveMarkdownEditor
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
