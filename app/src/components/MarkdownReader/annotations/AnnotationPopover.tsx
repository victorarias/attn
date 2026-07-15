/**
 * AnnotationPopover — the comment composer, ported from plannotator's
 * CommentPopover minus images/AskAI/drag/dialog-expand/jump-back pill.
 *
 * Contract (spec E10–E13):
 * - Cmd/Ctrl+Enter submits (guarded against IME composing); Escape closes
 *   via the centralized useEscapeStack (LIFO with every other overlay);
 *   empty text cannot submit.
 * - Capture-phase outside pointerdown closes ONLY while the textarea is
 *   clean; typed text blocks it (hasUnsavedContentRef).
 * - Draft text survives unmount via a module-level Map keyed by `draftKey`
 *   (`${path}#<anchorKey>` / `${path}#global`); cleared on submit, deleted
 *   when emptied.
 * - Anchored below the selection (rect.bottom + 8), flipped above when
 *   spaceBelow < 280; width min(384, vw−32); horizontally clamped.
 * - Textarea autofocus via ref-callback + setTimeout(0): in WKWebView, 0ms
 *   timers can fire ahead of the commit that mounts the textarea, so an
 *   effect keyed on mount alone may never focus it (donor-documented trap —
 *   we ARE in WKWebView).
 */

import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../../hooks/useEscapeStack';

export interface AnnotationPopoverProps {
  /** Live anchor rect (selection or sidebar button); re-read on scroll/resize. */
  getAnchorRect: () => DOMRect | null;
  /** Truncated quote shown in the header; empty for global comments. */
  quote: string;
  isGlobal: boolean;
  /** Pre-filled text (type-to-comment seeding). */
  initialText?: string;
  /** Persistence key for the module-level draft store. */
  draftKey: string;
  onSubmit: (text: string) => void;
  onClose: () => void;
}

const MAX_POPOVER_WIDTH = 384;
const GAP = 8;
const FLIP_SPACE = 280;

// Module-level draft store: survives popover unmount so reopening the same
// key restores in-progress text (donor draftStore pattern, text-only).
const draftStore = new Map<string, string>();

/** Test seam: inspect/clear the draft store. */
export function peekAnnotationDraft(draftKey: string): string | undefined {
  return draftStore.get(draftKey);
}

function computePosition(anchorRect: DOMRect): {
  top: number;
  left: number;
  flipAbove: boolean;
  width: number;
} {
  const spaceBelow = window.innerHeight - anchorRect.bottom;
  const flipAbove = spaceBelow < FLIP_SPACE;
  const width = Math.min(MAX_POPOVER_WIDTH, window.innerWidth - 32);
  const top = flipAbove ? anchorRect.top - GAP : anchorRect.bottom + GAP;
  let left = anchorRect.left + anchorRect.width / 2 - width / 2;
  left = Math.max(16, Math.min(left, window.innerWidth - width - 16));
  return { top, left, flipAbove, width };
}

export function AnnotationPopover({
  getAnchorRect,
  quote,
  isGlobal,
  initialText = '',
  draftKey,
  onSubmit,
  onClose,
}: AnnotationPopoverProps) {
  const [text, setText] = useState(() => draftStore.get(draftKey) ?? initialText);
  const [position, setPosition] = useState<ReturnType<typeof computePosition> | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const hasUnsavedContent = text.trim().length > 0;
  const hasUnsavedContentRef = useRef(hasUnsavedContent);
  hasUnsavedContentRef.current = hasUnsavedContent;

  // Key change (new selection) → reload that key's draft or the seed char.
  useEffect(() => {
    setText(draftStore.get(draftKey) ?? initialText);
  }, [draftKey, initialText]);

  // Mirror text into the draft store so it outlives unmount; empty deletes.
  useEffect(() => {
    if (text.trim().length > 0) {
      draftStore.set(draftKey, text);
    } else {
      draftStore.delete(draftKey);
    }
  }, [draftKey, text]);

  // Track the anchor on capture-phase scroll + resize.
  useEffect(() => {
    const update = () => {
      const rect = getAnchorRect();
      if (rect) {
        setPosition(computePosition(rect));
      }
    };
    update();
    window.addEventListener('scroll', update, true);
    window.addEventListener('resize', update);
    return () => {
      window.removeEventListener('scroll', update, true);
      window.removeEventListener('resize', update);
    };
  }, [getAnchorRect]);

  // Autofocus via ref-callback + setTimeout(0) (WKWebView commit-order trap).
  const focusOnMountRef = useCallback((el: HTMLTextAreaElement | null) => {
    textareaRef.current = el;
    if (!el) {
      return;
    }
    setTimeout(() => {
      if (!el.isConnected) {
        return;
      }
      el.focus();
      el.selectionStart = el.selectionEnd = el.value.length;
    }, 0);
  }, []);

  // Click-outside: closes ONLY when clean (capture phase).
  useEffect(() => {
    const handlePointerDown = (e: PointerEvent) => {
      const target = e.target as Node | null;
      if (!target || popoverRef.current?.contains(target)) {
        return;
      }
      if (hasUnsavedContentRef.current) {
        return;
      }
      onClose();
    };
    document.addEventListener('pointerdown', handlePointerDown, true);
    return () => document.removeEventListener('pointerdown', handlePointerDown, true);
  }, [onClose]);

  const handleSubmit = useCallback(() => {
    if (!hasUnsavedContentRef.current) {
      return;
    }
    draftStore.delete(draftKey);
    onSubmit(text);
  }, [draftKey, onSubmit, text]);

  // Escape dismiss via the centralized stack (capture phase — fires before
  // the textarea sees the key, and LIFO with any other open overlay).
  useEscapeStack(onClose, true);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const headerLabel = isGlobal
    ? 'Global Comment'
    : quote
      ? `"${quote.length > 50 ? `${quote.slice(0, 50)}...` : quote}"`
      : 'Comment';

  if (!position) {
    return null;
  }

  return createPortal(
    <div
      ref={popoverRef}
      className={`md-annotation-popover ${position.flipAbove ? 'md-annotation-popover--above' : ''}`.trim()}
      style={{ top: position.top, left: position.left, width: position.width }}
      onPointerDown={(e) => e.stopPropagation()}
    >
      <div className="md-popover-header">
        <span className="md-popover-quote">{headerLabel}</span>
        <button type="button" className="md-popover-close" title="Close" onClick={onClose}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </div>
      <textarea
        ref={focusOnMountRef}
        className="md-popover-textarea"
        value={text}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder={isGlobal ? 'Add a global comment...' : 'Add a comment...'}
        rows={3}
      />
      <div className="md-popover-footer">
        <span className="md-popover-hint">⌘↩ to save</span>
        <button
          type="button"
          className="md-popover-submit"
          disabled={!hasUnsavedContent}
          onClick={handleSubmit}
        >
          {isGlobal ? 'Add' : 'Save'}
        </button>
      </div>
    </div>,
    document.body,
  );
}
