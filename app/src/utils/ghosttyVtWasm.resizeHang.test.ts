// @vitest-environment node
import { describe, expect, it } from 'vitest';
// @types/node isn't a direct dependency of this package (only a transitive peer
// of vite/vitest, so pnpm doesn't expose its types to tsc here); these three Node
// APIs are the only thing this file needs from it.
// @ts-expect-error -- see above
import { spawn } from 'node:child_process';
// @ts-expect-error -- see above
import { execPath } from 'node:process';
// @ts-expect-error -- see above
import { fileURLToPath } from 'node:url';

// Guards the vendored ghostty-vt wasm against an infinite loop in its resize
// path: at the old pin, hyperlink-heavy output followed by a widen and two
// narrow resizes never returned from ghostty_terminal_resize -- in the app that
// is a frozen pane at 100% CPU, with no trap and so no recovery.
//
// The repro drives the real wasm through the real ghostty-web wrapper and
// watchdogs each step from a worker thread; it must run in its own process
// because a synchronous wasm loop never yields the thread it runs on, so an
// in-process assertion would hang the test runner instead of failing it.
// Exit codes: 0 = every step returned, 1 = hang, 2 = trap/exception.
const reproScript = fileURLToPath(
  new URL('../../scripts/repro-ghostty-vt-resize-hang.mjs', import.meta.url),
);

function runRepro(): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(execPath, [reproScript], { stdio: ['ignore', 'pipe', 'pipe'] });
    let output = '';
    child.stdout.on('data', (chunk: unknown) => {
      output += String(chunk);
    });
    child.stderr.on('data', (chunk: unknown) => {
      output += String(chunk);
    });
    child.on('error', reject);
    child.on('close', (code: number | null) => resolve({ code, output }));
  });
}

describe('ghostty-vt wasm resize', () => {
  it(
    'completes consecutive narrowing resizes after hyperlink-heavy output',
    async () => {
      const { code, output } = await runRepro();

      expect(code, output).toBe(0);
    },
    60_000,
  );
});
