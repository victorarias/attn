import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isTauri } from '@tauri-apps/api/core';
import { PresentRoot } from './index';

// PresentRoot owns its own useDaemonSocket connection (it is a standalone
// Tauri window, not a component fed daemon functions via props), so
// createMockDaemon/setupDefaultResponses (the DiffDetailPanel-style idiom)
// don't apply here — there's no daemon-function prop surface to inject into.
// Instead this mirrors useDaemonSocket.test.tsx's FakeWebSocket, which is the
// sibling test for a component that talks to a real WebSocket.
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

function setSearch(search: string) {
  window.history.replaceState({}, '', `/?${search}`);
}

const round = {
  id: 'round-1',
  presentation_id: 'pres-1',
  seq: 1,
  base_sha: 'a1b2c3d4e5f6',
  head_sha: '00112233445566',
  created_at: '2026-07-01T00:00:00Z',
  manifest: {
    title: 'My change',
    summary: 'Adds the thing.',
    files: [
      { path: 'src/foo.ts', note: 'Core logic' },
      { path: 'src/foo.test.ts' },
    ],
    skip: [],
  },
};

const presentation = {
  id: 'pres-1',
  created_at: '2026-07-01T00:00:00Z',
  kind: 'pr',
  latest_round_seq: 1,
  latest_round_submitted: false,
  repo_path: '/repo/path',
  session_id: 'session-1',
  status: 'open',
  title: 'My presentation',
};

describe('PresentRoot', () => {
  let originalWebSocket: typeof WebSocket;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;
    vi.mocked(isTauri).mockReturnValue(true);

    // Mirrors the static #loading-screen div in index.html that every window
    // boots behind until React takes over.
    const loadingScreen = document.createElement('div');
    loadingScreen.id = 'loading-screen';
    document.body.appendChild(loadingScreen);
  });

  afterEach(() => {
    globalThis.WebSocket = originalWebSocket;
    document.getElementById('loading-screen')?.remove();
    vi.clearAllMocks();
  });

  it('hides the boot splash on mount, even before any data has loaded', async () => {
    setSearch('window=present&presentation=pres-1');
    render(<PresentRoot />);

    await waitFor(() => {
      expect(document.getElementById('loading-screen')).toHaveClass('hidden');
    });
  });

  it('renders round info from a get_presentation_round result', async () => {
    setSearch('window=present&presentation=pres-1');
    render(<PresentRoot />);

    const ws = await waitForOpenSocket();
    act(() => {
      ws.emit({ event: 'initial_state' });
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.some((m) => m.cmd === 'get_presentation_round' && m.presentation_id === 'pres-1')).toBe(true);
    });

    act(() => {
      ws.emit({
        event: 'get_presentation_round_result',
        success: true,
        presentation,
        round,
        comments: [],
      });
    });

    await waitFor(() => {
      expect(screen.getByText('My presentation')).toBeInTheDocument();
    });
    expect(screen.getByText('Adds the thing.')).toBeInTheDocument();
    expect(screen.getByText('src/foo.ts')).toBeInTheDocument();
    expect(screen.getByText('Core logic')).toBeInTheDocument();
    expect(screen.getByText(/Round 1/)).toBeInTheDocument();
    expect(screen.getByText('a1b2c3d…0011223')).toBeInTheDocument();
  });

  it('shows an error state for an unknown presentation id', async () => {
    setSearch('window=present&presentation=missing-id');
    render(<PresentRoot />);

    const ws = await waitForOpenSocket();
    act(() => {
      ws.emit({ event: 'initial_state' });
    });

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.some((m) => m.cmd === 'get_presentation_round')).toBe(true);
    });

    act(() => {
      ws.emit({
        event: 'get_presentation_round_result',
        success: false,
        error: 'presentation not found',
      });
    });

    await waitFor(() => {
      expect(screen.getByText('presentation not found')).toBeInTheDocument();
    });
  });

  it('shows an error state when no presentation id is given', async () => {
    setSearch('window=present');
    render(<PresentRoot />);

    await waitFor(() => {
      expect(screen.getByText('No presentation specified.')).toBeInTheDocument();
    });
  });
});
