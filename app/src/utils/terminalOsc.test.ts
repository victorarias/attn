import { describe, expect, it } from 'vitest';
import { parseOsc52Writes } from './terminalOsc';

describe('parseOsc52Writes', () => {
  it('parses clipboard writes split across PTY chunks', () => {
    let state = { pending: '' };
    let parsed = parseOsc52Writes(state, 'prefix\x1b]5');
    expect(parsed.payloads).toEqual([]);
    state = parsed.state;

    parsed = parseOsc52Writes(state, '2;c;aGVs');
    expect(parsed.payloads).toEqual([]);
    state = parsed.state;

    parsed = parseOsc52Writes(state, 'bG8=\x07tail');
    expect(parsed.payloads).toEqual(['aGVsbG8=']);
    expect(parsed.state.pending).toBe('');
  });

  it('accepts ST termination and rejects clipboard read queries', () => {
    const parsed = parseOsc52Writes(
      { pending: '' },
      '\x1b]52;c;?\x1b\\\x1b]52;c;d29ybGQ=\x1b\\',
    );

    expect(parsed.payloads).toEqual(['d29ybGQ=']);
  });
});
