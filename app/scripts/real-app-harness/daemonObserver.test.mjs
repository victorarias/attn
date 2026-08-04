import { describe, expect, it } from 'vitest';
import { attachSnapshotText } from './daemonObserver.mjs';

const encoded = (value) => Buffer.from(value, 'utf8').toString('base64');

describe('attachSnapshotText', () => {
  it('reads the server-authoritative VT dump', () => {
    expect(attachSnapshotText({
      snapshot: { vt_dump_b64: encoded('current terminal') },
      scrollback: encoded('obsolete raw scrollback'),
    })).toBe('current terminal');
  });

  it('keeps compatibility with older attach results', () => {
    expect(attachSnapshotText({ scrollback: encoded('legacy terminal') })).toBe('legacy terminal');
  });
});
