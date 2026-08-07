/**
 * QuickLabelPicker — floating quick-label list, ported from plannotator's
 * FloatingQuickLabelPicker. Appears at the last mouseup cursor position
 * (clamped to the viewport) so the first row sits under the pointer; falls
 * back to the anchor element when no cursor hint exists.
 *
 * It measures itself before settling vertically: the list is as tall as the
 * label set makes it, so where it fits is not something the code can be told
 * once.
 *
 * Interaction contract (spec E14–E16):
 * - bare digits 1..9,0 AND Alt+digit apply label N (0 = 10th);
 * - Escape dismisses;
 * - outside pointerdown dismisses, but the listener installs one tick late
 *   (setTimeout 0) so the click that OPENED the picker never dismisses it.
 */

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
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

// `height` is the picker's measured height, 0 before it has ever been laid
// out. It is measured rather than assumed because the picker is a list and its
// height is whatever the label set makes it — a constant guessed against one
// label count is how a picker ends up hanging off the bottom of the window
// after somebody adds a label.
function computePosition(
  anchorEl: HTMLElement,
  cursorHint: { x: number; y: number } | null | undefined,
  height: number,
): { top: number; left: number } {
  const rect = anchorEl.getBoundingClientRect();

  // Vertical: below the anchor, above it when it does not fit below, and
  // clamped into the viewport when it fits neither way — a picker drawn
  // half off-screen is a picker whose last labels cannot be clicked.
  const below = rect.bottom + GAP;
  const above = rect.top - GAP - height;
  const lowestTop = window.innerHeight - VIEWPORT_PADDING - height;
  let top = below;
  if (height > 0 && below > lowestTop) {
    top = above >= VIEWPORT_PADDING ? above : Math.max(VIEWPORT_PADDING, lowestTop);
  }

  // Horizontal: prefer cursor x (first row's text directly under the
  // pointer), fallback to the anchor's right edge.
  let left = cursorHint ? cursorHint.x - 28 : rect.right - PICKER_WIDTH / 2;
  left = Math.max(
    VIEWPORT_PADDING,
    Math.min(left, window.innerWidth - PICKER_WIDTH - VIEWPORT_PADDING),
  );

  return { top, left };
}

export function QuickLabelPicker({ anchorEl, cursorHint, onSelect, onDismiss }: QuickLabelPickerProps) {
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const [height, setHeight] = useState(0);
  const ref = useRef<HTMLDivElement>(null);

  // Position tracking. The first pass places the picker with height 0 (below
  // the anchor); the layout effect below measures it and the placement is
  // corrected before the browser paints, so it is never seen off-screen.
  useEffect(() => {
    const update = () => setPosition(computePosition(anchorEl, cursorHint, height));
    update();
    window.addEventListener('scroll', update, true);
    window.addEventListener('resize', update);
    return () => {
      window.removeEventListener('scroll', update, true);
      window.removeEventListener('resize', update);
    };
  }, [anchorEl, cursorHint, height]);

  useLayoutEffect(() => {
    const measured = ref.current?.offsetHeight ?? 0;
    if (measured > 0) {
      setHeight((current) => (current === measured ? current : measured));
    }
  });

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
      className="md-quick-label-picker"
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
            {/* Only the first ten have a digit; past that the badge would
                promise a shortcut that applies a different label. */}
            {index < 10 && <span className="md-quick-label-num">{(index + 1) % 10}</span>}
          </button>
        );
      })}
    </div>,
    document.body,
  );
}
