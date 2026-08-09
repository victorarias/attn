/**
 * Floating quick-label list at the last mouseup position, clamped to the
 * viewport; falls back to the anchor element with no cursor hint.
 *
 * Interaction contract: bare digits 1..9,0 and Alt+digit apply label N (0 =
 * 10th); Escape dismisses; outside pointerdown dismisses, but that listener
 * installs one tick late so the click that OPENED the picker never dismisses.
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

// `height` is measured, 0 before first layout: the list is as tall as the
// label set makes it, and a guessed constant hangs off-screen once one is added.
function computePosition(
  anchorEl: HTMLElement,
  cursorHint: { x: number; y: number } | null | undefined,
  height: number,
): { top: number; left: number } {
  const rect = anchorEl.getBoundingClientRect();

  // Vertical: below the anchor, above when it does not fit, else viewport-clamped.
  const below = rect.bottom + GAP;
  const above = rect.top - GAP - height;
  const lowestTop = window.innerHeight - VIEWPORT_PADDING - height;
  let top = below;
  if (height > 0 && below > lowestTop) {
    top = above >= VIEWPORT_PADDING ? above : Math.max(VIEWPORT_PADDING, lowestTop);
  }

  // Horizontal: cursor x puts the first row under the pointer; else anchor edge.
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

  // First pass places with height 0; the layout effect corrects before paint.
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

  // Mounts after the toolbar, so the stack closes the picker first.
  useEscapeStack(onDismiss, true);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
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

  // Deferred one tick so the opening click misses the capture-phase listener.
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
            {/* Past ten, a badge would promise a shortcut for another label. */}
            {index < 10 && <span className="md-quick-label-num">{(index + 1) % 10}</span>}
          </button>
        );
      })}
    </div>,
    document.body,
  );
}
