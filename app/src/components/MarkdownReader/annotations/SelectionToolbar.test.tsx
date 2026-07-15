/**
 * SelectionToolbar — the interaction contract:
 * - positioning modes (center-above / top-right) and scroll-out close (E5);
 * - Delete / 👍 / Cancel buttons (E6, E7 wiring);
 * - Alt+1..Alt+0 quick labels (E8);
 * - the full type-to-comment guard set, one assertion per guard (E9);
 * - keyboard suppression while the picker is open (E16);
 * - outside-pointerdown dismiss.
 */

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { SelectionToolbar, type SelectionToolbarProps } from './SelectionToolbar';
import { QUICK_LABELS, THUMBS_UP_LABEL } from './quickLabels';

function fakeRect(overrides: Partial<DOMRect> = {}): DOMRect {
  const base = { top: 200, bottom: 220, left: 100, right: 300, width: 200, height: 20, x: 100, y: 200 };
  return { ...base, ...overrides, toJSON: () => ({}) } as DOMRect;
}

function renderToolbar(overrides: Partial<SelectionToolbarProps> = {}) {
  const props: SelectionToolbarProps = {
    getAnchorRect: () => fakeRect(),
    positionMode: 'center-above',
    copyText: 'selected words',
    onDelete: vi.fn(),
    onRequestComment: vi.fn(),
    onQuickLabel: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
  const view = render(<SelectionToolbar {...props} />);
  return { view, props };
}

function pressKey(init: KeyboardEventInit & { isComposing?: boolean }) {
  act(() => {
    const event = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init });
    if (init.isComposing && !event.isComposing) {
      Object.defineProperty(event, 'isComposing', { value: true });
    }
    window.dispatchEvent(event);
  });
}

