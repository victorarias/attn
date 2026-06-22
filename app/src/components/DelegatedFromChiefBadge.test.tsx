import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';

describe('DelegatedFromChiefBadge', () => {
  it('renders the label and an accessible name by default', () => {
    render(<DelegatedFromChiefBadge />);
    const badge = screen.getByLabelText('Delegated from chief of staff');
    expect(badge).toHaveTextContent('chief');
    expect(badge).toHaveClass('delegated-from-chief-badge');
    expect(badge).not.toHaveClass('compact');
  });

  it('hides the text label and adds the compact class in compact mode', () => {
    render(<DelegatedFromChiefBadge compact />);
    const badge = screen.getByLabelText('Delegated from chief of staff');
    expect(badge).toHaveClass('compact');
    expect(badge).not.toHaveTextContent('chief');
  });
});
