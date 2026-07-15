/**
 * useAnnotations — the markdown annotation engine. Owns:
 *
 * - the annotation list + a parallel non-persisted orphan map,
 * - painter integration (comment/deletion highlights via the anchoring
 *   paint layer; 'global' annotations have no paint),
 * - the content-keyed resolve/rebase pass (the live-reload contract the
 *   PR4 spike proved — see the effect comment below),
 * - selection machinery (mouseup → validation → pending anchor the
 *   selection toolbar consumes),
 * - daemon draft persistence (hydrate on mount, 500ms debounced full-list
 *   saves with a pre-incremented generation, tombstoning clears — the
 *   plannotator useAnnotationDraft contract over the websocket transport).
 *
 * Mounted in the OUTER MarkdownReader (outside the memo-gated body) so the
 * content effect fires exactly when the body remounted: unchanged content →
 * memo gate blocks the re-render → the effect never fires → painted Ranges
 * stay valid; zero work on the 750ms live-reload common path. Changed
 * content → the body remounted synchronously during commit, so by effect
 * time the DOM is final: clearAll → resolveOrRebase every anchor → repaint.
 *
 * Known seam (inherited from the spike): async shiki swaps code-block
 * innards post-commit, which detaches in-`pre` Ranges. Mitigation: one
 * rAF-deferred repaint pass for annotations whose block contains a `pre`.
 * If live-verify shows it insufficient, document — do not add a
 * MutationObserver in this PR.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { RefObject } from 'react';
import {
  createAnchor,
  extractBlockTexts,
  createHighlightPainter,
  resolveDomRange,
  resolveOrRebase,
} from '../anchoring';
import type { BlockText, HighlightKind, HighlightPainter, OrphanReason } from '../anchoring';
import {
  registerMarkdownAnnotationsAutomationHandle,
  type MarkdownAnnotationsAutomationState,
} from './annotationsAutomation';
import { evaluateSelection, type PendingSelection, type SelectionLike } from './selection';
import { getMarkdownAnnotationsTransport, type MarkdownAnnotationsTransport } from './transport';
import { annotationFromWire, annotationToWire, type Annotation } from './types';
import type { QuickLabel } from './quickLabels';

export const ANNOTATION_SAVE_DEBOUNCE_MS = 500;
/** Re-try cadence after a failed hydrate — saves stay locked until one succeeds. */
export const ANNOTATION_HYDRATE_RETRY_MS = 2000;
/** Re-try cadence after a failed save/clear (daemon down, socket blip). */
export const ANNOTATION_SAVE_RETRY_MS = 5000;

/** Painter id for the provisional (pre-toolbar-action) selection highlight. */
const PENDING_PAINT_ID = 'md-pending-selection';
/** Painter id for the transient sidebar-focus glow. */
const FOCUS_PAINT_ID = 'md-focus-glow';
const FOCUS_GLOW_MS = 2000;

export type AnnotationOrphanReason = OrphanReason | 'non-paintable-block' | 'unpaintable';

export interface UseAnnotationsOptions {
  rootRef: RefObject<HTMLElement | null>;
  /** Raw markdown content — MUST be the same string the reader body renders. */
  content: string;
  /** Absolute document path — the daemon draft key. */
  path: string;
  /** False disables everything (chat-surface readers never annotate). */
  enabled: boolean;
  /** Test seam. Defaults to the module-registered app transport. */
  transport?: MarkdownAnnotationsTransport | null;
}

export interface UseAnnotationsApi {
  annotations: Annotation[];
  /** Non-persisted, derived per content pass. Orphans stay listed + sendable. */
  orphans: Map<string, AnnotationOrphanReason>;
  selectedId: string | null;
  pending: PendingSelection | null;

  /** Feed a (real or synthetic) selection through validation. Used by the
      internal mouseup listener; exposed for tests and the toolbar layer. */
  handleSelectionChange(selection: SelectionLike | null): PendingSelection | null;
  /** Whole-block pending (code-block hover toolbar path). */
  beginBlockSelection(blockId: string): PendingSelection | null;
  clearPendingSelection(): void;

  addDeletion(): Annotation | null;
  submitComment(text: string): Annotation | null;
  applyQuickLabel(label: QuickLabel): Annotation | null;
  addGlobalComment(text: string): Annotation | null;
  deleteAnnotation(id: string): void;
  clearAll(): void;

