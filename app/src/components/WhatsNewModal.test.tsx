import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { WhatsNewModal } from './WhatsNewModal';

function renderModal(overrides: Partial<Parameters<typeof WhatsNewModal>[0]> = {}) {
  const props = {
    isOpen: true,
    onClose: vi.fn(),
    onViewShortcuts: vi.fn(),
    ...overrides,
  };
  render(<WhatsNewModal {...props} />);
  return props;
}

describe('WhatsNewModal', () => {
  it('renders nothing when closed', () => {
    const { container } = render(
      <WhatsNewModal isOpen={false} onClose={() => {}} onViewShortcuts={() => {}} />
    );
    expect(container.firstChild).toBeNull();
  });

  it('leads with the ⌘N-in-workspace change as a flagged callout', () => {
    renderModal();
    expect(screen.getByRole('dialog', { name: /workspaces/i })).toBeInTheDocument();
    expect(screen.getByText('The sidebar lists workspaces')).toBeInTheDocument();
    expect(screen.getByText('Shells live here too')).toBeInTheDocument();

    // The headline change is rendered as the flagged callout and contrasts
    // ⌘N (add to this workspace) with ⌘T (new workspace), separated by "/".
    const hero = screen.getByText(/opens a session inside this workspace/).closest('.whats-new-item');
    expect(hero).not.toBeNull();
    expect(hero!.classList.contains('whats-new-item--key')).toBe(true);
    expect(hero!.querySelector('.whats-new-tag')?.textContent).toBe('Changed');
    expect(hero!.textContent).toContain('N');
    expect(hero!.textContent).toContain('T');
    expect(hero!.querySelector('.key-combos-sep')?.textContent).toBe('/');
  });

  it('dismisses and hands off to the full shortcuts list', () => {
    const onClose = vi.fn();
    const onViewShortcuts = vi.fn();
    renderModal({ onClose, onViewShortcuts });

    fireEvent.click(screen.getByRole('button', { name: 'View all shortcuts →' }));
    expect(onViewShortcuts).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'Got it' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
