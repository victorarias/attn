/**
 * QuickLabelPicker — floating quick-label list, ported from plannotator's
 * FloatingQuickLabelPicker. Appears at the last mouseup cursor position
 * (clamped to the viewport) so the first row sits under the pointer; falls
 * back to the anchor element when no cursor hint exists.
 *
 * Interaction contract (spec E14–E16):
 * - bare digits 1..9,0 AND Alt+digit apply label N (0 = 10th);
 * - Escape dismisses;
 * - outside pointerdown dismisses, but the listener installs one tick late
 *   (setTimeout 0) so the click that OPENED the picker never dismisses it.
 */

import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../../hooks/useEscapeStack';
import { LABEL_COLOR_MAP, QUICK_LABELS, type QuickLabel } from './quickLabels';

export interface QuickLabelPickerProps {
  anchorEl: HTMLElement;
  /** Mouse coordinates at the last mouseup — picker appears here. */
  cursorHint?: { x: number; y: number } | null;
  onSelect: (label: QuickLabel) => void;
  onDismiss: () => void;
}

const PICKER_WIDTH = 192;
const GAP = 6;
const VIEWPORT_PADDING = 12;

function computePosition(
  anchorEl: HTMLElement,
  cursorHint?: { x: number; y: number } | null,
): { top: number; left: number; flipAbove: boolean } {
  const rect = anchorEl.getBoundingClientRect();

  // Vertical: anchor rect decides above/below and placement.
  const spaceBelow = window.innerHeight - rect.bottom;
  const flipAbove = spaceBelow < 220;
  const top = flipAbove ? rect.top - GAP : rect.bottom + GAP;

  // Horizontal: prefer cursor x (first row's text directly under the
  // pointer), fallback to the anchor's right edge.
  let left = cursorHint ? cursorHint.x - 28 : rect.right - PICKER_WIDTH / 2;
  left = Math.max(
    VIEWPORT_PADDING,
    Math.min(left, window.innerWidth - PICKER_WIDTH - VIEWPORT_PADDING),
  );

  return { top, left, flipAbove };
}

export function QuickLabelPicker({ anchorEl, cursorHint, onSelect, onDismiss }: QuickLabelPickerProps) {
  const [position, setPosition] = useState<{ top: number; left: number; flipAbove: boolean } | null>(
    null,
  );
  const ref = useRef<HTMLDivElement>(null);

  // Position tracking.
  useEffect(() => {
    const update = () => setPosition(computePosition(anchorEl, cursorHint));
    update();
    window.addEventListener('scroll', update, true);
    window.addEventListener('resize', update);
    return () => {
      window.removeEventListener('scroll', update, true);
      window.removeEventListener('resize', update);
    };
  }, [anchorEl, cursorHint]);

  // Escape dismiss via the centralized stack: the picker mounts after (so
  // registers above) the toolbar — Escape closes picker first, then toolbar.
  useEscapeStack(onDismiss, true);

  // Keyboard: 1-9/0 or Alt+1-9/0 applies label.
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Picker is open, so digits mean labels — bare or with Alt.
      const isDigit = (e.code >= 'Digit1' && e.code <= 'Digit9') || e.code === 'Digit0';
      if (isDigit && !e.ctrlKey && !e.metaKey) {
        e.preventDefault();
        const digit = parseInt(e.code.slice(5), 10);
        const index = digit === 0 ? 9 : digit - 1;
        if (index < QUICK_LABELS.length) {
          onSelect(QUICK_LABELS[index]);
        }
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onDismiss, onSelect]);

  // Click outside to dismiss — deferred one tick so the opening click never
  // catches the capture-phase listener (E15).
  useEffect(() => {
    const handlePointerDown = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onDismiss();
      }
    };
    const timer = setTimeout(() => {
      document.addEventListener('pointerdown', handlePointerDown, true);
    }, 0);
    return () => {
      clearTimeout(timer);
      document.removeEventListener('pointerdown', handlePointerDown, true);
    };
  }, [onDismiss]);

  if (!position) {
    return null;
  }

  return createPortal(
    <div
      ref={ref}
      className={`md-quick-label-picker ${position.flipAbove ? 'md-quick-label-picker--above' : ''}`.trim()}
      style={{ top: position.top, left: position.left, width: PICKER_WIDTH }}
      onMouseDown={(e) => e.stopPropagation()}
    >
      {QUICK_LABELS.map((label, index) => {
        const color = LABEL_COLOR_MAP[label.color];
        return (
          <button
            key={label.id}
            type="button"
            className="md-quick-label-row"
            onClick={() => onSelect(label)}
            title={label.tip}
          >
            <span
              className="md-ql-chip"
              style={
                color
                  ? ({
                      background: color.bg,
                      '--md-ql-text': color.text,
                      '--md-ql-text-dark': color.darkText,
                    } as React.CSSProperties)
                  : undefined
              }
            >
              {label.emoji}
            </span>
            <span className="md-quick-label-text">{label.text}</span>
            <span className="md-quick-label-num">{(index + 1) % 10}</span>
          </button>
        );
      })}
    </div>,
    document.body,
  );
}
