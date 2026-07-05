import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PresentRoot } from './index';
import { DiffView } from '../DiffView';
import type { DiffViewProps } from '../DiffView';

// DiffView renders the @pierre/diffs custom element (shadow DOM + Shiki),
// which jsdom cannot exercise. These tests cover the reader's data flow
// (file selection, fetching pinned diffs, keyboard nav, comment drafts), not
// diff rendering internals — mirrors the DiffDetailPanel.test.tsx idiom. The
// mock captures its full props so tests can invoke onAddComment etc.
// directly and assert on the comments PresentRoot passes down.
vi.mock('../DiffView', () => ({
  DiffView: vi.fn(({ original, modified }) => (
    <div data-testid="diff-view">
      <div className="original">{original}</div>
      <div className="modified">{modified}</div>
    </div>
  )),
}));

// PresentRoot hides the present window on a successful submit via
// getCurrentWindow().hide() (mirrors the app's Tauri-window close-on-submit
// convention). jsdom has no real Tauri window, so this stands in for it.
const mockHide = vi.fn();
vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => ({ hide: mockHide }),
}));

function latestDiffViewProps(): DiffViewProps {
  const calls = vi.mocked(DiffView).mock.calls;
  const props = calls[calls.length - 1]?.[0];
  if (!props) throw new Error('DiffView has not been rendered yet');
  return props as unknown as DiffViewProps;
}

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

// A generous timeout: under full-suite parallel load these real-timer/microtask
// waits can be slower than testing-library's 1000ms default, independent of
// this component's own logic.
const WAIT_OPTS = { timeout: 5000 };

// Tests that chain several sequential waitFor calls can, in aggregate, exceed
// vitest's 5000ms default per-test timeout under full-suite contention even
// though each individual waitFor stays within WAIT_OPTS. Applied as the third
// `it(...)` argument on those multi-step tests.
const TEST_TIMEOUT = 15000;

async function waitForOpenSocket(): Promise<FakeWebSocket> {
  await waitFor(() => {
    expect(FakeWebSocket.instances.length).toBeGreaterThan(0);
  }, WAIT_OPTS);
  const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
  await waitFor(() => {
    expect(ws.readyState).toBe(FakeWebSocket.OPEN);
  }, WAIT_OPTS);
  return ws;
}

function setSearch(search: string) {
  window.history.replaceState({}, '', `/?${search}`);
}

/** Renders PresentRoot and drives it through a loaded round, returning the socket for further interaction. */
async function loadRound(options?: {
  round?: typeof round;
  repoHeadSha?: string;
  comments?: Array<Record<string, unknown>>;
}): Promise<FakeWebSocket> {
  setSearch('window=present&presentation=pres-1');
  render(<PresentRoot />);

  const ws = await waitForOpenSocket();
  act(() => {
    ws.emit({ event: 'initial_state' });
  });

  await waitFor(() => {
    const sent = ws.sent.map((entry) => JSON.parse(entry));
    expect(sent.some((m) => m.cmd === 'get_presentation_round' && m.presentation_id === 'pres-1')).toBe(true);
  }, WAIT_OPTS);

  act(() => {
    ws.emit({
      event: 'get_presentation_round_result',
      success: true,
      presentation,
      round: options?.round ?? round,
      comments: options?.comments ?? [],
      ...(options?.repoHeadSha !== undefined && { repo_head_sha: options.repoHeadSha }),
    });
  });

  await waitFor(() => {
    expect(screen.getByText('My presentation')).toBeInTheDocument();
  }, WAIT_OPTS);

  return ws;
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
    skip: [] as string[],
  },
};

