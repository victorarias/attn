// Replay a captured Ghostty model fault against the real vendored wasm.
//
// The app keeps a bounded ring of the raw inputs fed to each pane's terminal
// model (app/src/utils/ghosttyModelOpRing.ts). When the model traps, that ring
// is written into the `model_fault` record of
// `$APPLOCALDATA/debug/terminal-diagnostics.jsonl`. This script turns such a
// record back into a running repro: it rebuilds a terminal at the captured
// geometry, applies the attach-restore VT dump the model was built from, then
// replays every write/resize/reset in order.
//
// Usage:
//   node app/scripts/replay-ghostty-model-fault.mjs <diagnostics.jsonl> [index]
//   node app/scripts/replay-ghostty-model-fault.mjs <diagnostics.jsonl> --list
//
//   index      0-based, over the model_fault records that carry a capture.
//              Negative counts from the end. Default -1 (the most recent).
//   --list     print the capturing faults in the file and exit.
//   --watchdog-ms <n>   per-step wall-clock budget (default 5000).
//   --no-update         skip the per-op terminal.update() (the model-side call
//                       the renderer makes; it is where the production
//                       "Out of bounds memory access in update()" fault fired).
//
// Exit codes (same convention as repro-ghostty-vt-resize-hang.mjs):
//   0  every op applied, no fault — the capture did not reproduce
//   1  HANG: a step exceeded the watchdog (wasm-level infinite loop)
//   2  TRAP/EXCEPTION: the model faulted — the capture reproduced
//   3  REFUSED: the record cannot be replayed (see the message)
//   4  usage / input error
//
// A capture whose `snapshotTruncated` is true is refused rather than replayed:
// its restore dump is a prefix of what the model actually received, so the
// replay would start from a different screen and every divergence after it is
// meaningless. That is why the ring marks truncation explicitly.
//
// The wasm is a synchronous, single-threaded blob: a wasm-level infinite loop
// (a known fault mode of this core, see
// docs/plans/2026-07-27-ghostty-wasm-model-crashes.md) cannot be detected from
// the thread running it. The replay therefore runs in a worker_thread the
// parent can terminate.

