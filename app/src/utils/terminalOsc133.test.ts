import { describe, expect, it } from 'vitest';
import { emptyOsc133State, parseOsc133, type Osc133Segment } from './terminalOsc133';

const encoder = new TextEncoder();
const decoder = new TextDecoder();

function bytes(text: string): Uint8Array {
  return encoder.encode(text);
}

function joinedText(segments: Osc133Segment[]): string {
  return segments.map((segment) => decoder.decode(segment.bytes)).join('');
}

describe('parseOsc133', () => {
  it('passes plain output through untouched on the fast path', () => {
    const chunk = bytes('plain output, no escapes');
    const result = parseOsc133(emptyOsc133State(), chunk);
    expect(result.segments).toHaveLength(1);
    expect(result.segments[0].bytes).toBe(chunk);
    expect(result.segments[0].marker).toBeUndefined();
    expect(result.state.pending).toBeNull();
  });

  it('parses the full fish marker lifecycle with BEL terminators', () => {
    const stream = ']133;A;click_events=1prompt> '
      + ']133;Becho hello\r\n'
      + ']133;C;cmdline_url=echo%20hellohello\r\n'
      + ']133;D;0';
    const result = parseOsc133(emptyOsc133State(), bytes(stream));
    const markers = result.segments.map((segment) => segment.marker).filter(Boolean);
    expect(markers).toEqual([
      { kind: 'prompt-start' },
      { kind: 'input-start' },
      { kind: 'pre-exec', cmdline: 'echo hello' },
      { kind: 'command-end', exitCode: 0 },
    ]);
    // Every input byte is preserved across segments.
    expect(joinedText(result.segments)).toBe(stream);
    expect(result.state.pending).toBeNull();
  });

  it('supports ST terminators and unknown subtypes', () => {
    const stream = ']133;P;k=v\\middle]133;D;1\\';
    const result = parseOsc133(emptyOsc133State(), bytes(stream));
    const markers = result.segments.map((segment) => segment.marker);
    expect(markers[0]).toBeUndefined();
    expect(markers).toContainEqual({ kind: 'command-end', exitCode: 1 });
    expect(joinedText(result.segments)).toBe(stream);
  });

  it('reassembles a marker split across chunks at every byte boundary', () => {
    const stream = `before]133;C;cmdline_url=ls%20-laafter`;
    const raw = bytes(stream);
    for (let split = 1; split < raw.length; split += 1) {
      let state = emptyOsc133State();
      const segments: Osc133Segment[] = [];
      for (const part of [raw.subarray(0, split), raw.subarray(split)]) {
        const result = parseOsc133(state, part);
        state = result.state;
        segments.push(...result.segments);
      }
      const markers = segments.map((segment) => segment.marker).filter(Boolean);
      expect(markers, `split at ${split}`).toEqual([{ kind: 'pre-exec', cmdline: 'ls -la' }]);
      expect(joinedText(segments), `split at ${split}`).toBe(stream);
      expect(state.pending).toBeNull();
    }
  });

  it('does not hold back unrelated escape sequences', () => {
    const stream = '[31mred[0m and ]0;title done';
    const result = parseOsc133(emptyOsc133State(), bytes(stream));
    expect(joinedText(result.segments)).toBe(stream);
    expect(result.state.pending).toBeNull();
    expect(result.segments.every((segment) => !segment.marker)).toBe(true);
  });

  it('abandons a marker that never terminates', () => {
    let state = emptyOsc133State();
    const head = parseOsc133(state, bytes(']133;C;cmdline_url='));
    state = head.state;
    expect(state.pending).not.toBeNull();
    const flood = parseOsc133(state, bytes('x'.repeat(5000)));
    expect(flood.state.pending).toBeNull();
    expect(joinedText([...head.segments, ...flood.segments]))
      .toBe(`]133;C;cmdline_url=${'x'.repeat(5000)}`);
  });

  it('decodes malformed percent encoding as missing cmdline', () => {
    const result = parseOsc133(emptyOsc133State(), bytes(']133;C;cmdline_url=%zz'));
    expect(result.segments[0].marker).toEqual({ kind: 'pre-exec', cmdline: undefined });
  });
});
