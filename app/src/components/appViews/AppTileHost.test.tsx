import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { AppTileHost } from './AppTileHost';
import { AppViewLoadError } from './loadAppView';
import { DaemonApiProvider, type DaemonApi } from '../../contexts/DaemonApiContext';
import { useDaemonStore } from '../../store/daemonSessions';
import type { AppRegistryEntry } from '../../hooks/useDaemonSocket';

// The host's whole job is what the user sees when a view does not mount, and
// what the authoring agent sees afterwards. Every case here is one of those:
// each says what is wrong and the way back, none of them removes the tile, and
// the one that crashes is reported against the version that served it.

const loadAppView = vi.hoisted(() => vi.fn());
vi.mock('./loadAppView', async () => {
  const actual = await vi.importActual<typeof import('./loadAppView')>('./loadAppView');
  return { ...actual, loadAppView };
});

const HASH_A = 'a'.repeat(64);
const HASH_B = 'b'.repeat(64);

function entry(overrides: Partial<AppRegistryEntry> = {}): AppRegistryEntry {
  return {
    name: 'reviewer',
    enabled: true,
    version_id: 7,
    content_hash: HASH_A,
    views: [{ name: 'approvals', kind: 'tile', title: 'Pending approvals' }],
    ...overrides,
  } as AppRegistryEntry;
}

function renderHost(apps: AppRegistryEntry[], sendAppViewCrash = vi.fn()) {
  act(() => {
    useDaemonStore.getState().setApps(apps);
  });
  const api = { sendAppViewCrash } as unknown as DaemonApi;
  render(
    <DaemonApiProvider api={api}>
      <AppTileHost
        app="reviewer"
        view="approvals"
        workspaceId="ws-1"
        sessionId="sess-1"
        tileId="tile-7"
        params="t-42"
      />
    </DaemonApiProvider>,
  );
  return sendAppViewCrash;
}

beforeEach(() => {
  loadAppView.mockReset();
  act(() => {
    useDaemonStore.getState().setApps([]);
  });
});

describe('a view that mounts', () => {
  it('is given where it sits and what the user typed when docking', async () => {
    const seen: Record<string, unknown>[] = [];
    loadAppView.mockResolvedValue((props: Record<string, unknown>) => {
      seen.push(props);
      return <div>approvals body</div>;
    });

    renderHost([entry()]);

    await screen.findByText('approvals body');
    expect(seen[0]).toEqual({
      workspaceId: 'ws-1',
      sessionId: 'sess-1',
      tileId: 'tile-7',
      params: 't-42',
    });
  });
});

describe('a view that cannot mount', () => {
  it('says an uninstalled app is gone and leaves the tile where it is', async () => {
    renderHost([]);
    const message = await screen.findByText(/is not installed/);
    expect(message.textContent).toContain('reviewer');
    expect(screen.getByText(/stays where you put it/)).toBeTruthy();
    expect(loadAppView).not.toHaveBeenCalled();
  });

  it('names the command that turns a disabled app back on', async () => {
    renderHost([entry({ enabled: false })]);
    await screen.findByText(/reviewer is disabled/);
    expect(screen.getByText(/attn app enable reviewer/)).toBeTruthy();
    expect(loadAppView).not.toHaveBeenCalled();
  });

  it('lists what the serving version does offer when the view is gone', async () => {
    renderHost([entry({ views: [{ name: 'history', kind: 'tile', title: 'History' }] } as Partial<AppRegistryEntry>)]);
    await screen.findByText(/no longer has a view called/);
    expect(screen.getByText(/offers: history/)).toBeTruthy();
  });

  it('offers Retry when the bundle will not load, and retries on click', async () => {
    loadAppView.mockRejectedValue(new AppViewLoadError('This view could not be loaded.', 'Importing failed: boom'));
    renderHost([entry()]);

    await screen.findByText('This view could not be loaded.');
    expect(screen.getByText(/Importing failed: boom/)).toBeTruthy();
    expect(loadAppView).toHaveBeenCalledTimes(1);

    loadAppView.mockResolvedValue(() => <div>approvals body</div>);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByText('approvals body');
    // A distinct URL, or the browser's module map would answer a top-level throw
    // and a missing default export from cache and Retry could never recover.
    expect(loadAppView.mock.calls[1][0]).not.toBe(loadAppView.mock.calls[0][0]);
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_A);
  });

  it('names the binding when the module exports no component', async () => {
    loadAppView.mockRejectedValue(new AppViewLoadError(
      'This view exports no component.',
      'It must export a React component as its default export. It exports: Approvals.',
    ));
    renderHost([entry()]);
    await screen.findByText('This view exports no component.');
    expect(screen.getByText(/It exports: Approvals/)).toBeTruthy();
  });
});