import { Worker, isMainThread, parentPort, workerData } from 'node:worker_threads';
import { readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';

const APP_ROOT = fileURLToPath(new URL('..', import.meta.url));
const WASM_PATH = `${APP_ROOT}/vendor/ghostty-vt/ghostty-vt.wasm`;

const DEFAULT_WATCHDOG_MS = 5000;
// Mirrors HISTORICAL_REPLAY_CHUNK_BYTES in
// app/src/components/SessionTerminalWorkspace/useGhosttyPaneRuntime.ts: the app
// feeds a restore dump to the model in chunks of this size, and the ring stores
// the dump reassembled, so the replay re-chunks it the same way.
const RESTORE_CHUNK_BYTES = 16 * 1024;

const ENCODER = new TextEncoder();
// DEC mode 2027 (Unicode grapheme clustering). The app enables it at model
// construction and re-asserts it after any RIS — see
// app/src/components/terminalGraphemeMode.ts, which these two helpers mirror.
// The ring captures writes PRE-wrapper, so a faithful replay has to apply the
// same wrapper. `ghosttyModelOpRing.replay.test.ts` pins them together: it
// drives one terminal through the app's real terminalGraphemeMode helper and
// another through this file, and fails if the grids differ.
const GRAPHEME_CLUSTERING_MODE = 2027;
const ENABLE_CLUSTERING = ENCODER.encode('\x1b[?2027h');
const ESC = 0x1b;
const RIS_FINAL = 0x63; // 'c'; ESC c is RIS.

// Mirrors app/src/utils/ghosttyResize.ts: every fit resize disables DEC
// wraparound around the resize to select ghostty's no-reflow path. Those extra
// writes reach the model, so a `noReflow` op must reproduce them.
const DEC_WRAPAROUND_MODE = 7;
const DISABLE_WRAPAROUND = ENCODER.encode('\x1b[?7l');
const ENABLE_WRAPAROUND = ENCODER.encode('\x1b[?7h');

export function writeReassertingClustering(terminal, bytes, trailingEsc) {
  if (bytes.length === 0) return trailingEsc;
  let start = 0;
  if (trailingEsc && bytes[0] === RIS_FINAL) {
    terminal.write(bytes.subarray(0, 1));
    terminal.write(ENABLE_CLUSTERING);
    start = 1;
  }
  for (let i = start; i + 1 < bytes.length; i += 1) {
    if (bytes[i] === ESC && bytes[i + 1] === RIS_FINAL) {
      terminal.write(bytes.subarray(start, i + 2));
      terminal.write(ENABLE_CLUSTERING);
      start = i + 2;
      i += 1;
    }
  }
  if (start < bytes.length) terminal.write(bytes.subarray(start));
  return bytes[bytes.length - 1] === ESC;
}

export function ensureClustering(terminal) {
  if (terminal.getMode(GRAPHEME_CLUSTERING_MODE)) return false;
  terminal.write(ENABLE_CLUSTERING);
  return true;
}

export function resizeNoReflow(terminal, cols, rows) {
  if (!terminal.getMode(DEC_WRAPAROUND_MODE)) {
    terminal.resize(cols, rows);
    return;
  }
  terminal.write(DISABLE_WRAPAROUND);
  try {
    terminal.resize(cols, rows);
  } finally {
    terminal.write(ENABLE_WRAPAROUND);
  }
}

function base64ToBytes(value) {
  return new Uint8Array(Buffer.from(value, 'base64'));
}

/**
 * Turn a `model_fault` record's capture into replay input.
 * Throws when the record cannot be replayed; the message names the field.
 */
export function decodeCapture(capture) {
  if (!capture || typeof capture !== 'object') {
    throw new Error('record has no `capture` — it predates capture-on-fault instrumentation');
  }
  if (capture.snapshotTruncated) {
    throw new Error(
      'capture.snapshotTruncated is true: the restore dump was cut at the ring cap '
      + `(kept ${capture.snapshot?.len ?? 0} bytes, dropped ${capture.snapshot?.dropped ?? 0}), `
      + 'so this record cannot be replayed faithfully',
    );
  }
  const cols = capture.snapshot ? capture.snapshot.cols : capture.startCols;
  const rows = capture.snapshot ? capture.snapshot.rows : capture.startRows;
  if (typeof cols !== 'number' || typeof rows !== 'number' || cols < 1 || rows < 1) {
    throw new Error(`capture has no usable starting geometry (cols=${cols}, rows=${rows})`);
  }
  return {
    cols,
    rows,
    snapshot: capture.snapshot ? base64ToBytes(capture.snapshot.b64) : null,
    ops: (capture.ops || []).map((op) => (
      op.kind === 'write' ? { ...op, bytes: base64ToBytes(op.b64) } : op
    )),
  };
}

/**
 * Apply a decoded capture to a live ghostty terminal, in order.
 * `onStep(label)` is called before each step so a watchdog can name what hung.
 */
export function replayCapture(terminal, decoded, options = {}) {
  const onStep = options.onStep || (() => {});
  const update = options.update !== false;
  let trailingEsc = false;

  onStep('enable grapheme clustering (model construction)');
  terminal.write(ENABLE_CLUSTERING);

  if (decoded.snapshot) {
    const total = decoded.snapshot.length;
    for (let offset = 0; offset < total; offset += RESTORE_CHUNK_BYTES) {
      const chunk = decoded.snapshot.subarray(offset, offset + RESTORE_CHUNK_BYTES);
      onStep(`restore dump ${offset}..${offset + chunk.length} of ${total}`);
      trailingEsc = writeReassertingClustering(terminal, chunk, trailingEsc);
      ensureClustering(terminal);
    }
    if (update) terminal.update();
  }

  decoded.ops.forEach((op, index) => {
    if (op.kind === 'write') {
      onStep(`op ${index} write ${op.bytes.length}B`);
      trailingEsc = writeReassertingClustering(terminal, op.bytes, trailingEsc);
      ensureClustering(terminal);
    } else if (op.kind === 'resize') {
      onStep(`op ${index} resize ${op.cols}x${op.rows}${op.noReflow ? ' [noReflow]' : ''}`);
      if (op.noReflow) {
        resizeNoReflow(terminal, op.cols, op.rows);
      } else {
        terminal.resize(op.cols, op.rows);
      }
    } else {
      // A marker: the app's reset writes RIS through the same write chain, so
      // the reset's effect on the model is the write op recorded right after.
      onStep(`op ${index} reset (marker)`);
      return;
    }
    if (update) terminal.update();
  });
}

export function readCapturingFaults(path) {
  const text = readFileSync(path, 'utf8');
  const records = [];
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    let record;
    try {
      record = JSON.parse(line);
    } catch {
      continue; // a partially written trailing line
    }
    if (record && record.kind === 'model_fault' && record.capture) {
      records.push(record);
    }
  }
  return records;
}

