/**
 * AnnotationLayer — the UI orchestration over the useAnnotations engine:
 *
 * - SelectionToolbar over a pending selection (center-above for prose,
 *   top-right for code blocks) or over a hovered code block (whole-block
 *   annotation, donor behavior);
 * - AnnotationPopover for comment composition (type-to-comment seeding,
 *   draft survival, dirty-close blocking) and global comments;
 * - QuickLabelPicker (inside the toolbar) at the last mouseup cursor;
 * - AnnotationSidebar column — collapsed by default at 0 annotations,
 *   auto-opens on the first one (tile real estate is precious).
 *
 * Rendered as a sibling of the reader's document area inside the flex row;
 * toolbar/popover/picker are body portals, the sidebar is the in-flow column.
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import type { RefObject } from 'react';
import { resolveDomRange } from '../anchoring';
import { AnnotationPopover } from './AnnotationPopover';
import { AnnotationSidebar } from './AnnotationSidebar';
import { SelectionToolbar } from './SelectionToolbar';
import type { PendingSelection } from './selection';
import type { QuickLabel } from './quickLabels';
import type { UseAnnotationsApi } from './useAnnotations';

const HOVER_HIDE_GRACE_MS = 200;

type PopoverState =
  | {
      kind: 'selection';
      /** Snapshot at open time — the quote and draft key must not shift. */
      pending: PendingSelection;
      initialText?: string;
    }
  | { kind: 'global'; anchorEl: HTMLElement };

interface HoverBlock {
  blockId: string;
  /** The .md-codeblock wrapper (toolbar anchor + copy text source). */
  element: HTMLElement;
}

export interface AnnotationLayerProps {
  api: UseAnnotationsApi;
  rootRef: RefObject<HTMLElement | null>;
  path: string;
}

function pendingDraftKey(path: string, pending: PendingSelection): string {
  const { anchor } = pending;
  return `${path}#${anchor.blockId}:${anchor.start}:${anchor.end}`;
}

