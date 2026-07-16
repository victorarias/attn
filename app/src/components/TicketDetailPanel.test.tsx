import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor, sleep, fireEvent } from '../test/utils';
import { TicketDetailPanel } from './TicketDetailPanel';
import type { Ticket } from '../hooks/useDaemonSocket';
import { TicketStatus, TicketActivityKind } from '../types/generated';
import { open } from '@tauri-apps/plugin-dialog';

vi.mock('@tauri-apps/plugin-dialog', () => ({ open: vi.fn() }));

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: 'store-migration',
    title: 'Migrate the store',
    description: 'Move the store to X',
    status: TicketStatus.InReview,
    assignee: 'sess-1',
    cwd: '/repo',
    last_agent_id: 'codex',
    project_id: '',
    created_at: '2026-06-27T10:00:00Z',
    updated_at: '2026-06-27T10:05:00Z',
    activity: [
      {
        id: 1,
        kind: TicketActivityKind.StatusChange,
        author: 'sess-1',
        from_status: TicketStatus.Working,
        to_status: TicketStatus.InReview,
        comment: 'ready for a look',
        created_at: '2026-06-27T10:05:00Z',
      },
      {
        id: 2,
        kind: TicketActivityKind.Comment,
        author: 'chief-1',
        comment: 'looks good',
        created_at: '2026-06-27T10:06:00Z',
      },
    ],
    artifacts: [
      { filename: 'report.md', path: '/repo/report.md', notebook_path: 'tickets/store-migration/report.md' },
    ],
    ...overrides,
  };
}

