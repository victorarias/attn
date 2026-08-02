// Annotations on an agent's messages, anchored to markdown offsets and painted
// over whichever terminal rows currently show that markdown.
//
// The store owns four things the pure aligner does not: which messages are
// annotatable, which message each annotation belongs to, when a cached
// alignment stops being usable, and the gate that decides whether a wash may be
// painted at all.
//
// The gate is the important one. Before emitting a wash, the store re-reads the
// text sitting at the rows it is about to paint and confirms it still quotes the
// anchored text. Measured live, every wash that would have landed on words the
// agent did not write came from an alignment that had outlived the writes which
// moved the text; the gate turns each of those into a refusal — a wash that
// vanishes for a frame — instead of a misattribution.
//
// Many messages, not one. An annotation is about what the agent said, and the
// agent saying something else afterwards is not a reason to lose it, so every
// message in the annotatable window carries its own alignment and its own
// annotations. Offsets address one specific markdown string, which is why they
// are stored beside the key of the message they address rather than against
// whatever the newest turn happens to be.
//
// See docs/decisions/2026-08-02-terminal-annotations-anchor-to-the-transcript.md.

import {
  alignMessage,
  offsetsForSelection,
  quotesAnchor,
  rowsForOffsets,
  type MessageAlignment,
  type RowRange,
} from './terminalMessageAlign';

// The terminal, reduced to what a projection needs. Mirrors `BlockRowAccess`.
export interface MessageRowAccess {
  cols(): number;
  totalRows(): number;
  rowText(bufferRow: number): string;
  // Text between two code-unit columns of a row. The gate reads exactly the
  // cells a wash would cover, so a wash that drifted sideways is caught rather
  // than excused by the rest of the row's words.
  rowTextRange(bufferRow: number, startCol: number, endCol: number): string;
}

// One assistant message that can be annotated, as the daemon serves it.
export interface AnnotatableMessage {
  key: string;
  markdown: string;
}

export interface TerminalAnnotation {
  // Stable for the life of the annotation, across saves and reloads.
  id: string;
  // Which message the offsets address.
  messageKey: string;
  // Offsets into that message's markdown (UTF-16 code units).
  start: number;
  end: number;
  // The markdown the offsets covered when the annotation was made. Kept so the
  // panel can list it, and the draft can be submitted, even when the message
  // has fallen out of the annotatable window.
  quote: string;
  emoji: string;
  comment: string;
}

// A wash that passed the gate, in BUFFER rows. Callers convert to viewport rows
// at paint time; a viewport row shifts under scroll and must never be stored.
export interface AnnotationWash {
  annotationId: string;
  rows: RowRange[];
}

// An anchor resolved from a drag: which message, and where in it.
export interface MessageAnchor {
  messageKey: string;
  start: number;
  end: number;
  quote: string;
}

// Cap on a whole-buffer search. Real scrollback runs far longer than any single
// message, and aligning against all of it buys nothing.
const ALIGN_WINDOW_ROWS = 2000;
// How far either side of the last known span to search once the message's
// location is known. Bounds the per-frame cost independently of scrollback size.
const LOCAL_MARGIN_ROWS = 60;

interface AlignmentCache {
  alignment: MessageAlignment;
  writeGeneration: number;
  geometryGeneration: number;
  cols: number;
  totalRows: number;
  // Where the message resolved last time. Seeds the bounded search window.
  lastSpan: { firstRow: number; lastRow: number } | null;
}

function newAnnotationId(): string {
  const uuid = globalThis.crypto?.randomUUID?.();
  // A WebView without randomUUID would otherwise hand every annotation the same
  // id, which the persisted list keys on.
  return uuid ?? `anno-${Math.random().toString(36).slice(2)}-${Date.now().toString(36)}`;
}

export class TerminalAnnotationStore {
  // Oldest first, newest last — the annotatable window, as served.
  private messages: AnnotatableMessage[] = [];
  private markdownByKey = new Map<string, string>();
  private annotations: TerminalAnnotation[] = [];
  // One alignment per message, because each is a separate search for separate
  // text; they invalidate together but resolve independently.
  private caches = new Map<string, AlignmentCache>();

  private writeGeneration = 0;
  private geometryGeneration = 0;

  // Replaces the annotatable window. Annotations are kept: they address message
  // keys, and a key that is still in the window still paints. One that has
  // fallen out keeps its quote and stays in the panel — it is the user's work,
  // and losing it because a turn scrolled away is the bug this exists to fix.
  //
  // Returns whether the window actually changed, so a caller can skip a repaint
  // for the common case of re-fetching the same turns.
  setMessages(messages: readonly AnnotatableMessage[]): boolean {
    const same = messages.length === this.messages.length
      && messages.every((message, index) => message.key === this.messages[index].key
        && message.markdown === this.messages[index].markdown);
    if (same) return false;
    this.messages = messages.map((message) => ({ ...message }));
    this.markdownByKey = new Map(this.messages.map((message) => [message.key, message.markdown]));
    // A message whose text changed under its key would resolve against a stale
    // alignment; dropping the caches costs one search each.
    this.caches.clear();
    return true;
  }