describe('a view that throws while rendering', () => {
  it('costs its own tile and is reported against the serving version', async () => {
    // React logs a caught render error; the noise is not the assertion.
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    loadAppView.mockResolvedValue(() => {
      throw new Error('cannot read properties of undefined');
    });

    const sendAppViewCrash = renderHost([entry()]);

    await screen.findByText(/crashed while rendering/);
    expect(screen.getByText(/attn app logs reviewer/)).toBeTruthy();
    await waitFor(() => expect(sendAppViewCrash).toHaveBeenCalledTimes(1));
    const report = sendAppViewCrash.mock.calls[0][0];
    expect(report.app).toBe('reviewer');
    expect(report.view).toBe('approvals');
    expect(report.versionId).toBe(7);
    expect(report.tileId).toBe('tile-7');
    expect(report.error).toContain('cannot read properties of undefined');
    consoleError.mockRestore();
  });

  it('drops the crashed component on Reload rather than reporting the same crash again', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    loadAppView.mockResolvedValue(() => {
      throw new Error('cannot read properties of undefined');
    });

    const sendAppViewCrash = renderHost([entry()]);
    await screen.findByText(/crashed while rendering/);
    await waitFor(() => expect(sendAppViewCrash).toHaveBeenCalledTimes(1));

    // Held open so the assertion lands in the window the bug lived in: after the
    // boundary's reset key changed, before the fresh module resolved.
    let resolveSecond: (component: unknown) => void = () => {};
    loadAppView.mockReturnValue(new Promise((resolve) => { resolveSecond = resolve; }));
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));

    await screen.findByText(/Loading reviewer\/approvals/);
    expect(sendAppViewCrash).toHaveBeenCalledTimes(1);

    await act(async () => {
      resolveSecond(() => <div>approvals body</div>);
    });
    await screen.findByText('approvals body');
    expect(sendAppViewCrash).toHaveBeenCalledTimes(1);
    consoleError.mockRestore();
  });
});

describe('a version that moves under a docked tile', () => {
  it('remounts against the new bundle when the tile does not hold focus', async () => {
    loadAppView.mockResolvedValue(() => <div>approvals body</div>);
    renderHost([entry()]);
    await screen.findByText('approvals body');
    expect(loadAppView).toHaveBeenCalledTimes(1);

    act(() => {
      useDaemonStore.getState().setApps([entry({ version_id: 8, content_hash: HASH_B })]);
    });

    await waitFor(() => expect(loadAppView).toHaveBeenCalledTimes(2));
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_B);
  });

  it('waits for the user to leave rather than pulling the view out mid-keystroke', async () => {
    loadAppView.mockResolvedValue(() => <input aria-label="app input" />);
    renderHost([entry()]);
    const input = await screen.findByLabelText('app input');

    fireEvent.focus(input);
    act(() => {
      useDaemonStore.getState().setApps([entry({ version_id: 8, content_hash: HASH_B })]);
    });

    // The badge is the whole notice, and it does not animate.
    await screen.findByText(/reloading when you leave this tile/);
    expect(loadAppView).toHaveBeenCalledTimes(1);

    fireEvent.blur(input, { relatedTarget: document.body });
    await waitFor(() => expect(loadAppView).toHaveBeenCalledTimes(2));
    expect(loadAppView.mock.calls[1][0]).toContain(HASH_B);
  });
});
