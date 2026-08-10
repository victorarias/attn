import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { NotificationsPanel } from './NotificationsPanel';
import type { DaemonNotification } from '../hooks/useDaemonSocket';
import { NotificationSeverity } from '../types/generated';

function notification(over: Partial<DaemonNotification>): DaemonNotification {
  return {
    id: 'n1',
    kind: 'task_failed',
    severity: NotificationSeverity.Info,
    title: 'Something happened',
    body: 'body',
    detail: '',
    source_kind: '',
    source_id: '',
    created_at: new Date().toISOString(),
    read_at: '',
    ...over,
  } as DaemonNotification;
}

function renderPanel(notifications: DaemonNotification[]) {
  const listNotifications = vi.fn().mockResolvedValue({
    notifications,
    unreadCount: notifications.filter((n) => !n.read_at).length,
    critical: { count: 0, title: '' },
  });
  render(
    <NotificationsPanel
      open
      onClose={vi.fn()}
      listNotifications={listNotifications}
      markRead={vi.fn().mockResolvedValue(0)}
      retryTask={vi.fn().mockResolvedValue(null)}
      changeSignal={0}
    />,
  );
  return { listNotifications };
}

function rowFor(title: string): HTMLElement {
  const row = screen.getByText(title).closest('li');
  if (!row) throw new Error(`no row for ${title}`);
  return row;
}

// Severity is what the panel styles each row by, so the class that carries it
// has to be on the row for every value — including one the daemon may send that
// this build does not know, which must land somewhere rather than nowhere.
describe('NotificationsPanel severity', () => {
  it('styles each row by its severity', async () => {
    renderPanel([
      notification({ id: 'a', severity: NotificationSeverity.Critical, title: 'Plugin stopped' }),
      notification({ id: 'b', severity: NotificationSeverity.Warning, title: 'Ticket reconciliation failed' }),
      notification({ id: 'c', severity: NotificationSeverity.Info, title: 'Compaction finished' }),
    ]);

    await waitFor(() => expect(screen.getByText('Plugin stopped')).toBeInTheDocument());

    expect(rowFor('Plugin stopped')).toHaveClass('sev-critical');
    expect(rowFor('Ticket reconciliation failed')).toHaveClass('sev-warning');
    expect(rowFor('Compaction finished')).toHaveClass('sev-info');
  });

  it('treats an unrecognized severity as info rather than leaving a row unstyled', async () => {
    renderPanel([
      notification({ id: 'a', severity: 'catastrophic' as DaemonNotification['severity'], title: 'From the future' }),
    ]);

    await waitFor(() => expect(screen.getByText('From the future')).toBeInTheDocument());

    expect(rowFor('From the future')).toHaveClass('sev-info');
  });

  it('keeps severity on a row after it is read', async () => {
    renderPanel([
      notification({
        id: 'a',
        severity: NotificationSeverity.Critical,
        title: 'Plugin stopped',
        read_at: new Date().toISOString(),
      }),
    ]);

    await waitFor(() => expect(screen.getByText('Plugin stopped')).toBeInTheDocument());

    const row = rowFor('Plugin stopped');
    // Severity describes the event, not whether the user has seen it.
    expect(row).toHaveClass('sev-critical');
    expect(row).not.toHaveClass('is-unread');
  });
});
