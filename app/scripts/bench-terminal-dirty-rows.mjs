// Inspect the vendored Ghostty model's dirty-row contract with representative
// terminal updates. This is intentionally a real-WASM probe: renderer tests use
// fakes and cannot establish which rows libghostty-vt marks dirty.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { Ghostty } from 'ghostty-web';

const APP_ROOT = fileURLToPath(new URL('..', import.meta.url));
const wasm = await WebAssembly.compile(readFileSync(`${APP_ROOT}/vendor/ghostty-vt/ghostty-vt.wasm`));
let instance;
instance = await WebAssembly.instantiate(wasm, {
  env: {
    log: (ptr, len) => {
      const memory = new Uint8Array(instance.exports.memory.buffer, ptr, len);
      console.error(new TextDecoder().decode(memory));
    },
  },
});
const ghostty = new Ghostty(instance);
const terminal = ghostty.createTerminal(20, 6);

function dirtyRows() {
  const state = terminal.update();
  const rows = [];
  for (let row = 0; row < terminal.rows; row += 1) {
    if (terminal.isRowDirty(row)) rows.push(row);
  }
  const printable = terminal.getViewport().slice(0, terminal.cols * terminal.rows)
    .filter((cell) => cell?.codepoint > 32).length;
  terminal.markClean();
  return { state, rows, printable };
}

function write(label, bytes) {
  terminal.write(bytes);
  console.log(JSON.stringify({ label, ...dirtyRows() }));
}

// Establish the initial FULL state and then measure independent updates.
console.log(JSON.stringify({ label: 'initial', ...dirtyRows() }));
write('plain text', 'hello');
write('carriage-return progress', '\rprogress 42%');
write('erase-line progress', '\r\x1b[2Kprogress 43%');
write(
  '4 KiB erase-line progress',
  Array.from({ length: 300 }, (_, index) => `\r\x1b[2Kp ${String(index).padStart(6, '0')} xx`).join('').slice(0, 4096),
);
write('cursor move only', '\x1b[2;4H');
write('overwrite one row', 'updated');
write('erase display', '\x1b[2J');
const denseRows = Array.from({ length: terminal.rows }, (_, row) => (
  `\x1b[${row + 1};1H${`${String(row).padStart(3, '0')} ${'.'.repeat(terminal.cols)}`.slice(0, terminal.cols - 1)}`
)).join('') + '\x1b[1;1H';
write('dense positioned rows', denseRows);
write('dense-row progress', '\r\x1b[2Kp 000001 xx');
write('clear before combined writes', '\x1b[2J');
terminal.write(denseRows);
terminal.write('\r\x1b[2Kp 000002 xx');
console.log(JSON.stringify({ label: 'unpainted seed then progress', ...dirtyRows() }));
write('fill viewport', Array.from({ length: 6 }, (_, row) => `row ${row}\r\n`).join(''));
write('scroll one line', 'scroll\r\n');
write('enter alternate screen', '\x1b[?1049h');
write('alternate-screen text', 'alternate');
write('leave alternate screen', '\x1b[?1049l');

terminal.free();
