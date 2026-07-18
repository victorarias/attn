// The in-editor frontmatter card (notebook UI stage 4b, "Option A"). A note's leading
// `---…---` YAML renders as a compact properties strip in place of the raw block. The
// card yields to raw YAML only when the user explicitly clicks it to edit properties.
//
// The card shows PROPERTIES only (type, summary, tags, sources, dates) — never a
// title. A note's title is its leading `# H1` (the single canonical title; the keeper
// no longer writes a frontmatter `title:`). So the card sits as a metadata header
// directly above the rendered H1.
//
// CM constraint that shapes this file: decorations that affect vertical layout (block
// widgets, replacements spanning line breaks) MUST come directly from a StateField via
// `EditorView.decorations.from(...)` — the view plugin that powers the inline preview
// is computed after layout and is forbidden from introducing them. So the card lives
// here, in its own field, separate from liveMarkdownPreview's ViewPlugin.

import { type EditorState, type Extension, RangeSet, StateEffect, StateField } from '@codemirror/state';
import { Decoration, type DecorationSet, EditorView, WidgetType } from '@codemirror/view';
import { type Frontmatter, type FrontmatterValue, parseFrontmatterFromDoc } from './frontmatter';

// Raw frontmatter is an explicit editing mode, not a consequence of editor focus.
// CodeMirror focuses before it places a pointer selection. Tying the card to focus
// therefore expanded the YAML under an in-flight body click (the stale selection was
// still at position 0), changing document geometry while CM resolved that click.
const setEditingFrontmatter = StateEffect.define<boolean>();

const editingFrontmatterField = StateField.define<boolean>({
  create: () => false,
  update(value, tr) {
    for (const effect of tr.effects) {
      if (effect.is(setEditingFrontmatter)) value = effect.value;
    }
    if (value && tr.selection) {
      const fm = parseFrontmatterFromDoc(tr.state.doc);
      const remainsInside = fm && tr.state.selection.ranges.some(
        (range) => range.from < fm.to && range.to > fm.from,
      );
      if (!remainsInside) value = false;
    }
    return value;
  },
});

// Date-ish fields shown on the card's meta line (type gets its own pill; title is
// never shown — the `# H1` is the title). Everything else parsed from the block is
// ignored by the card but still lives in the file.
const META_KEYS = ['created', 'updated'];

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
    let h = 22; // header row (type pill + dates)
    if (f.summary) h += 22;
    if (f.tags) h += 24;
    if (f.sources) h += 22;
    return h + 24 + 16; // padding + hint
  }

  // Clicks must reach the editor so the card can hand off to raw editing.
  ignoreEvent() {
    return false;
  }

  toDOM(view: EditorView) {
    const f = this.fm.fields;
    const card = document.createElement('div');
    card.className = 'cm-md-frontmatter';
    card.setAttribute('role', 'button');
    card.setAttribute('aria-label', 'Edit note properties');
    card.tabIndex = 0;

    // Header row: the type pill and the dates — the card's compact identity line.
    // No title: the note's `# H1` below the card is the title.
    const header = document.createElement('div');
    header.className = 'cm-md-fm-header';
    if (f.type) {
      const pill = document.createElement('span');
      pill.className = 'cm-md-fm-type';
      pill.textContent = asText(f.type);
      header.appendChild(pill);
    }
    const dateParts = META_KEYS.filter((k) => f[k]).map((k) => `${k} ${asText(f[k])}`);
    if (dateParts.length) {
      const dates = document.createElement('span');
      dates.className = 'cm-md-fm-dates';
      dates.textContent = dateParts.join('  ·  ');
      header.appendChild(dates);
    }
    if (header.childNodes.length) card.appendChild(header);

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

    const hint = document.createElement('span');
    hint.className = 'cm-md-fm-hint';
    hint.textContent = 'click to edit';
    card.appendChild(hint);

    // Pointer or keyboard activation reveals the raw YAML for editing: move the cursor
    // just inside the block (the first YAML line) so the reveal gate fires, then hand
    // focus back to the editor at that visible selection.
    const reveal = () => {
      const revealAt = view.state.doc.line(2).from; // line 1 is the opening fence
      view.dispatch({
        selection: { anchor: revealAt },
        effects: setEditingFrontmatter.of(true),
      });
      view.focus();
    };
    card.addEventListener('mousedown', (event) => {
      // Keep CodeMirror from placing a selection around the atomic widget before the
      // ensuing semantic click activates it.
      event.preventDefault();
    });
    card.addEventListener('click', (event) => {
      event.preventDefault();
      event.stopPropagation();
      reveal();
    });
    card.addEventListener('keydown', (event) => {
      if (event.key !== 'Enter' && event.key !== ' ') return;
      event.preventDefault();
      event.stopPropagation();
      reveal();
    });

    return card;
  }
}

// Pure decoration builder (no view), so the gate is unit-testable headlessly like
// buildDecorations. Returns the block-replace card, or an empty set when there's no
// frontmatter or while explicit frontmatter editing is active.
export function frontmatterCardDecorations(state: EditorState, editing: boolean): DecorationSet {
  const fm = parseFrontmatterFromDoc(state.doc);
  if (!fm || fm.to <= fm.from) return Decoration.none;
  // Don't swallow the whole document: a note that is ONLY frontmatter has no body line
  // to keep the cursor on, so leave it as raw text.
  if (fm.to >= state.doc.length) return Decoration.none;
  if (editing) {
    for (const range of state.selection.ranges) {
      if (range.from < fm.to && range.to > fm.from) return Decoration.none;
    }
  }
  return Decoration.set(
    Decoration.replace({ block: true, widget: new FrontmatterCardWidget(fm) }).range(fm.from, fm.to),
  );
}

const cardField = StateField.define<DecorationSet>({
  create: (state) => frontmatterCardDecorations(
    state,
    state.field(editingFrontmatterField, false) ?? false,
  ),
  update(value, tr) {
    if (
      tr.docChanged ||
      tr.selection ||
      tr.effects.some((effect) => effect.is(setEditingFrontmatter))
    ) {
      return frontmatterCardDecorations(
        tr.state,
        tr.state.field(editingFrontmatterField, false) ?? false,
      );
    }
    return value.map(tr.changes);
  },
  provide: (field) => EditorView.decorations.from(field),
});

// Leaving the editor ends property editing. Ordinary focus does nothing, so a body
// click cannot temporarily replace the card using CM's stale position-0 selection.
const blurTracker = EditorView.domEventHandlers({
  blur: (_event, view) => {
    const fm = parseFrontmatterFromDoc(view.state.doc);
    const selectionInside = fm && view.state.selection.ranges.some(
      (range) => range.from < fm.to && range.to > fm.from,
    );
    view.dispatch({
      ...(selectionInside ? { selection: { anchor: fm.to } } : {}),
      effects: setEditingFrontmatter.of(false),
    });
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
  '.cm-md-fm-header': { display: 'flex', alignItems: 'center', gap: '10px' },
  '.cm-md-fm-dates': {
    fontFamily: "ui-monospace, 'SF Mono', SFMono-Regular, Menlo, monospace",
    fontSize: '0.72em',
    color: 'var(--color-text-muted, #888)',
  },
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
  '.cm-md-fm-hint': {
    display: 'block',
    marginTop: '8px',
    fontSize: '0.68em',
    color: 'var(--color-text-dimmed, #666)',
  },
});

// The extension: explicit editing state, the card field, its atomic range, and styles. Order
// puts the field before the atomic facet, which reads it.
export function frontmatterCard(): Extension {
  return [editingFrontmatterField, cardField, cardAtomic, blurTracker, cardTheme];
}
