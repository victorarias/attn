import { describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { SessionLabel } from './SessionLabel';

/**
 * The reveal exists because generated session names outrun the sidebar's width.
 * What these tests pin is the part that is easy to regress by "simplifying":
 * the trigger is the whole row, the panel only appears for names that were
 * actually cut off, and it starts outside the rail so it cannot bury the row's
 * hover-revealed actions.
 */

const LONG = 'judge yielded stops so background waits stay green';

const RAIL_RIGHT = 240;
const ACTIONS_RIGHT = 232;

/** happy-dom reports 0 for every layout box, so the geometry has to be staged. */
function stageRect(el: HTMLElement, rect: Partial<DOMRect>) {
  el.getBoundingClientRect = () =>
    ({ top: 0, left: 0, width: 0, height: 0, right: 0, bottom: 0, x: 0, y: 0, ...rect, toJSON: () => ({}) }) as DOMRect;
}

function renderRow(label: string, widths: { scrollWidth: number; clientWidth: number }) {
  const utils = render(
    <div className="sidebar" data-testid="rail">
      <div className="session-item" data-testid="row">
        <SessionLabel label={label} />
        <div className="session-actions">
          <button data-testid="actions">•••</button>
        </div>
      </div>
    </div>,
  );
  const span = utils.container.querySelector('.session-label') as HTMLElement;
  Object.defineProperty(span, 'scrollWidth', { value: widths.scrollWidth, configurable: true });
  Object.defineProperty(span, 'clientWidth', { value: widths.clientWidth, configurable: true });
  stageRect(span, { top: 40, left: 16, width: widths.clientWidth, height: 18, right: 16 + widths.clientWidth, bottom: 58 });
  stageRect(screen.getByTestId('rail'), { top: 0, left: 0, width: RAIL_RIGHT, height: 800, right: RAIL_RIGHT, bottom: 800 });
  stageRect(screen.getByTestId('actions'), { top: 42, left: 216, width: 16, height: 20, right: ACTIONS_RIGHT, bottom: 62 });
  return { ...utils, row: screen.getByTestId('row'), span };
}

const panel = () => document.querySelector('[data-testid="session-label-reveal"]');

describe('SessionLabel hover reveal', () => {
  it('reveals the full name when the row — not just the label — is entered', () => {
    const { row } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });

    expect(panel()).toBeNull();
    // Entering through the row's vertical padding never touches the label span.
    // Binding the trigger to the span would drop the reveal for that whole band,
    // so the row is what must carry it.
    fireEvent.pointerEnter(row);

    expect(panel()?.textContent).toBe(LONG);
  });

  it('stays away for a name the row already shows in full', () => {
    const { row } = renderRow('attn', { scrollWidth: 40, clientWidth: 180 });

    fireEvent.pointerEnter(row);

    expect(panel()).toBeNull();
  });

  it('withdraws on pointer leave', () => {
    const { row } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });
    fireEvent.pointerEnter(row);
    expect(panel()).not.toBeNull();

    fireEvent.pointerLeave(row);

    expect(panel()).toBeNull();
  });

  it('withdraws on pointer down, since a click can reorder the list under a still pointer', () => {
    const { row } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });
    fireEvent.pointerEnter(row);
    expect(panel()).not.toBeNull();

    fireEvent.pointerDown(row);

    expect(panel()).toBeNull();
  });

  it('withdraws when the sidebar scrolls, because the panel is positioned once', () => {
    const { row } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });
    fireEvent.pointerEnter(row);
    expect(panel()).not.toBeNull();

    // Captured at the window, so a scroll on the inner scroller counts too.
    fireEvent.scroll(row);

    expect(panel()).toBeNull();
  });

  it('clears the row entirely, leaving the hover-revealed actions visible', () => {
    // The `•••` button appears on the same hover that opens this panel, so a
    // panel starting anywhere inside the rail would bury the actions for exactly
    // as long as they are reachable.
    const { row } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });

    fireEvent.pointerEnter(row);

    expect(parseFloat((panel() as HTMLElement).style.left)).toBeGreaterThanOrEqual(ACTIONS_RIGHT);
  });

  it("starts at the rail edge on the row's own baseline", () => {
    const { row } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });

    fireEvent.pointerEnter(row);

    const style = (panel() as HTMLElement).style;
    // Flush against the sidebar, so the panel reads as this row continuing out
    // of it. Anchored to the rail rather than the row, whose right edge shifts
    // with nesting depth and would make the panel jitter down the list.
    expect(style.left).toBe(`${RAIL_RIGHT}px`);
    // Lifted by the panel's own vertical padding so the revealed glyphs sit on
    // the same line as the truncated ones.
    expect(style.top).toBe('34px');
  });

  it('leaves the clipped label in place as the accessible copy', () => {
    const { row, span } = renderRow(LONG, { scrollWidth: 420, clientWidth: 180 });
    fireEvent.pointerEnter(row);

    expect(span.textContent).toBe(LONG);
    expect(panel()).toHaveAttribute('aria-hidden', 'true');
  });
});
