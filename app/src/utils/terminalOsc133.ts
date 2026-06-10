// OSC 133 semantic-prompt parsing (shell integration markers).
//
// fish 4.x emits these natively around every interactive command:
//   ESC]133;A;click_events=1 BEL   prompt start
//   ESC]133;B BEL                  prompt end / input start
//   ESC]133;C;cmdline_url=... BEL  pre-exec (command text, percent-encoded)
//   ESC]133;D;<exit> BEL           command finished
//
// The parser segments a PTY byte stream at marker boundaries so the caller
// can write each segment to the terminal model and then read the model's
// cursor to learn the marker's buffer position. It works on raw bytes —
// decoding to a string and re-encoding would corrupt multibyte characters
// split across PTY chunks.

export type Osc133Marker =
  | { kind: 'prompt-start' }
  | { kind: 'input-start' }
  | { kind: 'pre-exec'; cmdline?: string }
  | { kind: 'command-end'; exitCode?: number };

export interface Osc133State {
  pending: Uint8Array | null;
}

export interface Osc133Segment {
  bytes: Uint8Array;
  marker?: Osc133Marker;
}

export interface Osc133ParseResult {
  state: Osc133State;
  segments: Osc133Segment[];
}

const PREFIX = new Uint8Array([0x1b, 0x5d, 0x31, 0x33, 0x33, 0x3b]); // ESC ] 1 3 3 ;
const BEL = 0x07;
const ESC = 0x1b;
const BACKSLASH = 0x5c;
// A marker that never terminates is abandoned past this size so a broken
// producer cannot make the parser buffer output forever.
const MAX_PENDING_BYTES = 4096;

const payloadDecoder = new TextDecoder();

function indexOfPrefix(buffer: Uint8Array, from: number): number {
  const last = buffer.length - PREFIX.length;
  for (let i = from; i <= last; i += 1) {
    if (buffer[i] !== ESC) continue;
    let matched = true;
    for (let j = 1; j < PREFIX.length; j += 1) {
      if (buffer[i + j] !== PREFIX[j]) {
        matched = false;
        break;
      }
    }
    if (matched) return i;
  }
  return -1;
}

// Length of the longest buffer suffix that is a strict prefix of the marker
// sequence — bytes that must be held back in case the next chunk completes it.
function partialPrefixSuffixLength(buffer: Uint8Array): number {
  const max = Math.min(buffer.length, PREFIX.length - 1);
  for (let length = max; length > 0; length -= 1) {
    let matched = true;
    for (let i = 0; i < length; i += 1) {
      if (buffer[buffer.length - length + i] !== PREFIX[i]) {
        matched = false;
        break;
      }
    }
    if (matched) return length;
  }
  return 0;
}

function markerFromPayload(payload: string): Osc133Marker | undefined {
  switch (payload[0]) {
    case 'A':
      return { kind: 'prompt-start' };
    case 'B':
      return { kind: 'input-start' };
    case 'C': {
      let cmdline: string | undefined;
      for (const part of payload.slice(2).split(';')) {
        if (part.startsWith('cmdline_url=')) {
          try {
            cmdline = decodeURIComponent(part.slice('cmdline_url='.length));
          } catch {
            cmdline = undefined;
          }
        } else if (part.startsWith('cmdline=') && cmdline === undefined) {
          cmdline = part.slice('cmdline='.length);
        }
      }
      return { kind: 'pre-exec', cmdline };
    }
    case 'D': {
      const exitCode = Number.parseInt(payload.slice(2), 10);
      return { kind: 'command-end', exitCode: Number.isFinite(exitCode) ? exitCode : undefined };
    }
    default:
      // Unknown subtype: the sequence is still consumed (written through to
      // the terminal, which ignores it) but produces no marker.
      return undefined;
  }
}

export function emptyOsc133State(): Osc133State {
  return { pending: null };
}

export function parseOsc133(state: Osc133State, chunk: Uint8Array): Osc133ParseResult {
  // Fast path: nothing held back and no ESC byte in the chunk.
  if (!state.pending && chunk.indexOf(ESC) === -1) {
    return { state, segments: chunk.length > 0 ? [{ bytes: chunk }] : [] };
  }

  let buffer: Uint8Array;
  if (state.pending && state.pending.length > 0) {
    buffer = new Uint8Array(state.pending.length + chunk.length);
    buffer.set(state.pending, 0);
    buffer.set(chunk, state.pending.length);
  } else {
    buffer = chunk;
  }

  const segments: Osc133Segment[] = [];
  let segmentStart = 0;
  let searchFrom = 0;

  for (;;) {
    const markerStart = indexOfPrefix(buffer, searchFrom);
    if (markerStart === -1) {
      const hold = partialPrefixSuffixLength(buffer);
      const flushEnd = buffer.length - hold;
      if (flushEnd > segmentStart) {
        segments.push({ bytes: buffer.subarray(segmentStart, flushEnd) });
      }
      return {
        state: { pending: hold > 0 ? buffer.slice(flushEnd) : null },
        segments,
      };
    }

    // Find the terminator: BEL or ESC \ (7-bit ST).
    let terminatorEnd = -1;
    for (let i = markerStart + PREFIX.length; i < buffer.length; i += 1) {
      if (buffer[i] === BEL) {
        terminatorEnd = i + 1;
        break;
      }
      if (buffer[i] === ESC && i + 1 < buffer.length && buffer[i + 1] === BACKSLASH) {
        terminatorEnd = i + 2;
        break;
      }
    }
    if (terminatorEnd === -1) {
      if (buffer.length - markerStart > MAX_PENDING_BYTES) {
        // Broken marker: give up and pass everything through.
        segments.push({ bytes: buffer.subarray(segmentStart) });
        return { state: { pending: null }, segments };
      }
      if (markerStart > segmentStart) {
        segments.push({ bytes: buffer.subarray(segmentStart, markerStart) });
      }
      return { state: { pending: buffer.slice(markerStart) }, segments };
    }

    const payloadEnd = buffer[terminatorEnd - 1] === BEL ? terminatorEnd - 1 : terminatorEnd - 2;
    const payload = payloadDecoder.decode(buffer.subarray(markerStart + PREFIX.length, payloadEnd));
    segments.push({
      bytes: buffer.subarray(segmentStart, terminatorEnd),
      marker: markerFromPayload(payload),
    });
    segmentStart = terminatorEnd;
    searchFrom = terminatorEnd;
  }
}
