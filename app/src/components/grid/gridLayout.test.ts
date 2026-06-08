import { afterEach, describe, expect, it } from 'vitest';
import {
  AUTO_LAYOUT,
  MAX_GRID_COLS,
  MAX_GRID_ROWS,
  autoGrid,
  persistGridLayout,
  readGridLayout,
  resolveGridLayout,
} from './gridLayout';

describe('autoGrid', () => {
  it('keeps a single tile at 1×1', () => {
    expect(autoGrid(0)).toEqual({ rows: 1, cols: 1 });
    expect(autoGrid(1)).toEqual({ rows: 1, cols: 1 });
  });

  it('produces a near-square that fits every tile', () => {
    expect(autoGrid(4)).toEqual({ rows: 2, cols: 2 });
    expect(autoGrid(5)).toEqual({ rows: 2, cols: 3 });
    expect(autoGrid(9)).toEqual({ rows: 3, cols: 3 });
    expect(autoGrid(25)).toEqual({ rows: 5, cols: 5 });
  });
});

describe('resolveGridLayout', () => {
  it('auto fits every tile (capacity == tile count)', () => {
    const r = resolveGridLayout(8, AUTO_LAYOUT);
    expect(r).toEqual({ rows: 3, cols: 3, capacity: 8 });
  });

  it('fixed caps capacity at rows×cols so extras stay off-board', () => {
    const r = resolveGridLayout(8, { mode: 'fixed', rows: 2, cols: 3 });
    expect(r).toEqual({ rows: 2, cols: 3, capacity: 6 });
  });

  it('clamps an out-of-range fixed shape to the picker bounds', () => {
    const r = resolveGridLayout(40, { mode: 'fixed', rows: 9, cols: 9 });
    expect(r).toEqual({ rows: MAX_GRID_ROWS, cols: MAX_GRID_COLS, capacity: MAX_GRID_ROWS * MAX_GRID_COLS });
  });
});

describe('grid layout persistence', () => {
  afterEach(() => {
    window.localStorage.clear();
  });

  it('round-trips a fixed layout', () => {
    persistGridLayout({ mode: 'fixed', rows: 2, cols: 3 });
    expect(readGridLayout()).toEqual({ mode: 'fixed', rows: 2, cols: 3 });
  });

  it('round-trips auto', () => {
    persistGridLayout({ mode: 'fixed', rows: 4, cols: 4 });
    persistGridLayout(AUTO_LAYOUT);
    expect(readGridLayout()).toEqual({ mode: 'auto' });
  });

  it('defaults to auto when nothing is stored', () => {
    expect(readGridLayout()).toEqual({ mode: 'auto' });
  });

  it('defaults to auto on corrupt data', () => {
    window.localStorage.setItem('attn.grid.layout', '{not json');
    expect(readGridLayout()).toEqual({ mode: 'auto' });
  });

  it('clamps an out-of-range stored fixed shape', () => {
    window.localStorage.setItem('attn.grid.layout', JSON.stringify({ mode: 'fixed', rows: 99, cols: 0 }));
    expect(readGridLayout()).toEqual({ mode: 'fixed', rows: MAX_GRID_ROWS, cols: 1 });
  });
});
