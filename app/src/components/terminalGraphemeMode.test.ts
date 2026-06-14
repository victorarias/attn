import { describe, it, expect, vi } from 'vitest';
import {
  GRAPHEME_CLUSTERING_MODE,
  enableGraphemeClustering,
  ensureGraphemeClustering,
  type GraphemeModeTerminal,
} from './terminalGraphemeMode';

// DECSET 2027 — the bytes we expect written to the model to enable clustering.
const ENABLE_2027 = new TextEncoder().encode('\x1b[?2027h');

function fakeTerminal(modeOn: boolean): GraphemeModeTerminal & {
  writes: Uint8Array[];
  getMode: ReturnType<typeof vi.fn>;
} {
  const writes: Uint8Array[] = [];
  return {
    writes,
    getMode: vi.fn((mode: number) => (mode === GRAPHEME_CLUSTERING_MODE ? modeOn : false)),
    write: (data: Uint8Array) => {
      writes.push(data);
    },
  };
}

describe('enableGraphemeClustering', () => {
  it('writes DECSET 2027 unconditionally', () => {
    const term = fakeTerminal(true); // already on — still writes
    enableGraphemeClustering(term);
    expect(term.writes).toHaveLength(1);
    expect(Array.from(term.writes[0])).toEqual(Array.from(ENABLE_2027));
  });
});

describe('ensureGraphemeClustering', () => {
  it('re-enables and reports it when the mode was reset off (e.g. after RIS)', () => {
    const term = fakeTerminal(false);
    const reEnabled = ensureGraphemeClustering(term);
    expect(reEnabled).toBe(true);
    expect(term.getMode).toHaveBeenCalledWith(GRAPHEME_CLUSTERING_MODE);
    expect(term.writes).toHaveLength(1);
    expect(Array.from(term.writes[0])).toEqual(Array.from(ENABLE_2027));
  });

  it('is a no-op when grapheme clustering is already on', () => {
    const term = fakeTerminal(true);
    const reEnabled = ensureGraphemeClustering(term);
    expect(reEnabled).toBe(false);
    expect(term.writes).toHaveLength(0);
  });
});
