// The YAML text buffer for the automation definition editor: CodeMirror 6 with
// syntax highlighting, no live-preview decorations (unlike LiveMarkdownEditor —
// this is a code buffer, not prose). Controlled `value`, plus an imperative
// `applyExternalContent` handle for AutomationEditor's explicit Reload action
// (see minimalEdit.ts's doc comment for why: a plain controlled-value swap
// snaps CodeMirror's scroll/selection back to the top, which would be jarring
// mid-edit after a reload that mostly re-confirms unchanged text).

import { forwardRef, useImperativeHandle, useMemo, useRef } from 'react';
import CodeMirror, { type ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { yaml } from '@codemirror/lang-yaml';
import { syntaxHighlighting } from '@codemirror/language';
import { search, searchKeymap } from '@codemirror/search';
import { classHighlighter } from '@lezer/highlight';
import { EditorView, keymap, type KeyBinding } from '@codemirror/view';
import { computeMinimalEdit } from '../notebook/minimalEdit';

// searchKeymap binds its commands with CodeMirror's "Mod-" modifier, which CM
// resolves to Meta only when it detects a Mac platform (navigator.platform) and
// to Ctrl everywhere else. attn only ships on macOS (see AGENTS.md), so "Mod"
// must always mean Cmd — but a Linux CI browser (e.g. headless Chromium on the
// e2e runners) reports a non-Mac platform and silently rebinds Cmd+F to Ctrl+F,
// leaving Cmd+F inert. Rewrite every "Mod-" prefix to an explicit "Cmd-" so the
// binding is platform-independent instead of relying on CM's own detection.
// (Same fix as LiveMarkdownEditor.tsx — duplicated rather than shared because
// the two editors otherwise have no coupling.)
const macSearchKeymap: readonly KeyBinding[] = searchKeymap.map((binding) =>
  binding.key?.startsWith('Mod-') ? { ...binding, key: `Cmd-${binding.key.slice(4)}` } : binding,
);

export interface AutomationYamlEditorHandle {
  // Replace the document with `next` as a MINIMAL edit (shared prefix/suffix
  // trimmed) so scroll position and selection stay anchored. Used by
  // AutomationEditor's Reload action to pull the daemon's current
  // definition_yaml into an already-open buffer without yanking the viewport
  // to the top. No-op until the view mounts, or when `next` is already
  // current.
  applyExternalContent: (next: string) => void;
  focus: () => void;
  // Live buffer text straight from the CodeMirror doc, for the UI automation
  // bridge's automation_editor_* verbs — see automationEditorAutomation.ts's
  // doc comment for why this exists instead of scraping the DOM. '' before
  // the view has mounted.
  getDocText: () => string;
}

interface AutomationYamlEditorProps {
  value: string;
  onChange: (value: string) => void;
  ariaLabel?: string;
  autoFocus?: boolean;
}

// Themed via CSS variables, same rationale as LiveMarkdownEditor.tsx: skip
// @uiw/react-codemirror's built-in theme (its default "light" paints a white
// background, which would be a white box on the app's dark pane) and own every
// surface CM's base `&light`/`&dark` rules would otherwise pick a light-biased
// default for — background, foreground, cursor, selection.
const editorTheme = EditorView.theme({
  '&': {
    height: '100%',
    backgroundColor: 'transparent',
    color: 'var(--color-text-primary, inherit)',
    fontSize: '13px',
  },
  '.cm-content': {
    fontFamily: "var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace)",
    lineHeight: '1.5',
    padding: '8px 0 40px',
  },
  '.cm-scroller': { overflow: 'auto' },
  '&.cm-focused': { outline: 'none' },
  '.cm-gutters': {
    backgroundColor: 'transparent',
    color: 'var(--color-text-tertiary, rgba(128, 128, 128, 0.7))',
    border: 'none',
  },
  '.cm-activeLineGutter': {
    backgroundColor: 'color-mix(in srgb, var(--color-text-primary, #e8e8e8) 8%, transparent)',
  },
  // basicSetup's drawSelection hides the native caret and renders its own
  // .cm-cursor element — see LiveMarkdownEditor.tsx's editorTheme for why this
  // selector (deep enough to outrank CM's base &light/&dark .cm-cursor rule)
  // is needed to keep the caret visible in both themes.
  '.cm-cursorLayer .cm-cursor, .cm-dropCursor': {
    borderLeftColor: 'var(--color-text-primary, #e8e8e8)',
    borderLeftWidth: '2px',
  },
  '.cm-selectionLayer .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 30%, transparent) !important',
  },
  '.cm-panels': {
    color: 'var(--color-text-primary, inherit)',
    backgroundColor: 'var(--color-bg-elevated, rgba(128, 128, 128, 0.08))',
  },
  '.cm-panels.cm-panels-top': {
    borderBottom: '1px solid var(--color-border, rgba(128, 128, 128, 0.3))',
  },
  '.cm-panel.cm-search': {
    padding: '4px 6px',
    fontFamily: 'system-ui, -apple-system, sans-serif',
    fontSize: '12px',
  },
  '.cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-search label': {
    fontFamily: 'inherit',
    fontSize: 'inherit',
    color: 'var(--color-text-primary, inherit)',
  },
  '.cm-panel.cm-search input': {
    backgroundColor: 'var(--color-bg-input, transparent)',
    border: '1px solid var(--color-border, rgba(128, 128, 128, 0.3))',
    borderRadius: '3px',
    padding: '2px 4px',
  },
  '.cm-panel.cm-search button': {
    backgroundImage: 'none',
    backgroundColor: 'var(--color-bg-button, transparent)',
    border: '1px solid var(--color-border, rgba(128, 128, 128, 0.3))',
    borderRadius: '3px',
  },
  '.cm-searchMatch': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 25%, transparent)',
  },
  '.cm-searchMatch-selected': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 50%, transparent)',
  },
});

