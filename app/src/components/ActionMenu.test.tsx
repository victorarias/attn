import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ActionMenu, type ActionMenuItem } from './ActionMenu';

function actions(overrides: Partial<ActionMenuItem>[] = []): ActionMenuItem[] {
  const base: ActionMenuItem[] = [
    {
      id: 'contexts',
      title: 'Browse workspace contexts',
      description: 'Navigate shared context',
      keywords: ['memory'],
      icon: <span>C</span>,
      run: vi.fn(),
    },
    {
      id: 'attention',
      title: 'Open attention drawer',
      description: 'Show waiting work',
      keywords: ['notifications'],
      icon: <span>A</span>,
      run: vi.fn(),
    },
  ];
  return base.map((action, index) => ({ ...action, ...overrides[index] }));
}

describe('ActionMenu', () => {
  it('filters actions by keywords and runs the selected result', () => {
    const items = actions();
    const onClose = vi.fn();
    render(<ActionMenu isOpen actions={items} onClose={onClose} />);

    fireEvent.change(screen.getByLabelText('Search actions'), { target: { value: 'memory' } });
    expect(screen.getByText('Browse workspace contexts')).toBeVisible();
    expect(screen.queryByText('Open attention drawer')).toBeNull();

    fireEvent.keyDown(screen.getByLabelText('Search actions'), { key: 'Enter' });
    expect(items[0].run).toHaveBeenCalledOnce();
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('moves selection with arrow keys', () => {
    const items = actions();
    render(<ActionMenu isOpen actions={items} onClose={() => {}} />);

    const input = screen.getByLabelText('Search actions');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(items[1].run).toHaveBeenCalledOnce();
  });
});
