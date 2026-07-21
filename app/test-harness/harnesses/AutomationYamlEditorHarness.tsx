/**
 * AutomationYamlEditor Test Harness
 *
 * Renders the CodeMirror-backed automation-definition YAML buffer in a real
 * browser (CM can't mount under happy-dom), so its rendering, theming, and
 * interactions can be exercised. Exposes window.__HARNESS__ controls: the
 * current value is recorded on every change; applyExternal / swapValue mirror
 * LiveMarkdownEditorHarness's contrast pair for the minimal-edit vs
 * full-document-swap scroll behavior AutomationEditor's Reload relies on.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
// Pull in the app's design tokens so the transparent editor renders over the
// real dark pane, matching the automation editor's actual host.
import '../../src/App.css';
import '../../src/components/automations/AutomationEditor.css';
import {
  AutomationYamlEditor,
  type AutomationYamlEditorHandle,
} from '../../src/components/automations/AutomationYamlEditor';
import type { HarnessProps } from '../types';

const SAMPLE = `id: pr-reviewer
name: PR reviewer
enabled: true
trigger:
  type: schedule
  cron: "0 9 * * *"
action:
  type: agent
  prompt: |
    Review open PRs assigned to me and leave comments.
`;

// A document tall enough to scroll, so the scroll-preservation test has
// somewhere to scroll to. Each line is uniquely numbered so an external edit
// can target one, same shape as LiveMarkdownEditorHarness's LONG fixture.
const LONG = `id: long-automation\nname: Long automation\n# ${Array.from({ length: 80 }, (_, i) => `comment line number ${i + 1} of the long automation`).join('\n# ')}\n`;

// Editing-harness control the spec drives directly (kept off the typed
// HarnessAPI, which is a fixed shape). applyExternal pushes content through
// the minimal-edit handle — the same path AutomationEditor's Reload uses.
//
// LiveMarkdownEditorHarness also exposes a swapValue that replaces the
// controlled `value` wholesale, so its spec can contrast the two scroll
// outcomes. This harness deliberately does not: see the selection test in
// automation-yaml-editor.spec.ts for why that contrast is not sound evidence
// for this buffer.
interface EditorHarnessControls {
  applyExternal: (next: string) => void;
}
declare global {
  interface Window {
    __EDITOR_HARNESS__?: EditorHarnessControls;
  }
}

export function AutomationYamlEditorHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const initial = params.get('empty') === '1' ? '' : params.get('long') === '1' ? LONG : SAMPLE;
  const [value, setValue] = useState(initial);
  const [, force] = useState(0);
  const editorRef = useRef<AutomationYamlEditorHandle>(null);

  const handleChange = useCallback((next: string) => {
    window.__HARNESS__.recordCall('change', [next]);
    setValue(next);
  }, []);

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', 'dark');
    setTriggerRerender(() => force((n) => n + 1));
    window.__EDITOR_HARNESS__ = {
      applyExternal: (next: string) => editorRef.current?.applyExternalContent(next),
    };
    onReady();
  }, [onReady, setTriggerRerender]);

  return (
    <div style={{ height: '100vh', display: 'flex', flexDirection: 'column', padding: 24, background: 'var(--color-bg-app)' }}>
      <div className="automation-editor__buffer">
        <AutomationYamlEditor ref={editorRef} value={value} onChange={handleChange} ariaLabel="Automation definition" />
      </div>
    </div>
  );
}
