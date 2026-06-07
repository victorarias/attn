import { act, render, waitFor } from '@testing-library/react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { BrowserTileBody } from './BrowserTileBody';

const browserHost = vi.hoisted(() => ({
  mount: vi.fn(async () => {}),
  unmount: vi.fn(async () => {}),
  update: vi.fn(async () => {}),
}));

vi.mock('../../browser/host', () => ({
  browserHostLabel: (workspaceId: string, tileId: string) => `browser-${workspaceId}-${tileId}`,
  mountBrowserHost: browserHost.mount,
  unmountBrowserHost: browserHost.unmount,
  updateBrowserHost: browserHost.update,
}));

describe('BrowserTileBody', () => {
  beforeAll(() => {
    class ResizeObserverMock {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
    globalThis.ResizeObserver = ResizeObserverMock as unknown as typeof ResizeObserver;
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('retargets an existing native host without unmounting it', async () => {
    const props = {
      workspaceId: 'workspace-1',
      tileId: 'tile-browser',
      dragging: false,
      visible: true,
      onClose: vi.fn(),
    };
    const view = render(<BrowserTileBody {...props} url="https://first.example" />);

    await waitFor(() => {
      expect(browserHost.mount).toHaveBeenCalledWith(
        'browser-workspace-1-tile-browser',
        'https://first.example',
        expect.any(Object),
        true,
      );
    });

    view.rerender(<BrowserTileBody {...props} url="https://second.example" />);

    await waitFor(() => {
      expect(browserHost.mount).toHaveBeenCalledWith(
        'browser-workspace-1-tile-browser',
        'https://second.example',
        expect.any(Object),
        true,
      );
    });
    expect(browserHost.unmount).not.toHaveBeenCalled();

    view.unmount();
    await waitFor(() => {
      expect(browserHost.unmount).toHaveBeenCalledWith('browser-workspace-1-tile-browser');
    });
  });

  it('does not navigate again when tile params persist a native location', async () => {
    const props = {
      workspaceId: 'workspace-1',
      tileId: 'tile-browser',
      dragging: false,
      visible: true,
      onClose: vi.fn(),
    };
    const view = render(<BrowserTileBody {...props} url="https://first.example" />);
    await waitFor(() => expect(browserHost.mount).toHaveBeenCalledTimes(1));

    act(() => {
      window.dispatchEvent(new CustomEvent('attn:browser-location', {
        detail: {
          label: 'browser-workspace-1-tile-browser',
          url: 'https://first.example/dashboard',
        },
      }));
    });
    view.rerender(<BrowserTileBody {...props} url="https://first.example/dashboard" />);

    await new Promise((resolve) => window.setTimeout(resolve, 0));
    expect(browserHost.mount).toHaveBeenCalledTimes(1);
    view.unmount();
    await waitFor(() => {
      expect(browserHost.unmount).toHaveBeenCalledWith('browser-workspace-1-tile-browser');
    });
  });

  it('unmounts after an in-flight native mount completes', async () => {
    let finishMount = () => {};
    browserHost.mount.mockImplementationOnce(() => new Promise<void>((resolve) => {
      finishMount = resolve;
    }));
    const view = render(
      <BrowserTileBody
        workspaceId="workspace-1"
        tileId="tile-browser"
        url="https://first.example"
        dragging={false}
        visible
        onClose={vi.fn()}
      />,
    );
    await waitFor(() => expect(browserHost.mount).toHaveBeenCalledTimes(1));

    view.unmount();
    expect(browserHost.unmount).not.toHaveBeenCalled();
    finishMount();

    await waitFor(() => {
      expect(browserHost.unmount).toHaveBeenCalledWith('browser-workspace-1-tile-browser');
    });
  });

  it('applies the latest visibility after an in-flight native mount completes', async () => {
    let finishMount = () => {};
    browserHost.mount.mockImplementationOnce(() => new Promise<void>((resolve) => {
      finishMount = resolve;
    }));
    const props = {
      workspaceId: 'workspace-1',
      tileId: 'tile-browser',
      url: 'https://first.example',
      dragging: false,
      onClose: vi.fn(),
    };
    const view = render(<BrowserTileBody {...props} visible />);
    await waitFor(() => expect(browserHost.mount).toHaveBeenCalledTimes(1));

    view.rerender(<BrowserTileBody {...props} visible={false} />);
    finishMount();

    await waitFor(() => {
      expect(browserHost.update).toHaveBeenLastCalledWith(
        'browser-workspace-1-tile-browser',
        expect.any(Object),
        false,
      );
    });
    view.unmount();
    await waitFor(() => {
      expect(browserHost.unmount).toHaveBeenCalledWith('browser-workspace-1-tile-browser');
    });
  });
});