// The four calls replayCapture makes, over the raw wasm exports. The app's own
// binding (app/src/ghostty) is TypeScript and this worker loads plain .mjs with
// no transform, so the CLI path carries its own minimal model; the vitest side
// hands replayCapture the real binding instead.
async function createTerminal(cols, rows) {
  const bytes = readFileSync(WASM_PATH);
  const mod = await WebAssembly.compile(bytes);
  let instance;
  instance = await WebAssembly.instantiate(mod, {
    env: {
      log: (ptr, len) => {
        const memory = instance.exports.memory.buffer;
        console.error('[ghostty-vt]', new TextDecoder().decode(new Uint8Array(memory, ptr, len)));
      },
    },
  });
  const e = instance.exports;
  const dv = () => new DataView(e.memory.buffer);
  const scratch = e.ghostty_wasm_alloc_u8_array(16);

  const out = e.ghostty_wasm_alloc_opaque();
  e.ghostty_terminal_new(0, out, cols, rows);
  const handle = dv().getUint32(out, true);
  e.ghostty_render_state_new(0, out);
  const state = dv().getUint32(out, true);

  return {
    write(data) {
      const payload = typeof data === 'string' ? new TextEncoder().encode(data) : data;
      if (payload.length === 0) return;
      const ptr = e.ghostty_wasm_alloc_u8_array(payload.length);
      new Uint8Array(e.memory.buffer).set(payload, ptr);
      e.ghostty_terminal_vt_write(handle, ptr, payload.length);
      e.ghostty_wasm_free_u8_array(ptr, payload.length);
    },
    resize(nextCols, nextRows) {
      e.ghostty_terminal_resize(handle, nextCols, nextRows);
    },
    update() {
      e.ghostty_render_state_update(state, handle);
    },
    getMode(mode) {
      // GhosttyTerminalModeConfig: u16 mode in, bool value out. The high bit
      // marks an ANSI mode; every mode the replay asks about is DEC private.
      dv().setUint16(scratch, mode & 0x7fff, true);
      dv().setUint8(scratch + 2, 0);
      if (e.ghostty_terminal_get(handle, 37 /* DATA_MODE */, scratch) !== 0) return false;
      return dv().getUint8(scratch + 2) !== 0;
    },
  };
}

async function runInWorker() {
  const decoded = {
    cols: workerData.cols,
    rows: workerData.rows,
    snapshot: workerData.snapshotB64 ? base64ToBytes(workerData.snapshotB64) : null,
    ops: workerData.ops.map((op) => (
      op.kind === 'write' ? { ...op, bytes: base64ToBytes(op.b64) } : op
    )),
  };
  const terminal = await createTerminal(decoded.cols, decoded.rows);
  replayCapture(terminal, decoded, {
    update: workerData.update,
    onStep: (label) => parentPort.postMessage({ type: 'step', label }),
  });
  parentPort.postMessage({ type: 'all-done' });
}

function parseArgs(argv) {
  const args = { path: null, index: -1, list: false, watchdogMs: DEFAULT_WATCHDOG_MS, update: true };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--list') args.list = true;
    else if (arg === '--no-update') args.update = false;
    else if (arg === '--watchdog-ms') { args.watchdogMs = Number(argv[i + 1]); i += 1; }
    else if (arg.startsWith('--')) throw new Error(`unknown flag ${arg}`);
    else if (args.path === null) args.path = arg;
    else args.index = Number(arg);
  }
  if (!args.path) throw new Error('usage: replay-ghostty-model-fault.mjs <diagnostics.jsonl> [index] [--list]');
  if (!Number.isFinite(args.index)) throw new Error('index must be a number');
  if (!Number.isFinite(args.watchdogMs) || args.watchdogMs <= 0) throw new Error('--watchdog-ms must be a positive number');
  return args;
}

