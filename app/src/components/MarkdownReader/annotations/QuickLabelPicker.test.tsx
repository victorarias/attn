/**
 * QuickLabelPicker — cursor-hint positioning with viewport clamping (E14),
 * one-tick-deferred outside dismiss (E15), digit/Alt+digit selection and
 * Escape (E16), and grouped label rendering.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import {
  QuickLabelPicker,
  type FloatingQuickLabelPickerProps,
} from '../../../annotations/QuickLabelPicker';
import { QUICK_LABEL_PICKER_GROUPS, QUICK_LABEL_PICKER_LABELS } from './quickLabels';

function makeAnchor(rect: Partial<DOMRect> = {}): HTMLElement {
  const el = document.createElement('button');
  document.body.appendChild(el);
  const base = { top: 100, bottom: 120, left: 200, right: 230, width: 30, height: 20, x: 200, y: 100 };
  el.getBoundingClientRect = () => ({ ...base, ...rect, toJSON: () => ({}) }) as DOMRect;
  return el;
}

function renderPicker(overrides: Partial<FloatingQuickLabelPickerProps> = {}) {
  const anchorEl = overrides.anchorEl ?? makeAnchor();
  const props: FloatingQuickLabelPickerProps = {
    className: 'md-quick-label-picker',
    groups: QUICK_LABEL_PICKER_GROUPS,
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
  it('renders the filtered groups with dividers and nine digit shortcuts', () => {
    renderPicker();
    const rows = document.querySelectorAll('.md-quick-label-row');
    expect(rows).toHaveLength(9);
    expect(document.querySelectorAll('.md-quick-label-divider')).toHaveLength(4);
    expect(Array.from(rows, (row) => row.querySelector('.md-ql-chip')?.textContent)).toEqual([
      '💯', '😕', '🔍', '🧾', '🔬', '🔄', '🪓', '🪙', '🙋',
    ]);
    expect(Array.from(rows, (row) => row.getAttribute('data-quick-label-id'))).toEqual(
      QUICK_LABEL_PICKER_LABELS.map((label) => label.id),
    );
    expect(rows[0].textContent).toContain(QUICK_LABEL_PICKER_LABELS[0].text);
    expect(rows[0].querySelector('.md-quick-label-num')!.textContent).toBe('1');
    expect(rows[8].querySelector('.md-quick-label-num')!.textContent).toBe('9');
    expect(document.querySelectorAll('.md-quick-label-num')).toHaveLength(9);
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

  it('bare digits and Alt+digits select the flattened group order; 0 does nothing (E16)', () => {
    const { props } = renderPicker();
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { code: 'Digit1', key: '1', bubbles: true }));
    });
    expect(props.onSelect).toHaveBeenLastCalledWith(QUICK_LABEL_PICKER_LABELS[0]);
    act(() => {
      window.dispatchEvent(
        new KeyboardEvent('keydown', { code: 'Digit3', key: '3', altKey: true, bubbles: true }),
      );
    });
    expect(props.onSelect).toHaveBeenLastCalledWith(QUICK_LABEL_PICKER_LABELS[2]);
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { code: 'Digit0', key: '0', bubbles: true }));
    });
    expect(props.onSelect).toHaveBeenCalledTimes(2);
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
    fireEvent.click(screen.getByText(QUICK_LABEL_PICKER_LABELS[4].text));
    expect(props.onSelect).toHaveBeenCalledWith(QUICK_LABEL_PICKER_LABELS[4]);
  });
});