  /** Run any armed debounced save now; resolves when it settles (PR6 send).
      Also awaits a save that already left the debounce window and is
      mid-round-trip, so a Send can never race past an in-flight save. */
  flushPendingSave(): Promise<void>;
  /** True once the daemon draft (or its absence) has been loaded. While
      false, local edits are NOT persisted (saves are suppressed), so a Send
      would deliver the daemon's stale draft — callers must gate on this. */
  isHydrated(): boolean;
  /** Empty local state after a delivered send WITHOUT a daemon clear (the
      daemon already tombstoned); seeds the generation counter from `floor`. */
  applyDeliveredClear(generationFloor: number): void;

  selectAnnotation(id: string | null): void;
  /** Select + glow + scroll the highlight into view (sidebar card click). */
  focusAnnotation(id: string): void;

  /** Set on every create; the sidebar skips focus-scroll for it. */
  justCreatedIdRef: RefObject<string | null>;
  /** The layer mirrors its popover-open state here so THIS tile's mouseup
      guard reads its own popover, not any popover in the document (two
      markdown tiles must not block each other's selection handling). */
  popoverOpenRef: RefObject<boolean>;
  /** Last capture-phase mouseup position (quick-label picker placement). */
  lastMousePosRef: RefObject<{ x: number; y: number } | null>;
  painterMode: 'custom-highlight' | 'mark' | 'none';
}

function paintKindFor(annotation: Annotation): HighlightKind {
  return annotation.type === 'deletion' ? 'deletion' : 'comment';
}

