/**
 * FrontmatterCard Test Harness
 *
 * Renders the live-preview editor over a note that opens with a YAML frontmatter
 * block, so the in-editor frontmatter card (stage 4b) can be exercised in a real
 * browser — the card is a CodeMirror block widget and CM can't mount under happy-dom.
 * The current value is recorded on every change so the raw-reveal round-trip is
 * observable. ?empty=1 starts with a note that has frontmatter but no body.
 */
import { useCallback, useEffect, useState } from 'react';
import '../../src/App.css';
import { LiveMarkdownEditor } from '../../src/components/notebook/LiveMarkdownEditor';
import type { HarnessProps } from '../types';

const SAMPLE = `---
type: area
summary: The right rail that shows outline and backlinks.
tags: [notebook, ui, codemirror]
sources:
  - /knowledge/areas/notebook.md
created: 2026-06-20
updated: 2026-06-21
---

# Context rail

A paragraph of body text below the frontmatter card. The note's title is the
heading above, not a frontmatter field.
`;

const SAMPLE_NO_BODY = `---
type: area
summary: A note that is only properties, with no body.
---
`;

export function FrontmatterCardHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const [value, setValue] = useState(params.get('empty') === '1' ? SAMPLE_NO_BODY : SAMPLE);
  const [, force] = useState(0);

  const handleChange = useCallback((next: string) => {
    window.__HARNESS__.recordCall('change', [next]);
    setValue(next);
  }, []);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => force((n) => n + 1));
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', padding: 24, background: 'var(--color-bg-app)' }}>
      <div style={{ flex: 1, minHeight: 0, border: '1px solid var(--color-border)', borderRadius: 8 }}>
        <LiveMarkdownEditor value={value} onChange={handleChange} ariaLabel="Note" />
      </div>
    </div>
  );
}
