/**
 * SelectionToolbar — the floating action bar over a pending selection (or a
 * hovered code block). Ported from plannotator's AnnotationToolbar minus
 * math/images/keyboard-copy.
 *
 * Buttons: Copy, Delete (redline — creates instantly, no popover), Comment,
 * ⚡ quick-label picker toggle, fixed 👍 (thumbs-up label), Cancel.
 *
 * Positioning: `center-above` for prose (centered over the selection rect),
 * `top-right` for code blocks (right-aligned above the block); recomputed on
 * capture-phase scroll + resize, closing when the anchor rect scrolls fully
 * out of the viewport (closeOnScrollOut).
 *
 * Escape goes through useEscapeStack (repo-wide LIFO dismiss contract), NOT
 * the local keydown handler: the picker registers above the toolbar, so
 * Escape dismisses picker → toolbar in open order, and any app overlay
 * stacked later wins first.
 *
 * Type-to-comment guard set (spec E9, donor order minus Escape — see above):
 *   1. IME composing → ignore
 *   2. editable event target or activeElement → ignore
 *   3. picker open → picker owns the keyboard
 *   4. Alt+Digit1..0 (no ctrl/meta) → quick-label N
 *   5. remaining ctrl/meta/alt combos → ignore
 *   6. Tab/Enter → ignore
 *   7. non-single-char keys → ignore
 *   8. else → open the comment popover seeded with the typed char
 */

import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../../hooks/useEscapeStack';
import { QuickLabelPicker } from './QuickLabelPicker';
import { QUICK_LABELS, THUMBS_UP_LABEL, type QuickLabel } from './quickLabels';

export type ToolbarPositionMode = 'center-above' | 'top-right';

const isEditableElement = (node: EventTarget | Element | null): boolean => {
  if (!(node instanceof Element)) {
    return false;
  }
  if (node.matches('input, textarea, select, [role="textbox"]')) {
    return true;
  }
  if (node.closest('[contenteditable]:not([contenteditable="false"])')) {
    return true;
  }
  return (node as HTMLElement).isContentEditable;
};

export interface SelectionToolbarProps {
  /** Live anchor rect — re-read on every scroll/resize tick. */
  getAnchorRect: () => DOMRect | null;
  positionMode: ToolbarPositionMode;
  /** Text the Copy button places on the clipboard. */
  copyText: string;
  onDelete: () => void;
  onRequestComment: (initialChar?: string) => void;
  onQuickLabel: (label: QuickLabel) => void;
  onClose: () => void;
  /** Close when the anchor rect fully leaves the viewport. */
  closeOnScrollOut?: boolean;
  /** Read at zap-click time: last mouseup position for picker placement. */
  getCursorHint?: () => { x: number; y: number } | null;
  /** Hover grace callbacks (code-block hover toolbar keeps itself alive). */
  onMouseEnter?: () => void;
  onMouseLeave?: () => void;
}

