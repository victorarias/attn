// A single read-and-type markdown surface: CodeMirror 6 with the live-preview
// decorations from liveMarkdownPreview. There is no view/edit toggle — the document
// always renders inline and is always editable, with raw markdown revealed on the
// cursor's line. The parent owns persistence (hash-CAS autosave); this component only
// emits value changes, link follows, and the current selection (for "send to chief").

import { forwardRef, useImperativeHandle, useMemo, useRef } from 'react';
import CodeMirror, { type ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { EditorView, type ViewUpdate } from '@codemirror/view';
import { liveMarkdownPreview } from './liveMarkdownPreview';

export interface LiveSelection {
  text: string;
  // Viewport coordinates of the selection start, for floating UI.
  top: number;
  left: number;
}

// Imperative surface the parent drives for navigation that originates OUTSIDE the
// editor (the context rail's outline). The editor still owns its own scroll for
// typing; this only lets the outline jump to a heading.
export interface LiveMarkdownEditorHandle {
  // Scroll the given character offset to the top of the viewport, place the cursor
  // there, and take focus. A no-op until the view has mounted, or if the offset is
  // out of range for the current document.
  scrollToPos: (pos: number) => void;
}

interface LiveMarkdownEditorProps {
  value: string;
  onChange: (value: string) => void;
  onFollowLink?: (href: string) => void;
  onSelectionChange?: (selection: LiveSelection | null) => void;
  ariaLabel?: string;
  autoFocus?: boolean;
}

// Themed entirely through the app's CSS variables so it tracks the app's light/dark
// mode automatically — no CodeMirror dark/light flag to keep in sync. The catch:
// CodeMirror's *base* theme styles the cursor/selection/caret from `&light`/`&dark`
// rules that only activate when a theme sets the `darkTheme` facet (which we don't,
// on purpose — the CSS variables already track the mode). So a complete app theme
// must OWN every surface CM would otherwise pick a light-biased default for:
// background, foreground, the cursor, and the selection. Get this wrong and you get
// a black caret / lavender selection on a dark pane.
const editorTheme = EditorView.theme({
  '&': {
    height: '100%',
    backgroundColor: 'transparent',
    color: 'var(--color-text-primary, inherit)',
    fontSize: '14px',
  },
  '.cm-content': {
    fontFamily:
      "ui-serif, Georgia, 'Times New Roman', var(--font-sans, system-ui), serif",
    lineHeight: '1.65',
    padding: '4px 0 80px',
  },
  '.cm-scroller': { overflow: 'auto' },
  '&.cm-focused': { outline: 'none' },
  '.cm-line': { padding: '0 2px' },
  // basicSetup's drawSelection hides the native caret (`caret-color: transparent`)
  // and renders its OWN `.cm-cursor` element, so the caret color is that element's
  // left border — NOT `.cm-content { caretColor }`. This selector is deep enough to
  // outrank CM's base `&light/&dark .cm-cursor` rule (theme beats baseTheme on a
  // specificity tie), so the app's text color wins in both themes.
  '.cm-cursorLayer .cm-cursor, .cm-dropCursor': {
    borderLeftColor: 'var(--color-text-primary, #e8e8e8)',
    borderLeftWidth: '2px',
  },
  // CM's base focused-selection rule is highly specific (`&dark.cm-focused > … >
  // .cm-selectionLayer .cm-selectionBackground`), more than a theme can match, so
  // !important is the clean way to assert the app accent over it in both themes.
  '.cm-selectionLayer .cm-selectionBackground': {
    backgroundColor: 'color-mix(in srgb, var(--accent, #ff6b35) 30%, transparent) !important',
  },
});

export const LiveMarkdownEditor = forwardRef<LiveMarkdownEditorHandle, LiveMarkdownEditorProps>(function LiveMarkdownEditor({
  value,
  onChange,
  onFollowLink,
  onSelectionChange,
  ariaLabel,
  autoFocus,
}, ref) {
  const cmRef = useRef<ReactCodeMirrorRef>(null);

  useImperativeHandle(ref, () => ({
    scrollToPos: (pos: number) => {
      const view = cmRef.current?.view;
      if (!view) return;
      // Clamp into range: the outline is parsed from the same draft, but a click can
      // race a keystroke that shortened the doc. Out-of-range dispatch would throw.
      const target = Math.max(0, Math.min(pos, view.state.doc.length));
      view.dispatch({
        selection: { anchor: target },
        effects: EditorView.scrollIntoView(target, { y: 'start' }),
      });
      view.focus();
    },
  }), []);

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
      ref={cmRef}
      value={value}
      onChange={onChange}
      onUpdate={handleUpdate}
      extensions={extensions}
      autoFocus={autoFocus}
      height="100%"
      aria-label={ariaLabel}
      // Skip @uiw/react-codemirror's built-in theme (default "light" paints a white
      // background). Our editorTheme keeps the surface transparent so it inherits the
      // app's dark document pane and tracks light/dark via CSS variables.
      theme="none"
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
});
