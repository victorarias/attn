import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { PROTOCOL_VERSION, useDaemonSocket } from './useDaemonSocket';

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  sent: string[] = [];

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
    queueMicrotask(() => {
      this.readyState = FakeWebSocket.OPEN;
      this.onopen?.(new Event('open'));
    });
  }

  send(data: string) {
    this.sent.push(data);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.(new CloseEvent('close'));
  }

  emit(data: unknown) {
    this.onmessage?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

async function waitForOpenSocket(): Promise<FakeWebSocket> {
  await waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  });
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  });
  return ws;
}

function seed(id: string, title: string) {
  return {
    id,
    title,
    body: '',
    status: 'planted',
    step_slug: title,
    workspace_id: 'ws-1',
    planter_session: '',
    planter_member: '',
    tender_session: '',
    tender_member: '',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    rev: 1,
    created_at: '2026-08-12T10:00:00Z',
    updated_at: '2026-08-12T10:00:00Z',
  };
}

// Slice 1's whole contract is that a seed planted from a terminal shows up
// without the user doing anything. The panel holds no fetch, so the push is the
// entire mechanism: if the socket drops garden_seeds_updated, the garden looks
// empty until the app is restarted and nothing anywhere says why.
describe('useDaemonSocket garden', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(false);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    vi.clearAllMocks();
  });

  async function renderWithGarden(initialSeeds?: unknown[]) {
    const onSeedsUpdate = vi.fn();
    renderHook(() =>
      useDaemonSocket({
        onSessionsUpdate: vi.fn(),
        onWorkspacesUpdate: vi.fn(),
        onPRsUpdate: vi.fn(),
        onReposUpdate: vi.fn(),
        onAuthorsUpdate: vi.fn(),
        onSeedsUpdate,
        wsUrl: 'ws://localhost:9999/ws',
      }),
    );
    const ws = await waitForOpenSocket();
    act(() => {
      ws.emit({
        event: 'initial_state',
        protocol_version: PROTOCOL_VERSION,
        sessions: [],
        workspaces: [],
        prs: [],
        repos: [],
        authors: [],
        settings: {},
        ...(initialSeeds ? { seeds: initialSeeds } : {}),
      });
    });
    return { ws, onSeedsUpdate };
  }

  it('seeds the garden from initial_state', async () => {
    const { onSeedsUpdate } = await renderWithGarden([seed('s-aaa111', 'already planted')]);

    expect(onSeedsUpdate).toHaveBeenCalledWith(
      [expect.objectContaining({ id: 's-aaa111', title: 'already planted' })],
      1,
    );
  });

  it('replaces the garden on every planting broadcast', async () => {
    const { ws, onSeedsUpdate } = await renderWithGarden([seed('s-aaa111', 'already planted')]);

    act(() => {
      ws.emit({
        event: 'garden_seeds_updated',
        seeds: [seed('s-bbb222', 'just planted'), seed('s-aaa111', 'already planted')],
        total: 2,
      });
    });

    expect(onSeedsUpdate).toHaveBeenLastCalledWith(
      [expect.objectContaining({ id: 's-bbb222' }), expect.objectContaining({ id: 's-aaa111' })],
      2,
    );
  });

  // An outpost has no garden, and a daemon older than the app sends no seeds at
  // all. Both must read as an empty garden rather than leaving the panel showing
  // a garden from a previous connection.
  it('reads a garden-less daemon as an empty garden', async () => {
    const { onSeedsUpdate } = await renderWithGarden();

    expect(onSeedsUpdate).toHaveBeenCalledWith([], 0);
  });

  // The push is bounded. When the garden outgrows one push the total is what
  // keeps the shortfall visible, so it has to survive the trip: a callback that
  // drops it turns a truncated list back into a silent one.
  it('carries how many seeds the garden holds, not just the ones it sent', async () => {
    const { ws, onSeedsUpdate } = await renderWithGarden();

    act(() => {
      ws.emit({
        event: 'garden_seeds_updated',
        seeds: [seed('s-bbb222', 'the newest one')],
        total: 1421,
      });
    });

    expect(onSeedsUpdate).toHaveBeenLastCalledWith(
      [expect.objectContaining({ id: 's-bbb222' })],
      1421,
    );
  });

  // A daemon a version behind sends no total at all. Reading that as zero would
  // make the panel claim the garden is smaller than the list it is showing.
  it('reads a total-less push as exactly what it sent', async () => {
    const { ws, onSeedsUpdate } = await renderWithGarden();

    act(() => {
      ws.emit({ event: 'garden_seeds_updated', seeds: [seed('s-ccc333', 'no total here')] });
    });

    expect(onSeedsUpdate).toHaveBeenLastCalledWith(
      [expect.objectContaining({ id: 's-ccc333' })],
      1,
    );
  });
});
