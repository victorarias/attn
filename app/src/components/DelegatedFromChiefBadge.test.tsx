import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { DelegatedFromChiefBadge } from './DelegatedFromChiefBadge';

describe('DelegatedFromChiefBadge', () => {
  it('renders an icon-only chip with an accessible name and no text label', () => {
    render(<DelegatedFromChiefBadge />);
    const badge = screen.getByLabelText('Delegated from chief of staff');
    expect(badge).toHaveClass('delegated-from-chief-badge');
    // Icon-only: the "↳" glyph is present but no worded label like "chief".
    expect(badge).toHaveTextContent('↳');
    expect(badge.textContent).not.toMatch(/chief/i);
  });
});