export function useAnnotations({
  rootRef,
  content,
  path,
  enabled,
  transport,
}: UseAnnotationsOptions): UseAnnotationsApi {
  const [annotations, setAnnotations] = useState<Annotation[]>([]);
  const [orphans, setOrphans] = useState<Map<string, AnnotationOrphanReason>>(new Map());
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [pending, setPending] = useState<PendingSelection | null>(null);

  const annotationsRef = useRef<Annotation[]>(annotations);
  const pendingRef = useRef<PendingSelection | null>(null);
  const selectedIdRef = useRef<string | null>(null);
  const painterRef = useRef<HighlightPainter | null>(null);
  const blocksRef = useRef<BlockText[] | null>(null);
  const rangesRef = useRef<Map<string, Range>>(new Map());
  const contentRef = useRef(content);
  contentRef.current = content;
  // Written by the hydrate effect (not render) so its CLEANUP still sees the
  // previous path and can flush the old document's pending save.
  const pathRef = useRef(path);
  const generationRef = useRef(0);
  const hasHydratedRef = useRef(false);
  const hydrateTokenRef = useRef(0);
  const hydrateRetryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const rafRef = useRef<number | null>(null);
  const focusTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const justCreatedIdRef = useRef<string | null>(null);
  const lastMousePosRef = useRef<{ x: number; y: number } | null>(null);
  const popoverOpenRef = useRef(false);
  const orphansRef = useRef(orphans);

  const transportRef = useRef<MarkdownAnnotationsTransport | null | undefined>(transport);
  transportRef.current = transport;
  const getTransport = useCallback((): MarkdownAnnotationsTransport | null => {
    return transportRef.current !== undefined
      ? transportRef.current
      : getMarkdownAnnotationsTransport();
  }, []);

  const ensurePainter = useCallback((): HighlightPainter | null => {
    const root = rootRef.current;
    if (!root) {
      return null;
    }
    return (painterRef.current ??= createHighlightPainter(root));
  }, [rootRef]);

  // ---- persistence -------------------------------------------------------

  const persistNowRef = useRef<(() => void) | null>(null);
  /** The save/clear currently on the wire (null when none). flushPendingSave
      awaits it so a Send after the debounce fired — but before its request
      settled — cannot read the draft ahead of that save and then tombstone
      the edit undelivered. */
  const inFlightPersistRef = useRef<Promise<void> | null>(null);

  /** Transport failure: warn (repo logging conventions) and re-arm the save
      timer so the draft re-persists once the socket is back, instead of
      silently dropping the last debounced edit. Retry stops on unmount and
      on path change (the guard below). */
  const schedulePersistRetry = useCallback((savePath: string, op: string, err: unknown) => {
    console.warn(`[md-annotations] ${op} failed for ${savePath}; retrying`, err);
    if (!mountedRef.current || pathRef.current !== savePath || saveTimerRef.current !== null) {
      return;
    }
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null;
      persistNowRef.current?.();
    }, ANNOTATION_SAVE_RETRY_MS);
  }, []);

  /** Returns a promise that settles when the triggered save/clear settles
      (resolves on failure too — the retry timer handles re-persisting). */
  const persistNow = useCallback((): Promise<void> => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    if (!hasHydratedRef.current) {
      return Promise.resolve(); // never save over a draft we have not loaded yet
    }
    const t = getTransport();
    if (!t) {
      return Promise.resolve(); // local-only mode
    }
    const savePath = pathRef.current;
    generationRef.current += 1;
    const generation = generationRef.current;
    const list = annotationsRef.current;
    const request = list.length === 0
      ? // Last annotation removed: tombstone instead of saving [] so a stale
        // stored draft can never offer back deleted content (plannotator
        // remove-on-empty semantics; also the primitive PR6's clear-on-send uses).
        t.clearMarkdownAnnotations(savePath, generation)
          .then(({ generation: floor }) => {
            generationRef.current = Math.max(generationRef.current, floor);
          })
          .catch((err: unknown) => schedulePersistRetry(savePath, 'clear', err))
      : t.saveMarkdownAnnotations(savePath, list.map(annotationToWire), generation)
          .then(({ stale }) => {
            if (stale && pathRef.current === savePath) {
              // A tombstone (or newer writer) raced us: drop local pending state
              // and re-hydrate the authoritative draft.
              void hydrateRef.current?.();
            }
          })
          .catch((err: unknown) => schedulePersistRetry(savePath, 'save', err));
    inFlightPersistRef.current = request;
    const settle = () => {
      if (inFlightPersistRef.current === request) {
        inFlightPersistRef.current = null;
      }
    };
    request.then(settle, settle);
    return request;
  }, [getTransport, schedulePersistRetry]);
  persistNowRef.current = persistNow;

  const scheduleSave = useCallback(() => {
    if (!hasHydratedRef.current) {
      return;
    }
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
    }
    saveTimerRef.current = setTimeout(() => {
      saveTimerRef.current = null;
      persistNow();
    }, ANNOTATION_SAVE_DEBOUNCE_MS);
  }, [persistNow]);

  /** Run any armed debounced save immediately. Resolves when the flushed
      save settles (on `stale` too — the daemon has newer truth either way);
      no-op resolve when nothing is pending. The PR6 send flow awaits this
      before submitting so the payload includes the last keystroke's edit. */
  const flushPendingSave = useCallback((): Promise<void> => {
    if (saveTimerRef.current === null) {
      // No armed debounce — but a save whose debounce already fired may still
      // be mid-round-trip; await it so callers observe a settled draft.
      return inFlightPersistRef.current ?? Promise.resolve();
    }
    clearTimeout(saveTimerRef.current);
    saveTimerRef.current = null;
    return persistNow();
  }, [persistNow]);

  // ---- paint / rebase ----------------------------------------------------

  /**
   * Bring every annotation up to date against `nextContent` and repaint.
   * Rebased anchors are written back into the list AND re-persisted (the
   * re-baselined lines are what PR6 sends). Orphans keep their last-known
   * anchor for sidebar display but are never painted.
   */
  const refreshAndPaint = useCallback(
    (nextContent: string) => {
      const root = rootRef.current;
      const painter = ensurePainter();
      if (!root || !painter) {
        return;
      }
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      // Stale Ranges reference detached nodes after the body remount: always
      // clear before repainting.
      painter.clearAll();
      rangesRef.current.clear();

      const blocks = extractBlockTexts(nextContent);
      blocksRef.current = blocks;

      const next: Annotation[] = [];
      const nextOrphans = new Map<string, AnnotationOrphanReason>();
      let rebasedAny = false;

      const paintOne = (annotation: Annotation): boolean => {
        const anchor = annotation.anchor!;
        const blockEl = root.querySelector(`[data-block-id="${anchor.blockId}"]`);
        const range = blockEl ? resolveDomRange(blockEl, anchor.start, anchor.end) : null;
        if (!range) {
          return false;
        }
        painter.paint(annotation.id, range, paintKindFor(annotation));
        rangesRef.current.set(annotation.id, range);
        return true;
      };

      for (const annotation of annotationsRef.current) {
        if (!annotation.anchor) {
          next.push(annotation); // global: nothing to resolve or paint
          continue;
        }
        const result = resolveOrRebase(nextContent, annotation.anchor, blocks);
        if (result.state === 'orphan') {
          nextOrphans.set(annotation.id, result.reason);
          next.push(annotation); // keep last-known anchor for sidebar display
          continue;
        }
        let updated = annotation;
        if (result.state === 'rebased') {
          updated = { ...annotation, anchor: result.anchor };
          rebasedAny = true;
        }
        if (blocks.find((b) => b.blockId === result.blockId)?.nonPaintable) {
          // Valid in text space but the DOM renders an svg (mermaid): keep
          // the record, skip the paint.
          nextOrphans.set(updated.id, 'non-paintable-block');
          next.push(updated);
          continue;
        }
        next.push(updated);
        if (!paintOne(updated)) {
          nextOrphans.set(updated.id, 'unpaintable');
        }
      }

      annotationsRef.current = next;
      orphansRef.current = nextOrphans;
      setAnnotations(next);
      setOrphans(nextOrphans);
      if (rebasedAny) {
        scheduleSave(); // re-baselining must persist (plan §Anchoring)
      }

      // Shiki seam: async highlighting swaps <pre> innards after commit,
      // detaching any Range painted inside. One deferred repaint pass.
      const inPreIds = next
        .filter((a) => {
          if (!a.anchor || nextOrphans.has(a.id)) {
            return false;
          }
          const el = root.querySelector(`[data-block-id="${a.anchor.blockId}"]`);
          return !!el && (el.tagName === 'PRE' || el.querySelector('pre') !== null);
        })
        .map((a) => a.id);
      if (inPreIds.length > 0 && typeof requestAnimationFrame === 'function') {
        rafRef.current = requestAnimationFrame(() => {
          rafRef.current = null;
          // `nextOrphans` was already committed via setOrphans: a late paint
          // failure must publish a FRESH map (mutating the committed one
          // bypasses React and hides the orphan badge) and drop the stale
          // detached Range so click hit-testing never uses dead rects.
          const failed: string[] = [];
          for (const id of inPreIds) {
            const annotation = annotationsRef.current.find((a) => a.id === id);
            if (annotation && !paintOne(annotation)) {
              painter.clear(id);
              rangesRef.current.delete(id);
              failed.push(id);
            }
          }
          if (failed.length > 0) {
            const republished = new Map(orphansRef.current);
            for (const id of failed) {
              republished.set(id, 'unpaintable');
            }
            orphansRef.current = republished;
            setOrphans(republished);
          }
        });
      }
    },
    [ensurePainter, rootRef, scheduleSave],
  );

  // Content-keyed effect — the live-reload contract (see module comment).
  useEffect(() => {
    if (!enabled) {
      return;
    }
    refreshAndPaint(content);
    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      painterRef.current?.clearAll();
      rangesRef.current.clear();
    };
  }, [content, enabled, refreshAndPaint, rootRef]);

  // ---- hydration ---------------------------------------------------------

  const hydrate = useCallback(async () => {
    const token = ++hydrateTokenRef.current;
    if (hydrateRetryTimerRef.current) {
      clearTimeout(hydrateRetryTimerRef.current);
      hydrateRetryTimerRef.current = null;
    }
    hasHydratedRef.current = false;
    const t = getTransport();
    if (!t) {
      hasHydratedRef.current = true; // local-only: annotations work in-session
      return;
    }
    try {
      const result = await t.getMarkdownAnnotations(pathRef.current);
      if (hydrateTokenRef.current !== token) {
        return; // superseded by a newer hydrate (path change / stale re-sync)
      }
      generationRef.current = Math.max(generationRef.current, result.generation);
      const list = result.annotations
        .map(annotationFromWire)
        .filter((a): a is Annotation => a !== null);
      annotationsRef.current = list;
      hasHydratedRef.current = true;
      // Anchors resolve/rebase against the CURRENT content (records carry
      // contentHash, so a file edited while closed rebases exactly once here).
      refreshAndPaint(contentRef.current);
    } catch (err) {
      if (hydrateTokenRef.current !== token) {
        return;
      }
      // Keep saves SUPPRESSED (hasHydratedRef stays false) and retry: marking
      // this "hydrated" would let a generation-0 save go out, come back stale
      // against any prior draft/tombstone floor, and the stale re-hydrate
      // would then wipe every annotation the user just created.
      console.warn(`[md-annotations] hydrate failed for ${pathRef.current}; retrying`, err);
      hydrateRetryTimerRef.current = setTimeout(() => {
        hydrateRetryTimerRef.current = null;
        if (hydrateTokenRef.current === token && mountedRef.current) {
          void hydrateRef.current?.();
        }
      }, ANNOTATION_HYDRATE_RETRY_MS);
    }
  }, [getTransport, refreshAndPaint]);
  const hydrateRef = useRef<typeof hydrate | null>(null);
  hydrateRef.current = hydrate;

  useEffect(() => {
    if (!enabled) {
      return;
    }
    pathRef.current = path;
    generationRef.current = 0;
    annotationsRef.current = [];
    orphansRef.current = new Map();
    setAnnotations([]);
    setOrphans(new Map());
    setSelectedId(null);
    selectedIdRef.current = null;
    pendingRef.current = null;
    setPending(null);
    void hydrate();
    return () => {
      // Leaving this document (path change or unmount): flush any pending
      // save for it before the refs are reset for the next one.
      flushPendingSave();
      hydrateTokenRef.current += 1; // invalidate in-flight hydration
      if (hydrateRetryTimerRef.current) {
        clearTimeout(hydrateRetryTimerRef.current);
        hydrateRetryTimerRef.current = null;
      }
      hasHydratedRef.current = false;
    };
  }, [path, enabled, hydrate, flushPendingSave]);

  // Flush the debounce window when the app is backgrounded or the window
  // closes — best-effort fire-through-socket, no keepalive equivalent needed.
  useEffect(() => {
    if (!enabled) {
      return;
    }
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        flushPendingSave();
      }
    };
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('pagehide', flushPendingSave);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('pagehide', flushPendingSave);
    };
  }, [enabled, flushPendingSave]);

  // ---- pending selection -------------------------------------------------

  const clearPendingSelection = useCallback(() => {
    painterRef.current?.clear(PENDING_PAINT_ID);
    pendingRef.current = null;
    setPending(null);
    try {
      window.getSelection()?.removeAllRanges();
    } catch {
      // test DOMs without a Selection implementation
    }
  }, []);

  const paintPending = useCallback(
    (next: PendingSelection): void => {
      const root = rootRef.current;
      const painter = ensurePainter();
      if (!root || !painter) {
        return;
      }
      const blockEl = root.querySelector(`[data-block-id="${next.anchor.blockId}"]`);
      const range = blockEl ? resolveDomRange(blockEl, next.anchor.start, next.anchor.end) : null;
      if (range) {
        painter.paint(PENDING_PAINT_ID, range, 'comment');
      }
    },
    [ensurePainter, rootRef],
  );

  const handleSelectionChange = useCallback(
    (selection: SelectionLike | null): PendingSelection | null => {
      const root = rootRef.current;
      if (!root) {
        return null;
      }
      const blocks = (blocksRef.current ??= extractBlockTexts(contentRef.current));
      const next = evaluateSelection(root, selection, contentRef.current, blocks);
      if (!next) {
        clearPendingSelection();
        return null;
      }
      painterRef.current?.clear(PENDING_PAINT_ID);
      paintPending(next);
      pendingRef.current = next;
      setPending(next);
      return next;
    },
    [clearPendingSelection, paintPending, rootRef],
  );

  const beginBlockSelection = useCallback(
    (blockId: string): PendingSelection | null => {
      const root = rootRef.current;
      const blocks = (blocksRef.current ??= extractBlockTexts(contentRef.current));
      const block = blocks.find((b) => b.blockId === blockId);
      if (!root || !block || block.nonPaintable || block.text.trim() === '') {
        return null;
      }
      const anchor = createAnchor(contentRef.current, blockId, 0, block.text.length, blocks);
      if (!anchor) {
        return null;
      }
      const blockEl = root.querySelector(`[data-block-id="${anchor.blockId}"]`);
      let rect: DOMRect | null = null;
      try {
        rect = blockEl?.getBoundingClientRect() ?? null;
      } catch {
        rect = null;
      }
      const next: PendingSelection = {
        anchor,
        selectionText: anchor.exact,
        clamped: false,
        blockId: anchor.blockId,
        isCodeBlock: true,
        rect,
      };
      painterRef.current?.clear(PENDING_PAINT_ID);
      paintPending(next);
      pendingRef.current = next;
      setPending(next);
      // Same focus claim as the mouseup path: macOS WebKit does not focus
      // buttons on click, so the whole-block gesture must also pull keyboard
      // focus into the reader for type-to-comment.
      try {
        root.focus({ preventScroll: true });
      } catch {
        // test DOMs without focus options support
      }
      return next;
    },
    [paintPending, rootRef],
  );

  // ---- selection / hit-test listeners -------------------------------------

  useEffect(() => {
    if (!enabled) {
      return;
    }
    const root = rootRef.current;
    if (!root) {
      return;
    }

    const onDocMouseUp = (event: MouseEvent) => {
      lastMousePosRef.current = { x: event.clientX, y: event.clientY };
    };

    const onRootMouseUp = (event: MouseEvent) => {
      const target = event.target;
      if (target instanceof Element && target.closest('.md-selection-toolbar, .md-annotation-popover, .md-quick-label-picker, .md-annotations-sidebar')) {
        return; // interacting with annotation chrome never disturbs pending
      }
      if (popoverOpenRef.current) {
        // A comment is being composed IN THIS TILE: a stray click must not
        // clear the pending selection out from under the open popover (its
        // dirty-close guard keeps it open, and submit needs the pending).
        // Per-tile state, not a document query: another tile's popover must
        // not block selection handling here.
        return;
      }
      const next = handleSelectionChange(window.getSelection());
      if (next) {
        // Claim keyboard focus on an explicit selection gesture: WebKit does
        // not move focus on mousedown in non-focusable content, so without
        // this the terminal's hidden input keeps document.activeElement —
        // type-to-comment keys leak to the shell and the toolbar's
        // editable-element guard blocks them. Only a real selection steals
        // focus (terminal focus ownership stays intact for plain clicks).
        try {
          root.focus({ preventScroll: true });
        } catch {
          // test DOMs without focus options support
        }
      }
    };

    const onRootClick = (event: MouseEvent) => {
      const selection = window.getSelection?.();
      if (selection && !selection.isCollapsed) {
        return; // a drag-select click is not a highlight click
      }
      // Mark-fallback mode: the paint itself split/wrapped the text nodes,
      // collapsing the Ranges cached below — but the wrapper spans carry the
      // annotation id, so hit the DOM directly first.
      const markEl =
        event.target instanceof Element ? event.target.closest('[data-md-mark]') : null;
      const markId = markEl?.getAttribute('data-md-mark');
      if (markId && annotationsRef.current.some((a) => a.id === markId)) {
        selectedIdRef.current = markId;
        setSelectedId(markId);
        return;
      }
      // CustomHighlightPainter has no DOM to click: point-in-range hit-test
      // over the painted ranges. O(annotations); lists are small.
      for (const [id, range] of rangesRef.current) {
        for (const rect of range.getClientRects()) {
          if (
            event.clientX >= rect.left &&
            event.clientX <= rect.right &&
            event.clientY >= rect.top &&
            event.clientY <= rect.bottom
          ) {
            selectedIdRef.current = id;
            setSelectedId(id);
            return;
          }
        }
      }
    };

    document.addEventListener('mouseup', onDocMouseUp, true);
    root.addEventListener('mouseup', onRootMouseUp);
    root.addEventListener('click', onRootClick);
    return () => {
      document.removeEventListener('mouseup', onDocMouseUp, true);
      root.removeEventListener('mouseup', onRootMouseUp);
      root.removeEventListener('click', onRootClick);
    };
  }, [enabled, handleSelectionChange, rootRef]);

  // ---- creation / mutation -----------------------------------------------

  const addAnnotation = useCallback(
    (annotation: Annotation): Annotation => {
      const next = [...annotationsRef.current, annotation];
      annotationsRef.current = next;
      setAnnotations(next);
      justCreatedIdRef.current = annotation.id;
      if (annotation.anchor) {
        const root = rootRef.current;
        const painter = ensurePainter();
        const blockEl = root?.querySelector(`[data-block-id="${annotation.anchor.blockId}"]`);
        const range = blockEl
          ? resolveDomRange(blockEl, annotation.anchor.start, annotation.anchor.end)
          : null;
        if (painter && range) {
          painter.paint(annotation.id, range, paintKindFor(annotation));
          rangesRef.current.set(annotation.id, range);
        }
      }
      scheduleSave();
      return annotation;
    },
    [ensurePainter, rootRef, scheduleSave],
  );

  const createFromPending = useCallback(
    (build: (pendingSelection: PendingSelection) => Omit<Annotation, 'id' | 'createdAt'>): Annotation | null => {
      const pendingSelection = pendingRef.current;
      if (!pendingSelection) {
        return null;
      }
      const annotation: Annotation = {
        id: crypto.randomUUID(),
        createdAt: Date.now(),
        ...build(pendingSelection),
      };
      clearPendingSelection();
      return addAnnotation(annotation);
    },
    [addAnnotation, clearPendingSelection],
  );

  const addDeletion = useCallback(
    () => createFromPending((p) => ({ type: 'deletion', anchor: p.anchor })),
    [createFromPending],
  );

  const submitComment = useCallback(
    (text: string) =>
      createFromPending((p) => ({ type: 'comment', text, anchor: p.anchor })),
    [createFromPending],
  );

  const applyQuickLabel = useCallback(
    (label: QuickLabel) =>
      createFromPending((p) => ({
        type: 'comment',
        anchor: p.anchor,
        quickLabelId: label.id,
        ...(label.tip !== undefined ? { quickLabelTip: label.tip } : {}),
        // Display text snapshotted at creation: the daemon-side send-payload
        // formatter has no copy of the label set, so the wire record carries
        // what the user saw (falls back to the raw id for older drafts).
        quickLabelText: `${label.emoji} ${label.text}`,
      })),
    [createFromPending],
  );

  const addGlobalComment = useCallback(
    (text: string): Annotation | null => {
      if (text.trim() === '') {
        return null;
      }
      return addAnnotation({
        id: crypto.randomUUID(),
        type: 'global',
        text,
        createdAt: Date.now(),
      });
    },
    [addAnnotation],
  );

  const deleteAnnotation = useCallback(
    (id: string) => {
      const next = annotationsRef.current.filter((a) => a.id !== id);
      if (next.length === annotationsRef.current.length) {
        return;
      }
      annotationsRef.current = next;
      setAnnotations(next);
      painterRef.current?.clear(id);
      rangesRef.current.delete(id);
      if (orphansRef.current.has(id)) {
        const nextOrphans = new Map(orphansRef.current);
        nextOrphans.delete(id);
        orphansRef.current = nextOrphans;
        setOrphans(nextOrphans);
      }
      if (selectedIdRef.current === id) {
        selectedIdRef.current = null;
        setSelectedId(null);
      }
      // Deleting the LAST annotation routes to the tombstone clear inside
      // persistNow (empty list ⇒ clear, never save-[]).
      scheduleSave();
    },
    [scheduleSave],
  );

  const clearAll = useCallback(() => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    annotationsRef.current = [];
    orphansRef.current = new Map();
    setAnnotations([]);
    setOrphans(new Map());
    selectedIdRef.current = null;
    setSelectedId(null);
    painterRef.current?.clearAll();
    rangesRef.current.clear();
    pendingRef.current = null;
    setPending(null);
    const t = getTransport();
    if (t && hasHydratedRef.current) {
      // Invalidate any in-flight re-hydrate (stale-save path): its `get` was
      // sent before this clear, so letting it resolve would resurrect the
      // pre-clear list locally. (Only when hydrated — a first hydrate still
      // in flight must survive, or saves would stay locked forever.)
      hydrateTokenRef.current += 1;
      const clearPath = pathRef.current;
      generationRef.current += 1;
      t.clearMarkdownAnnotations(clearPath, generationRef.current)
        .then(({ generation: floor }) => {
          generationRef.current = Math.max(generationRef.current, floor);
        })
        .catch((err: unknown) => schedulePersistRetry(clearPath, 'clear', err));
    }
  }, [getTransport, schedulePersistRetry]);

  /**
   * Local-only mirror of clearAll after a successful PR6 send: the daemon
   * already tombstone-cleared the draft at delivery time, so this must NOT
   * issue a second daemon clear — it only empties local state and seeds the
   * generation counter from the daemon's new floor.
   *
   * Resurrection guard: a debounced save scheduled before Send that races
   * past flushPendingSave cannot resurrect drafts — the daemon clear
   * tombstoned at the stored generation, so the straggler save comes back
   * `{stale: true}`, the save path drops pending state and re-hydrates, and
   * (thanks to the hydrate-token bump below invalidating anything older) the
   * re-hydrate returns the empty post-clear draft.
   */
  const applyDeliveredClear = useCallback((generationFloor: number) => {
    if (saveTimerRef.current) {
      clearTimeout(saveTimerRef.current);
      saveTimerRef.current = null;
    }
    hydrateTokenRef.current += 1; // invalidate in-flight re-hydrates
    // The token bump above makes any in-flight/retrying hydrate return early
    // WITHOUT ever setting hasHydratedRef — but the post-clear empty state IS
    // the authoritative daemon state at the returned floor, so mark hydrated
    // here (and stop the failed-hydrate retry loop). Leaving it false would
    // permanently suppress every subsequent save: silent draft data loss.
    if (hydrateRetryTimerRef.current) {
      clearTimeout(hydrateRetryTimerRef.current);
      hydrateRetryTimerRef.current = null;
    }
    hasHydratedRef.current = true;
    generationRef.current = Math.max(generationRef.current, generationFloor);
    annotationsRef.current = [];
    orphansRef.current = new Map();
    setAnnotations([]);
    setOrphans(new Map());
    selectedIdRef.current = null;
    setSelectedId(null);
    painterRef.current?.clearAll();
    rangesRef.current.clear();
    pendingRef.current = null;
    setPending(null);
  }, []);

  /** Call-time read (a ref, not state): the send flow checks this at submit. */
  const isHydrated = useCallback(() => hasHydratedRef.current, []);

  // ---- selection focus -----------------------------------------------------

  const selectAnnotation = useCallback((id: string | null) => {
    selectedIdRef.current = id;
    setSelectedId(id);
  }, []);

  const focusAnnotation = useCallback(
    (id: string) => {
      selectAnnotation(id);
      const painter = painterRef.current;
      // Re-resolve from the anchor rather than trusting the cached Range: in
      // mark-fallback mode the annotation's own paint split/replaced the text
      // nodes, which collapses the Range captured before painting. Offsets are
      // over text content, so re-resolution is immune to the span wrapping.
      const annotation = annotationsRef.current.find((a) => a.id === id);
      let range: Range | null = null;
      if (annotation?.anchor) {
        const blockEl = rootRef.current?.querySelector(`[data-block-id="${annotation.anchor.blockId}"]`);
        range = blockEl ? resolveDomRange(blockEl, annotation.anchor.start, annotation.anchor.end) : null;
      }
      if (!range) {
        range = rangesRef.current.get(id) ?? null;
      }
      if (!painter || !range) {
        return; // orphan: nothing painted, card click never scrolls (E22)
      }
      rangesRef.current.set(id, range);
      if (focusTimerRef.current) {
        clearTimeout(focusTimerRef.current);
      }
      painter.paint(FOCUS_PAINT_ID, range, 'focus');
      focusTimerRef.current = setTimeout(() => {
        focusTimerRef.current = null;
        painterRef.current?.clear(FOCUS_PAINT_ID);
      }, FOCUS_GLOW_MS);
      if (justCreatedIdRef.current === id) {
        justCreatedIdRef.current = null;
        return; // just-created: glow but skip the scroll (E19)
      }
      const container = range.startContainer;
      const el = container instanceof Element ? container : container.parentElement;
      el?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
    },
    [selectAnnotation],
  );

  // ---- automation bridge ---------------------------------------------------

  useEffect(() => {
    if (!enabled) {
      return;
    }
    const handle = {
      getState(): MarkdownAnnotationsAutomationState {
        return {
          available: true,
          mode: painterRef.current?.mode ?? 'none',
          path: pathRef.current,
          generation: generationRef.current,
          hydrated: hasHydratedRef.current,
          pendingSelection: pendingRef.current !== null,
          selectedId: selectedIdRef.current,
          annotations: annotationsRef.current.map((a) => ({
            id: a.id,
            type: a.type,
            text: a.text ?? null,
            quickLabelId: a.quickLabelId ?? null,
            orphaned: orphansRef.current.has(a.id),
            orphanReason: orphansRef.current.get(a.id) ?? null,
            exact: a.anchor?.exact ?? null,
            blockId: a.anchor?.blockId ?? null,
            startLine: a.anchor?.startLine ?? null,
            endLine: a.anchor?.endLine ?? null,
            start: a.anchor?.start ?? null,
            end: a.anchor?.end ?? null,
          })),
        };
      },
    };
    // Registry (not a single slot): unregistering THIS handle on unmount must
    // never blind the bridge to another still-open annotating tile.
    return registerMarkdownAnnotationsAutomationHandle(handle);
  }, [enabled]);

  // Unmount marker for async retry guards (save/hydrate timers must not
  // re-arm after the hook is gone).
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Focus-glow timer teardown.
  useEffect(() => {
    return () => {
      if (focusTimerRef.current) {
        clearTimeout(focusTimerRef.current);
      }
    };
  }, []);

  return useMemo(
    () => ({
      annotations,
      orphans,
      selectedId,
      pending,
      handleSelectionChange,
      beginBlockSelection,
      clearPendingSelection,
      addDeletion,
      submitComment,
      applyQuickLabel,
      addGlobalComment,
      deleteAnnotation,
      clearAll,
      flushPendingSave,
      isHydrated,
      applyDeliveredClear,
      selectAnnotation,
      focusAnnotation,
      justCreatedIdRef,
      lastMousePosRef,
      popoverOpenRef,
      painterMode: painterRef.current?.mode ?? 'none',
    }),
    [
      annotations,
      orphans,
      selectedId,
      pending,
      handleSelectionChange,
      beginBlockSelection,
      clearPendingSelection,
      addDeletion,
      submitComment,
      applyQuickLabel,
      addGlobalComment,
      deleteAnnotation,
      clearAll,
      flushPendingSave,
      isHydrated,
      applyDeliveredClear,
      selectAnnotation,
      focusAnnotation,
    ],
  );
}
