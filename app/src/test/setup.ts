import '@testing-library/jest-dom/vitest';
import { vi } from 'vitest';
import { WHATS_NEW_ID, WHATS_NEW_STORAGE_KEY } from '../hooks/useWhatsNew';

// Mock Tauri APIs that components might use
vi.mock('@tauri-apps/api/core', () => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => false),
}));

vi.mock('@tauri-apps/api/app', () => ({
  getVersion: vi.fn(async () => '0.0.0'),
}));

if (typeof window !== 'undefined') {
  // Mock window.matchMedia for components that use it
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  });
}

// Mock ResizeObserver
(globalThis as typeof globalThis & { ResizeObserver: typeof ResizeObserver }).ResizeObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
})) as unknown as typeof ResizeObserver;

// Node 22+ ships a built-in `localStorage` that shadows happy-dom's Storage
// when vitest installs `window` onto globalThis. Node's stub is missing the
// Storage methods unless --localstorage-file is set, which makes any test
// that touches localStorage throw "removeItem is not a function". Install a
// minimal Storage-compatible shim so the test environment matches the real
// browser API regardless of node version.
if (typeof window !== 'undefined') {
  const ensureLocalStorage = () => {
    const candidate = window.localStorage;
    if (candidate && typeof candidate.getItem === 'function') {
      return;
    }
    const data = new Map<string, string>();
    const storage: Storage = {
      get length() { return data.size; },
      clear: () => data.clear(),
      getItem: (key: string) => (data.has(key) ? data.get(key)! : null),
      key: (index: number) => Array.from(data.keys())[index] ?? null,
      removeItem: (key: string) => { data.delete(key); },
      setItem: (key: string, value: string) => { data.set(key, String(value)); },
    };
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get: () => storage,
    });
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get: () => storage,
    });
  };
  ensureLocalStorage();

  // Treat the one-time "what's new" announcement as already seen so it does not
  // render over unrelated component/App tests. Tests that exercise the modal
  // clear localStorage and assert the gating themselves.
  window.localStorage.setItem(WHATS_NEW_STORAGE_KEY, WHATS_NEW_ID);
}
