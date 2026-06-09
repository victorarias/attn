import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ChiefOfStaffTransferPrompt } from './ChiefOfStaffTransferPrompt';

describe('ChiefOfStaffTransferPrompt', () => {
  it('describes the transfer without implying either session stops', () => {
    const onConfirm = vi.fn();
    render(
      <ChiefOfStaffTransferPrompt
        isVisible
        currentLabel="Current planner"
        targetLabel="New planner"
        isSaving={false}
        onConfirm={onConfirm}
        onCancel={() => {}}
      />
    );

    expect(screen.getByText(/Both sessions will keep running/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Transfer role' }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});
