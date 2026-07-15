/**
 * AnnotationSidebar — position sort with globals last (E18), orphan cards
 * (E22 surface), hover-reveal delete, two-step clear-all, quick-label chips,
 * global-comment button, count-pill toggle.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import type { AnchorRecord } from '../anchoring';
import { AnnotationSidebar, sortAnnotations, type AnnotationSidebarProps } from './AnnotationSidebar';
import { QUICK_LABELS } from './quickLabels';
import type { Annotation } from './types';
import type { AnnotationOrphanReason } from './useAnnotations';

function anchor(startLine: number, start: number, exact = 'quoted text'): AnchorRecord {
  return {
    blockId: `b${startLine}`,
    startLine,
    endLine: startLine,
    exact,
    prefix: '',
    suffix: '',
    start,
    end: start + exact.length,
    contentHash: 'h',
  };
}

function ann(id: string, overrides: Partial<Annotation> = {}): Annotation {
  return { id, type: 'comment', text: `text-${id}`, createdAt: 1, ...overrides };
}

function renderSidebar(overrides: Partial<AnnotationSidebarProps> = {}) {
  const props: AnnotationSidebarProps = {
    annotations: [],
    orphans: new Map<string, AnnotationOrphanReason>(),
    selectedId: null,
    onCardClick: vi.fn(),
    onDelete: vi.fn(),
    onClearAll: vi.fn(),
    onGlobalComment: vi.fn(),
    onToggle: vi.fn(),
    ...overrides,
  };
  const view = render(<AnnotationSidebar {...props} />);
  return { view, props };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('sortAnnotations', () => {
  it('sorts by startLine, then start; globals last by createdAt (E18)', () => {
    const list = [
      ann('global-late', { type: 'global', createdAt: 20 }),
      ann('line9', { anchor: anchor(9, 0) }),
      ann('global-early', { type: 'global', createdAt: 10 }),
      ann('line3-late', { anchor: anchor(3, 40) }),
      ann('line3-early', { anchor: anchor(3, 5) }),
    ];
    expect(sortAnnotations(list).map((a) => a.id)).toEqual([
      'line3-early',
      'line3-late',
      'line9',
      'global-early',
      'global-late',
    ]);
  });
});

describe('AnnotationSidebar', () => {
  it('renders cards in document order with type badges and quotes', () => {
    renderSidebar({
      annotations: [
        ann('two', { anchor: anchor(8, 0, 'second quote') }),
        ann('one', { type: 'deletion', text: undefined, anchor: anchor(2, 0, 'first quote') }),
      ],
    });
    const cards = document.querySelectorAll('.md-annotation-card');
    expect(cards).toHaveLength(2);
    expect(cards[0].textContent).toContain('first quote');
    expect(cards[0].querySelector('.md-card-badge')!.textContent).toBe('deletion');
    expect(cards[1].textContent).toContain('second quote');
    expect(cards[1].textContent).toContain('text-two');
  });

  it('shows the empty state and count', () => {
    renderSidebar();
    expect(screen.getByText('No annotations yet.')).not.toBeNull();
    expect(document.querySelector('.md-sidebar-count')!.textContent).toBe('0');
    // No clear-all button when there is nothing to clear.
    expect(screen.queryByText('Clear all')).toBeNull();
  });

  it('card click reports the id; delete stops propagation (E19/E20 wiring)', () => {
    const { props } = renderSidebar({
      annotations: [ann('a1', { anchor: anchor(1, 0) })],
    });
    fireEvent.click(document.querySelector('.md-annotation-card')!);
    expect(props.onCardClick).toHaveBeenCalledWith('a1');

    fireEvent.click(screen.getByTitle('Remove annotation'));
    expect(props.onDelete).toHaveBeenCalledWith('a1');
    expect(props.onCardClick).toHaveBeenCalledTimes(1); // delete did not select
  });

  it('orphan card shows the badge, quote, and ~line N (moved) (E22)', () => {
    renderSidebar({
      annotations: [ann('gone', { anchor: anchor(14, 3, 'vanished words') })],
      orphans: new Map([['gone', 'text-not-found' as AnnotationOrphanReason]]),
    });
    const card = document.querySelector('.md-annotation-card--orphan')!;
    expect(card).not.toBeNull();
    expect(card.textContent).toContain('⚠ moved');
    expect(card.textContent).toContain('vanished words');
    expect(card.textContent).toContain('~line 14 (moved)');
    // Still deletable.
    expect(card.querySelector('.md-card-delete')).not.toBeNull();
  });

  it('clear-all is a two-step confirm wired to onClearAll (E21 wiring)', () => {
    const { props } = renderSidebar({ annotations: [ann('a1', { anchor: anchor(1, 0) })] });
    fireEvent.click(screen.getByText('Clear all'));
    expect(props.onClearAll).not.toHaveBeenCalled(); // first click arms
    fireEvent.click(screen.getByText('Confirm?'));
    expect(props.onClearAll).toHaveBeenCalledTimes(1);
  });

  it('the armed clear-all confirm disarms after a beat', () => {
    const { props } = renderSidebar({ annotations: [ann('a1', { anchor: anchor(1, 0) })] });
    fireEvent.click(screen.getByText('Clear all'));
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.queryByText('Confirm?')).toBeNull();
    expect(props.onClearAll).not.toHaveBeenCalled();
  });

  it('quick-label annotations render the label chip, unknown ids render raw (forward compat)', () => {
    const label = QUICK_LABELS[1];
    renderSidebar({
      annotations: [
        ann('ql', { text: undefined, quickLabelId: label.id, anchor: anchor(1, 0) }),
        ann('unknown', { text: undefined, quickLabelId: 'future-label', anchor: anchor(2, 0) }),
      ],
    });
    const cards = document.querySelectorAll('.md-annotation-card');
    expect(cards[0].querySelector('.md-ql-chip')!.textContent).toContain(label.text);
    expect(cards[0].querySelector('.md-ql-chip')!.textContent).toContain(label.emoji);
    expect(cards[1].querySelector('.md-card-badge')!.textContent).toBe('future-label');
  });

  it('global comment button hands its element to the popover opener (E13 wiring)', () => {
    const { props } = renderSidebar();
    const button = screen.getByTitle('Add a document-wide comment');
    fireEvent.click(button);
    expect(props.onGlobalComment).toHaveBeenCalledWith(button);
  });

  it('the count pill toggles (collapses) the sidebar', () => {
    const { props } = renderSidebar({ annotations: [ann('a1', { anchor: anchor(1, 0) })] });
    fireEvent.click(screen.getByTitle('Collapse annotations sidebar'));
    expect(props.onToggle).toHaveBeenCalledTimes(1);
  });

  it('marks the selected card', () => {
    renderSidebar({
      annotations: [ann('a1', { anchor: anchor(1, 0) }), ann('a2', { anchor: anchor(2, 0) })],
      selectedId: 'a2',
    });
    const selected = document.querySelector('.md-annotation-card--selected')!;
    expect(selected.textContent).toContain('text-a2');
  });
});
