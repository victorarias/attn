import { render, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { NotebookTile } from './NotebookTile';
import {
  NotebookSurfaceProvider,
  type NotebookSurfaceContextValue,
  type NotebookSurfaceDaemon,
} from '../../contexts/NotebookSurfaceContext';
import type { FsWatchResult } from '../../hooks/useDaemonSocket';

// NotebookSurface itself (CodeMirror-backed tree/editor/finder) is covered by
// NotebookBrowser.test.tsx; here we only need to observe what NotebookTile
// hands it, so stub it to a thin recorder.
const surfaceCalls = vi.hoisted(() => [] as Array<{ changeSignal?: number }>);
vi.mock('../NotebookSurface', () => ({
  NotebookSurface: (props: { changeSignal?: number }) => {
    surfaceCalls.push(props);
    return <div data-testid="notebook-surface" data-change-signal={props.changeSignal} />;
  },
}));

function fakeDaemon(changeSignal = 0): NotebookSurfaceDaemon {
  return {
    listDir: vi.fn().mockResolvedValue([]),
    readFile: vi.fn(),
    writeFile: vi.fn(),
    existsFile: vi.fn(),
    readAsset: vi.fn(),
    backlinksNotebook: vi.fn(),
    sendToChief: vi.fn(),
    listFiles: vi.fn(),
    changeSignal,
  };
}

interface Harness {
  value: NotebookSurfaceContextValue;
  makeDaemon: ReturnType<typeof vi.fn>;
  sendFsWatch: ReturnType<typeof vi.fn>;
  sendFsUnwatch: ReturnType<typeof vi.fn>;
  callLog: string[];
}

function makeHarness(opts: {
  effectiveNotebookRoot?: string;
  watchResolvesTo?: (root: string) => string;
  watchRejects?: boolean;
  connectionGeneration?: number;
} = {}): Harness {
  const callLog: string[] = [];
  const resolveRoot = opts.watchResolvesTo ?? ((root: string) => root);
  const makeDaemon = vi.fn((_root?: string) => fakeDaemon());
  const sendFsWatch = vi.fn((root?: string): Promise<FsWatchResult> => {
    callLog.push(`watch:${root}`);
    if (opts.watchRejects) {
      return Promise.reject(new Error('watch cap reached'));
    }
    return Promise.resolve({ root: resolveRoot(root || '') });
  });
  const sendFsUnwatch = vi.fn((root?: string): Promise<FsWatchResult> => {
    callLog.push(`unwatch:${root}`);
    return Promise.resolve({ root: root || '' });
  });
  const value: NotebookSurfaceContextValue = {
    makeDaemon,
    effectiveNotebookRoot: opts.effectiveNotebookRoot ?? '/notebook-root',
    sendFsWatch,
    sendFsUnwatch,
    connectionGeneration: opts.connectionGeneration ?? 0,
  };
  return { value, makeDaemon, sendFsWatch, sendFsUnwatch, callLog };
}

afterEach(() => {
  surfaceCalls.length = 0;
  vi.restoreAllMocks();
});

describe('NotebookTile root-bound daemon + watch lifecycle', () => {
  it('binds the daemon via makeDaemon(root) and never watches a rootless (notebook-rooted) tile', async () => {
    const harness = makeHarness();
    render(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath="a.md" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );

    await waitFor(() => expect(surfaceCalls.length).toBeGreaterThan(0));
    expect(harness.makeDaemon).toHaveBeenCalledWith(undefined);
    expect(harness.sendFsWatch).not.toHaveBeenCalled();
    expect(harness.sendFsUnwatch).not.toHaveBeenCalled();
  });

  it('never watches a tile explicitly bound to the effective notebook root (server already watches it)', async () => {
    const harness = makeHarness({ effectiveNotebookRoot: '/notebook-root' });
    render(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath="a.md" root="/notebook-root" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );

    await waitFor(() => expect(surfaceCalls.length).toBeGreaterThan(0));
    expect(harness.makeDaemon).toHaveBeenCalledWith('/notebook-root');
    expect(harness.sendFsWatch).not.toHaveBeenCalled();
  });

  it('watches an arbitrary root on mount, adopts the resolved root for the daemon, and unwatches it on unmount', async () => {
    const harness = makeHarness({
      effectiveNotebookRoot: '/notebook-root',
      // fs_watch_result may normalize the path (e.g. resolve symlinks/trailing slash) —
      // fs_changed events for this subscription carry that resolved form, not the raw
      // prop, so both the fs_* calls and the changeSignal lookup must key off it too.
      watchResolvesTo: () => '/repo-resolved',
    });
    const { unmount } = render(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath={null} root="/repo" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );

    // Before resolution: bound to the raw root prop.
    expect(harness.makeDaemon).toHaveBeenCalledWith('/repo');

    await waitFor(() => expect(harness.sendFsWatch).toHaveBeenCalledWith('/repo'));

    // After fs_watch_result lands: the daemon is rebuilt against the resolved
    // root, and the surface re-renders with that instance.
    await waitFor(() => expect(harness.makeDaemon).toHaveBeenCalledWith('/repo-resolved'));
    await waitFor(() => expect(surfaceCalls.length).toBeGreaterThan(1));

    unmount();

    await waitFor(() => expect(harness.sendFsUnwatch).toHaveBeenCalledWith('/repo-resolved'));
    // Mount-then-unmount ordering: watch strictly precedes the matching unwatch.
    expect(harness.callLog).toEqual(['watch:/repo', 'unwatch:/repo-resolved']);
  });

  it('unwatches a root whose fs_watch resolves only after the tile has already unmounted (no leaked watcher)', async () => {
    const callLog: string[] = [];
    let resolveWatch!: (result: FsWatchResult) => void;
    const sendFsWatch = vi.fn((root?: string) => {
      callLog.push(`watch:${root}`);
      return new Promise<FsWatchResult>((resolve) => {
        resolveWatch = resolve;
      });
    });
    const sendFsUnwatch = vi.fn((root?: string): Promise<FsWatchResult> => {
      callLog.push(`unwatch:${root}`);
      return Promise.resolve({ root: root || '' });
    });
    const value: NotebookSurfaceContextValue = {
      makeDaemon: vi.fn((_root?: string) => fakeDaemon()),
      effectiveNotebookRoot: '/notebook-root',
      sendFsWatch,
      sendFsUnwatch,
      connectionGeneration: 0,
    };

    const { unmount } = render(
      <NotebookSurfaceProvider value={value}>
        <NotebookTile initialPath={null} root="/repo" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );
    await waitFor(() => expect(sendFsWatch).toHaveBeenCalledWith('/repo'));

    // Unmount BEFORE fs_watch resolves: the effect's own cleanup runs with
    // watchedRootRef still null, so it can't unwatch anything itself.
    unmount();
    expect(sendFsUnwatch).not.toHaveBeenCalled();

    // The daemon-side watcher lands only now — it must still be dropped, or
    // it leaks until app restart.
    resolveWatch({ root: '/repo-resolved' });
    await waitFor(() => expect(sendFsUnwatch).toHaveBeenCalledWith('/repo-resolved'));
    expect(callLog).toEqual(['watch:/repo', 'unwatch:/repo-resolved']);
  });

  it('unwatches the previously resolved root and re-watches the new one when root changes', async () => {
    const harness = makeHarness({
      effectiveNotebookRoot: '/notebook-root',
      watchResolvesTo: (root) => `${root}-resolved`,
    });
    const { rerender } = render(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath={null} root="/repo-a" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );
    await waitFor(() => expect(harness.sendFsWatch).toHaveBeenCalledWith('/repo-a'));

    rerender(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath={null} root="/repo-b" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );

    await waitFor(() => expect(harness.sendFsUnwatch).toHaveBeenCalledWith('/repo-a-resolved'));
    await waitFor(() => expect(harness.sendFsWatch).toHaveBeenCalledWith('/repo-b'));
    expect(harness.callLog).toEqual(['watch:/repo-a', 'unwatch:/repo-a-resolved', 'watch:/repo-b']);
  });

  it('re-issues fs_watch after a reconnect (connectionGeneration bump), unwatching the pre-reconnect ref first', async () => {
    // The daemon drops an explicit fs_watch ref whenever the owning client's
    // socket disconnects, but a normal frontend reconnect leaves the tile
    // mounted with the same root/callback identities — nothing else in the
    // effect's deps would re-fire the subscription without connectionGeneration.
    const harness = makeHarness({
      effectiveNotebookRoot: '/notebook-root',
      watchResolvesTo: () => '/repo-resolved',
      connectionGeneration: 1,
    });
    const { rerender } = render(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath={null} root="/repo" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );
    await waitFor(() => expect(harness.sendFsWatch).toHaveBeenCalledWith('/repo'));
    await waitFor(() => expect(harness.makeDaemon).toHaveBeenCalledWith('/repo-resolved'));

    // Simulate a reconnect: same root, same context shape, only the
    // generation counter bumps.
    rerender(
      <NotebookSurfaceProvider value={{ ...harness.value, connectionGeneration: 2 }}>
        <NotebookTile initialPath={null} root="/repo" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );

    await waitFor(() => expect(harness.sendFsUnwatch).toHaveBeenCalledWith('/repo-resolved'));
    await waitFor(() => expect(harness.sendFsWatch).toHaveBeenCalledTimes(2));
    expect(harness.sendFsWatch.mock.calls[1]).toEqual(['/repo']);
    expect(harness.callLog).toEqual(['watch:/repo', 'unwatch:/repo-resolved', 'watch:/repo']);
  });

  it('survives a watch failure (e.g. the daemon watch cap) without throwing — the tile still renders', async () => {
    const harness = makeHarness({ effectiveNotebookRoot: '/notebook-root', watchRejects: true });
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    render(
      <NotebookSurfaceProvider value={harness.value}>
        <NotebookTile initialPath={null} root="/repo" onOpenFile={() => {}} />
      </NotebookSurfaceProvider>,
    );

    await waitFor(() => expect(harness.sendFsWatch).toHaveBeenCalledWith('/repo'));
    await waitFor(() => expect(warnSpy).toHaveBeenCalled());
    expect(warnSpy.mock.calls[0][0]).toMatch(/^\[NotebookTile\]/);
    expect(surfaceCalls.length).toBeGreaterThan(0);
  });
});
