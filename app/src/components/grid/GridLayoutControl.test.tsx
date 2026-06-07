import { fireEvent, render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import { GridLayoutControl } from './GridLayoutControl';
import { AUTO_LAYOUT, type GridLayout } from './gridLayout';

function open(layout: GridLayout = AUTO_LAYOUT) {
  const onSelect = vi.fn();
  const { container } = render(<GridLayoutControl layout={layout} onSelect={onSelect} />);
  fireEvent.click(screen.getByLabelText('Grid layout'));
  return { onSelect, container };
}

const onCells = (container: HTMLElement) => container.querySelectorAll('.grid-layout-cell.on').length;
const labelText = (container: HTMLElement) => container.querySelector('.grid-layout-label')?.textContent;

describe('GridLayoutControl', () => {
  it('keeps the popover closed until the button is clicked', () => {
    const onSelect = vi.fn();
    const { container } = render(<GridLayoutControl layout={AUTO_LAYOUT} onSelect={onSelect} />);
    expect(container.querySelector('.grid-layout-popover')).toBeNull();
    fireEvent.click(screen.getByLabelText('Grid layout'));
    expect(container.querySelector('.grid-layout-popover')).not.toBeNull();
    // Default 5×5 picker.
    expect(container.querySelectorAll('.grid-layout-cell')).toHaveLength(25);
  });

  it('highlights the top-left→cursor rectangle on hover', () => {
    const { container } = open();
    fireEvent.mouseEnter(container.querySelector('[data-rc="2x3"]')!);
    // 2 rows × 3 cols = the 6 cells from the top-left corner.
    expect(onCells(container)).toBe(6);
    expect(labelText(container)).toBe('2 × 3');
  });

  it('commits the hovered shape and closes on click', () => {
    const { onSelect, container } = open();
    fireEvent.click(container.querySelector('[data-rc="3x2"]')!);
    expect(onSelect).toHaveBeenCalledWith({ mode: 'fixed', rows: 3, cols: 2 });
    expect(container.querySelector('.grid-layout-popover')).toBeNull();
  });

  it('commits Auto from the Auto chip', () => {
    const { onSelect } = open({ mode: 'fixed', rows: 2, cols: 2 });
    fireEvent.click(screen.getByText('Auto'));
    expect(onSelect).toHaveBeenCalledWith({ mode: 'auto' });
  });

  it('shows the saved fixed selection at rest', () => {
    const { container } = open({ mode: 'fixed', rows: 2, cols: 3 });
    expect(onCells(container)).toBe(6);
    expect(labelText(container)).toBe('2 × 3');
    expect(screen.getByText('Auto').className).not.toContain('active');
  });

  it('marks Auto active and highlights nothing when layout is auto', () => {
    const { container } = open(AUTO_LAYOUT);
    expect(onCells(container)).toBe(0);
    expect(labelText(container)).toBe('Auto');
    expect(container.querySelector('.grid-layout-auto')!.className).toContain('active');
  });
});