describe('TicketDetailPanel', () => {
  it('fetches the full record exactly once on open and renders it', async () => {
    const full = makeTicket();
    const fetchTicket = vi.fn().mockResolvedValue(full);

    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );

    await waitFor(() => screen.getByText('Move the store to X'));
    // history and artifacts are the full-record detail
    expect(screen.getByText('ready for a look')).toBeTruthy();
    expect(screen.getByText('looks good')).toBeTruthy();
    expect(screen.getByText('report.md')).toBeTruthy();

    expect(fetchTicket).toHaveBeenCalledTimes(1);
    expect(fetchTicket).toHaveBeenCalledWith('store-migration');
    // No fetch loop after settle.
    await sleep(60);
    expect(fetchTicket).toHaveBeenCalledTimes(1);
  });

  it('does not fetch while closed', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const { container } = render(
      <TicketDetailPanel
        isOpen={false}
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await sleep(40);
    expect(fetchTicket).not.toHaveBeenCalled();
    // Closed panel renders nothing.
    expect(container.querySelector('[data-testid="ticket-detail-panel"]')).toBeNull();
  });

  it('re-fetches when the ticket changes under an open panel (live refresh)', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const row = makeTicket();
    const { rerender } = render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={row}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => expect(fetchTicket).toHaveBeenCalledTimes(1));

    // A mutation bumps updated_at on the board row → the open panel re-fetches.
    rerender(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={{ ...row, updated_at: '2026-06-27T11:00:00Z' }}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => expect(fetchTicket).toHaveBeenCalledTimes(2));
    await sleep(60);
    expect(fetchTicket).toHaveBeenCalledTimes(2);
  });

  it('shows the bare-row header while the full record is still loading', async () => {
    // A fetch that never resolves: the header must still appear from the bare row.
    const fetchTicket = vi.fn().mockReturnValue(new Promise<Ticket>(() => {}));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket({ title: 'Bare row title' })}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByText('Bare row title'));
    expect(screen.getByText('Loading…')).toBeTruthy();
  });

  it('surfaces a fetch error', async () => {
    const fetchTicket = vi.fn().mockRejectedValue(new Error('ticket not found: store-migration'));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={undefined}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByRole('alert'));
    expect(screen.getByText('ticket not found: store-migration')).toBeTruthy();
  });

  it('calls onClose when the close button is clicked', async () => {
    const onClose = vi.fn();
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={onClose}
      />,
    );
    await waitFor(() => screen.getByText('Move the store to X'));
    screen.getByLabelText('Close ticket detail').click();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('changes status through the select', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onChangeStatus = vi.fn().mockResolvedValue(undefined);
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onChangeStatus={onChangeStatus}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-status-select'));

    fireEvent.change(screen.getByTestId('ticket-status-select'), { target: { value: 'done' } });
    expect(onChangeStatus).toHaveBeenCalledTimes(1);
    expect(onChangeStatus).toHaveBeenCalledWith('store-migration', 'done');
  });

  it('adds a comment and clears the draft on success', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onAddComment = vi.fn().mockResolvedValue(undefined);
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onAddComment={onAddComment}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-comment-input'));

    const input = screen.getByTestId('ticket-comment-input') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'try the other approach' } });
    fireEvent.click(screen.getByTestId('ticket-add-comment'));

    expect(onAddComment).toHaveBeenCalledWith('store-migration', 'try the other approach');
    await waitFor(() => expect(input.value).toBe(''));
  });

  it('submits a comment with ⌘Return and with Ctrl+Return, clearing the draft', async () => {
    for (const modifier of ['metaKey', 'ctrlKey'] as const) {
      const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
      const onAddComment = vi.fn().mockResolvedValue(undefined);
      const { unmount } = render(
        <TicketDetailPanel
          isOpen
          ticketId="store-migration"
          ticketRow={makeTicket()}
          fetchTicket={fetchTicket}
          onAddComment={onAddComment}
          onClose={() => {}}
        />,
      );
      await waitFor(() => screen.getByTestId('ticket-comment-input'));

      const input = screen.getByTestId('ticket-comment-input') as HTMLTextAreaElement;
      fireEvent.change(input, { target: { value: 'ship it' } });
      fireEvent.keyDown(input, { key: 'Enter', [modifier]: true });

      expect(onAddComment).toHaveBeenCalledWith('store-migration', 'ship it');
      await waitFor(() => expect(input.value).toBe(''));
      unmount();
    }
  });

  it('does not submit on a plain Enter (newline, not send)', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onAddComment = vi.fn().mockResolvedValue(undefined);
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onAddComment={onAddComment}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-comment-input'));

    const input = screen.getByTestId('ticket-comment-input') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'a line' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(onAddComment).not.toHaveBeenCalled();
  });

  it('keeps the submit button disabled until the draft has non-whitespace text', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onAddComment = vi.fn().mockResolvedValue(undefined);
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onAddComment={onAddComment}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-comment-input'));

    const input = screen.getByTestId('ticket-comment-input') as HTMLTextAreaElement;
    const button = screen.getByTestId('ticket-add-comment') as HTMLButtonElement;
    expect(button.disabled).toBe(true);

    // Whitespace only stays disabled, and the shortcut is a no-op on it.
    fireEvent.change(input, { target: { value: '   ' } });
    expect(button.disabled).toBe(true);
    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });
    expect(onAddComment).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: 'real text' } });
    expect(button.disabled).toBe(false);
  });

  it('disables the submit button and relabels it while the post is in flight', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    // A never-resolving post keeps the action pending so the busy UI is observable.
    const onAddComment = vi.fn().mockReturnValue(new Promise<void>(() => {}));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onAddComment={onAddComment}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-comment-input'));

    const input = screen.getByTestId('ticket-comment-input') as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: 'hang on' } });
    fireEvent.click(screen.getByTestId('ticket-add-comment'));

    const button = screen.getByTestId('ticket-add-comment') as HTMLButtonElement;
    await waitFor(() => expect(button.disabled).toBe(true));
    expect(button.textContent).toBe('Adding…');
    // A second shortcut press must not fire another post while one is pending.
    fireEvent.keyDown(input, { key: 'Enter', metaKey: true });
    expect(onAddComment).toHaveBeenCalledTimes(1);
  });

  it('edits the description through the edit toggle', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onEditDescription = vi.fn().mockResolvedValue(undefined);
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onEditDescription={onEditDescription}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-edit-description'));

    fireEvent.click(screen.getByTestId('ticket-edit-description'));
    const input = (await screen.findByTestId('ticket-description-input')) as HTMLTextAreaElement;
    // The editor pre-fills with the current description.
    expect(input.value).toBe('Move the store to X');

    fireEvent.change(input, { target: { value: 'Re-scoped: read path only' } });
    fireEvent.click(screen.getByTestId('ticket-save-description'));

    expect(onEditDescription).toHaveBeenCalledWith('store-migration', 'Re-scoped: read path only');
    // The editor closes on success.
    await waitFor(() => expect(screen.queryByTestId('ticket-description-input')).toBeNull());
  });

  it('surfaces an action error without claiming success', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onChangeStatus = vi.fn().mockRejectedValue(new Error('daemon refused the move'));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onChangeStatus={onChangeStatus}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-status-select'));

    fireEvent.change(screen.getByTestId('ticket-status-select'), { target: { value: 'failed' } });
    await waitFor(() => screen.getByRole('alert'));
    expect(screen.getByText('daemon refused the move')).toBeTruthy();
  });

  it('shows Resume and calls onResume with the ticket id for a delegated ticket', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onResume = vi.fn();
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        // Bare row already carries cwd + last_agent_id → the button is live before
        // the full record loads.
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onResume={onResume}
        onClose={() => {}}
      />,
    );
    const button = await screen.findByTestId('ticket-resume');
    fireEvent.click(button);
    expect(onResume).toHaveBeenCalledTimes(1);
    expect(onResume).toHaveBeenCalledWith('store-migration');
  });

  it('hides Resume when the ticket has no agent session to resume', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket({ cwd: '', last_agent_id: '' }));
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket({ cwd: '', last_agent_id: '' })}
        fetchTicket={fetchTicket}
        onResume={vi.fn()}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByText('Move the store to X'));
    expect(screen.queryByTestId('ticket-resume')).toBeNull();
  });

  it('renders read-only when no action handlers are wired', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByText('Move the store to X'));

    expect(screen.queryByTestId('ticket-status-select')).toBeNull();
    expect(screen.queryByTestId('ticket-comment-input')).toBeNull();
    expect(screen.queryByTestId('ticket-edit-description')).toBeNull();
  });

  it('hands over files of any type with optional state and comment', async () => {
    vi.mocked(open).mockResolvedValue(['/tmp/design.md', '/tmp/prototype.html', '/tmp/results.zip']);
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onAttach = vi.fn().mockResolvedValue(undefined);
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onAttach={onAttach}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByTestId('ticket-choose-attach'));
    fireEvent.click(screen.getByTestId('ticket-choose-attach'));
    await waitFor(() => screen.getByTestId('ticket-attach-form'));
    fireEvent.change(screen.getByLabelText('Resulting ticket state'), { target: { value: 'ready_for_review' } });
    fireEvent.change(screen.getByPlaceholderText('Decision context (optional)'), { target: { value: 'Chosen approach' } });
    fireEvent.click(screen.getByTestId('ticket-submit-attach'));
    await waitFor(() => expect(onAttach).toHaveBeenCalledWith(
      'store-migration',
      ['/tmp/design.md', '/tmp/prototype.html', '/tmp/results.zip'],
      'ready_for_review',
      'Chosen approach',
    ));
    expect(open).toHaveBeenCalledWith({ multiple: true });
  });

  it('opens, renames, and deletes a current artifact', async () => {
    const fetchTicket = vi.fn().mockResolvedValue(makeTicket());
    const onOpenArtifact = vi.fn();
    const onRenameArtifact = vi.fn().mockResolvedValue(undefined);
    const onDeleteArtifact = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(window, 'confirm', { configurable: true, value: vi.fn(() => true) });
    render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={makeTicket()}
        fetchTicket={fetchTicket}
        onOpenArtifact={onOpenArtifact}
        onRenameArtifact={onRenameArtifact}
        onDeleteArtifact={onDeleteArtifact}
        onClose={() => {}}
      />,
    );
    const artifact = await screen.findByText('report.md');
    fireEvent.click(artifact);
    expect(onOpenArtifact).toHaveBeenCalledWith('tickets/store-migration/report.md');

    fireEvent.click(screen.getByText('Rename'));
    fireEvent.change(screen.getByLabelText('Rename report.md'), { target: { value: 'implementation.html' } });
    fireEvent.click(screen.getByText('Save'));
    await waitFor(() => expect(onRenameArtifact).toHaveBeenCalledWith(
      'tickets/store-migration/report.md',
      'tickets/store-migration/implementation.html',
    ));

    fireEvent.click(screen.getByText('Delete'));
    await waitFor(() => expect(onDeleteArtifact).toHaveBeenCalledWith('tickets/store-migration/report.md'));
  });

  it('shows the orphan badge for an open reconciled ticket and hides it otherwise', async () => {
    const orphan = makeTicket({ status: TicketStatus.Working, reconciled_at: '2026-06-27T10:30:00Z' });
    const fetchTicket = vi.fn().mockResolvedValue(orphan);
    const { rerender } = render(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={orphan}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => screen.getByText('Move the store to X'));
    expect(screen.getByTestId('ticket-detail-orphan-badge')).toBeTruthy();

    // Respawn/reassign clears the stamp daemon-side → badge disappears.
    const owned = makeTicket({ status: TicketStatus.Working });
    fetchTicket.mockResolvedValue(owned);
    rerender(
      <TicketDetailPanel
        isOpen
        ticketId="store-migration"
        ticketRow={{ ...owned, updated_at: '2026-06-27T11:00:00Z' }}
        fetchTicket={fetchTicket}
        onClose={() => {}}
      />,
    );
    await waitFor(() => expect(screen.queryByTestId('ticket-detail-orphan-badge')).toBeNull());
  });
});
