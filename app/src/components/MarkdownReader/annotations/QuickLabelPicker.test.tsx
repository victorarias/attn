/**
 * QuickLabelPicker — cursor-hint positioning with viewport clamping (E14),
 * one-tick-deferred outside dismiss (E15), digit/Alt+digit selection and
 * Escape (E16), and the full label row set.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { QuickLabelPicker, type QuickLabelPickerProps } from './QuickLabelPicker';
import { QUICK_LABELS } from './quickLabels';

function makeAnchor(rect: Partial<DOMRect> = {}): HTMLElement {
  const el = document.createElement('button');
  document.body.appendChild(el);
  const base = { top: 100, bottom: 120, left: 200, right: 230, width: 30, height: 20, x: 200, y: 100 };
  el.getBoundingClientRect = () => ({ ...base, ...rect, toJSON: () => ({}) }) as DOMRect;
  return el;
}

function renderPicker(overrides: Partial<QuickLabelPickerProps> = {}) {
  const anchorEl = overrides.anchorEl ?? makeAnchor();
  const props: QuickLabelPickerProps = {
    cursorHint: { x: 300, y: 110 },
    onSelect: vi.fn(),
    onDismiss: vi.fn(),
    ...overrides,
    anchorEl,
  };
  const view = render(<QuickLabelPicker {...props} />);
  return { view, props };
}

// jsdom lays nothing out, so the picker measures 0 and stays below the anchor.
// A placement test has to say how tall it is.
function withPickerHeight(height: number) {
  Object.defineProperty(HTMLDivElement.prototype, 'offsetHeight', {
    configurable: true,
    get(this: HTMLDivElement) {
      return this.classList.contains('md-quick-label-picker') ? height : 0;
    },
  });
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(HTMLDivElement.prototype, 'offsetHeight');
});

describe('QuickLabelPicker', () => {
  it('renders the whole label set, numbering the ten that have a shortcut', () => {
    // Past the tenth there is no digit to press, and a badge there would name
    // a key that applies a different label.
    renderPicker();
    const rows = document.querySelectorAll('.md-quick-label-row');
    expect(rows).toHaveLength(QUICK_LABELS.length);
    expect(rows[0].textContent).toContain(QUICK_LABELS[0].text);
    expect(rows[0].querySelector('.md-quick-label-num')!.textContent).toBe('1');
    expect(rows[9].querySelector('.md-quick-label-num')!.textContent).toBe('0');
    expect(rows[10]?.querySelector('.md-quick-label-num')).toBeNull();
  });

  it('positions at the cursor hint (x − 28) below the anchor (E14)', () => {
    renderPicker({ cursorHint: { x: 300, y: 110 } });
    const picker = document.querySelector<HTMLElement>('.md-quick-label-picker')!;
    expect(picker.style.left).toBe('272px');
    expect(picker.style.top).toBe('126px'); // anchor bottom (120) + 6 gap
  });

  it('clamps to the viewport with 12px padding (E14)', () => {
    renderPicker({ cursorHint: { x: 2, y: 110 } });
    const picker = document.querySelector<HTMLElement>('.md-quick-label-picker')!;
    expect(picker.style.left).toBe('12px');
  });

  it('goes above the anchor when its measured height does not fit below', () => {
    // The picker is as tall as the label set makes it. It used to flip on a
    // guessed 220px, which a ten-row list already exceeded — so adding labels
    // pushed the last ones off the bottom of the window, unclickable.
    withPickerHeight(300);
    const anchorEl = makeAnchor({ top: window.innerHeight - 40, bottom: window.innerHeight - 20 });
    renderPicker({ anchorEl });
    const picker = document.querySelector<HTMLElement>('.md-quick-label-picker')!;
    // anchor top (innerHeight - 40) - 6 gap - 300 height
    expect(picker.style.top).toBe(`${window.innerHeight - 346}px`);
  });

  it('clamps into the viewport when it fits neither above nor below', () => {
    withPickerHeight(window.innerHeight);
    const anchorEl = makeAnchor({ top: window.innerHeight - 40, bottom: window.innerHeight - 20 });
    renderPicker({ anchorEl });
    const picker = document.querySelector<HTMLElement>('.md-quick-label-picker')!;
    expect(picker.style.top).toBe('12px');
  });

  it('the opening click does not dismiss it; the next outside pointerdown does (E15)', () => {
    const { props } = renderPicker();
    const outside = document.createElement('div');
    document.body.appendChild(outside);

    // Before the one-tick deferral elapses (the opening click's own
    // pointerdown), nothing dismisses.
    act(() => {
      outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onDismiss).not.toHaveBeenCalled();

    // After the deferred listener installs, outside pointerdown dismisses.
    act(() => {
      vi.advanceTimersByTime(1);
    });
    act(() => {
      outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onDismiss).toHaveBeenCalledTimes(1);
    outside.remove();
  });

  it('bare digits and Alt+digits select labels; 0 is the tenth (E16)', () => {
    const { props } = renderPicker();
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { code: 'Digit1', key: '1', bubbles: true }));
    });
    expect(props.onSelect).toHaveBeenLastCalledWith(QUICK_LABELS[0]);
    act(() => {
      window.dispatchEvent(
        new KeyboardEvent('keydown', { code: 'Digit3', key: '3', altKey: true, bubbles: true }),
      );
    });
    expect(props.onSelect).toHaveBeenLastCalledWith(QUICK_LABELS[2]);
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { code: 'Digit0', key: '0', bubbles: true }));
    });
    expect(props.onSelect).toHaveBeenLastCalledWith(QUICK_LABELS[9]);
  });

  it('ctrl/meta digits are left alone (app shortcuts)', () => {
    const { props } = renderPicker();
    act(() => {
      window.dispatchEvent(
        new KeyboardEvent('keydown', { code: 'Digit1', key: '1', metaKey: true, bubbles: true }),
      );
    });
    expect(props.onSelect).not.toHaveBeenCalled();
  });

  it('Escape dismisses (E16)', () => {
    const { props } = renderPicker();
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    });
    expect(props.onDismiss).toHaveBeenCalledTimes(1);
  });

  it('clicking a row selects its label', () => {
    const { props } = renderPicker();
    fireEvent.click(screen.getByText(QUICK_LABELS[4].text));
    expect(props.onSelect).toHaveBeenCalledWith(QUICK_LABELS[4]);
  });
});
