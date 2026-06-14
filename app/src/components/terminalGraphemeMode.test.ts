import { describe, it, expect } from 'vitest';
import {
  GRAPHEME_CLUSTERING_MODE,
  enableGraphemeClustering,
  ensureGraphemeClustering,
  writeReassertingClustering,
  type GraphemeModeTerminal,
} from './terminalGraphemeMode';

// DECSET 2027 — the bytes we expect written to the model to enable clustering.
const ENABLE_2027 = new TextEncoder().encode('\x1b[?2027h');
const RIS = new TextEncoder().encode('\x1bc'); // ESC c, full reset
// 👨‍👩‍👧‍👦 — a ZWJ family, the canonical multi-codepoint cluster. A 2027h written
// before these bytes reach the model keeps them in one cell (ghostty-web
// behaviour, exercised by the live app); a RIS in front of them turns 2027 off
// and the model splits them across cells.
const FAMILY = new TextEncoder().encode('\u{1F468}\u{200D}\u{1F469}\u{200D}\u{1F467}\u{200D}\u{1F466}');

// A terminal that records every write (copied, since writes are buffer views).
function recordingTerminal(modeOn = true): GraphemeModeTerminal & { writes: number[][] } {
  const writes: number[][] = [];
  return {
    writes,
    getMode: (mode: number) => (mode === GRAPHEME_CLUSTERING_MODE ? modeOn : false),
    write: (data: Uint8Array) => writes.push(Array.from(data)),
  };
}

const bytes = (a: Uint8Array) => Array.from(a);
const concat = (...arrs: Uint8Array[]) => {
  const out: number[] = [];
  for (const a of arrs) out.push(...a);
  return Uint8Array.from(out);
};

describe('enableGraphemeClustering', () => {
  it('writes DECSET 2027 unconditionally', () => {
    const term = recordingTerminal(true); // already on — still writes
    enableGraphemeClustering(term);
    expect(term.writes).toEqual([bytes(ENABLE_2027)]);
  });
});

describe('ensureGraphemeClustering', () => {
  it('re-enables and reports it when the mode was reset off', () => {
    const term = recordingTerminal(false);
    expect(ensureGraphemeClustering(term)).toBe(true);
    expect(term.writes).toEqual([bytes(ENABLE_2027)]);
  });

  it('is a no-op when grapheme clustering is already on', () => {
    const term = recordingTerminal(true);
    expect(ensureGraphemeClustering(term)).toBe(false);
    expect(term.writes).toHaveLength(0);
  });
});

describe('writeReassertingClustering', () => {
  it('passes plain output straight through with no extra writes', () => {
    const term = recordingTerminal();
    const carry = writeReassertingClustering(term, FAMILY, false);
    expect(term.writes).toEqual([bytes(FAMILY)]);
    expect(carry).toBe(false);
  });

  // The regression the reviewer asked for: a RIS followed by a clustered emoji
  // in ONE chunk. Clustering must be re-enabled BETWEEN the reset and the emoji
  // bytes — if the emoji reached the model first it would parse into split cells
  // before any re-enable. We assert the exact write order at the model boundary.
  it('re-enables clustering between a RIS and emoji in the same chunk', () => {
    const term = recordingTerminal();
    const carry = writeReassertingClustering(term, concat(RIS, FAMILY), false);
    expect(term.writes).toEqual([bytes(RIS), bytes(ENABLE_2027), bytes(FAMILY)]);
    expect(carry).toBe(false);
  });

  it('handles several RIS in one chunk, re-enabling after each', () => {
    const term = recordingTerminal();
    writeReassertingClustering(term, concat(RIS, FAMILY, RIS, FAMILY), false);
    // The model receives, in order: reset, enable, family, reset, enable, family.
    expect(term.writes.flat()).toEqual(bytes(concat(RIS, ENABLE_2027, FAMILY, RIS, ENABLE_2027, FAMILY)));
    // Exactly one re-enable per reset (0x3f is the '?' of ESC[?2027h).
    const enables = term.writes.filter((w) => w.length === ENABLE_2027.length && w[2] === 0x3f);
    expect(enables).toHaveLength(2);
  });

  it('reports a lone trailing ESC and completes a boundary-straddling RIS next call', () => {
    const term = recordingTerminal();
    // Chunk 1 ends on a lone ESC (the start of a RIS split across the boundary).
    const carry = writeReassertingClustering(term, Uint8Array.from([0x41, 0x1b]), false);
    expect(carry).toBe(true);
    term.writes.length = 0;
    // Chunk 2 opens with 'c', completing the RIS, then an emoji.
    const carry2 = writeReassertingClustering(term, concat(Uint8Array.from([0x63]), FAMILY), carry);
    expect(term.writes).toEqual([[0x63], bytes(ENABLE_2027), bytes(FAMILY)]);
    expect(carry2).toBe(false);
  });

  it('does not treat a trailing ESC that begins a non-RIS sequence as a reset', () => {
    const term = recordingTerminal();
    const carry = writeReassertingClustering(term, Uint8Array.from([0x1b]), false);
    expect(carry).toBe(true);
    term.writes.length = 0;
    // Next chunk is a CSI ("[m…"), not a RIS — no re-enable injected.
    const carry2 = writeReassertingClustering(term, Uint8Array.from([0x5b, 0x6d]), carry);
    expect(term.writes).toEqual([[0x5b, 0x6d]]);
    expect(carry2).toBe(false);
  });
});