export const AutomationYamlEditor = forwardRef<AutomationYamlEditorHandle, AutomationYamlEditorProps>(
  function AutomationYamlEditor({ value, onChange, ariaLabel, autoFocus }, ref) {
    const cmRef = useRef<ReactCodeMirrorRef>(null);

    useImperativeHandle(
      ref,
      () => ({
        applyExternalContent: (next: string) => {
          const view = cmRef.current?.view;
          if (!view) return;
          const edit = computeMinimalEdit(view.state.doc.toString(), next);
          if (!edit) return; // unchanged → no transaction, scroll/selection untouched
          view.dispatch({
            changes: { from: edit.from, to: edit.to, insert: edit.insert },
            scrollIntoView: false,
          });
        },
        focus: () => {
          cmRef.current?.view?.focus();
        },
        getDocText: () => cmRef.current?.view?.state.doc.toString() ?? '',
      }),
      [],
    );

    const extensions = useMemo(
      () => [
        yaml(),
        syntaxHighlighting(classHighlighter),
        EditorView.lineWrapping,
        search({ top: true }),
        keymap.of(macSearchKeymap),
        editorTheme,
      ],
      [],
    );

    return (
      <CodeMirror
        ref={cmRef}
        value={value}
        onChange={onChange}
        extensions={extensions}
        autoFocus={autoFocus}
        height="100%"
        aria-label={ariaLabel}
        // Skip @uiw/react-codemirror's built-in theme — see editorTheme's doc
        // comment above.
        theme="none"
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          highlightActiveLine: false,
          highlightActiveLineGutter: true,
          highlightSelectionMatches: false,
          bracketMatching: true,
          closeBrackets: false,
          autocompletion: false,
          searchKeymap: false,
          lintKeymap: false,
          // The default highlight style fights classHighlighter above.
          syntaxHighlighting: false,
        }}
      />
    );
  },
);
