import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PresentTour } from '../PresentTour';
import type { PresentTourProps } from '../PresentTour';

// Regression test for a figgyster review finding on PR #498: the diff-fetch
// apply guard in PresentRoot checked only path identity. A late-arriving diff
// response belonging to a STALE round (round-1 -> round-2 via
// presentation_updated, same file path in both) could still get applied even
// though the active round has already moved on. The fix adds a round-identity
// check (`activeRoundKeyRef`) alongside the path check, in the `isStale()`
// guard inside PresentRoot's fetch-all-files effect (ported here unchanged in
// spirit as part of the slice-1 fetch-all rework).
//
// This is exercised via a full useDaemonSocket mock (rather than the
// FakeWebSocket harness in PresentRoot.test.tsx) because the real wire
// protocol correlates `get_file_diff`/`file_diff_result` by path alone
// (`get_file_diff_<path>` in useDaemonSocket.ts) — a second in-flight
// request for the same path overwrites the first's promise handle, so a
// black-box WS simulation can't independently resolve an "old round"
// response after a "new round" request for the same path has already been
// sent. Mocking sendGetFileDiff directly gives each call its own
// independently-controllable promise, isolating exactly the client-side
// guard this test protects.
vi.mock('../PresentTour', () => ({
  PresentTour: vi.fn(({ files }: PresentTourProps) => (
    <div data-testid="present-tour">
      {files.map((f) => (
        <div key={f.path} data-testid={`tour-file-${f.path}`}>
          {f.diff.original !== undefined && <div className="original">{f.diff.original}</div>}
          {f.diff.modified !== undefined && <div className="modified">{f.diff.modified}</div>}
        </div>
      ))}
    </div>
  )),
}));

vi.mock('@tauri-apps/api/window', () => ({
  getCurrentWindow: () => ({ hide: vi.fn() }),
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

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

const roundOne = {
  id: 'round-1',
  presentation_id: 'pres-1',
  seq: 1,
  base_sha: 'aaaaaaaaaaaa',
  head_sha: 'bbbbbbbbbbbb',
  created_at: '2026-07-01T00:00:00Z',
  manifest: {
    title: 'My change',
    summary: 'Adds the thing.',
    files: [{ path: 'src/foo.ts' }],
    skip: [] as string[],
  },
};

const roundTwo = {
  ...roundOne,
  id: 'round-2',
  seq: 2,
  base_sha: 'cccccccccccc',
  head_sha: 'dddddddddddd',
};

type FileDiffResult = { success: true; original: string; modified: string };

let getPresentationRoundCalls: Array<{ resolve: (value: any) => void }>;
let sendGetFileDiffCalls: Array<{ resolve: (value: FileDiffResult) => void }>;
let capturedOnPresentationUpdated: ((p: { id: string }) => void) | undefined;

// Stable across renders (module scope, not re-created per useDaemonSocket()
// call) — PresentRoot's effects depend on these function identities, so a
// fresh vi.fn() per render would re-trigger them every render.
const mockGetPresentationRound = vi.fn(() => {
  const d = deferred<any>();
  getPresentationRoundCalls.push({ resolve: d.resolve });
  return d.promise;
});
const mockSendGetFileDiff = vi.fn(() => {
  const d = deferred<FileDiffResult>();
  sendGetFileDiffCalls.push({ resolve: d.resolve });
  return d.promise;
});

vi.mock('../../hooks/useDaemonSocket', () => ({
  useDaemonSocket: (options: { onPresentationUpdated?: (p: { id: string }) => void }) => {
    capturedOnPresentationUpdated = options.onPresentationUpdated;
    return {
      hasReceivedInitialState: true,
      connectionError: null,
      getPresentationRound: mockGetPresentationRound,
      sendGetFileDiff: mockSendGetFileDiff,
      submitPresentationRound: vi.fn(),
    };
  },
}));

function latestTourProps(): PresentTourProps | undefined {
  const calls = vi.mocked(PresentTour).mock.calls;
  return calls[calls.length - 1]?.[0] as unknown as PresentTourProps | undefined;
}

function foundFile(props: PresentTourProps | undefined) {
  return props?.files.find((f) => f.path === 'src/foo.ts');
}

function setSearch(search: string) {
  window.history.replaceState({}, '', `/?${search}`);
}

describe('PresentRoot diff-fetch round guard', () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    getPresentationRoundCalls = [];
    sendGetFileDiffCalls = [];
    capturedOnPresentationUpdated = undefined;
  });

  it('does not apply a stale round-1 diff response after a round-2 transition for the same file', async () => {
    getPresentationRoundCalls = [];
    sendGetFileDiffCalls = [];

    const { PresentRoot } = await import('./index');

    setSearch('window=present&presentation=pres-1');
    render(<PresentRoot />);

    // Initial load resolves round-1.
    await waitFor(() => expect(getPresentationRoundCalls).toHaveLength(1));
    act(() => {
      getPresentationRoundCalls[0].resolve({
        presentation,
        round: roundOne,
        comments: [],
        repoHeadSha: roundOne.head_sha,
      });
    });
    await waitFor(() => expect(screen.getByText('My presentation')).toBeInTheDocument());

    // The fetch-all effect fires for src/foo.ts under round-1 (call #1);
    // leave it unresolved to simulate a slow response.
    await waitFor(() => expect(sendGetFileDiffCalls).toHaveLength(1));

    // Simulate presentation_updated -> round reloads to round-2, same file
    // path as round-1.
    expect(capturedOnPresentationUpdated).toBeDefined();
    act(() => {
      capturedOnPresentationUpdated!({ id: 'pres-1' });
    });
    await waitFor(() => expect(getPresentationRoundCalls).toHaveLength(2));
    act(() => {
      getPresentationRoundCalls[1].resolve({
        presentation,
        round: roundTwo,
        comments: [],
        repoHeadSha: roundTwo.head_sha,
      });
    });

    // Round-2 load triggers a fresh diff fetch (call #2) for the same path.
    await waitFor(() => expect(sendGetFileDiffCalls).toHaveLength(2));

    // Now the STALE round-1 fetch (call #1) finally resolves. It must NOT
    // be applied, since the active round has already moved to round-2.
    act(() => {
      sendGetFileDiffCalls[0].resolve({
        success: true,
        original: 'STALE round-1 original',
        modified: 'STALE round-1 modified',
      });
    });

    // Give the stale .then() a tick to (incorrectly) apply, if the guard
    // were missing/broken.
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    const staleFile = foundFile(latestTourProps());
    expect(staleFile?.diff.original).not.toBe('STALE round-1 original');
    expect(staleFile?.diff.modified).not.toBe('STALE round-1 modified');

    // The fresh round-2 fetch resolving still applies correctly.
    act(() => {
      sendGetFileDiffCalls[1].resolve({
        success: true,
        original: 'FRESH round-2 original',
        modified: 'FRESH round-2 modified',
      });
    });
    await waitFor(() => {
      expect(foundFile(latestTourProps())?.diff.original).toBe('FRESH round-2 original');
    });
    expect(foundFile(latestTourProps())?.diff.modified).toBe('FRESH round-2 modified');
  });
});
