import { describe, expect, it } from 'vitest';
import { bumpFsChangeSignal, fsChangeSignalKey } from './App';

// Pure-logic coverage for the per-root fs_changed routing that feeds every
// notebook surface's changeSignal (see makeNotebookSurfaceDaemon in App.tsx).
// App.tsx has no dedicated render-level test suite for this wiring (see
// App.presentationNotices.test.ts for the same pattern applied to another
// piece of App.tsx state), so this exercises the extracted key/reducer
// functions directly rather than standing up a full App render + mocked
// WebSocket.

const NOTEBOOK_ROOT = '/Users/victor/attn-notebook';
const ROOT_A = '/Users/victor/code/repo-a';
const ROOT_B = '/Users/victor/code/repo-b';

describe('fsChangeSignalKey', () => {
  it('uses the event root verbatim when present', () => {
    expect(fsChangeSignalKey(ROOT_A, NOTEBOOK_ROOT)).toBe(ROOT_A);
  });

  it('falls back to the effective notebook root for an empty/missing root', () => {
    expect(fsChangeSignalKey('', NOTEBOOK_ROOT)).toBe(NOTEBOOK_ROOT);
  });
});

describe('bumpFsChangeSignal (per-root fs_changed routing)', () => {
  it('bumps only the signal for the event root, leaving other roots untouched', () => {
    let signals: Record<string, number> = { [ROOT_A]: 1, [ROOT_B]: 5 };
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    expect(signals).toEqual({ [ROOT_A]: 2, [ROOT_B]: 5 });
  });

  it('an event for root A never bumps a tile keyed to root B', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    // Root B's key was never touched — a tile bound to it reads the default (0).
    expect(signals[ROOT_B]).toBeUndefined();
    expect(signals[ROOT_A]).toBe(1);
  });

  it('an empty-root event reaches notebook-rooted tiles (keyed to the effective notebook root)', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, '', NOTEBOOK_ROOT);
    expect(signals[NOTEBOOK_ROOT]).toBe(1);
    // It must not create a spurious '' key that no tile would ever look up.
    expect(signals['']).toBeUndefined();
  });

  it('a notebook-root event and an empty-root event land on the same key (both mean notebook-rooted)', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, NOTEBOOK_ROOT, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, '', NOTEBOOK_ROOT);
    expect(signals[NOTEBOOK_ROOT]).toBe(2);
  });

  it('accumulates independently across many roots', () => {
    let signals: Record<string, number> = {};
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, ROOT_A, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, ROOT_B, NOTEBOOK_ROOT);
    signals = bumpFsChangeSignal(signals, '', NOTEBOOK_ROOT);
    expect(signals).toEqual({ [ROOT_A]: 2, [ROOT_B]: 1, [NOTEBOOK_ROOT]: 1 });
  });
});
