import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import './SessionLabel.css';

/**
 * A session name that unfurls out of the sidebar while its row is hovered.
 *
 * Session names are generated from the conversation and run up to 48 characters,
 * which no sidebar width fits, so the resting row ellipsizes. Hovering the row
 * slides the full name out past the rail's edge on its own line, sharing the
 * row's baseline so it reads as that row continuing into the space beside it.
 * There is no dwell delay and nothing in the list moves.
 *
 * The panel begins at the rail's right edge and never re-enters it. That is the
 * load-bearing constraint, not a stylistic one: a row's `•••` actions live at
 * its right end and are themselves hover-revealed, so anything drawn over the
 * row's own width would black out the actions at precisely the moment they
 * appear. Anchoring to the rail — rather than to each row's right edge, which
 * varies with nesting — also keeps the panel's left edge on one vertical line as
 * the pointer sweeps down the list.
 *
 * Two more details make it hold together:
 *
 * - The panel is a `document.body` portal. The sidebar's scroll container clips
 *   overflow, so an in-flow element could not cross the rail's edge at all.
 * - It copies the label's own computed typography and colour. Portaling escapes
 *   inheritance, and anything but an exact copy would set the revealed name in a
 *   different face from the row it belongs to.
 *
 * Hover binds to the enclosing `.session-item` row, not to this span. The span
 * only covers the row's text line, so binding it here would drop the reveal
 * whenever the pointer crossed in through the row's vertical padding.
 * `.session-item` is the shared row class behind every call site (workspace tree
 * rows, muted rows, and agent-queue band rows).
 */

/** Vertical padding, mirrored as a negative offset so the text keeps the row's baseline. */
const PAD_Y = 6;
/** Past this the panel stops being a name and starts being a paragraph. */
const MAX_PANEL_WIDTH = 460;
/** Breathing room kept between the panel and the viewport edges. */
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
    // Only worth revealing what the row actually cut off. The extra pixel
    // absorbs sub-pixel rounding on fractional layout widths.
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
    // A click can reorder or replace the list under a pointer that never moves,
    // which would leave the panel painted over a row it no longer describes.
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
    // Fixed positioning is measured once, so any viewport change invalidates it.
    // Capture the scroll so the sidebar's own scroller counts, not just window.
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
    // A wrapped name on one of the last rows would run off the bottom; lift it
    // just enough to fit rather than flipping it above the row, which would
    // break the "it is this row, continued" premise.
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
              // The clipped span still carries the full name in the accessibility
              // tree, so this is decoration. It is also pointer-transparent: the
              // content beside the rail must stay clickable through it.
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
              {/* Its own element so the name can arrive a beat after the surface
                  it lands on; the panel cannot stagger against itself. */}
              <span className="session-label-reveal-text">{label}</span>
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