  messageKeys(): string[] {
    return this.messages.map((message) => message.key);
  }

  markdownFor(key: string): string | null {
    return this.markdownByKey.get(key) ?? null;
  }

  // Whether anything can be annotated right now — i.e. whether a drag over the
  // grid could resolve to an anchor.
  hasMessages(): boolean {
    return this.messages.length > 0;
  }

  list(): readonly TerminalAnnotation[] {
    return this.annotations;
  }

  // Replaces the whole list, as hydrating from the daemon does. Ids come from
  // the stored annotations, so a later save addresses the same entries.
  hydrate(annotations: readonly TerminalAnnotation[]): void {
    this.annotations = annotations.map((annotation) => ({ ...annotation }));
  }

  add(messageKey: string, start: number, end: number, emoji = '', comment = ''): TerminalAnnotation | null {
    const markdown = this.markdownByKey.get(messageKey);
    if (markdown === undefined) return null;
    if (start < 0 || end > markdown.length || start >= end) return null;
    const annotation: TerminalAnnotation = {
      id: newAnnotationId(),
      messageKey,
      start,
      end,
      quote: markdown.slice(start, end),
      emoji,
      comment,
    };
    this.annotations.push(annotation);
    return annotation;
  }

  update(id: string, patch: { emoji?: string; comment?: string }): TerminalAnnotation | null {
    const annotation = this.annotations.find((entry) => entry.id === id);
    if (!annotation) return null;
    if (patch.emoji !== undefined) annotation.emoji = patch.emoji;
    if (patch.comment !== undefined) annotation.comment = patch.comment;
    return annotation;
  }

  remove(id: string): boolean {
    const before = this.annotations.length;
    this.annotations = this.annotations.filter((entry) => entry.id !== id);
    return this.annotations.length !== before;
  }

  clear(): void {
    this.annotations = [];
  }

  // The buffer the anchors were resolved against is gone (alt-screen
  // enter/exit, attach restore, a fresh terminal model). Only the alignments
  // are dropped: the annotations themselves address markdown, not rows, and
  // survive to be re-resolved against whatever the buffer becomes.
  reset(): void {
    this.caches.clear();
  }

  hasWork(): boolean {
    return this.annotations.length > 0 && this.messages.length > 0;
  }

  noteWrite(): void {
    this.writeGeneration += 1;
  }

  noteGeometryChange(): void {
    this.geometryGeneration += 1;
  }

  // Where to look for a message this time.
  //
  // A whole-buffer search costs O(scrollback). Re-confirming a message near
  // where it was costs O(message) and is independent of scrollback, so once its
  // location is known the per-frame cost goes flat. The margin covers the
  // message doubling in height — a width reflow can do that — plus drift from
  // output appended between frames.
  private searchWindow(
    lastSpan: { firstRow: number; lastRow: number } | null,
    totalRows: number,
  ): { base: number; end: number; local: boolean } {
    if (!lastSpan) {
      return { base: Math.max(0, totalRows - ALIGN_WINDOW_ROWS), end: totalRows, local: false };
    }
    const half = Math.max(LOCAL_MARGIN_ROWS, lastSpan.lastRow - lastSpan.firstRow + 1);
    return {
      base: Math.max(0, lastSpan.firstRow - half),
      end: Math.min(totalRows, lastSpan.lastRow + half + 1),
      local: true,
    };
  }

  private align(key: string, markdown: string, access: MessageRowAccess): AlignmentCache {
    const totalRows = access.totalRows();
    const cols = access.cols();

    // A span is only meaningful in the geometry it was measured in. A reflow
    // re-lays the whole buffer, so the old rows hold different text — seeding a
    // bounded search from them does not merely miss, it finds the message
    // shifted and resolves a window that clips its head. Geometry changes are
    // rare; paying for one whole-buffer search after each is cheap.
    const previous = this.caches.get(key);
    let lastSpan = previous?.lastSpan ?? null;
    if (previous && (previous.cols !== cols || previous.geometryGeneration !== this.geometryGeneration)) {
      lastSpan = null;
    }

    const readRows = (from: number, to: number): string[] => {
      const out: string[] = [];
      for (let row = from; row < to; row += 1) out.push(access.rowText(row));
      return out;
    };

    let { base, end, local } = this.searchWindow(lastSpan, totalRows);
    let rows = readRows(base, end);
    let alignment = alignMessage(markdown, rows, base);

    // Widen to the whole buffer when the bounded window missed the message, or
    // found it pressed against an edge. An edge hit means the message probably
    // continues outside the window, and trusting it would silently drop the part
    // that is off-window.
    const missed = alignment.firstRow < 0;
    const atEdge = !missed
      && ((alignment.firstRow <= base + 1 && base > 0)
        || (alignment.lastRow >= end - 2 && end < totalRows));
    if (local && (missed || atEdge)) {
      base = Math.max(0, totalRows - ALIGN_WINDOW_ROWS);
      end = totalRows;
      local = false;
      rows = readRows(base, end);
      alignment = alignMessage(markdown, rows, base);
    }

    const entry: AlignmentCache = {
      alignment,
      writeGeneration: this.writeGeneration,
      geometryGeneration: this.geometryGeneration,
      cols,
      totalRows,
      lastSpan: alignment.firstRow >= 0
        ? { firstRow: alignment.firstRow, lastRow: alignment.lastRow }
        : null,
    };
    this.caches.set(key, entry);
    return entry;
  }

