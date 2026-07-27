// Minimal deterministic repro for an infinite-loop hang in the vendored
// ghostty-vt wasm's resize path.
//
// Spike artifact of docs/plans/2026-07-27-ghostty-wasm-model-crashes.md.
// Exit 0 is the pass criterion for a fixed wasm build: run this script
// against a candidate ghostty-vt.wasm during the bisect/cherry-pick to
// confirm the fix (exit 1 means the hang still reproduces).
//
// Sequence: create a 59x58 terminal, write one line containing a truncated
// OSC 8 hyperlink + box-drawing + a long run of 'w' that overflows the
// terminal width, then widen to 69 cols, narrow to 68, narrow to 67. The
// THIRD call (the second consecutive narrow, 68->67) never returns: 100% CPU,
// no exception, no trap signal -- a true infinite loop inside
// ghostty_terminal_resize (or something it calls), distinct from the
// `unreachable`/OOB traps seen in production from ghostty_terminal_write and
// the renderer's update(). Confirmed root requirements (see accompanying
// report/plan doc):
//   - a widen of at least +10 cols (59->69; +9 does not reproduce)
//   - followed by two separate narrow-by-1 resize() calls (a single narrow
//     after the widen does not reproduce)
//   - specific write content: plain ASCII alone does not reproduce; the
//     OSC 8 fragment + box-drawing + overflowing 'w' run does
//
// Discrimination result: reproduces with BOTH plain term.resize() (as used
// here) AND the app's mode-7 no-reflow wrapper from
// app/src/utils/ghosttyResize.ts (write('\x1b[?7l') -> resize() ->
// write('\x1b[?7h')) -- confirmed by wrapping the same three resize calls in
// that dance and observing the identical hang at the identical step. So this
// is not specific to either of the app's two resize call sites
// (resizeGhosttyWithoutReflow vs. plain resizeLocal); both reach the same
// wasm-level infinite loop.
//
// Loads the wasm exactly like app/src/utils/ghosttyHyperlinks.test.ts. Since
// the hang is a synchronous, wasm-level infinite loop, it cannot be detected
// from the same thread it runs on (the event loop never gets control back).
// This script runs the repro in a worker_thread so the parent can apply a
// wall-clock watchdog and terminate() the worker if a step never reports
// back -- worker termination works even mid-blocking-synchronous-call.
//
// Exit codes: 0 = no fault (all steps completed); 1 = HANG (a step exceeded
// the watchdog budget); 2 = unexpected exception/trap while running a step.

import { Worker, isMainThread, parentPort } from 'node:worker_threads';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const APP_ROOT = fileURLToPath(new URL('..', import.meta.url));
const WASM_PATH = `${APP_ROOT}/vendor/ghostty-vt/ghostty-vt.wasm`;
const GHOSTTY_WEB_ENTRY = `file://${APP_ROOT}/node_modules/ghostty-web/dist/ghostty-web.js`;

// The exact minimized payload (truncated OSC 8, box drawing, overflowing
// 'w' run). Do not "clean up" the malformed-looking OSC 8 fragments -- they
// are load-bearing; plain ASCII of the same length does not reproduce.
const PAYLOAD =
  '\x1b]8;;/\x1bine 31 ┌──┐│\x1b]8;;2\x1b\\entry 32\x1b]8;;7\x1b\\entry ' + 'w'.repeat(104);

const WATCHDOG_MS = 2500;

if (isMainThread) {
  main();
} else {
  runInWorker();
}

async function runInWorker() {
  const { Ghostty } = await import(GHOSTTY_WEB_ENTRY);
  const bytes = readFileSync(WASM_PATH);
  const mod = await WebAssembly.compile(bytes);
  const instance = await WebAssembly.instantiate(mod, { env: { log: () => {} } });
  const ghostty = new Ghostty(instance);
  const term = ghostty.createTerminal(59, 58);

  const steps = [
    ['write payload', () => term.write(PAYLOAD)],
    ['resize(69, 58) [widen]', () => term.resize(69, 58)],
    ['resize(68, 58) [narrow #1]', () => term.resize(68, 58)],
    ['resize(67, 58) [narrow #2 -- expected to hang]', () => term.resize(67, 58)],
  ];

  for (const [label, fn] of steps) {
    parentPort.postMessage({ type: 'starting', label });
    fn();
    parentPort.postMessage({ type: 'done', label });
  }
  parentPort.postMessage({ type: 'all-done' });
}

async function main() {
  const worker = new Worker(new URL(import.meta.url), { workerData: {} });
  let lastLabel = '(not started)';
  let settled = false;

  const finish = (code, message) => {
    if (settled) return;
    settled = true;
    console.log(message);
    clearTimeout(watchdog);
    worker.terminate().finally(() => process.exit(code));
  };

  let watchdog = setTimeout(() => {
    finish(1, `HANG: step "${lastLabel}" did not return within ${WATCHDOG_MS}ms (wasm-level infinite loop). This is expected -- confirms the repro.`);
  }, WATCHDOG_MS);

  worker.on('message', (msg) => {
    if (msg.type === 'starting') {
      lastLabel = msg.label;
      console.log('>>> ' + msg.label);
      clearTimeout(watchdog);
      watchdog = setTimeout(() => {
        finish(1, `HANG: step "${lastLabel}" did not return within ${WATCHDOG_MS}ms (wasm-level infinite loop). This is expected -- confirms the repro.`);
      }, WATCHDOG_MS);
    } else if (msg.type === 'done') {
      console.log('<<< ' + msg.label);
    } else if (msg.type === 'all-done') {
      finish(0, 'NO_HANG: all steps completed -- repro did not reproduce this run.');
    }
  });

  worker.on('error', (err) => {
    finish(2, 'EXCEPTION/TRAP: ' + (err && (err.stack || err.message || String(err))));
  });

  worker.on('exit', (code) => {
    if (!settled) {
      settled = true;
      clearTimeout(watchdog);
      console.log(`worker exited unexpectedly with code ${code}`);
      process.exit(2);
    }
  });
}
