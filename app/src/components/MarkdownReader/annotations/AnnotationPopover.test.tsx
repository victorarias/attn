/**
 * AnnotationPopover — submit keys, empty-submit block, Escape, dirty
 * click-outside blocking, draft survival across unmount, header labels
 * (E10–E13 surface).
 */

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { AnnotationPopover, peekAnnotationDraft, type AnnotationPopoverProps } from './AnnotationPopover';

function fakeRect(overrides: Partial<DOMRect> = {}): DOMRect {
  const base = { top: 100, bottom: 120, left: 100, right: 300, width: 200, height: 20, x: 100, y: 100 };
  return { ...base, ...overrides, toJSON: () => ({}) } as DOMRect;
}

let keySeq = 0;

function renderPopover(overrides: Partial<AnnotationPopoverProps> = {}) {
  const props: AnnotationPopoverProps = {
    getAnchorRect: () => fakeRect(),
    quote: 'quoted selection',
    isGlobal: false,
    draftKey: overrides.draftKey ?? `/tmp/doc.md#b1:0:5-${keySeq++}`,
    onSubmit: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  };
  const view = render(<AnnotationPopover {...props} />);
  return { view, props };
}

function textarea(): HTMLTextAreaElement {
  return document.querySelector<HTMLTextAreaElement>('.md-popover-textarea')!;
}

describe('AnnotationPopover', () => {
  it('Cmd+Enter submits the typed text (E10)', () => {
    const { props } = renderPopover();
    fireEvent.change(textarea(), { target: { value: 'a note' } });
    fireEvent.keyDown(textarea(), { key: 'Enter', metaKey: true });
    expect(props.onSubmit).toHaveBeenCalledWith('a note');
  });

  it('Ctrl+Enter submits too (E10)', () => {
    const { props } = renderPopover();
    fireEvent.change(textarea(), { target: { value: 'ctrl note' } });
    fireEvent.keyDown(textarea(), { key: 'Enter', ctrlKey: true });
    expect(props.onSubmit).toHaveBeenCalledWith('ctrl note');
  });

  it('empty (or whitespace-only) text cannot submit (E10)', () => {
    const { props } = renderPopover();
    expect(screen.getByText('Save')).toBeDisabled();
    fireEvent.keyDown(textarea(), { key: 'Enter', metaKey: true });
    fireEvent.change(textarea(), { target: { value: '   ' } });
    fireEvent.keyDown(textarea(), { key: 'Enter', metaKey: true });
    fireEvent.click(screen.getByText('Save'));
    expect(props.onSubmit).not.toHaveBeenCalled();
  });

  it('Escape closes (E10)', () => {
    const { props } = renderPopover();
    fireEvent.keyDown(textarea(), { key: 'Escape' });
    expect(props.onClose).toHaveBeenCalledTimes(1);
  });

  it('click-outside closes only when clean; typed text keeps it open (E11)', () => {
    const { props } = renderPopover();
    const outside = document.createElement('div');
    document.body.appendChild(outside);

    // Dirty: stays open.
    fireEvent.change(textarea(), { target: { value: 'unsaved' } });
    act(() => {
      outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onClose).not.toHaveBeenCalled();

    // Clean again: closes.
    fireEvent.change(textarea(), { target: { value: '' } });
    act(() => {
      outside.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onClose).toHaveBeenCalledTimes(1);
    outside.remove();
  });

  it('pointerdown inside the popover never closes it', () => {
    const { props } = renderPopover();
    act(() => {
      textarea().dispatchEvent(new Event('pointerdown', { bubbles: true }));
    });
    expect(props.onClose).not.toHaveBeenCalled();
  });

  it('draft text survives close→reopen for the same key; submit clears it (E12)', () => {
    const draftKey = '/tmp/doc.md#b2:3:9';
    const first = renderPopover({ draftKey });
    fireEvent.change(textarea(), { target: { value: 'work in progress' } });
    first.view.unmount();
    expect(peekAnnotationDraft(draftKey)).toBe('work in progress');

    // Reopen the same key: the draft is restored.
    const second = renderPopover({ draftKey });
    expect(textarea().value).toBe('work in progress');

    // Submit clears the stored draft.
    fireEvent.keyDown(textarea(), { key: 'Enter', metaKey: true });
    expect(second.props.onSubmit).toHaveBeenCalledWith('work in progress');
    expect(peekAnnotationDraft(draftKey)).toBeUndefined();
  });

  it('a different draft key does not leak another selection draft (E12)', () => {
    const first = renderPopover({ draftKey: '/tmp/doc.md#b1:0:4' });
    fireEvent.change(textarea(), { target: { value: 'for b1' } });
    first.view.unmount();

    renderPopover({ draftKey: '/tmp/doc.md#b9:0:4' });
    expect(textarea().value).toBe('');
  });

  it('seeds initialText for type-to-comment', () => {
    renderPopover({ initialText: 'x' });
    expect(textarea().value).toBe('x');
  });

  it('shows the truncated quote header; "Global Comment" for globals (E13 surface)', () => {
    const long = 'q'.repeat(60);
    const first = renderPopover({ quote: long });
    expect(screen.getByText(`"${'q'.repeat(50)}..."`)).not.toBeNull();
    first.view.unmount();

    renderPopover({ isGlobal: true, quote: '' });
    expect(screen.getByText('Global Comment')).not.toBeNull();
    expect(textarea().placeholder).toBe('Add a global comment...');
  });
});
