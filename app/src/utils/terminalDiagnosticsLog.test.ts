import { describe, expect, it } from 'vitest';
import { recordDiag } from './terminalDiagnosticsLog';

describe('terminal diagnostics ring', () => {
  it('keeps the newest events in chronological order after wrapping', () => {
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
    for (let sequence = 0; sequence < 3005; sequence += 1) {
      recordDiag({ kind: 'write', sequence });
    }

    const events = window.__ATTN_TERMINAL_DIAG_DUMP?.() ?? [];
    expect(events).toHaveLength(3000);
    expect(events[0]?.sequence).toBe(5);
    expect(events[events.length - 1]?.sequence).toBe(3004);
  });
});
