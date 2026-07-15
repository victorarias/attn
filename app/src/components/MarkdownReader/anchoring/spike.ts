/**
 * Anchor paint SPIKE — temporary dev affordance proving the anchoring core +
 * paint layer end-to-end in the packaged app. DELETED in PR5 (annotation UI).
 *
 * Three entry points (the ui-automation bridge has no js-eval action, so a
 * window global alone would not be harness-drivable):
 *
 * 1. Marker comments in the document itself:
 *      <!-- attn-anchor-spike: "quoted rendered text" -->
 *      <!-- attn-anchor-spike: deletion "quoted text" -->
 *    Scanned on the RAW content string (sanitize strips comments, so markers
 *    never render). The quoted text is matched against RENDERED block texts —
 *    quote what appears on screen, post smart-punctuation.
 * 2. `window.__attnAnchorSpike` — `annotate(text, kind?)`, `list()`, `clear()`
 *    for human console poking.
 * 3. The `markdown_get_anchor_spike_state` bridge action reads the same
 *    global so the harness can assert paint/rebase/orphan without
 *    screenshots-only evidence.
 *
 * Live-reload survival: the hook lives in the OUTER MarkdownReader with an
 * effect keyed on `content`. Unchanged string → parent memo blocks the
 * re-render, the effect never fires, Ranges stay valid (CSS.highlights
 * survives untouched DOM) — zero work on the common path, same contract as
 * the body's re-render gate. Changed string → the body remounted during the
 * commit (react-markdown renders synchronously; the DOM is final by effect
 * time), so we clearAll → resolveOrRebase each tracked anchor → repaint;
 * orphans are logged with the '[md-anchor-spike]' prefix.
 *
 * Known limitation (accepted for the spike): async shiki highlighting swaps
 * code-block innards after commit, so paints inside `pre` blocks may detach
 * until the next content change. PR5 can add a MutationObserver or
 * repaint-on-highlight-done if code-block annotation matters.
 */

import { useEffect, useRef } from 'react';
import type { RefObject } from 'react';
import { createAnchor } from './create';
import { resolveDomRange } from './domRange';
import { extractBlockTexts } from './extractBlocks';
import { createHighlightPainter, type HighlightKind, type HighlightPainter } from './painter';
import { resolveOrRebase } from './resolve';
import type { AnchorRecord, BlockText } from './types';

/** Cheap gate token: documents without it cost one includes() per reload. */
const MARKER_TOKEN = 'attn-anchor-spike';

const MARKER_RE = /<!--\s*attn-anchor-spike:\s*(?:(comment|deletion)\s+)?"([^"]+)"\s*-->/g;

const WARN_PREFIX = '[md-anchor-spike]';

interface SpikeEntry {
  /** Stable key: marker kind+text+occurrence, or manual:<n>. */
  key: string;
  kind: HighlightKind;
  /** The quoted text the entry was created from (for display/debugging). */
  markerText: string;
  /** Last known record; kept on orphaned entries for inspection. */
  anchor: AnchorRecord | null;
  state: 'painted' | 'orphan';
  reason?: string;
}

export interface AnchorSpikeState {
  mode: 'custom-highlight' | 'mark' | 'none';
  anchors: Array<{
    key: string;
    kind: HighlightKind;
    state: 'painted' | 'orphan';
    reason?: string;
    exact: string | null;
    blockId: string | null;
    start: number | null;
    end: number | null;
    startLine: number | null;
    endLine: number | null;
  }>;
}

export interface AnchorSpikeGlobal {
  annotate(text: string, kind?: HighlightKind): AnchorSpikeState['anchors'][number] | null;
  list(): AnchorSpikeState;
  clear(): void;
}

declare global {
  interface Window {
    /** Spike-only; removed with this module in PR5. */
    __attnAnchorSpike?: AnchorSpikeGlobal;
  }
}

function parseMarkers(content: string): Array<{ key: string; kind: HighlightKind; text: string }> {
  const seen = new Map<string, number>();
  const out: Array<{ key: string; kind: HighlightKind; text: string }> = [];
  for (const match of content.matchAll(MARKER_RE)) {
    const kind = (match[1] ?? 'comment') as HighlightKind;
    const text = match[2];
    const base = `${kind}:${text}`;
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    out.push({ key: `${base}#${n}`, kind, text });
  }
  return out;
}

/**
 * Anchor over the first occurrence of `text` in the FIRST deepest paintable
 * block containing it (spec §8: first block, deepest owner). nonPaintable
 * blocks (mermaid) are excluded — their text-space diverges from the DOM.
 */
function createAnchorForText(
  content: string,
  text: string,
  blocks: BlockText[],
): AnchorRecord | null {
  const hits = blocks.filter((b) => !b.nonPaintable && b.text.includes(text));
  if (hits.length === 0) {
    return null;
  }
  // `>` (not `>=`) keeps the FIRST block in document order among equal depths.
  const block = hits.reduce((a, b) => (b.depth > a.depth ? b : a));
  const start = block.text.indexOf(text);
  return createAnchor(content, block.blockId, start, start + text.length, blocks);
}

function toStateEntry(entry: SpikeEntry): AnchorSpikeState['anchors'][number] {
  return {
    key: entry.key,
    kind: entry.kind,
    state: entry.state,
    reason: entry.reason,
    exact: entry.anchor?.exact ?? null,
    blockId: entry.anchor?.blockId ?? null,
    start: entry.anchor?.start ?? null,
    end: entry.anchor?.end ?? null,
    startLine: entry.anchor?.startLine ?? null,
    endLine: entry.anchor?.endLine ?? null,
  };
}

