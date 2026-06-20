// A single read-and-type markdown surface: CodeMirror 6 with the live-preview
// decorations from liveMarkdownPreview. There is no view/edit toggle — the document
// always renders inline and is always editable, with raw markdown revealed on the
// cursor's line. The parent owns persistence (hash-CAS autosave); this component only
// emits value changes, link follows, and the current selection (for "send to chief").

import { useMemo } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { EditorView, type ViewUpdate } from '@codemirror/view';
import { liveMarkdownPreview } from './liveMarkdownPreview';

export interface LiveSelection {
  text: string;
  // Viewport coordinates of the selection start, for floating UI.
  top: number;
  left: number;
}

interface LiveMarkdownEditorProps {
  value: string;
  onChange: (value: string) => void;
  onFollowLink?: (href: string) => void;
  onSelectionChange?: (selection: LiveSelection | null) => void;
  ariaLabel?: string;
  autoFocus?: boolean;
}

// Match the surrounding document pane: transparent background, inherit the app's
// theme colors via CSS variables so it tracks light/dark without a CM theme swap.
const editorTheme = EditorView.theme({
  '&': {
    height: '100%',
    backgroundColor: 'transparent',
    color: 'var(--color-text-secondary, inherit)',
    fontSize: '14px',
  },
  '.cm-content': {
    fontFamily:
      "ui-serif, Georgia, 'Times New Roman', var(--font-sans, system-ui), serif",
    lineHeight: '1.65',
    padding: '4px 0 80px',
    caretColor: 'var(--color-accent, #7c9cff)',
  },
  '.cm-scroller': { overflow: 'auto' },
  '&.cm-focused': { outline: 'none' },
  '.cm-line': { padding: '0 2px' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': {
    backgroundColor: 'var(--color-accent-soft, rgba(124,156,255,0.22))',
  },
});

export function LiveMarkdownEditor({
  value,
  onChange,
  onFollowLink,
  onSelectionChange,
  ariaLabel,
  autoFocus,
}: LiveMarkdownEditorProps) {
  const extensions = useMemo(
    () => [
      markdown({ base: markdownLanguage }),
      EditorView.lineWrapping,
      liveMarkdownPreview({ onFollowLink }),
      editorTheme,
    ],
    [onFollowLink],
  );

  const handleUpdate = useMemo(
    () =>
      (update: ViewUpdate) => {
        if (!onSelectionChange) return;
        if (!update.selectionSet && !update.docChanged && !update.focusChanged) return;
        const range = update.state.selection.main;
        if (range.empty) {
          onSelectionChange(null);
          return;
        }
        const text = update.state.sliceDoc(range.from, range.to).trim();
        if (!text) {
          onSelectionChange(null);
          return;
        }
        const coords = update.view.coordsAtPos(range.from);
        if (!coords) {
          onSelectionChange(null);
          return;
        }
        onSelectionChange({ text, top: coords.top, left: (coords.left + coords.right) / 2 });
      },
    [onSelectionChange],
  );

  return (
    <CodeMirror
      value={value}
      onChange={onChange}
      onUpdate={handleUpdate}
      extensions={extensions}
      autoFocus={autoFocus}
      height="100%"
      aria-label={ariaLabel}
      basicSetup={{
        lineNumbers: false,
        foldGutter: false,
        highlightActiveLine: false,
        highlightActiveLineGutter: false,
        highlightSelectionMatches: false,
        bracketMatching: false,
        closeBrackets: false,
        autocompletion: false,
        searchKeymap: false,
        lintKeymap: false,
        // The default highlight style underlines headings and colors prose, fighting
        // the live-preview decorations that own how rendered markdown looks. Disable
        // it so this component's theme/decorations are the single source of styling.
        syntaxHighlighting: false,
      }}
    />
  );
}
