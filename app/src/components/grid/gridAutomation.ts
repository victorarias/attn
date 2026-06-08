// A tiny module-level registry so the UI automation bridge can introspect grid
// mode without a reference into the conditionally-mounted GridView. GridView
// publishes a handle while it's mounted and clears it on unmount; the bridge
// reads through getGridAutomationHandle(). This is a test affordance only — it
// exposes read state, zoom control, and (since zoomed tiles are interactive)
// keyboard input into the zoomed tile, exercising the real InputHandler path.
import type { CompositorStats, GridTileSummary } from './GridCompositor';
import type { GridStatePresentation } from './gridStatePresentation';

export interface GridAutomationState {
  active: boolean;
  tileCount: number;
  zoomedId: string | null;
  layout: { rows: number; cols: number };
  statePresentation: GridStatePresentation;
  stats: CompositorStats | null;
  tiles: GridTileSummary[];
}

export interface GridAutomationHandle {
  getState(): GridAutomationState;
  getTileText(id: string): string | null;
  zoom(id: string | null): void;
  setStatePresentation(presentation: GridStatePresentation): void;
  hitTest(x: number, y: number): string | null;
  // Type into the zoomed tile via the real keydown -> InputHandler -> PTY path.
  // Returns false if there is no stage to receive the keys.
  sendText(text: string): boolean;
}

let handle: GridAutomationHandle | null = null;

export function setGridAutomationHandle(next: GridAutomationHandle | null): void {
  handle = next;
}

export function getGridAutomationHandle(): GridAutomationHandle | null {
  return handle;
}

// State when grid mode isn't mounted — so callers get a stable shape either way.
export const INACTIVE_GRID_STATE: GridAutomationState = {
  active: false,
  tileCount: 0,
  zoomedId: null,
  layout: { rows: 0, cols: 0 },
  statePresentation: 'border',
  stats: null,
  tiles: [],
};