/**
 * Mount in the outer (non-memoized) MarkdownReader. No-ops for documents
 * without the marker token unless console annotations exist.
 */
export function useAnchorSpike(rootRef: RefObject<HTMLDivElement | null>, content: string): void {
  const registryRef = useRef<Map<string, SpikeEntry>>(new Map());
  const painterRef = useRef<HighlightPainter | null>(null);
  const manualCounterRef = useRef(0);
  // The effect reads the latest content; the global's annotate() does too.
  const contentRef = useRef(content);
  contentRef.current = content;

  useEffect(() => {
    const registry = registryRef.current;
    if (!content.includes(MARKER_TOKEN) && registry.size === 0) {
      return; // zero cost for every normal document
    }
    const root = rootRef.current;
    if (!root) {
      return;
    }
    const painter = (painterRef.current ??= createHighlightPainter(root));

    // Stale Ranges reference detached nodes after the body remount: always
    // clear before repainting.
    painter.clearAll();

    // One pipeline run per content change, shared by every entry below.
    const blocks = extractBlockTexts(content);
    const next = new Map<string, SpikeEntry>();

    // Markers: keep rebasing entries that survive, create the new ones.
    for (const marker of parseMarkers(content)) {
      const existing = registry.get(marker.key);
      next.set(marker.key, refreshEntry(existing ?? {
        key: marker.key,
        kind: marker.kind,
        markerText: marker.text,
        anchor: null,
        state: 'orphan',
      }, content, blocks));
    }
    // Console-made annotations survive reloads via the same rebase path.
    for (const [key, entry] of registry) {
      if (key.startsWith('manual:')) {
        next.set(key, refreshEntry(entry, content, blocks));
      }
    }

    registryRef.current = next;
    for (const entry of next.values()) {
      paintEntry(root, painter, entry);
    }

    // Unmount: drop this reader's highlights (Ranges over soon-detached
    // nodes; under Custom Highlights they'd otherwise linger in the shared
    // per-document registry). Also runs before each re-run — redundant with
    // the clearAll above, harmless.
    return () => {
      painter.clearAll();
    };
  }, [content, rootRef]);

  // Window global — registered once per reader mount, torn down on unmount.
  useEffect(() => {
    const global: AnchorSpikeGlobal = {
      annotate(text, kind = 'comment') {
        const root = rootRef.current;
        if (!root) {
          return null;
        }
        const painter = (painterRef.current ??= createHighlightPainter(root));
        const entry: SpikeEntry = refreshEntry({
          key: `manual:${manualCounterRef.current++}`,
          kind,
          markerText: text,
          anchor: null,
          state: 'orphan',
        }, contentRef.current, extractBlockTexts(contentRef.current));
        registryRef.current.set(entry.key, entry);
        paintEntry(root, painter, entry);
        return toStateEntry(entry);
      },
      list() {
        return {
          mode: painterRef.current?.mode ?? 'none',
          anchors: [...registryRef.current.values()].map(toStateEntry),
        };
      },
      clear() {
        painterRef.current?.clearAll();
        registryRef.current.clear();
      },
    };
    window.__attnAnchorSpike = global;
    return () => {
      if (window.__attnAnchorSpike === global) {
        delete window.__attnAnchorSpike;
      }
    };
  }, [rootRef]);
}

/**
 * Bring an entry up to date against `content`: resolve/rebase a tracked
 * anchor, or create a fresh one from the marker text. Pure over the entry
 * (returns a new object); logs orphan transitions.
 */
function refreshEntry(entry: SpikeEntry, content: string, blocks: BlockText[]): SpikeEntry {
  if (entry.anchor) {
    const result = resolveOrRebase(content, entry.anchor, blocks);
    if (result.state === 'orphan') {
      console.warn(WARN_PREFIX, 'orphan', result.reason, JSON.stringify(entry.anchor.exact));
      return { ...entry, state: 'orphan', reason: result.reason };
    }
    // nonPaintable owner (mermaid): the anchor is valid in text space but the
    // DOM renders an svg — painting would hit unrelated nodes. Keep the
    // record for inspection, skip the paint.
    if (blocks.find((b) => b.blockId === result.blockId)?.nonPaintable) {
      console.warn(WARN_PREFIX, 'orphan', 'non-paintable-block', JSON.stringify(result.anchor.exact));
      return { ...entry, anchor: result.anchor, state: 'orphan', reason: 'non-paintable-block' };
    }
    return { ...entry, anchor: result.anchor, state: 'painted', reason: undefined };
  }
  const anchor = createAnchorForText(content, entry.markerText, blocks);
  if (!anchor) {
    console.warn(WARN_PREFIX, 'orphan', 'marker-text-not-found', JSON.stringify(entry.markerText));
    return { ...entry, state: 'orphan', reason: 'marker-text-not-found' };
  }
  return { ...entry, anchor, state: 'painted', reason: undefined };
}

/** Resolve the DOM range and paint; downgrades to orphan when unpaintable. */
function paintEntry(root: HTMLElement, painter: HighlightPainter, entry: SpikeEntry): void {
  if (entry.state !== 'painted' || !entry.anchor) {
    return;
  }
  const blockEl = root.querySelector(`[data-block-id="${entry.anchor.blockId}"]`);
  const range = blockEl ? resolveDomRange(blockEl, entry.anchor.start, entry.anchor.end) : null;
  if (!range) {
    console.warn(WARN_PREFIX, 'orphan', 'unpaintable', JSON.stringify(entry.anchor.exact));
    entry.state = 'orphan';
    entry.reason = 'unpaintable';
    return;
  }
  painter.paint(entry.key, range, entry.kind);
}