function describe(record, position) {
  const capture = record.capture;
  const at = new Date(record.at || 0).toISOString();
  return `[${position}] ${at} pane=${record.pane} op=${record.operation} error=${JSON.stringify(record.error)}`
    + ` grid=${capture.startCols}x${capture.startRows} ops=${capture.opCount}`
    + ` bytes=${capture.retainedWriteBytes} snapshot=${capture.snapshot ? capture.snapshot.len : 0}`
    + `${capture.snapshotTruncated ? ' SNAPSHOT_TRUNCATED' : ''}`
    + `${capture.droppedOps ? ` droppedOps=${capture.droppedOps}` : ''}`
    + `${capture.droppedForRecordBudget ? ` droppedForRecordBudget=${capture.droppedForRecordBudget}` : ''}`;
}

async function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exit(4);
  }

  let records;
  try {
    records = readCapturingFaults(args.path);
  } catch (error) {
    console.error(`cannot read ${args.path}: ${error.message}`);
    process.exit(4);
  }
  if (records.length === 0) {
    console.error(`no model_fault records with a capture in ${args.path}`);
    process.exit(4);
  }
  if (args.list) {
    records.forEach((record, index) => console.log(describe(record, index)));
    process.exit(0);
  }

  const position = args.index < 0 ? records.length + args.index : args.index;
  const record = records[position];
  if (!record) {
    console.error(`index ${args.index} out of range (${records.length} capturing faults)`);
    process.exit(4);
  }
  console.log(describe(record, position));

  let decoded;
  try {
    decoded = decodeCapture(record.capture);
  } catch (error) {
    console.error(`REFUSED: ${error.message}`);
    process.exit(3);
  }

  console.log(
    `replaying ${decoded.ops.length} ops at ${decoded.cols}x${decoded.rows}`
    + `${decoded.snapshot ? ` after a ${decoded.snapshot.length}B restore dump` : ' (no restore dump)'}`,
  );

  const worker = new Worker(new URL(import.meta.url), {
    workerData: {
      cols: decoded.cols,
      rows: decoded.rows,
      snapshotB64: record.capture.snapshot ? record.capture.snapshot.b64 : null,
      ops: record.capture.ops,
      update: args.update,
    },
  });

  let lastLabel = '(not started)';
  let settled = false;
  let watchdog = null;

  const finish = (code, message) => {
    if (settled) return;
    settled = true;
    console.log(message);
    if (watchdog) clearTimeout(watchdog);
    worker.terminate().finally(() => process.exit(code));
  };

  const arm = () => {
    if (watchdog) clearTimeout(watchdog);
    watchdog = setTimeout(() => {
      finish(1, `HANG: step "${lastLabel}" did not return within ${args.watchdogMs}ms (wasm-level infinite loop).`);
    }, args.watchdogMs);
  };
  arm();

  worker.on('message', (msg) => {
    if (msg.type === 'step') {
      lastLabel = msg.label;
      arm();
    } else if (msg.type === 'all-done') {
      finish(0, `NO_FAULT: all ${decoded.ops.length} ops applied cleanly — this capture did not reproduce.`);
    }
  });
  worker.on('error', (err) => {
    finish(2, `TRAP/EXCEPTION at step "${lastLabel}": ${(err && (err.stack || err.message)) || String(err)}`);
  });
  worker.on('exit', (code) => {
    if (settled) return;
    settled = true;
    if (watchdog) clearTimeout(watchdog);
    console.log(`worker exited unexpectedly with code ${code} at step "${lastLabel}"`);
    process.exit(2);
  });
}

// Importing this file (the replay round-trip test does) must not start a run.
if (!isMainThread && workerData && workerData.ops) {
  runInWorker();
} else if (isMainThread && process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
