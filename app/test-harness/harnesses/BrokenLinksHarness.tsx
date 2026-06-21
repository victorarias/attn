/**
 * BrokenLinks Test Harness
 *
 * Renders the live-preview editor in a real browser with an `existsFile` stub, so the
 * broken-link flagging — which depends on async existence checks resolving and forcing
 * a CodeMirror repaint — can be exercised end to end (happy-dom can't mount CM, and the
 * headless unit tests can't cover the async StateEffect repaint path). Dedicated to
 * broken links so it doesn't collide with the shared LiveMarkdownEditor harness.
 *
 * The stub treats any path containing "missing" as absent and everything else as
 * present, and records each checked path on window.__HARNESS__.
 */
import { useCallback, useEffect, useState } from 'react';
import '../../src/App.css';
import { LiveMarkdownEditor } from '../../src/components/notebook/LiveMarkdownEditor';
import type { HarnessProps } from '../types';

const SAMPLE = `# Broken link demo

A link to a real note: [real](/knowledge/areas/real.md).

A link to a missing note: [ghost](/knowledge/areas/missing.md).

An external link, never flagged: [site](https://example.com).
`;

export function BrokenLinksHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [value, setValue] = useState(SAMPLE);
  const [, force] = useState(0);

  const handleChange = useCallback((next: string) => {
    window.__HARNESS__.recordCall('change', [next]);
    setValue(next);
  }, []);

  const existsFile = useCallback(async (path: string) => {
    window.__HARNESS__.recordCall('existsFile', [path]);
    return { path, exists: !path.includes('missing') };
  }, []);

  // Append a second link to a missing note, so a test can prove a freshly-typed
  // broken link is checked and flagged after the document changes (the docChanged
  // re-check path), without fragile in-editor caret manipulation.
  const addMissingLink = useCallback(() => {
    setValue((v) => `${v}\nAnother dead one: [extra](/knowledge/areas/missing-extra.md).\n`);
  }, []);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => force((n) => n + 1));
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', padding: 24, background: 'var(--color-bg-app)' }}>
      <button type="button" data-testid="add-missing-link" onClick={addMissingLink} style={{ marginBottom: 12 }}>
        Add missing link
      </button>
      <div style={{ flex: 1, minHeight: 0, border: '1px solid var(--color-border)', borderRadius: 8 }}>
        <LiveMarkdownEditor
          value={value}
          onChange={handleChange}
          existsFile={existsFile}
          ariaLabel="Note"
        />
      </div>
    </div>
  );
}
