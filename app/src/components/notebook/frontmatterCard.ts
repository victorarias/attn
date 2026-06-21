// The in-editor frontmatter card (notebook UI stage 4b, "Option A"). A note's leading
// `---…---` YAML renders as a compact card in place of the raw block; when the editor
// is focused and the cursor enters the block, the card yields to the raw YAML so it
// can be edited — the same reveal model the inline live-preview uses, at block level.
//
// CM constraint that shapes this file: decorations that affect vertical layout (block
// widgets, replacements spanning line breaks) MUST come directly from a StateField via
// `EditorView.decorations.from(...)` — the view plugin that powers the inline preview
// is computed after layout and is forbidden from introducing them. So the card lives
// here, in its own field, separate from liveMarkdownPreview's ViewPlugin.

import { type EditorState, type Extension, RangeSet, StateEffect, StateField } from '@codemirror/state';
import { Decoration, type DecorationSet, EditorView, WidgetType } from '@codemirror/view';
import { type Frontmatter, type FrontmatterValue, parseFrontmatter } from './frontmatter';

// Editor focus, carried into state. A StateField can't read `view.hasFocus`, but the
// card must show when the editor is unfocused even though CM keeps a selection at
// position 0 (which sits inside the frontmatter block). Without this, a freshly opened
// note would show raw YAML instead of the card.
const setFocused = StateEffect.define<boolean>();

const focusedField = StateField.define<boolean>({
  create: () => false,
  update(value, tr) {
    for (const effect of tr.effects) if (effect.is(setFocused)) value = effect.value;
    return value;
  },
});

// Fields the card surfaces prominently, in render order. Everything else parsed from
// the block is ignored by the card (but still lives in the file).
const META_KEYS = ['type', 'created', 'updated'];

function asText(value: FrontmatterValue | undefined): string {
  if (value == null) return '';
  return Array.isArray(value) ? value.join(', ') : value;
}

class FrontmatterCardWidget extends WidgetType {
  constructor(readonly fm: Frontmatter) {
    super();
  }

  // Re-render only when the parsed fields change, so CM reuses the DOM (no flicker)
  // as the cursor moves elsewhere in the document.
  eq(other: FrontmatterCardWidget) {
    return JSON.stringify(this.fm.fields) === JSON.stringify(other.fm.fields);
  }

  // Reserve roughly the right height before the DOM is measured, so the first layout
  // doesn't jump the scroll position. CM re-measures the real height after mount.
  get estimatedHeight() {
    const f = this.fm.fields;
    let h = 30; // title row
    if (f.summary) h += 22;
    if (f.tags) h += 24;
    if (f.sources) h += 22;
    if (META_KEYS.some((k) => f[k])) h += 20;
    return h + 24; // padding
  }

  // Clicks must reach the editor so the card can hand off to raw editing.
  ignoreEvent() {
    return false;
  }

  toDOM(view: EditorView) {
    const f = this.fm.fields;
    const card = document.createElement('div');
    card.className = 'cm-md-frontmatter';
    card.setAttribute('role', 'group');
    card.setAttribute('aria-label', 'Note properties');

    const titleRow = document.createElement('div');
    titleRow.className = 'cm-md-fm-titlerow';
    const title = document.createElement('span');
    title.className = 'cm-md-fm-title';
    title.textContent = asText(f.title) || '(untitled)';
    titleRow.appendChild(title);
    if (f.type) {
      const pill = document.createElement('span');
      pill.className = 'cm-md-fm-type';
      pill.textContent = asText(f.type);
      titleRow.appendChild(pill);
    }
    card.appendChild(titleRow);

    if (f.summary) {
      const summary = document.createElement('p');
      summary.className = 'cm-md-fm-summary';
      summary.textContent = asText(f.summary);
      card.appendChild(summary);
    }

    if (Array.isArray(f.tags) && f.tags.length) {
      const tags = document.createElement('div');
      tags.className = 'cm-md-fm-tags';
      for (const tag of f.tags) {
        const chip = document.createElement('span');
        chip.className = 'cm-md-fm-tag';
        chip.textContent = tag;
        tags.appendChild(chip);
      }
      card.appendChild(tags);
    }

    if (f.sources) {
      const sources = document.createElement('div');
      sources.className = 'cm-md-fm-sources';
      const list = Array.isArray(f.sources) ? f.sources : [f.sources];
      for (const src of list) {
        const item = document.createElement('span');
        item.className = 'cm-md-fm-source';
        item.textContent = src;
        sources.appendChild(item);
      }
      card.appendChild(sources);
    }

    const metaParts = META_KEYS.filter((k) => f[k]).map((k) => `${k}: ${asText(f[k])}`);
    if (metaParts.length) {
      const meta = document.createElement('div');
      meta.className = 'cm-md-fm-meta';
      meta.textContent = metaParts.join('  ·  ');
      card.appendChild(meta);
    }

    const hint = document.createElement('span');
    hint.className = 'cm-md-fm-hint';
    hint.textContent = 'click to edit';
    card.appendChild(hint);

    // Click reveals the raw YAML for editing: move the cursor just inside the block
    // (the first YAML line) so the reveal gate fires, and take focus. preventDefault
    // stops CM from also placing a selection at the click point.
    card.addEventListener('mousedown', (event) => {
      event.preventDefault();
      const revealAt = view.state.doc.line(2).from; // line 1 is the opening fence
      view.dispatch({ selection: { anchor: revealAt } });
      view.focus();
    });

    return card;
  }
}

