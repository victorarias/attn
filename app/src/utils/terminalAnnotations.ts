// Annotations on an agent's messages, anchored to markdown offsets and painted
// over whichever terminal rows currently show that markdown.
//
// The gate is the invariant: before emitting a wash the store re-reads the text
// at the rows it is about to paint and confirms it still quotes the anchor, so a
// stale alignment costs a refusal rather than a misattribution. Every message in
// the annotatable window carries its own alignment and annotations, because
// offsets address one specific markdown string.
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
  // Text between two code-unit columns: the gate reads exactly the cells a wash
  // would cover, so sideways drift is caught.
  rowTextRange(bufferRow: number, startCol: number, endCol: number): string;
}

// One assistant message that can be annotated.
export interface AnnotatableMessage {
  key: string;
  markdown: string;
}

export interface TerminalAnnotation {
  id: string;
  messageKey: string;
  // Offsets into that message's markdown (UTF-16 code units).
  start: number;
  end: number;
  // The markdown the offsets covered when made; kept so the panel still lists
  // an annotation whose message fell out of the window.
  quote: string;
  emoji: string;
  comment: string;
}

// A wash that passed the gate, in BUFFER rows — a viewport row shifts under
// scroll and must never be stored.
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

// Cap on a whole-buffer search; scrollback far outruns any single message.
const ALIGN_WINDOW_ROWS = 2000;
// Search margin around the last known span; bounds per-frame cost.
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
  // A WebView without randomUUID must not hand every annotation the same id.
  return uuid ?? `anno-${Math.random().toString(36).slice(2)}-${Date.now().toString(36)}`;
}

export class TerminalAnnotationStore {
  // Oldest first, newest last — the annotatable window, as served.
  private messages: AnnotatableMessage[] = [];
  private markdownByKey = new Map<string, string>();
  private annotations: TerminalAnnotation[] = [];
  // One alignment per message: they invalidate together, resolve independently.
  private caches = new Map<string, AlignmentCache>();

  private writeGeneration = 0;
  private geometryGeneration = 0;

  // Replaces the annotatable window, keeping annotations (they address message
  // keys). Returns whether the window actually changed, so re-fetching the same
  // turns skips the repaint.
  setMessages(messages: readonly AnnotatableMessage[]): boolean {
    const same = messages.length === this.messages.length
      && messages.every((message, index) => message.key === this.messages[index].key
        && message.markdown === this.messages[index].markdown);
    if (same) return false;
    this.messages = messages.map((message) => ({ ...message }));
    this.markdownByKey = new Map(this.messages.map((message) => [message.key, message.markdown]));
    // Text changed under a key would resolve against a stale alignment.
    this.caches.clear();
    return true;
  }

  messageKeys(): string[] {
    return this.messages.map((message) => message.key);
  }

  markdownFor(key: string): string | null {
    return this.markdownByKey.get(key) ?? null;
  }

  // Whether a drag over the grid could resolve to an anchor at all.
  hasMessages(): boolean {
    return this.messages.length > 0;
  }

  list(): readonly TerminalAnnotation[] {
    return this.annotations;
  }

  // Replaces the whole list; ids come from the stored annotations.
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

  // The buffer the anchors resolved against is gone (alt-screen, restore, fresh
  // model). Only alignments drop; annotations address markdown, not rows.
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

  // Where to look for a message this time. A whole-buffer search costs
  // O(scrollback); re-confirming near the last span costs O(message). The margin
  // covers the message doubling in height plus drift from appended output.
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

    // A span is only meaningful in the geometry it was measured in: seeding a
    // bounded search across a reflow resolves a window that clips the message.
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

    // Widen to the whole buffer on a miss, or on an edge hit — the message
    // probably continues outside the window.
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

  // The alignment for one message, re-computed whenever anything could have
  // moved the text. A missed invalidation costs a refusal, not a wrong paint.
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

  // The per-frame entry point: only washes whose rows still quote their anchor.
  // A message outside the window resolves to nothing; its text is not on-grid.
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

  // Which annotation covers a buffer cell. Resolved from the same gated
  // projection the paint uses, so an annotation the gate refused is not
  // clickable; a later annotation wins an overlap, being the one drawn on top.
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

  // Turns a drag into an anchor on the message the dragged rows belong to,
  // newest first. Null when the selection covers no confidently-aligned words.
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

  // The rows every annotatable message currently occupies, newest first.
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
