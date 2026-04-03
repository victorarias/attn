export type TerminalRendererMode = 'webgl' | 'dom';

export const DEFAULT_TERMINAL_RENDERER_MODE: TerminalRendererMode = 'webgl';

export interface TerminalRendererConfig {
  mode: TerminalRendererMode;
  updatedAt: string | null;
}

type TerminalRendererListener = () => void;

const config: TerminalRendererConfig = {
  mode: DEFAULT_TERMINAL_RENDERER_MODE,
  updatedAt: null,
};

const listeners = new Set<TerminalRendererListener>();

function touch() {
  config.updatedAt = new Date().toISOString();
}

function notifyListeners() {
  for (const listener of listeners) {
    listener();
  }
}

export function getTerminalRendererConfig(): TerminalRendererConfig {
  return { ...config };
}

export function setTerminalRendererConfig(mode: TerminalRendererMode) {
  config.mode = mode;
  touch();
  notifyListeners();
  return getTerminalRendererConfig();
}

export function subscribeTerminalRendererConfig(listener: TerminalRendererListener) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