export function SelectionToolbar({
  getAnchorRect,
  positionMode,
  copyText,
  onDelete,
  onRequestComment,
  onQuickLabel,
  onClose,
  closeOnScrollOut = true,
  getCursorHint,
  onMouseEnter,
  onMouseLeave,
}: SelectionToolbarProps) {
  const [position, setPosition] = useState<{ top: number; left?: number; right?: number } | null>(
    null,
  );
  const [copied, setCopied] = useState(false);
  const [showQuickLabels, setShowQuickLabels] = useState(false);
  const [pickerHint, setPickerHint] = useState<{ x: number; y: number } | null>(null);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const zapButtonRef = useRef<HTMLButtonElement>(null);
  const showQuickLabelsRef = useRef(showQuickLabels);
  showQuickLabelsRef.current = showQuickLabels;

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(copyText);
    } catch {
      const textarea = document.createElement('textarea');
      textarea.value = copyText;
      textarea.style.cssText = 'position:fixed;opacity:0';
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand('copy');
      textarea.remove();
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  // Position on mount + capture-phase scroll + resize; scroll-out close.
  useEffect(() => {
    const updatePosition = () => {
      const rect = getAnchorRect();
      if (!rect) {
        return;
      }
      if (closeOnScrollOut && (rect.bottom < 0 || rect.top > window.innerHeight)) {
        onClose();
        return;
      }
      if (positionMode === 'center-above') {
        setPosition({ top: rect.top - 48, left: rect.left + rect.width / 2 });
      } else {
        setPosition({ top: rect.top - 40, right: window.innerWidth - rect.right });
      }
    };

    updatePosition();
    window.addEventListener('scroll', updatePosition, true);
    window.addEventListener('resize', updatePosition);
    return () => {
      window.removeEventListener('scroll', updatePosition, true);
      window.removeEventListener('resize', updatePosition);
    };
  }, [getAnchorRect, positionMode, closeOnScrollOut, onClose]);

  // Type-to-comment + Alt+N quick labels + Escape (guard set — see module doc).
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.isComposing) {
        return;
      }
      if (isEditableElement(e.target) || isEditableElement(document.activeElement)) {
        return;
      }
      // Picker open → QuickLabelPicker owns all keyboard input.
      if (showQuickLabelsRef.current) {
        return;
      }
      const isDigit = (e.code >= 'Digit1' && e.code <= 'Digit9') || e.code === 'Digit0';
      if (isDigit && !e.ctrlKey && !e.metaKey && e.altKey) {
        e.preventDefault();
        const digit = parseInt(e.code.slice(5), 10);
        const index = digit === 0 ? 9 : digit - 1;
        if (index < QUICK_LABELS.length) {
          onQuickLabel(QUICK_LABELS[index]);
        }
        return;
      }
      if (e.ctrlKey || e.metaKey || e.altKey) {
        return;
      }
      if (e.key === 'Tab' || e.key === 'Enter') {
        return;
      }
      if (e.key.length !== 1) {
        return;
      }
      onRequestComment(e.key);
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onRequestComment, onQuickLabel]);

  // Escape dismiss via the centralized stack: registered on mount, so the
  // picker (which mounts later) sits above and dismisses first (LIFO).
  useEscapeStack(onClose, true);

  // Dismiss on outside pointerdown (disabled while the picker is open — the
  // picker owns its own outside-dismiss with one-tick deferral).
  useEffect(() => {
    const handlePointerDown = (e: PointerEvent) => {
      if (showQuickLabelsRef.current) {
        return;
      }
      const target = e.target as Node | null;
      if (!target || toolbarRef.current?.contains(target)) {
        return;
      }
      onClose();
    };
    document.addEventListener('pointerdown', handlePointerDown, true);
    return () => document.removeEventListener('pointerdown', handlePointerDown, true);
  }, [onClose]);

  if (!position) {
    return null;
  }

  const centered = position.left !== undefined;
  const style: React.CSSProperties = {
    top: position.top,
    ...(centered ? { left: position.left } : { right: position.right }),
  };

  return createPortal(
    <div
      ref={toolbarRef}
      className={`md-selection-toolbar ${centered ? 'md-selection-toolbar--centered' : ''}`.trim()}
      style={style}
      onMouseDown={(e) => e.stopPropagation()}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
    >
      <button
        type="button"
        className={`md-toolbar-btn ${copied ? 'md-toolbar-btn--copied' : ''}`.trim()}
        title={copied ? 'Copied!' : 'Copy'}
        onClick={handleCopy}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </button>
      <span className="md-toolbar-divider" />
      <button
        type="button"
        className="md-toolbar-btn md-toolbar-btn--delete"
        title="Delete"
        onClick={onDelete}
      >
        <TrashIcon />
      </button>
      <button
        type="button"
        className="md-toolbar-btn md-toolbar-btn--comment"
        title="Comment"
        onClick={() => onRequestComment()}
      >
        <CommentIcon />
      </button>
      <button
        ref={zapButtonRef}
        type="button"
        className={`md-toolbar-btn md-toolbar-btn--zap ${showQuickLabels ? 'md-toolbar-btn--active' : ''}`.trim()}
        title="Quick label"
        onClick={() => {
          setPickerHint(getCursorHint?.() ?? null);
          setShowQuickLabels((prev) => !prev);
        }}
      >
        <ZapIcon />
      </button>
      <button
        type="button"
        className="md-toolbar-btn"
        title="Looks good"
        onClick={() => onQuickLabel(THUMBS_UP_LABEL)}
      >
        <span className="md-toolbar-emoji">👍</span>
      </button>
      {showQuickLabels && zapButtonRef.current && (
        <QuickLabelPicker
          anchorEl={zapButtonRef.current}
          cursorHint={pickerHint}
          onSelect={(label) => {
            setShowQuickLabels(false);
            onQuickLabel(label);
          }}
          onDismiss={() => setShowQuickLabels(false)}
        />
      )}
      <span className="md-toolbar-divider" />
      <button type="button" className="md-toolbar-btn" title="Cancel" onClick={onClose}>
        <CloseIcon />
      </button>
    </div>,
    document.body,
  );
}

// ---- icons (donor SVGs, stroke=currentColor) --------------------------------

const CopyIcon = () => (
  <svg className="md-toolbar-icon" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
  </svg>
);

const CheckIcon = () => (
  <svg className="md-toolbar-icon" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
  </svg>
);

const TrashIcon = () => (
  <svg className="md-toolbar-icon" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
  </svg>
);

const CommentIcon = () => (
  <svg className="md-toolbar-icon" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M7 8h10M7 12h4m1 8l-4-4H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-3l-4 4z" />
  </svg>
);

const ZapIcon = () => (
  <svg className="md-toolbar-icon" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M13 10V3L4 14h7v7l9-11h-7z" />
  </svg>
);

const CloseIcon = () => (
  <svg className="md-toolbar-icon" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
  </svg>
);