describe('SelectionToolbar', () => {
  it('positions center-above for prose: top = rect.top - 48, horizontally centered', () => {
    renderToolbar({ getAnchorRect: () => fakeRect({ top: 300, left: 100, width: 200, right: 300 }) });
    const toolbar = document.querySelector<HTMLElement>('.md-selection-toolbar')!;
    expect(toolbar.style.top).toBe('252px');
    expect(toolbar.style.left).toBe('200px'); // rect center; CSS translateX(-50%) centers
    expect(toolbar.classList.contains('md-selection-toolbar--centered')).toBe(true);
  });

  it('positions top-right for code blocks: top = rect.top - 40, right-aligned', () => {
    renderToolbar({
      positionMode: 'top-right',
      getAnchorRect: () => fakeRect({ top: 300, right: 500 }),
    });
    const toolbar = document.querySelector<HTMLElement>('.md-selection-toolbar')!;
    expect(toolbar.style.top).toBe('260px');
    expect(toolbar.style.right).toBe(`${window.innerWidth - 500}px`);
    expect(toolbar.classList.contains('md-selection-toolbar--centered')).toBe(false);
  });

  it('closes when the anchor rect scrolls fully out of the viewport (E5)', () => {
    let rect = fakeRect();
    const { props } = renderToolbar({ getAnchorRect: () => rect, closeOnScrollOut: true });
    expect(props.onClose).not.toHaveBeenCalled();

    rect = fakeRect({ top: -100, bottom: -80 }); // fully above the viewport
    act(() => {
      window.dispatchEvent(new Event('scroll'));
    });
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it('Delete button fires onDelete instantly — the redline path (E6)', () => {
    const { props } = renderToolbar();
    fireEvent.click(screen.getByTitle('Delete'));
    expect(props.onDelete).toHaveBeenCalledTimes(1);
    expect(props.onRequestComment).not.toHaveBeenCalled();
  });

  it('👍 button applies the fixed thumbs-up label instantly (E7)', () => {
    const { props } = renderToolbar();
    fireEvent.click(screen.getByTitle('Looks good'));
    expect(props.onQuickLabel).toHaveBeenCalledWith(THUMBS_UP_LABEL);
  });

  it('Cancel button closes', () => {
    const { props } = renderToolbar();
    fireEvent.click(screen.getByTitle('Cancel'));
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it('Alt+1..Alt+9 and Alt+0 apply quick labels 1..10 (E8)', () => {
    const { props } = renderToolbar();
    pressKey({ code: 'Digit1', key: '1', altKey: true });
    expect(props.onQuickLabel).toHaveBeenLastCalledWith(QUICK_LABELS[0]);
    pressKey({ code: 'Digit9', key: '9', altKey: true });
    expect(props.onQuickLabel).toHaveBeenLastCalledWith(QUICK_LABELS[8]);
    pressKey({ code: 'Digit0', key: '0', altKey: true });
    expect(props.onQuickLabel).toHaveBeenLastCalledWith(QUICK_LABELS[9]);
    expect(props.onQuickLabel).toHaveBeenCalledTimes(3);
    expect(props.onRequestComment).not.toHaveBeenCalled();
  });

  describe('type-to-comment guard set (E9)', () => {
    it('opens the popover seeded with a printable key', () => {
      const { props } = renderToolbar();
      pressKey({ key: 'a' });
      expect(props.onRequestComment).toHaveBeenCalledWith('a');
    });

    it('guard 1: ignores keys during IME composition', () => {
      const { props } = renderToolbar();
      pressKey({ key: 'a', isComposing: true });
      expect(props.onRequestComment).not.toHaveBeenCalled();
    });

    it('guard 2: ignores keys targeted at editable elements', () => {
      const { props } = renderToolbar();
      const input = document.createElement('input');
      document.body.appendChild(input);
      act(() => {
        input.focus();
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', bubbles: true }));
      });
      expect(props.onRequestComment).not.toHaveBeenCalled();
      input.remove();
    });

    it('guard 3: picker open → picker owns the keyboard (E16)', () => {
      const { props } = renderToolbar();
      fireEvent.click(screen.getByTitle('Quick label')); // open picker
      expect(document.querySelector('.md-quick-label-picker')).not.toBeNull();
      pressKey({ key: 'a' });
      expect(props.onRequestComment).not.toHaveBeenCalled();
      // ...but digits now select labels via the picker.
      pressKey({ code: 'Digit2', key: '2' });
      expect(props.onQuickLabel).toHaveBeenCalledWith(QUICK_LABELS[1]);
    });

    it('guard 4: Escape closes the toolbar', () => {
      const { props } = renderToolbar();
      pressKey({ key: 'Escape' });
      expect(props.onClose).toHaveBeenCalledTimes(1);
      expect(props.onRequestComment).not.toHaveBeenCalled();
    });

    it('guard 6: ctrl/meta/alt combos are ignored', () => {
      const { props } = renderToolbar();
      pressKey({ key: 'c', metaKey: true });
      pressKey({ key: 'c', ctrlKey: true });
      pressKey({ key: 'c', altKey: true }); // alt without a digit
      expect(props.onRequestComment).not.toHaveBeenCalled();
      expect(props.onQuickLabel).not.toHaveBeenCalled();
    });

    it('guard 7: Tab and Enter are ignored', () => {
      const { props } = renderToolbar();
      pressKey({ key: 'Tab' });
      pressKey({ key: 'Enter' });
      expect(props.onRequestComment).not.toHaveBeenCalled();
    });

    it('guard 8: multi-char keys are ignored', () => {
      const { props } = renderToolbar();
      pressKey({ key: 'ArrowLeft' });
      pressKey({ key: 'F5' });
      expect(props.onRequestComment).not.toHaveBeenCalled();
    });
  });

  it('dismisses on outside pointerdown, but not while the picker is open', () => {
    const { props } = renderToolbar();
    const outside = document.createElement('div');
    document.body.appendChild(outside);

    // Picker open: outside pointerdown must NOT close the toolbar.
    fireEvent.click(screen.getByTitle('Quick label'));
    act(() => {
      outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onClose).not.toHaveBeenCalled();

    // Picker closed again: outside pointerdown closes.
    fireEvent.click(screen.getByTitle('Quick label'));
    act(() => {
      outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onClose).toHaveBeenCalledTimes(1);
    outside.remove();
  });

  it('clicks inside the toolbar never dismiss it', () => {
    const { props } = renderToolbar();
    const toolbar = document.querySelector<HTMLElement>('.md-selection-toolbar')!;
    act(() => {
      toolbar.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onClose).not.toHaveBeenCalled();
  });
});
