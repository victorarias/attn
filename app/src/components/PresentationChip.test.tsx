import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { HeaderPresentationChip } from './PresentationChip';
import type { Presentation } from '../types/generated';

function makePresentation(overrides: Partial<Presentation> = {}): Presentation {
  return {
    id: 'pres-1',
    created_at: '2026-07-01T00:00:00Z',
    kind: 'pr',
    latest_round_seq: 1,
    latest_round_submitted: false,
    repo_path: '/repo',
    session_id: 'session-1',
    status: 'open',
    title: 'My presentation title',
    ...overrides,
  };
}

describe('HeaderPresentationChip', () => {
  it('shows the review label and the presentation title as a tooltip', () => {
    render(<HeaderPresentationChip presentation={makePresentation()} onOpen={vi.fn()} />);

    const button = screen.getByRole('button');
    expect(screen.getByText('▶ review')).toBeInTheDocument();
    expect(button).toHaveAttribute('title', 'My presentation title');
  });

  it('exposes presentation and session ids for automation', () => {
    render(<HeaderPresentationChip presentation={makePresentation({ id: 'pres-7', session_id: 'session-7' })} onOpen={vi.fn()} />);

    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('data-presentation-id', 'pres-7');
    expect(button).toHaveAttribute('data-session-id', 'session-7');
  });

  it('opens the presentation on click and does not bubble to the pane', () => {
    const onOpen = vi.fn();
    const onPaneClick = vi.fn();
    const onPanePointerDown = vi.fn();
    render(
      <div onClick={onPaneClick} onPointerDown={onPanePointerDown}>
        <HeaderPresentationChip presentation={makePresentation({ id: 'pres-42' })} onOpen={onOpen} />
      </div>,
    );

    const button = screen.getByRole('button');
    // In a split the pane header is a leaf-drag handle (beginLeafDrag); a press on
    // the chip must not reach it or a sloppy click would relocate the pane.
    fireEvent.pointerDown(button);
    expect(onPanePointerDown).not.toHaveBeenCalled();

    fireEvent.click(button);
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith('pres-42');
    expect(onPaneClick).not.toHaveBeenCalled();
  });
});