export function AnnotationLayer({ api, rootRef, path }: AnnotationLayerProps) {
  const { pending, annotations, orphans, selectedId } = api;
  const [popover, setPopover] = useState<PopoverState | null>(null);
  const [hoverBlock, setHoverBlock] = useState<HoverBlock | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const autoOpenedRef = useRef(false);
  const hoverHideTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Collapsed by default when empty; auto-open once on the first annotation
  // (creation or hydration).
  useEffect(() => {
    if (annotations.length > 0 && !autoOpenedRef.current) {
      autoOpenedRef.current = true;
      setSidebarOpen(true);
    }
  }, [annotations.length]);

  // Clicking a painted highlight selects it in the sidebar (E28) — reopen a
  // collapsed sidebar so the selection is actually visible.
  useEffect(() => {
    if (selectedId !== null) {
      setSidebarOpen(true);
    }
  }, [selectedId]);

  // Mirror popover-open state into the hook so its mouseup guard is scoped to
  // THIS tile (not a document-wide popover query — see useAnnotations).
  useEffect(() => {
    api.popoverOpenRef.current = popover !== null;
  }, [api.popoverOpenRef, popover]);

  // ---- code-block hover (react-side, event delegation on root) ------------
  // NOT inside CodeBlock.tsx: the annotation layer stays decoupled from the
  // renderer, and CodeBlock lives behind the memo gate where new props would
  // break the gate contract.

  const cancelHoverHide = useCallback(() => {
    if (hoverHideTimerRef.current) {
      clearTimeout(hoverHideTimerRef.current);
      hoverHideTimerRef.current = null;
    }
  }, []);

  const scheduleHoverHide = useCallback(() => {
    cancelHoverHide();
    hoverHideTimerRef.current = setTimeout(() => {
      hoverHideTimerRef.current = null;
      setHoverBlock(null);
    }, HOVER_HIDE_GRACE_MS);
  }, [cancelHoverHide]);

  useEffect(() => {
    const root = rootRef.current;
    if (!root) {
      return;
    }
    const onPointerOver = (event: Event) => {
      const target = event.target;
      if (!(target instanceof Element)) {
        return;
      }
      const wrapper = target.closest('.md-codeblock');
      if (!wrapper || !root.contains(wrapper)) {
        return;
      }
      const blockEl = wrapper.querySelector('[data-block-id]') ?? wrapper.closest('[data-block-id]');
      const blockId = blockEl?.getAttribute('data-block-id');
      if (!blockId) {
        return;
      }
      cancelHoverHide();
      setHoverBlock((prev) =>
        prev?.blockId === blockId ? prev : { blockId, element: wrapper as HTMLElement },
      );
    };
    const onPointerOut = (event: Event) => {
      const target = event.target;
      const related = (event as PointerEvent).relatedTarget;
      if (!(target instanceof Element) || !target.closest('.md-codeblock')) {
        return;
      }
      if (related instanceof Element && related.closest('.md-codeblock')) {
        return; // still inside the block
      }
      scheduleHoverHide();
    };
    root.addEventListener('pointerover', onPointerOver);
    root.addEventListener('pointerout', onPointerOut);
    return () => {
      root.removeEventListener('pointerover', onPointerOver);
      root.removeEventListener('pointerout', onPointerOut);
      cancelHoverHide();
    };
  }, [rootRef, cancelHoverHide, scheduleHoverHide]);

  // ---- toolbar anchoring ---------------------------------------------------

  const pendingRef = useRef(pending);
  pendingRef.current = pending;
  /** Live rect for the pending selection: re-resolve the DOM range so the
      toolbar tracks scroll (the stored rect is a snapshot). */
  const getPendingRect = useCallback((): DOMRect | null => {
    const current = pendingRef.current;
    if (!current) {
      return null;
    }
    const root = rootRef.current;
    const blockEl = root?.querySelector(`[data-block-id="${current.anchor.blockId}"]`);
    if (blockEl) {
      try {
        const range = resolveDomRange(blockEl, current.anchor.start, current.anchor.end);
        const rect = range?.getBoundingClientRect();
        if (rect) {
          return rect;
        }
      } catch {
        // fall through to the snapshot
      }
    }
    return current.rect;
  }, [rootRef]);

  const hoverBlockRef = useRef(hoverBlock);
  hoverBlockRef.current = hoverBlock;
  const getHoverRect = useCallback((): DOMRect | null => {
    return hoverBlockRef.current?.element.getBoundingClientRect() ?? null;
  }, []);

  const getCursorHint = useCallback(
    () => api.lastMousePosRef.current,
    [api.lastMousePosRef],
  );

  // ---- toolbar actions -----------------------------------------------------

  /** Hover-toolbar actions first turn the hovered block into a whole-block
      pending selection; selection-toolbar actions use the existing pending. */
  const ensurePending = useCallback((): PendingSelection | null => {
    if (pendingRef.current) {
      return pendingRef.current;
    }
    const hover = hoverBlockRef.current;
    return hover ? api.beginBlockSelection(hover.blockId) : null;
  }, [api]);

  const closeToolbar = useCallback(() => {
    api.clearPendingSelection();
    setHoverBlock(null);
  }, [api]);

  const handleDelete = useCallback(() => {
    if (!ensurePending()) {
      return;
    }
    api.addDeletion();
    setHoverBlock(null);
  }, [api, ensurePending]);

  const handleQuickLabel = useCallback(
    (label: QuickLabel) => {
      if (!ensurePending()) {
        return;
      }
      api.applyQuickLabel(label);
      setHoverBlock(null);
    },
    [api, ensurePending],
  );

  const handleRequestComment = useCallback(
    (initialChar?: string) => {
      const p = ensurePending();
      if (!p) {
        return;
      }
      setPopover({ kind: 'selection', pending: p, initialText: initialChar });
      setHoverBlock(null);
    },
    [ensurePending],
  );

  const handleGlobalComment = useCallback((anchorEl: HTMLElement) => {
    setPopover({ kind: 'global', anchorEl });
  }, []);

  // ---- popover -------------------------------------------------------------

  const popoverRef = useRef(popover);
  popoverRef.current = popover;
  const getPopoverRect = useCallback((): DOMRect | null => {
    const state = popoverRef.current;
    if (!state) {
      return null;
    }
    if (state.kind === 'global') {
      return state.anchorEl.isConnected ? state.anchorEl.getBoundingClientRect() : null;
    }
    return getPendingRect() ?? state.pending.rect;
  }, [getPendingRect]);

  const handlePopoverSubmit = useCallback(
    (text: string) => {
      const state = popoverRef.current;
      if (!state) {
        return;
      }
      if (state.kind === 'global') {
        api.addGlobalComment(text);
      } else {
        api.submitComment(text);
      }
      setPopover(null);
    },
    [api],
  );

  const handlePopoverClose = useCallback(() => {
    const state = popoverRef.current;
    setPopover(null);
    if (state?.kind === 'selection') {
      api.clearPendingSelection();
    }
  }, [api]);

  // ---- render --------------------------------------------------------------

  const showSelectionToolbar = pending !== null && popover === null;
  const showHoverToolbar = !showSelectionToolbar && popover === null && hoverBlock !== null;

  const hoverCopyText = hoverBlock?.element.querySelector('pre')?.textContent ?? '';

  return (
    <>
      {showSelectionToolbar && pending && (
        <SelectionToolbar
          getAnchorRect={getPendingRect}
          positionMode={pending.isCodeBlock ? 'top-right' : 'center-above'}
          copyText={pending.selectionText}
          onDelete={handleDelete}
          onRequestComment={handleRequestComment}
          onQuickLabel={handleQuickLabel}
          onClose={closeToolbar}
          closeOnScrollOut
          getCursorHint={getCursorHint}
        />
      )}
      {showHoverToolbar && hoverBlock && (
        <SelectionToolbar
          getAnchorRect={getHoverRect}
          positionMode="top-right"
          copyText={hoverCopyText}
          onDelete={handleDelete}
          onRequestComment={handleRequestComment}
          onQuickLabel={handleQuickLabel}
          onClose={closeToolbar}
          closeOnScrollOut
          getCursorHint={getCursorHint}
          onMouseEnter={cancelHoverHide}
          onMouseLeave={scheduleHoverHide}
        />
      )}
      {popover && (
        <AnnotationPopover
          getAnchorRect={getPopoverRect}
          quote={popover.kind === 'selection' ? popover.pending.selectionText : ''}
          isGlobal={popover.kind === 'global'}
          initialText={popover.kind === 'selection' ? popover.initialText : undefined}
          draftKey={
            popover.kind === 'global' ? `${path}#global` : pendingDraftKey(path, popover.pending)
          }
          onSubmit={handlePopoverSubmit}
          onClose={handlePopoverClose}
        />
      )}
      {sidebarOpen ? (
        <AnnotationSidebar
          annotations={annotations}
          orphans={orphans}
          selectedId={selectedId}
          onCardClick={api.focusAnnotation}
          onDelete={api.deleteAnnotation}
          onClearAll={api.clearAll}
          onGlobalComment={handleGlobalComment}
          onToggle={() => setSidebarOpen(false)}
        />
      ) : (
        <button
          type="button"
          className="md-sidebar-rail"
          title="Show annotations"
          onClick={() => setSidebarOpen(true)}
        >
          <span className="md-sidebar-rail-count">{annotations.length}</span>
          <span className="md-sidebar-rail-label">Annotations</span>
        </button>
      )}
    </>
  );
}
