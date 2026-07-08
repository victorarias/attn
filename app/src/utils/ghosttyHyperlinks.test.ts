// @vitest-environment node
import { describe, expect, it } from 'vitest';
// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest, so pnpm doesn't expose its types to tsc here); these two Node
// APIs are the only thing this file needs from it.
// @ts-expect-error -- see above
import { readFileSync } from 'node:fs';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';
import { Ghostty, type GhosttyTerminal } from 'ghostty-web';
import { hyperlinkUriAt, scrollbackHyperlinkUri } from './ghosttyHyperlinks';

// Exercises the real vendored wasm (no mocks) so this stays honest about the
// runtime shape ghosttyHyperlinks.ts reaches into.
const wasmPath = fileURLToPath(new URL('../../vendor/ghostty-vt/ghostty-vt.wasm', import.meta.url));

async function loadTerminal(): Promise<GhosttyTerminal> {
  const bytes = readFileSync(wasmPath);
  const mod = await WebAssembly.compile(bytes);
  let instance: WebAssembly.Instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr: number, len: number) => {
        const memory = (instance.exports.memory as WebAssembly.Memory).buffer;
        console.log('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  const ghostty = new Ghostty(instance);
  return ghostty.createTerminal(80, 24);
}

const OSC8_LINK = '\x1b]8;;https://example.com/x\x1b\\Click me\x1b]8;;\x1b\\';

describe('ghosttyHyperlinks', () => {
  it('reads the OSC 8 hyperlink URI of a cell in the active area', async () => {
    const term = await loadTerminal();
    term.write(OSC8_LINK);
    term.update();

    expect(hyperlinkUriAt(term, 0, 0)).toBe('https://example.com/x');
  });

  it('returns null for a column past the linked label', async () => {
    const term = await loadTerminal();
    term.write(OSC8_LINK);
    term.update();

    expect(hyperlinkUriAt(term, 0, 20)).toBeNull();
  });

  it('returns null for an out-of-range row', async () => {
    const term = await loadTerminal();
    term.write(OSC8_LINK);
    term.update();

    expect(hyperlinkUriAt(term, 999, 0)).toBeNull();
  });

  it('reads the OSC 8 hyperlink URI of a scrollback cell', async () => {
    const term = await loadTerminal();
    term.write(OSC8_LINK);
    for (let i = 0; i < 30; i++) term.write(`line ${i}\r\n`);
    term.update();

    expect(term.getScrollbackLength()).toBeGreaterThan(0);
    expect(scrollbackHyperlinkUri(term, 0, 0)).toBe('https://example.com/x');
  });

  it('returns null for an out-of-range scrollback offset', async () => {
    const term = await loadTerminal();
    term.write(OSC8_LINK);
    term.update();

    expect(scrollbackHyperlinkUri(term, 999, 0)).toBeNull();
  });
});
