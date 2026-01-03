/**
 * Test Harness Types
 *
 * Shared types for component test harnesses used with Playwright.
 * Each harness exposes a window.__HARNESS__ API for test control.
 */

export interface HarnessAPI {
  /** Name of the component being tested */
  componentName: string;

  /** Recorded function calls for verification */
  calls: Record<string, unknown[][]>;

  /** Record a function call */
  recordCall: (fnName: string, args: unknown[]) => void;

  /** Get calls for a specific function */
  getCalls: (fnName: string) => unknown[][];

  /** Clear all recorded calls */
  clearCalls: () => void;

  /** Trigger a re-render (component-specific implementation) */
  triggerRerender: () => void;

  /** Check if harness is ready */
  ready: boolean;
}

/** Extend Window to include harness API */
declare global {
  interface Window {
    __HARNESS__: HarnessAPI;
  }
}

/** Base props that all harnesses must handle */
export interface HarnessConfig {
  /** Component name for routing */
  name: string;
  /** React component to render */
  Component: React.ComponentType<HarnessProps>;
}

/** Props passed to harness components */
export interface HarnessProps {
  /** Callback when harness is ready */
  onReady: () => void;
  /** Register the triggerRerender function */
  setTriggerRerender: (fn: () => void) => void;
}
