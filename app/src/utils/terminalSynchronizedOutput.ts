const SYNC_OUTPUT_START = '\x1b[?2026h';
const SYNC_OUTPUT_END = '\x1b[?2026l';
const MAX_PENDING_SYNC_BYTES = Math.max(SYNC_OUTPUT_START.length, SYNC_OUTPUT_END.length) - 1;

export interface SynchronizedOutputState {
  active: boolean;
  pending: string;
}

export interface SynchronizedOutputParseResult {
  state: SynchronizedOutputState;
  shouldRender: boolean;
}

function pendingPrefixSuffix(text: string): string {
  const maxLength = Math.min(text.length, MAX_PENDING_SYNC_BYTES);
  for (let length = maxLength; length > 0; length -= 1) {
    const suffix = text.slice(-length);
    if (SYNC_OUTPUT_START.startsWith(suffix) || SYNC_OUTPUT_END.startsWith(suffix)) return suffix;
  }
  return '';
}

export function parseSynchronizedOutput(
  state: SynchronizedOutputState,
  chunk: string,
): SynchronizedOutputParseResult {
  let active = state.active;
  let shouldRender = !active;
  let index = 0;
  const buffer = state.pending + chunk;

  while (index < buffer.length) {
    const start = buffer.indexOf(SYNC_OUTPUT_START, index);
    const end = buffer.indexOf(SYNC_OUTPUT_END, index);
    if (start < 0 && end < 0) break;

    if (end >= 0 && (start < 0 || end < start)) {
      active = false;
      shouldRender = true;
      index = end + SYNC_OUTPUT_END.length;
      continue;
    }

    active = true;
    shouldRender = false;
    index = start + SYNC_OUTPUT_START.length;
  }

  if (!active) shouldRender = true;

  return {
    state: {
      active,
      pending: pendingPrefixSuffix(buffer),
    },
    shouldRender,
  };
}
