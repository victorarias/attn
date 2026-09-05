import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { SessionsPanel } from './SessionsPanel';
import type { SessionLedgerPage, SessionLedgerQuery } from '../hooks/daemonSessionLedgerEvents';
import type { SessionLedgerEntry } from '../types/generated';
import { SessionState } from '../types/generated';

function entry(overrides: Partial<SessionLedgerEntry> & { id: string }): SessionLedgerEntry {
  return {
    agent: 'claude',
    directory: '/Users/victor/projects/attn',
    label: `run ${overrides.id}`,
    last_seen: '2026-09-05T10:00:00Z',
    state: SessionState.Idle,
    workspace_id: 'ws-1',
    ...overrides,
  };
}

// The panel's only door to the daemon: every assertion is about the query it
// puts through here, and the page it renders back.
function listing(pages: SessionLedgerPage[]) {
  const calls: SessionLedgerQuery[] = [];
  let at = 0;
  const list = vi.fn(async (query: SessionLedgerQuery) => {
    calls.push(query);
    return pages[Math.min(at++, pages.length - 1)];
  });
  return { list, calls };
}

function page(overrides: Partial<SessionLedgerPage> = {}): SessionLedgerPage {
  return { entries: [], omitted: 0, ...overrides };
}

const NOW = new Date('2026-09-05T14:30:00Z');
const now = () => NOW;

async function open(list: (query: SessionLedgerQuery) => Promise<SessionLedgerPage>, props = {}) {
  const view = render(
    <SessionsPanel isOpen onClose={() => {}} listSessions={list} now={now} {...props} />,
  );
  await waitFor(() => expect(screen.queryByText('Reading the ledger…')).toBeNull());
  return view;
}

function when(label: string) {
  fireEvent.change(screen.getByLabelText('When'), { target: { value: label } });
}

describe('SessionsPanel filters', () => {
  it('asks for both live and closed rows, newest page first', async () => {
    const { list, calls } = listing([page({ entries: [entry({ id: 's1' })] })]);
    await open(list);

    expect(calls).toEqual([{ all: true, limit: 50 }]);
    expect(screen.getByText('run s1')).toBeTruthy();
  });

  it('narrows to closed rows without re-reading on every render', async () => {
    const { list, calls } = listing([page()]);
    await open(list);
    fireEvent.click(screen.getByRole('button', { name: 'Closed' }));

    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({ closed: true, limit: 50 });
    // A re-read loop would keep adding calls after the render settled.
    await act(async () => { await Promise.resolve(); });
    expect(calls).toHaveLength(2);
  });

  it('resolves date presets into instants in the viewer timezone', async () => {
    const { list, calls } = listing([page()]);
    await open(list);

    when('today');
    await waitFor(() => expect(calls).toHaveLength(2));
    const today = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate()).toISOString();
    expect(calls[1]).toEqual({ all: true, limit: 50, since: today });

    when('yesterday');
    await waitFor(() => expect(calls).toHaveLength(3));
    const yesterday = new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 1).toISOString();
    // Half-open: yesterday runs up to, but not into, today.
    expect(calls[2]).toEqual({ all: true, limit: 50, since: yesterday, until: today });

    when('7d');
    await waitFor(() => expect(calls).toHaveLength(4));
    expect(calls[3].since).toBe(new Date(NOW.getFullYear(), NOW.getMonth(), NOW.getDate() - 6).toISOString());
    expect(calls[3].until).toBeUndefined();
  });

  it('counts both ends of a custom range and refuses a backwards one', async () => {
    const { list, calls } = listing([page()]);
    await open(list);

    when('custom');
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-09-01' } });
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-09-03' } });

    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({
      all: true,
      limit: 50,
      since: new Date(2026, 8, 1).toISOString(),
      until: new Date(2026, 8, 4).toISOString(),
    });

    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-08-01' } });
    expect(screen.getByText('The range ends before it starts; swap the two dates')).toBeTruthy();
    // Nothing is asked of the daemon while the range itself is wrong.
    expect(calls).toHaveLength(2);
  });

  it('offers workspace and repository choices from the facets, with names', async () => {
    const { list, calls } = listing([page({
      entries: [entry({ id: 's1' })],
      facets: {
        workspaces: [{ value: 'ws-1', count: 4 }],
        repositories: [{ value: '/Users/victor/projects/attn', count: 7 }],
      },
    })]);
    await open(list, { workspaceNames: { 'ws-1': 'attn' } });

    expect(screen.getByRole('option', { name: 'attn (4)' })).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Repository'), { target: { value: '/Users/victor/projects/attn' } });

    await waitFor(() => expect(calls).toHaveLength(2));
    expect(calls[1]).toEqual({ all: true, limit: 50, repository: '/Users/victor/projects/attn' });
  });
});

describe('SessionsPanel pagination', () => {
  it('loads the next page from the cursor and appends it', async () => {
    const { list, calls } = listing([
      page({ entries: [entry({ id: 's1' })], omitted: 3, next_before: 's1' }),
      page({ entries: [entry({ id: 's2' })], omitted: 0 }),
    ]);
    await open(list);

    expect(screen.getByText('showing 1, 3 older')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));

    await waitFor(() => expect(screen.getByText('run s2')).toBeTruthy());
    expect(calls[1]).toEqual({ all: true, limit: 50, before: 's1' });
    expect(screen.getByText('run s1')).toBeTruthy();
    expect(screen.getByText('showing 2')).toBeTruthy();
    // Nothing left behind the cursor, so the button goes away.
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('does not offer more when the first page is the whole ledger', async () => {
    const { list } = listing([page({ entries: [entry({ id: 's1' })] })]);
    await open(list);

    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
    expect(screen.getByText('showing 1')).toBeTruthy();
  });
});