// Pure decoration builder (no view), so the gate is unit-testable headlessly like
// buildDecorations. Returns the block-replace card, or an empty set when there's no
// frontmatter or when a focused cursor sits inside the block (reveal raw for editing).
export function frontmatterCardDecorations(state: EditorState, focused: boolean): DecorationSet {
  const doc = state.doc.toString();
  const fm = parseFrontmatter(doc);
  if (!fm || fm.to <= fm.from) return Decoration.none;
  // Don't swallow the whole document: a note that is ONLY frontmatter has no body line
  // to keep the cursor on, so leave it as raw text.
  if (fm.to >= doc.length) return Decoration.none;
  if (focused) {
    for (const range of state.selection.ranges) {
      if (range.from < fm.to && range.to > fm.from) return Decoration.none;
    }
  }
  return Decoration.set(
    Decoration.replace({ block: true, widget: new FrontmatterCardWidget(fm) }).range(fm.from, fm.to),
  );
}

const cardField = StateField.define<DecorationSet>({
  create: (state) => frontmatterCardDecorations(state, state.field(focusedField, false) ?? false),
  update(value, tr) {
    // Recompute when the doc, the selection, or focus changes — any of which can flip
    // the card between shown and revealed.
    if (tr.docChanged || tr.selection || tr.effects.some((e) => e.is(setFocused))) {
      return frontmatterCardDecorations(tr.state, tr.state.field(focusedField, false) ?? false);
    }
    return value.map(tr.changes);
  },
  provide: (field) => EditorView.decorations.from(field),
});

// Push focus changes into state so the card field can react to them.
const focusTracker = EditorView.domEventHandlers({
  focus: (_event, view) => {
    view.dispatch({ effects: setFocused.of(true) });
    return false;
  },
  blur: (_event, view) => {
    view.dispatch({ effects: setFocused.of(false) });
    return false;
  },
});

// While the card is shown, its range behaves as one atom for cursor motion/deletion
// (arrows skip it; backspace at the body start doesn't dissolve it). Derived from the
// field, so it's automatically empty when the card is revealed.
const cardAtomic = EditorView.atomicRanges.of(
  (view) => view.state.field(cardField, false) ?? RangeSet.empty,
);

const cardTheme = EditorView.baseTheme({
  '.cm-md-frontmatter': {
    margin: '2px 0 14px',
    padding: '12px 14px',
    borderRadius: '10px',
    background: 'var(--color-bg-elevated, rgba(127,127,127,0.1))',
    border: '1px solid var(--color-border-subtle, rgba(127,127,127,0.25))',
    cursor: 'text',
    fontFamily: 'var(--font-sans, system-ui), sans-serif',
  },
  '.cm-md-fm-titlerow': { display: 'flex', alignItems: 'center', gap: '8px' },
  '.cm-md-fm-title': { fontWeight: '650', fontSize: '1.05em', color: 'var(--color-text-primary, #e8e8e8)' },
  '.cm-md-fm-type': {
    padding: '1px 7px',
    borderRadius: '6px',
    fontSize: '0.72em',
    fontWeight: '600',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    color: 'var(--accent, #ff6b35)',
    background: 'color-mix(in srgb, var(--accent, #ff6b35) 14%, transparent)',
  },
  '.cm-md-fm-summary': { margin: '6px 0 0', fontSize: '0.9em', color: 'var(--color-text-secondary, #b8b8b8)' },
  '.cm-md-fm-tags': { display: 'flex', flexWrap: 'wrap', gap: '5px', marginTop: '8px' },
  '.cm-md-fm-tag': {
    padding: '1px 8px',
    borderRadius: '999px',
    fontSize: '0.74em',
    color: 'var(--color-text-secondary, #b8b8b8)',
    background: 'color-mix(in srgb, var(--color-text-muted, #888) 16%, transparent)',
  },
  '.cm-md-fm-sources': { display: 'flex', flexWrap: 'wrap', gap: '4px 12px', marginTop: '8px' },
  '.cm-md-fm-source': {
    fontFamily: "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, monospace",
    fontSize: '0.72em',
    color: 'var(--accent, #ff6b35)',
  },
  '.cm-md-fm-meta': {
    marginTop: '8px',
    fontFamily: "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, monospace",
    fontSize: '0.72em',
    color: 'var(--color-text-muted, #888)',
  },
  '.cm-md-fm-hint': {
    display: 'block',
    marginTop: '8px',
    fontSize: '0.68em',
    color: 'var(--color-text-dimmed, #666)',
  },
});

// The extension: focus tracking, the card field, its atomic range, and styles. Order
// puts the field before the atomic facet, which reads it.
export function frontmatterCard(): Extension {
  return [focusedField, cardField, cardAtomic, focusTracker, cardTheme];
}