  // The alignment for one message, re-computed whenever anything that could
  // have moved the text has happened since the last one. The containment gate
  // backs this up, so a missed invalidation costs a refusal rather than a wrong
  // paint — but re-aligning is cheap enough that there is no reason to lean on
  // the gate for correctness.
  private currentAlignment(key: string, access: MessageRowAccess): MessageAlignment | null {
    const markdown = this.markdownByKey.get(key);
    if (markdown === undefined || markdown === '') return null;
    const cache = this.caches.get(key);
    if (
      cache
      && cache.writeGeneration === this.writeGeneration
      && cache.geometryGeneration === this.geometryGeneration
      && cache.cols === access.cols()
      && cache.totalRows === access.totalRows()
    ) {
      return cache.alignment;
    }
    return this.align(key, markdown, access).alignment;
  }

  // The per-frame entry point. Returns only washes whose rows still quote the
  // text they were anchored to. Annotations on messages outside the window
  // resolve to nothing, which is correct: their text is not on this grid.
  project(access: MessageRowAccess): AnnotationWash[] {
    if (!this.hasWork()) return [];
    const washes: AnnotationWash[] = [];
    const alignments = new Map<string, MessageAlignment | null>();
    for (const annotation of this.annotations) {
      if (!alignments.has(annotation.messageKey)) {
        alignments.set(annotation.messageKey, this.currentAlignment(annotation.messageKey, access));
      }
      const alignment = alignments.get(annotation.messageKey);
      if (!alignment) continue;
      const rows = rowsForOffsets(alignment, annotation.start, annotation.end);
      if (rows.length === 0) continue;
      const painted = rows
        .map((range) => access.rowTextRange(range.row, range.startCol, range.endCol))
        .join('\n');
      if (!quotesAnchor(annotation.quote, painted)) continue;
      washes.push({ annotationId: annotation.id, rows });
    }
    return washes;
  }

  // Which annotation covers a buffer cell, or null if none does.
  //
  // Resolved from the same gated projection the paint uses, so the affordance
  // and the wash can never disagree: an annotation the gate refused this frame
  // is invisible, and invisible things must not be clickable. A later
  // annotation wins an overlap, because it is the one drawn on top.
  annotationAt(access: MessageRowAccess, bufferRow: number, col: number): string | null {
    let hit: string | null = null;
    for (const wash of this.project(access)) {
      for (const range of wash.rows) {
        if (range.row !== bufferRow) continue;
        if (col < range.startCol || col >= range.endCol) continue;
        hit = wash.annotationId;
      }
    }
    return hit;
  }

  // Turns a drag over the grid into an anchor on whichever message the dragged
  // rows belong to. Newest first: that is where a drag almost always lands, and
  // stopping at the first hit keeps a normal selection to one alignment.
  //
  // Returns null when the selection covers no confidently-aligned words — the
  // user dragged over the TUI's chrome, a user turn, or a message that has
  // fallen out of the annotatable window.
  anchorForSelection(
    access: MessageRowAccess,
    selection: { startRow: number; startCol: number; endRow: number; endCol: number },
  ): MessageAnchor | null {
    for (let index = this.messages.length - 1; index >= 0; index -= 1) {
      const { key, markdown } = this.messages[index];
      const alignment = this.currentAlignment(key, access);
      if (!alignment) continue;
      const span = offsetsForSelection(alignment, selection);
      if (!span) continue;
      return { messageKey: key, start: span.start, end: span.end, quote: markdown.slice(span.start, span.end) };
    }
    return null;
  }

  // The rows every annotatable message currently occupies, newest first. Used
  // to decide whether the annotation affordance is worth offering at all.
  resolvedSpans(access: MessageRowAccess): Array<{ key: string; firstRow: number; lastRow: number }> {
    const spans: Array<{ key: string; firstRow: number; lastRow: number }> = [];
    for (let index = this.messages.length - 1; index >= 0; index -= 1) {
      const { key } = this.messages[index];
      const alignment = this.currentAlignment(key, access);
      if (!alignment || alignment.firstRow < 0) continue;
      spans.push({ key, firstRow: alignment.firstRow, lastRow: alignment.lastRow });
    }
    return spans;
  }
}
