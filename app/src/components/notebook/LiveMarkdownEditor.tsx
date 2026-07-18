// A single read-and-type markdown surface: CodeMirror 6 with the live-preview
// decorations from liveMarkdownPreview. There is no view/edit toggle — the document
// always renders inline and is always editable, with raw markdown revealed on the
// cursor's line. The parent owns persistence (hash-CAS autosave); this component only
// emits value changes, link follows, and the current selection (for "send to chief").

import { forwardRef, useEffect, useImperativeHandle, useMemo, useRef } from 'react';
import CodeMirror, { type ReactCodeMirrorRef } from '@uiw/react-codemirror';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { languages } from '@codemirror/language-data';
import { syntaxHighlighting } from '@codemirror/language';
import { closeSearchPanel, search, searchKeymap, searchPanelOpen } from '@codemirror/search';
import { classHighlighter } from '@lezer/highlight';
import { EditorView, keymap, type KeyBinding, type ViewUpdate } from '@codemirror/view';
import { brokenLinks, revalidateBrokenLinks, type ExistsCheck } from './brokenLinks';
import { formattingKeymap } from './formatting';
import { frontmatterCard } from './frontmatterCard';
import { imageWidget } from './imageWidget';
import { liveMarkdownPreview } from './liveMarkdownPreview';
import { noteDir } from './linkResolver';
import { markdownTables } from './tableWidget';
import { computeMinimalEdit } from './minimalEdit';

// searchKeymap binds its commands with CodeMirror's "Mod-" modifier, which CM
// resolves to Meta only when it detects a Mac platform (navigator.platform) and
// to Ctrl everywhere else. attn only ships on macOS (see AGENTS.md), so "Mod"
// must always mean Cmd — but a Linux CI browser (e.g. headless Chromium on the
// e2e runners) reports a non-Mac platform and silently rebinds Cmd+F to Ctrl+F,
// leaving Cmd+F inert. Rewrite every "Mod-" prefix to an explicit "Cmd-" so the
// binding is platform-independent instead of relying on CM's own detection.
const macSearchKeymap: readonly KeyBinding[] = searchKeymap.map((binding) =>
  binding.key?.startsWith('Mod-') ? { ...binding, key: `Cmd-${binding.key.slice(4)}` } : binding,
);

export interface LiveSelection {
  text: string;
  // Viewport coordinates for floating UI: top is the bottom edge of the selection's
  // last char, left is that char's horizontal midpoint — so a pill anchored here hangs
  // below the selection end and never covers the selected text or the line above it.
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
  // Replace the document with `next` as a MINIMAL edit (shared prefix/suffix trimmed)
  // so the reader's scroll position and selection stay anchored. Used to push an
  // on-disk change into an open note: the default controlled-value path swaps the whole
  // document, which snaps the viewport to the top — exactly what we must avoid while
  // someone is reading. No-op until the view mounts, or when `next` is already current.
  // Does NOT focus or scroll into view (the reader may be elsewhere on the page).
  applyExternalContent: (next: string) => void;
  // Close the in-editor search panel. Returns true if a panel was open.
  closeSearchPanel: () => boolean;
  // Restore keyboard focus to the editor without moving the cursor or scrolling
  // (unlike scrollToPos). Used after a chrome control outside the editor — e.g. a
  // conflict-banner button — steals focus, so typing works immediately again with no
  // extra click back into the document.
  focus: () => void;
}

