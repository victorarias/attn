const OSC_52_PREFIX = '\x1b]52;';
const MAX_PENDING_OSC_BYTES = 16 * 1024 * 1024;

function pendingPrefixSuffix(text: string): string {
  for (let length = Math.min(text.length, OSC_52_PREFIX.length - 1); length > 0; length -= 1) {
    const suffix = text.slice(-length);
    if (OSC_52_PREFIX.startsWith(suffix)) return suffix;
  }
  return '';
}

export interface Osc52State {
  pending: string;
}

export interface Osc52ParseResult {
  state: Osc52State;
  payloads: string[];
}

export function parseOsc52Writes(state: Osc52State, chunk: string): Osc52ParseResult {
  let buffer = state.pending + chunk;
  const payloads: string[] = [];

  while (buffer.length > 0) {
    const start = buffer.indexOf(OSC_52_PREFIX);
    if (start < 0) {
      return { state: { pending: pendingPrefixSuffix(buffer) }, payloads };
    }
    if (start > 0) buffer = buffer.slice(start);

    const bel = buffer.indexOf('\x07', OSC_52_PREFIX.length);
    const st = buffer.indexOf('\x1b\\', OSC_52_PREFIX.length);
    const end = bel < 0 ? st : st < 0 ? bel : Math.min(bel, st);
    if (end < 0) {
      return {
        state: { pending: buffer.slice(0, MAX_PENDING_OSC_BYTES) },
        payloads,
      };
    }

    const command = buffer.slice(OSC_52_PREFIX.length, end);
    const separator = command.indexOf(';');
    if (separator >= 0) {
      const payload = command.slice(separator + 1);
      if (payload !== '?') payloads.push(payload);
    }
    buffer = buffer.slice(end + (end === st ? 2 : 1));
  }

  return { state: { pending: '' }, payloads };
}
