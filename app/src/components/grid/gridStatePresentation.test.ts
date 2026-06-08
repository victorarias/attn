import { afterEach, describe, expect, it } from 'vitest';
import {
  DEFAULT_GRID_STATE_PRESENTATION,
  persistGridStatePresentation,
  readGridStatePresentation,
} from './gridStatePresentation';

describe('grid state presentation persistence', () => {
  afterEach(() => {
    window.localStorage.clear();
  });

  it('round-trips both presentation modes', () => {
    persistGridStatePresentation('background');
    expect(readGridStatePresentation()).toBe('background');

    persistGridStatePresentation('border');
    expect(readGridStatePresentation()).toBe('border');
  });

  it('defaults to borders for missing or unsupported values', () => {
    expect(readGridStatePresentation()).toBe(DEFAULT_GRID_STATE_PRESENTATION);

    window.localStorage.setItem('attn.grid.statePresentation', 'glow');
    expect(readGridStatePresentation()).toBe(DEFAULT_GRID_STATE_PRESENTATION);
  });
});
