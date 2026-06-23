import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, it, expect, vi } from 'vitest';
import { OnAgentMailOverlay, type PendingAgentMail } from './OnAgentMailOverlay';

const mail = (overrides: Partial<PendingAgentMail> = {}): PendingAgentMail => ({
  unreadCount: 2,
  dispatchId: 'dispatch-1',
  chiefSessionId: 'chief-1',
  wakeable: true,
  ...overrides,
});

describe('OnAgentMailOverlay', () => {
  it('renders nothing when there is no unread mail', () => {
    const { container } = render(<OnAgentMailOverlay mail={mail({ unreadCount: 0 })} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('fires the wake doorbell with the dispatch chief + id on click when wakeable', async () => {
    const onWake = vi.fn().mockResolvedValue(undefined);
    render(<OnAgentMailOverlay mail={mail()} onWake={onWake} />);
    await userEvent.click(screen.getByRole('button'));
    await waitFor(() => expect(onWake).toHaveBeenCalledWith('chief-1', 'dispatch-1'));
  });

  it('shows the count but stays non-interactive when not wakeable', async () => {
    const onWake = vi.fn().mockResolvedValue(undefined);
    render(<OnAgentMailOverlay mail={mail({ wakeable: false })} onWake={onWake} />);
    const button = screen.getByRole('button');
    expect(button).toBeDisabled();
    expect(button).toHaveTextContent('2');
    await userEvent.click(button);
    expect(onWake).not.toHaveBeenCalled();
  });

  it('surfaces a wake failure without crashing', async () => {
    const onWake = vi.fn().mockRejectedValue(new Error('agent is busy'));
    render(<OnAgentMailOverlay mail={mail()} onWake={onWake} />);
    await userEvent.click(screen.getByRole('button'));
    expect(await screen.findByText('agent is busy')).toBeInTheDocument();
  });
});
