import { beforeEach, describe, expect, it, vi } from 'vitest';
import { installTerminalRendererLifecycle } from './terminalRendererLifecycle';

const mockState = vi.hoisted(() => ({
  rendererMode: 'dom' as 'dom' | 'webgl',
  onContextLoss: null as null | (() => void),
  dispose: vi.fn(),
  loadAddon: vi.fn(),
  subscribe: vi.fn<(listener: () => void) => () => void>(),
  unsubscribe: vi.fn(),
  constructorError: null as Error | null,
}));

vi.mock('@xterm/addon-webgl', () => ({
  WebglAddon: class MockWebglAddon {
    constructor() {
      if (mockState.constructorError) {
        throw mockState.constructorError;
      }
    }

    onContextLoss(listener: () => void) {
      mockState.onContextLoss = listener;
    }

    dispose() {
      mockState.dispose();
    }
  },
}));

vi.mock('./terminalRenderer', () => ({
  getTerminalRendererConfig: () => ({ mode: mockState.rendererMode }),
  subscribeTerminalRendererConfig: (listener: () => void) => {
    mockState.subscribe(listener);
    return mockState.unsubscribe;
  },
}));

describe('installTerminalRendererLifecycle', () => {
  beforeEach(() => {
    mockState.rendererMode = 'dom';
    mockState.onContextLoss = null;
    mockState.dispose.mockReset();
    mockState.loadAddon.mockReset();
    mockState.subscribe.mockReset();
    mockState.unsubscribe.mockReset();
    mockState.unsubscribe.mockImplementation(() => {});
    mockState.constructorError = null;
  });

  it('attaches WebGL when configured and detaches on dispose', () => {
    mockState.rendererMode = 'webgl';
    const rendererModeRef = { current: 'dom' as 'dom' | 'webgl' };
    const setRendererDisplayMode = vi.fn();
    const term = { loadAddon: mockState.loadAddon } as any;

    const lifecycle = installTerminalRendererLifecycle({
      term,
      rendererModeRef,
      setRendererDisplayMode,
      onContextLoss: vi.fn(),
    });

    expect(mockState.loadAddon).toHaveBeenCalledTimes(1);
    expect(rendererModeRef.current).toBe('webgl');
    expect(setRendererDisplayMode).toHaveBeenCalledWith('webgl');

    lifecycle.dispose();

    expect(mockState.unsubscribe).toHaveBeenCalledTimes(1);
    expect(mockState.dispose).toHaveBeenCalledTimes(1);
    expect(rendererModeRef.current).toBe('dom');
    expect(setRendererDisplayMode).toHaveBeenLastCalledWith('dom');
  });

  it('falls back to DOM when the addon constructor throws', () => {
    mockState.rendererMode = 'webgl';
    mockState.constructorError = new Error('boom');
    const rendererModeRef = { current: 'dom' as 'dom' | 'webgl' };
    const setRendererDisplayMode = vi.fn();
    const term = { loadAddon: mockState.loadAddon } as any;
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    installTerminalRendererLifecycle({
      term,
      rendererModeRef,
      setRendererDisplayMode,
      onContextLoss: vi.fn(),
    });

    expect(mockState.loadAddon).not.toHaveBeenCalled();
    expect(rendererModeRef.current).toBe('dom');
    expect(setRendererDisplayMode).toHaveBeenCalledWith('dom');
    expect(warnSpy).toHaveBeenCalled();
  });

  it('detaches and refreshes through the supplied context-loss callback', () => {
    mockState.rendererMode = 'webgl';
    const rendererModeRef = { current: 'dom' as 'dom' | 'webgl' };
    const setRendererDisplayMode = vi.fn();
    const onContextLoss = vi.fn();
    const term = { loadAddon: mockState.loadAddon } as any;
    const infoSpy = vi.spyOn(console, 'info').mockImplementation(() => {});

    installTerminalRendererLifecycle({
      term,
      rendererModeRef,
      setRendererDisplayMode,
      onContextLoss,
    });

    mockState.onContextLoss?.();

    expect(mockState.dispose).toHaveBeenCalledTimes(1);
    expect(rendererModeRef.current).toBe('dom');
    expect(setRendererDisplayMode).toHaveBeenLastCalledWith('dom');
    expect(onContextLoss).toHaveBeenCalledTimes(1);
    expect(infoSpy).toHaveBeenCalled();
  });
});
