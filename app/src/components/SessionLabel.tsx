import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import './SessionLabel.css';

/**
 * A session name that unfurls out of the sidebar while its row is hovered.
 *
 * Constraints, all load-bearing: the panel starts at the rail's right edge and
 * never re-enters it, or it blacks out the row's hover-revealed `•••` actions;
 * it is a `document.body` portal, since the sidebar's scroller clips overflow;
 * it copies the label's computed typography, which portaling loses; and hover
 * binds to the enclosing `.session-item` row, not this span, which covers only
 * the text line and would drop the reveal on entry through the row's padding.
 */

const PAD_Y = 6;
const MAX_PANEL_WIDTH = 460;
const VIEWPORT_MARGIN = 12;

type RevealStyle = {
  top: number;
  left: number;
  maxWidth: number;
  fontFamily: string;
  fontSize: string;
  fontWeight: string;
  fontStyle: string;
  lineHeight: string;
  letterSpacing: string;
  color: string;
};

export function SessionLabel({ label }: { label: string }) {
  const spanRef = useRef<HTMLSpanElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const [reveal, setReveal] = useState<RevealStyle | null>(null);

  const hide = useCallback(() => setReveal(null), []);

  const show = useCallback(() => {
    const span = spanRef.current;
    if (!span) {
      return;
    }
    // Only reveal what the row cut off; the extra pixel absorbs sub-pixel rounding.
    if (span.scrollWidth <= span.clientWidth + 1) {
      return;
    }
    const rect = span.getBoundingClientRect();
    const style = window.getComputedStyle(span);
    // The rail's edge, or the row's own if this label is used outside one.
    const rail = span.closest('.sidebar') ?? span.closest('.session-item') ?? span;
    const left = rail.getBoundingClientRect().right;
    setReveal({
      top: rect.top - PAD_Y,
      left,
      maxWidth: Math.min(window.innerWidth - left - VIEWPORT_MARGIN, MAX_PANEL_WIDTH),
      fontFamily: style.fontFamily,
      fontSize: style.fontSize,
      fontWeight: style.fontWeight,
      fontStyle: style.fontStyle,
      lineHeight: style.lineHeight,
      letterSpacing: style.letterSpacing,
      color: style.color,
    });
  }, []);

  useEffect(() => {
    const span = spanRef.current;
    if (!span) {
      return;
    }
    const row = span.closest('.session-item') ?? span;
    row.addEventListener('pointerenter', show);
    row.addEventListener('pointerleave', hide);
    // A click can reorder the list under a motionless pointer, leaving the panel
    // over a row it no longer describes.
    row.addEventListener('pointerdown', hide);
    return () => {
      row.removeEventListener('pointerenter', show);
      row.removeEventListener('pointerleave', hide);
      row.removeEventListener('pointerdown', hide);
    };
  }, [show, hide]);

  useEffect(() => {
    if (!reveal) {
      return;
    }
    // Fixed positioning is measured once; capture the scroll so the sidebar's own
    // scroller invalidates it too.
    window.addEventListener('scroll', hide, true);
    window.addEventListener('resize', hide);
    return () => {
      window.removeEventListener('scroll', hide, true);
      window.removeEventListener('resize', hide);
    };
  }, [reveal, hide]);

  useLayoutEffect(() => {
    const panel = panelRef.current;
    if (!panel || !reveal) {
      return;
    }
    // Lift just enough to fit rather than flipping above the row, which would
    // break the "this row, continued" premise.
    const rect = panel.getBoundingClientRect();
    const overflow = rect.bottom - (window.innerHeight - VIEWPORT_MARGIN);
    if (overflow > 0) {
      panel.style.top = `${Math.max(VIEWPORT_MARGIN, rect.top - overflow)}px`;
    }
  }, [reveal]);

  return (
    <>
      <span className="session-label" ref={spanRef}>{label}</span>
      {reveal
        ? createPortal(
            <div
              ref={panelRef}
              className="session-label-reveal"
              data-testid="session-label-reveal"
              // Decoration: the clipped span already carries the full name in the
              // accessibility tree.
              aria-hidden="true"
              style={{
                top: reveal.top,
                left: reveal.left,
                maxWidth: reveal.maxWidth,
                fontFamily: reveal.fontFamily,
                fontSize: reveal.fontSize,
                fontWeight: reveal.fontWeight,
                fontStyle: reveal.fontStyle,
                lineHeight: reveal.lineHeight,
                letterSpacing: reveal.letterSpacing,
                color: reveal.color,
              }}
            >
              {/* Its own element so the name can arrive a beat after its surface. */}
              <span className="session-label-reveal-text">{label}</span>
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
