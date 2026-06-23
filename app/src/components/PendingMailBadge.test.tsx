import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { PendingMailBadge } from './PendingMailBadge';

describe('PendingMailBadge', () => {
  it('renders the count with an accessible name when there is unread mail', () => {
    render(<PendingMailBadge count={3} />);
    const badge = screen.getByLabelText('3 unread messages from chief');
    expect(badge).toHaveClass('pending-mail-badge');
    expect(badge).toHaveTextContent('3');
  });

  it('singularizes the label for one message', () => {
    render(<PendingMailBadge count={1} />);
    expect(screen.getByLabelText('1 unread message from chief')).toBeInTheDocument();
  });

  it('renders nothing when there is no unread mail', () => {
    const { container } = render(<PendingMailBadge count={0} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('hides the numeric label in compact mode', () => {
    render(<PendingMailBadge count={2} compact />);
    const badge = screen.getByLabelText('2 unread messages from chief');
    expect(badge).toHaveClass('compact');
  });
});