const roundWithSkip = {
  ...round,
  manifest: {
    ...round.manifest,
    skip: ['src/generated.ts', 'src/vendor.ts'],
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
  let originalSetTimeout: typeof globalThis.setTimeout;
  let originalClearTimeout: typeof globalThis.clearTimeout;
  let pendingTimeouts: Set<ReturnType<typeof globalThis.setTimeout>>;

  beforeEach(() => {
    originalWebSocket = globalThis.WebSocket;
    FakeWebSocket.instances = [];
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket;

    // Track every setTimeout (including useDaemonSocket's reconnect backoff)
    // so afterEach can kill stragglers before the next test's FakeWebSocket
    // gets polluted by a reconnect firing mid-test — mirrors
    // useDaemonSocket.test.tsx's timer-tracking idiom.
    originalSetTimeout = globalThis.setTimeout;
    originalClearTimeout = globalThis.clearTimeout;
    pendingTimeouts = new Set();
    globalThis.setTimeout = ((handler: TimerHandler, timeout?: number, ...args: unknown[]) => {
      let timeoutId: ReturnType<typeof globalThis.setTimeout>;
      timeoutId = originalSetTimeout((...callbackArgs: unknown[]) => {
        pendingTimeouts.delete(timeoutId);
        if (typeof handler === 'function') handler(...callbackArgs);
      }, timeout, ...args);
      pendingTimeouts.add(timeoutId);
      return timeoutId;
    }) as typeof globalThis.setTimeout;
    globalThis.clearTimeout = ((timeoutId?: ReturnType<typeof globalThis.setTimeout>) => {
      if (timeoutId !== undefined) pendingTimeouts.delete(timeoutId);
      return originalClearTimeout(timeoutId);
    }) as typeof globalThis.clearTimeout;

    // Mirrors the static #loading-screen div in index.html that every window
    // boots behind until React takes over.
    const loadingScreen = document.createElement('div');
    loadingScreen.id = 'loading-screen';
    document.body.appendChild(loadingScreen);
  });

  afterEach(() => {
    // Unmount explicitly, while setTimeout is still our tracked wrapper: the
    // socket's onclose-triggered reconnect scheduling runs during unmount, and
    // needs to land in pendingTimeouts below rather than escape as a real,
    // untracked 1000ms timer that later fires mid a subsequent test and
    // injects a stray FakeWebSocket instance (this is what made "moves the
    // selection..."/other later tests intermittently see the wrong socket).
    cleanup();
    for (const timeoutId of pendingTimeouts) {
      originalClearTimeout(timeoutId);
    }
    pendingTimeouts.clear();
    globalThis.setTimeout = originalSetTimeout;
    globalThis.clearTimeout = originalClearTimeout;
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

  it('renders the file list in manifest order with a note marker', async () => {
    await loadRound();

    const paths = screen.getAllByText(/^src\//).map((el) => el.textContent);
    expect(paths).toEqual(['src/foo.ts', 'src/foo.test.ts']);

    const notedItem = screen.getByText('src/foo.ts').closest('li');
    const unnotedItem = screen.getByText('src/foo.test.ts').closest('li');
    expect(notedItem?.querySelector('.present-root-file-note-marker')).not.toBeNull();
    expect(unnotedItem?.querySelector('.present-root-file-note-marker')).toBeNull();
  });

  it('fetches the pinned diff for the initially selected file and shows its note banner', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      const req = sent.find((m) => m.cmd === 'get_file_diff');
      expect(req).toMatchObject({
        directory: '/repo/path',
        path: 'src/foo.ts',
        base_ref: 'a1b2c3d4e5f6',
        head_ref: '00112233445566',
      });
    }, WAIT_OPTS);

    expect(screen.getByText('Core logic')).toBeInTheDocument();

    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: true,
        path: 'src/foo.ts',
        original: 'old content',
        modified: 'new content',
      });
    });

    await waitFor(() => {
      expect(screen.getByTestId('diff-view')).toBeInTheDocument();
    }, WAIT_OPTS);
    expect(screen.getByText('old content')).toBeInTheDocument();
    expect(screen.getByText('new content')).toBeInTheDocument();
  });

  it('fetches the pinned diff for a clicked file', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.some((m) => m.cmd === 'get_file_diff' && m.path === 'src/foo.ts')).toBe(true);
    }, WAIT_OPTS);

    fireEvent.click(screen.getByText('src/foo.test.ts'));

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      const req = sent.find((m) => m.cmd === 'get_file_diff' && m.path === 'src/foo.test.ts');
      expect(req).toMatchObject({
        base_ref: 'a1b2c3d4e5f6',
        head_ref: '00112233445566',
      });
    }, WAIT_OPTS);
  });

  it('moves the selection with j/k keyboard shortcuts', async () => {
    await loadRound();

    await waitFor(() => {
      expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('selected');
    }, WAIT_OPTS);

    fireEvent.keyDown(window, { key: 'j' });
    await waitFor(() => {
      expect(screen.getByText('src/foo.test.ts').closest('li')).toHaveClass('selected');
    }, WAIT_OPTS);

    fireEvent.keyDown(window, { key: 'k' });
    await waitFor(() => {
      expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('selected');
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('shows a drift banner iff repoHeadSha differs from the pinned round head', async () => {
    await loadRound({ repoHeadSha: 'deadbeef000000' });

    expect(screen.getByText(/repo has moved on/)).toBeInTheDocument();
  });

  it('shows no drift banner when repoHeadSha matches the pinned round head', async () => {
    await loadRound({ repoHeadSha: round.head_sha });

    expect(screen.queryByText(/repo has moved on/)).not.toBeInTheDocument();
  });

  it('shows an inline error when the diff fetch fails, without blanking the window', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.some((m) => m.cmd === 'get_file_diff')).toBe(true);
    }, WAIT_OPTS);

    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: false,
        path: 'src/foo.ts',
        error: 'git show failed',
      });
    });

    await waitFor(() => {
      expect(screen.getByText('git show failed')).toBeInTheDocument();
    }, WAIT_OPTS);
    // The window itself stays rendered — not replaced by a full-page error.
    expect(screen.getByText('My presentation')).toBeInTheDocument();
  });

  it('lists skipped files dimmed and non-clickable under a Skipped divider', async () => {
    await loadRound({ round: roundWithSkip });

    expect(screen.getByText('Skipped')).toBeInTheDocument();
    expect(screen.getByText('src/generated.ts')).toBeInTheDocument();
    expect(screen.getByText('src/vendor.ts')).toBeInTheDocument();
    expect(screen.getByText('src/generated.ts').closest('li')).toHaveClass('present-root-file-skipped');
  });

  async function loadRoundWithDiff(options?: {
    comments?: Array<Record<string, unknown>>;
  }): Promise<FakeWebSocket> {
    const ws = await loadRound({ comments: options?.comments });
    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: true,
        path: 'src/foo.ts',
        original: 'old content',
        modified: 'new content',
      });
    });
    await waitFor(() => {
      expect(screen.getByTestId('diff-view')).toBeInTheDocument();
    }, WAIT_OPTS);
    return ws;
  }

  it('surfaces a locally-added draft in the comments passed to DiffView', async () => {
    await loadRoundWithDiff();

    await act(async () => {
      await latestDiffViewProps().onAddComment(3, 5, 'looks off');
    });

    await waitFor(() => {
      const comments = latestDiffViewProps().comments;
      expect(comments).toHaveLength(1);
      expect(comments[0]).toMatchObject({ line_start: 3, line_end: 5, content: 'looks off' });
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('marks submitted comments read-only in DiffView while leaving drafts editable', async () => {
    await loadRoundWithDiff({
      comments: [
        {
          id: 'submitted-1',
          content: 'from a prior round',
          filepath: 'src/foo.ts',
          line_start: 2,
          line_end: 2,
          side: 'new',
          author: 'user',
          created_at: '2026-07-01T00:00:00Z',
          round_id: 'round-0',
        },
      ],
    });

    await act(async () => {
      await latestDiffViewProps().onAddComment(3, 5, 'looks off');
    });

    await waitFor(() => {
      const props = latestDiffViewProps();
      const comments = props.comments;
      expect(comments.some((c) => c.id === 'submitted-1')).toBe(true);
      expect(comments.some((c) => c.content === 'looks off')).toBe(true);

      const readOnlyIds = props.readOnlyCommentIds;
      expect(readOnlyIds?.has('submitted-1')).toBe(true);
      const draftComment = comments.find((c) => c.content === 'looks off');
      expect(readOnlyIds?.has(draftComment!.id)).toBe(false);
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('round-trips an old-side (negative line_end) draft through the signed convention', async () => {
    await loadRoundWithDiff();

    await act(async () => {
      await latestDiffViewProps().onAddComment(10, -12, 'stale comment');
    });

    await waitFor(() => {
      const comments = latestDiffViewProps().comments;
      const draft = comments.find((c) => c.content === 'stale comment');
      expect(draft).toMatchObject({ line_start: 10, line_end: -12 });
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('sends the correct wire shape when submitting drafts', async () => {
    const ws = await loadRoundWithDiff();

    await act(async () => {
      await latestDiffViewProps().onAddComment(3, 5, 'new-side comment');
    });
    await act(async () => {
      await latestDiffViewProps().onAddComment(10, -12, 'old-side comment');
    });

    fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      const req = sent.find((m) => m.cmd === 'present_submit_round');
      expect(req).toBeDefined();
      expect(req.round_id).toBe('round-1');
      expect(req.handback).toBe(true);
      expect(req.comments).toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            filepath: 'src/foo.ts',
            line_start: 3,
            line_end: 5,
            side: 'new',
            content: 'new-side comment',
          }),
          expect.objectContaining({
            filepath: 'src/foo.ts',
            line_start: 10,
            line_end: 12,
            side: 'old',
            content: 'old-side comment',
          }),
        ])
      );
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('clears drafts and refetches the round after a successful submit', async () => {
    const ws = await loadRoundWithDiff();

    await act(async () => {
      await latestDiffViewProps().onAddComment(3, 5, 'looks off');
    });

    fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() => {
      expect(ws.sent.map((e) => JSON.parse(e)).some((m) => m.cmd === 'present_submit_round')).toBe(true);
    }, WAIT_OPTS);

    act(() => {
      ws.emit({ event: 'present_submit_round_result', success: true, round_id: 'round-1' });
    });

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    }, WAIT_OPTS);
    expect(screen.getByText('Submit review')).toBeInTheDocument();

    // A successful submit bumps refreshSignal, which re-fetches the round.
    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      const refetches = sent.filter((m) => m.cmd === 'get_presentation_round');
      expect(refetches.length).toBeGreaterThanOrEqual(2);
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('hides the presentation window after a successful submit', async () => {
    const ws = await loadRoundWithDiff();

    fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() => {
      expect(ws.sent.map((e) => JSON.parse(e)).some((m) => m.cmd === 'present_submit_round')).toBe(true);
    }, WAIT_OPTS);

    act(() => {
      ws.emit({ event: 'present_submit_round_result', success: true, round_id: 'round-1' });
    });

    await waitFor(() => {
      expect(mockHide).toHaveBeenCalledTimes(1);
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('keeps drafts and shows an inline error when submit fails', async () => {
    const ws = await loadRoundWithDiff();

    await act(async () => {
      await latestDiffViewProps().onAddComment(3, 5, 'looks off');
    });

    fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() => {
      expect(ws.sent.map((e) => JSON.parse(e)).some((m) => m.cmd === 'present_submit_round')).toBe(true);
    }, WAIT_OPTS);

    act(() => {
      ws.emit({ event: 'present_submit_round_result', success: false, error: 'daemon unreachable' });
    });

    await waitFor(() => {
      expect(screen.getByText('daemon unreachable')).toBeInTheDocument();
    }, WAIT_OPTS);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(latestDiffViewProps().comments.some((c) => c.content === 'looks off')).toBe(true);
  }, TEST_TIMEOUT);

  it('keeps drafts made on one file when switching to another and back', async () => {
    const ws = await loadRoundWithDiff();

    await act(async () => {
      await latestDiffViewProps().onAddComment(3, 5, 'on foo.ts');
    });
    await waitFor(() => {
      expect(latestDiffViewProps().comments.some((c) => c.content === 'on foo.ts')).toBe(true);
    }, WAIT_OPTS);

    fireEvent.click(screen.getByText('src/foo.test.ts'));
    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: true,
        path: 'src/foo.test.ts',
        original: 'test old',
        modified: 'test new',
      });
    });
    await waitFor(() => {
      expect(latestDiffViewProps().comments.some((c) => c.content === 'on foo.ts')).toBe(false);
    }, WAIT_OPTS);

    fireEvent.click(screen.getByText('src/foo.ts'));
    await waitFor(() => {
      expect(latestDiffViewProps().comments.some((c) => c.content === 'on foo.ts')).toBe(true);
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);
});
