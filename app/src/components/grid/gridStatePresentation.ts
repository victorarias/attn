export type GridStatePresentation = 'border' | 'background';

export const DEFAULT_GRID_STATE_PRESENTATION: GridStatePresentation = 'border';

const GRID_STATE_PRESENTATION_STORAGE_KEY = 'attn.grid.statePresentation';

export function readGridStatePresentation(): GridStatePresentation {
  try {
    const value = window.localStorage.getItem(GRID_STATE_PRESENTATION_STORAGE_KEY);
    return value === 'background' || value === 'border'
      ? value
      : DEFAULT_GRID_STATE_PRESENTATION;
  } catch {
    return DEFAULT_GRID_STATE_PRESENTATION;
  }
}

export function persistGridStatePresentation(presentation: GridStatePresentation): void {
  try {
    window.localStorage.setItem(GRID_STATE_PRESENTATION_STORAGE_KEY, presentation);
  } catch (err) {
    console.warn('[grid] Failed to persist state presentation preference:', err);
  }
}
