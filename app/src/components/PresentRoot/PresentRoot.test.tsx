import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PresentRoot } from './index';
import { PresentTour } from '../PresentTour';
import type { PresentTourProps } from '../PresentTour';

// PresentTour renders the @pierre/diffs CodeView (shadow DOM + Shiki + a real
// virtualized scroll container), which jsdom cannot exercise — real browser
// coverage for the tour itself lives in the Playwright component harness
// (see app/test-harness). These tests cover PresentRoot's data flow: fetching
// every manifest file's diff up front, keyboard/rail navigation, and the
// comment-draft lifecycle — mirrors the DiffDetailPanel.test.tsx idiom. The
// mock captures its full props so tests can invoke onAddComment etc. directly
// and assert on what PresentRoot passes down.
vi.mock('../PresentTour', () => ({
  PresentTour: vi.fn(({ summary, files, reviewedPaths, onToggleReviewed }: PresentTourProps) => (
    <div data-testid="present-tour">
      {summary && <div data-testid="present-tour-summary">{summary}</div>}
      {files.map((f) => (
        <div key={f.path} data-testid={`tour-file-${f.path}`}>
          {f.note && <div className="note">{f.note}</div>}
          {f.diff.loading && <span className="loading">loading</span>}
          {f.diff.error && <span className="error">{f.diff.error}</span>}
          {f.diff.original !== undefined && <div className="original">{f.diff.original}</div>}
          {f.diff.modified !== undefined && <div className="modified">{f.diff.modified}</div>}
          <span className="reviewed-state">{reviewedPaths.has(f.path) ? 'reviewed' : 'unreviewed'}</span>
          <button type="button" onClick={() => onToggleReviewed(f.path)}>
            toggle-reviewed-{f.path}
          </button>
        </div>
      ))}
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

function latestTourProps(): PresentTourProps {
  const calls = vi.mocked(PresentTour).mock.calls;
  const props = calls[calls.length - 1]?.[0];
  if (!props) throw new Error('PresentTour has not been rendered yet');
  return props as unknown as PresentTourProps;
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

/** The `request_id` of the most recently sent `get_file_diff` for `path`. */
function latestFileDiffRequestId(ws: FakeWebSocket, path: string): string {
  const sent = ws.sent.map((entry) => JSON.parse(entry)).filter((m) => m.cmd === 'get_file_diff' && m.path === path);
  const last = sent[sent.length - 1];
  if (!last?.request_id) throw new Error(`no get_file_diff request_id found for ${path}`);
  return last.request_id;
}

/**
 * Resolves a `get_file_diff` for `path` with the given content, echoing the
 * most recently sent request's id back — as the real daemon does. Results are
 * correlated by request_id only (see `file_diff_result` in useDaemonSocket.ts),
 * so this must echo a real id or the promise never resolves.
 */
function emitFileDiff(ws: FakeWebSocket, path: string, original: string, modified: string) {
  const requestId = latestFileDiffRequestId(ws, path);
  act(() => {
    ws.emit({ event: 'file_diff_result', success: true, path, request_id: requestId, original, modified });
  });
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

    // usePresentReviewedMarks persists to localStorage; isolate every test.
    window.localStorage.clear();
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

  it('fetches every manifest file’s diff up front, exactly once per round', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry)).filter((m) => m.cmd === 'get_file_diff');
      expect(sent.map((m) => m.path).sort()).toEqual(['src/foo.test.ts', 'src/foo.ts']);
      for (const m of sent) {
        expect(m).toMatchObject({
          directory: '/repo/path',
          base_ref: 'a1b2c3d4e5f6',
          head_ref: '00112233445566',
        });
      }
    }, WAIT_OPTS);

    expect(latestTourProps().summary).toBe('Adds the thing.');
    expect(screen.getByTestId('tour-file-src/foo.ts').textContent).toContain('Core logic');

    emitFileDiff(ws, 'src/foo.ts', 'old content', 'new content');
    emitFileDiff(ws, 'src/foo.test.ts', 'test old', 'test new');

    await waitFor(() => {
      expect(screen.getByTestId('tour-file-src/foo.ts').textContent).toContain('old content');
      expect(screen.getByTestId('tour-file-src/foo.test.ts').textContent).toContain('test old');
    }, WAIT_OPTS);

    // No further get_file_diff calls fire from re-renders (comment drafts,
    // rail clicks, etc.) — the fetch-all effect must not loop.
    await act(async () => {
      await Promise.resolve();
    });
    const finalCount = ws.sent.filter((entry) => JSON.parse(entry).cmd === 'get_file_diff').length;
    expect(finalCount).toBe(2);
  });

  it('clicking a rail file makes it the active/highlighted file without refetching', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry)).filter((m) => m.cmd === 'get_file_diff');
      expect(sent).toHaveLength(2);
    }, WAIT_OPTS);

    fireEvent.click(screen.getByText('src/foo.test.ts'));

    await waitFor(() => {
      expect(screen.getByText('src/foo.test.ts').closest('li')).toHaveClass('selected');
    }, WAIT_OPTS);

    const sentAfter = ws.sent.map((entry) => JSON.parse(entry)).filter((m) => m.cmd === 'get_file_diff');
    expect(sentAfter).toHaveLength(2);
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

  it('shows an inline error when a diff fetch fails, without blanking the window', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      const sent = ws.sent.map((entry) => JSON.parse(entry));
      expect(sent.some((m) => m.cmd === 'get_file_diff' && m.path === 'src/foo.ts')).toBe(true);
    }, WAIT_OPTS);

    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: false,
        path: 'src/foo.ts',
        request_id: latestFileDiffRequestId(ws, 'src/foo.ts'),
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
    emitFileDiff(ws, 'src/foo.ts', 'old content', 'new content');
    emitFileDiff(ws, 'src/foo.test.ts', 'test old', 'test new');
    await waitFor(() => {
      expect(screen.getByTestId('tour-file-src/foo.ts').textContent).toContain('old content');
    }, WAIT_OPTS);
    return ws;
  }

  it('surfaces a locally-added draft in the comments passed to the tour', async () => {
    await loadRoundWithDiff();

    await act(async () => {
      latestTourProps().onAddComment('src/foo.ts', 3, 5, 'looks off');
    });

    await waitFor(() => {
      const comments = latestTourProps().comments;
      expect(comments).toHaveLength(1);
      expect(comments[0]).toMatchObject({ filepath: 'src/foo.ts', line_start: 3, line_end: 5, content: 'looks off' });
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('marks submitted comments read-only while leaving drafts editable', async () => {
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
      latestTourProps().onAddComment('src/foo.ts', 3, 5, 'looks off');
    });

    await waitFor(() => {
      const props = latestTourProps();
      const comments = props.comments;
      expect(comments.some((c) => c.id === 'submitted-1')).toBe(true);
      expect(comments.some((c) => c.content === 'looks off')).toBe(true);

      const readOnlyIds = props.readOnlyCommentIds;
      expect(readOnlyIds.has('submitted-1')).toBe(true);
      const draftComment = comments.find((c) => c.content === 'looks off');
      expect(readOnlyIds.has(draftComment!.id)).toBe(false);
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('round-trips an old-side (negative line_end) draft through the signed convention', async () => {
    await loadRoundWithDiff();

    await act(async () => {
      latestTourProps().onAddComment('src/foo.ts', 10, -12, 'stale comment');
    });

    await waitFor(() => {
      const comments = latestTourProps().comments;
      const draft = comments.find((c) => c.content === 'stale comment');
      expect(draft).toMatchObject({ filepath: 'src/foo.ts', line_start: 10, line_end: -12 });
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  it('sends the correct wire shape when submitting drafts', async () => {
    const ws = await loadRoundWithDiff();

    await act(async () => {
      latestTourProps().onAddComment('src/foo.ts', 3, 5, 'new-side comment');
    });
    await act(async () => {
      latestTourProps().onAddComment('src/foo.ts', 10, -12, 'old-side comment');
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
      latestTourProps().onAddComment('src/foo.ts', 3, 5, 'looks off');
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
      latestTourProps().onAddComment('src/foo.ts', 3, 5, 'looks off');
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
    expect(latestTourProps().comments.some((c) => c.content === 'looks off')).toBe(true);
  }, TEST_TIMEOUT);

  it('keeps a draft on one file visible in the tour after navigating to another file', async () => {
    const ws = await loadRoundWithDiff();

    await act(async () => {
      latestTourProps().onAddComment('src/foo.ts', 3, 5, 'on foo.ts');
    });
    await waitFor(() => {
      expect(latestTourProps().comments.some((c) => c.content === 'on foo.ts')).toBe(true);
    }, WAIT_OPTS);

    fireEvent.click(screen.getByText('src/foo.test.ts'));

    // Unlike the old single-selection pane, the tour renders every file at
    // once — navigating the rail must not drop comments on other files.
    expect(latestTourProps().comments.some((c) => c.content === 'on foo.ts')).toBe(true);
    void ws;
  }, TEST_TIMEOUT);

  // Wire-level counterpart to PresentRoot.roundGuard.test.tsx's mocked-hook
  // version: this drives the REAL useDaemonSocket against a FakeWebSocket, so
  // it also covers the request_id correlation fix in useDaemonSocket.ts
  // itself (get_file_diff/file_diff_result used to correlate by path alone,
  // so a second in-flight request for the same path clobbered the first's
  // pending promise, and a stale round's late reply could resolve the new
  // round's promise with the wrong content).
  it('does not apply a stale round’s late file_diff_result to a newer round for the same path', async () => {
    const ws = await loadRound();

    await waitFor(() => {
      expect(ws.sent.map((e) => JSON.parse(e)).some((m) => m.cmd === 'get_file_diff' && m.path === 'src/foo.ts')).toBe(true);
    }, WAIT_OPTS);
    const round1RequestId = latestFileDiffRequestId(ws, 'src/foo.ts');

    // Leave round-1's src/foo.ts request unresolved and transition to round-2
    // via presentation_updated (same file path in both rounds).
    act(() => {
      ws.emit({ event: 'presentation_updated', presentation: { id: 'pres-1' } });
    });

    await waitFor(() => {
      const refetches = ws.sent.map((e) => JSON.parse(e)).filter((m) => m.cmd === 'get_presentation_round');
      expect(refetches.length).toBeGreaterThanOrEqual(2);
    }, WAIT_OPTS);

    const round2 = { ...round, id: 'round-2', seq: 2, base_sha: 'fedcba098765', head_sha: '998877665544' };
    act(() => {
      ws.emit({
        event: 'get_presentation_round_result',
        success: true,
        presentation,
        round: round2,
        comments: [],
      });
    });

    await waitFor(() => {
      const sent = ws.sent.map((e) => JSON.parse(e)).filter((m) => m.cmd === 'get_file_diff' && m.path === 'src/foo.ts');
      expect(sent).toHaveLength(2);
    }, WAIT_OPTS);
    const round2RequestId = latestFileDiffRequestId(ws, 'src/foo.ts');
    expect(round2RequestId).not.toBe(round1RequestId);

    // The stale round-1 reply arrives late, echoing round-1's own request id.
    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: true,
        path: 'src/foo.ts',
        request_id: round1RequestId,
        original: 'STALE round-1 original',
        modified: 'STALE round-1 modified',
      });
    });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(screen.getByTestId('tour-file-src/foo.ts').textContent).not.toContain('STALE round-1');

    // Round-2's own reply, echoing round-2's request id, applies normally.
    act(() => {
      ws.emit({
        event: 'file_diff_result',
        success: true,
        path: 'src/foo.ts',
        request_id: round2RequestId,
        original: 'FRESH round-2 original',
        modified: 'FRESH round-2 modified',
      });
    });
    await waitFor(() => {
      expect(screen.getByTestId('tour-file-src/foo.ts').textContent).toContain('FRESH round-2 original');
    }, WAIT_OPTS);
  }, TEST_TIMEOUT);

  describe('review progress + keyboard model', () => {
    it('toggling reviewed from the tour updates the rail count and row styling', async () => {
      await loadRound();

      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');

      fireEvent.click(screen.getByText('toggle-reviewed-src/foo.ts'));

      await waitFor(() => {
        expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      }, WAIT_OPTS);
      expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('reviewed');
      expect(screen.getByTestId('tour-file-src/foo.ts').querySelector('.reviewed-state')?.textContent).toBe(
        'reviewed'
      );
    });

    it('r toggles reviewed on the active file', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 'r' });
      await waitFor(() => {
        expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      }, WAIT_OPTS);
      expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('reviewed');

      fireEvent.keyDown(window, { key: 'r' });
      await waitFor(() => {
        expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');
      }, WAIT_OPTS);
    });

    it('j marks the file being left as reviewed (auto-mark-on-leave), k never marks', async () => {
      await loadRound();

      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');

      fireEvent.keyDown(window, { key: 'j' });
      await waitFor(() => {
        expect(screen.getByText('src/foo.test.ts').closest('li')).toHaveClass('selected');
      }, WAIT_OPTS);
      // Leaving src/foo.ts via j marks it reviewed; arriving at src/foo.test.ts does not.
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('reviewed');
      expect(screen.getByText('src/foo.test.ts').closest('li')).not.toHaveClass('reviewed');

      fireEvent.keyDown(window, { key: 'k' });
      await waitFor(() => {
        expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('selected');
      }, WAIT_OPTS);
      // k never marks anything, and the earlier j-mark on foo.ts persists.
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
    });

    it('does not intercept single-letter shortcuts while typing in a comment textarea', async () => {
      await loadRound();

      const input = document.createElement('textarea');
      document.body.appendChild(input);
      input.focus();

      fireEvent.keyDown(input, { key: 'r' });
      fireEvent.keyDown(input, { key: 'j' });
      fireEvent.keyDown(input, { key: 's' });

      // Nothing moved, nothing got marked, and the submit dialog never opened.
      expect(screen.getByText('src/foo.ts').closest('li')).toHaveClass('selected');
      expect(screen.getByTestId('present-root-rail-count').textContent).toBe('0/2');
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

      document.body.removeChild(input);
    });

    it('s opens the submit dialog', async () => {
      await loadRound();

      fireEvent.keyDown(window, { key: 's' });
      await waitFor(() => {
        expect(screen.getByRole('dialog')).toBeInTheDocument();
      }, WAIT_OPTS);
    });

    it('shows an advisory, non-blocking coverage line in the submit dialog for unreviewed files', async () => {
      await loadRound();

      fireEvent.click(screen.getByText('toggle-reviewed-src/foo.ts'));
      await waitFor(() => {
        expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      }, WAIT_OPTS);

      fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));

      await waitFor(() => {
        expect(screen.getByTestId('present-root-submit-coverage').textContent).toContain('src/foo.test.ts');
      }, WAIT_OPTS);
      // Submit stays enabled — coverage is advisory only, never a gate.
      expect(screen.getByRole('button', { name: 'Submit' })).not.toBeDisabled();
    });

    it('shows no coverage line once every file is reviewed', async () => {
      await loadRound();

      fireEvent.click(screen.getByText('toggle-reviewed-src/foo.ts'));
      fireEvent.click(screen.getByText('toggle-reviewed-src/foo.test.ts'));
      await waitFor(() => {
        expect(screen.getByTestId('present-root-rail-count').textContent).toBe('2/2');
      }, WAIT_OPTS);

      fireEvent.click(screen.getByRole('button', { name: /Submit review/ }));

      expect(screen.queryByTestId('present-root-submit-coverage')).not.toBeInTheDocument();
    });

    it('persists reviewed marks in localStorage scoped to the presentation and round', async () => {
      await loadRound();

      fireEvent.click(screen.getByText('toggle-reviewed-src/foo.ts'));
      await waitFor(() => {
        expect(screen.getByTestId('present-root-rail-count').textContent).toBe('1/2');
      }, WAIT_OPTS);

      const raw = window.localStorage.getItem('attn.present.reviewed.pres-1.round-1');
      expect(JSON.parse(raw!)).toEqual(['src/foo.ts']);
    });
  });
});
