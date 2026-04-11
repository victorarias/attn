import type { Terminal as XTerm } from '@xterm/xterm';
import { WebglAddon } from '@xterm/addon-webgl';
import {
  getTerminalRendererConfig,
  subscribeTerminalRendererConfig,
  type TerminalRendererMode,
} from './terminalRenderer';

export interface TerminalRendererLifecycleOptions {
  term: XTerm;
  rendererModeRef: { current: 'webgl' | 'dom' };
  setRendererDisplayMode: (mode: TerminalRendererMode) => void;
  onContextLoss: () => void;
}

export interface TerminalRendererLifecycle {
  dispose: () => void;
}

export function installTerminalRendererLifecycle({
  term,
  rendererModeRef,
  setRendererDisplayMode,
  onContextLoss,
}: TerminalRendererLifecycleOptions): TerminalRendererLifecycle {
  let webglAddon: WebglAddon | null = null;

  const detachWebglRenderer = () => {
    webglAddon?.dispose();
    webglAddon = null;
    rendererModeRef.current = 'dom';
    setRendererDisplayMode('dom');
  };

  const attachWebglRenderer = () => {
    if (webglAddon) {
      return;
    }
    try {
      const nextWebglAddon = new WebglAddon();
      nextWebglAddon.onContextLoss(() => {
        console.info('[Terminal] WebGL context lost, disposing and falling back to DOM');
        detachWebglRenderer();
        onContextLoss();
      });
      term.loadAddon(nextWebglAddon);
      webglAddon = nextWebglAddon;
      rendererModeRef.current = 'webgl';
      setRendererDisplayMode('webgl');
    } catch (error) {
      console.warn('[Terminal] WebGL addon failed:', error);
      detachWebglRenderer();
    }
  };

  const syncConfiguredRendererMode = () => {
    if (getTerminalRendererConfig().mode === 'webgl') {
      attachWebglRenderer();
      return;
    }
    detachWebglRenderer();
  };

  syncConfiguredRendererMode();
  const unsubscribe = subscribeTerminalRendererConfig(syncConfiguredRendererMode);

  return {
    dispose: () => {
      unsubscribe();
      detachWebglRenderer();
    },
  };
}