interface LiveMarkdownEditorProps {
  value: string;
  onChange: (value: string) => void;
  onFollowLink?: (href: string) => void;
  onSelectionChange?: (selection: LiveSelection | null) => void;
  // Check whether an in-notebook link target exists, to flag broken links. Omit to
  // disable the flagging (e.g. the test harness, which has no daemon).
  existsFile?: (path: string) => Promise<ExistsCheck>;
  // Resolve an in-notebook image src to a displayable src (typically a data: URI) for
  // the inline image widget. Omit to disable resolution — non-direct image srcs (not
  // http(s)/data:/protocol-relative) then always render the broken placeholder.
  resolveImageSrc?: (src: string) => Promise<string | null>;
  // Bumped whenever the notebook changed on disk; clears the broken-link cache's
  // "missing" verdicts so a link to a just-created note re-checks. (A change counter,
  // not data — only its identity change matters.)
  revalidateSignal?: number;
  // Root-relative path of the note being edited, used to resolve bare-relative link
  // targets against its directory. Defaults to '' (the notebook root).
  notePath?: string;
  ariaLabel?: string;
  autoFocus?: boolean;
  // Called with the new open state whenever the in-editor search panel opens or
  // closes, so the parent can register/unregister an escape-stack entry.
  onSearchOpenChange?: (open: boolean) => void;
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
  // Search panel (⌘F): themed to the app's tokens. CM's base theme paints its
  // buttons with a background-image gradient, so that must be cleared explicitly.
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

export const LiveMarkdownEditor = forwardRef<LiveMarkdownEditorHandle, LiveMarkdownEditorProps>(function LiveMarkdownEditor({
  value,
  onChange,
  onFollowLink,
  onSelectionChange,
  existsFile,
  resolveImageSrc,
  revalidateSignal,
  notePath,
  ariaLabel,
  autoFocus,
  onSearchOpenChange,
}, ref) {
  const cmRef = useRef<ReactCodeMirrorRef>(null);
  // Previous search-panel-open value, so handleUpdate only calls onSearchOpenChange
  // on a genuine transition (not on every unrelated update while the panel is open).
  const searchOpenRef = useRef(false);

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
    applyExternalContent: (next: string) => {
      const view = cmRef.current?.view;
      if (!view) return;
      const edit = computeMinimalEdit(view.state.doc.toString(), next);
      if (!edit) return; // unchanged → no transaction, scroll/selection untouched
      // Dispatch only the changed range. CodeMirror maps the scroll anchor and the
      // selection through a minimal change, so the reader stays put; scrollIntoView is
      // left off (we don't chase a cursor we didn't move) and we don't take focus. The
      // resulting docChanged fires onChange, so the parent's `value` catches up and its
      // next controlled-value pass is a no-op.
      view.dispatch({
        changes: { from: edit.from, to: edit.to, insert: edit.insert },
        scrollIntoView: false,
      });
    },
    closeSearchPanel: () => {
      const view = cmRef.current?.view;
      if (!view || !searchPanelOpen(view.state)) return false;
      closeSearchPanel(view);
      return true;
    },
    focus: () => {
      cmRef.current?.view?.focus();
    },
  }), []);

  const extensions = useMemo(
    () => [
      // `languages` from @codemirror/language-data describes each fenced-code
      // language lazily — the parser itself only loads (and Vite only fetches its
      // chunk) the first time a fence actually needs it, so importing the full list
      // here is cheap.
      markdown({ base: markdownLanguage, codeLanguages: languages }),
      syntaxHighlighting(classHighlighter),
      EditorView.lineWrapping,
      frontmatterCard(),
      markdownTables(),
      imageWidget({ resolveSrc: resolveImageSrc }),
      liveMarkdownPreview({ onFollowLink }),
      brokenLinks({ existsFile, baseDir: noteDir(notePath ?? '') }),
      search({ top: true }),
      keymap.of(macSearchKeymap),
      formattingKeymap(),
      editorTheme,
    ],
    [onFollowLink, existsFile, resolveImageSrc, notePath],
  );

  // When the notebook changed on disk, ask the broken-link checker to re-verify any
  // links it had flagged missing — a just-created target should clear its flag. The
  // initial mount is skipped (nothing cached yet); only later bumps revalidate.
  const didMountRef = useRef(false);
  useEffect(() => {
    if (!didMountRef.current) {
      didMountRef.current = true;
      return;
    }
    cmRef.current?.view?.dispatch({ effects: revalidateBrokenLinks.of(null) });
  }, [revalidateSignal]);

  const handleUpdate = useMemo(
    () =>
      (update: ViewUpdate) => {
        if (onSearchOpenChange) {
          const isOpen = searchPanelOpen(update.state);
          if (isOpen !== searchOpenRef.current) {
            searchOpenRef.current = isOpen;
            onSearchOpenChange(isOpen);
          }
        }
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
        // Anchor at the selection's END, not its start: a pill placed above the start
        // covers the line before the selection, and one placed above the end covers the
        // selection itself. Below the end is the only spot that covers neither.
        const coords = update.view.coordsAtPos(range.to);
        if (!coords) {
          onSelectionChange(null);
          return;
        }
        onSelectionChange({ text, top: coords.bottom, left: (coords.left + coords.right) / 2 });
      },
    [onSelectionChange, onSearchOpenChange],
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
