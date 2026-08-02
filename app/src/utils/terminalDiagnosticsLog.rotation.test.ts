// Rotation is enforced in the shared appendToFile, which only runs under Tauri.
// These tests mock the fs plugin and drive the real write path, so they live in
// their own file: those mocks are module-wide and the byte counters are module
// state that each case needs fresh.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createGhosttyModelOpRing, type ModelFaultCapture } from './ghosttyModelOpRing';

const LIFECYCLE_PATH = 'debug/terminal-diagnostics.jsonl';
const INCIDENT_PATH = 'debug/terminal-incidents.jsonl';

const files = new Map<string, string>();
let writeCount = 0;
let notifyWrite: (() => void) | null = null;

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

vi.mock('@tauri-apps/api/core', () => ({ isTauri: () => true }));
vi.mock('@tauri-apps/plugin-fs', () => ({
  BaseDirectory: { AppLocalData: 'AppLocalData' },
  mkdir: async () => {},
  stat: async (path: string) => {
    const contents = files.get(path);
    if (contents === undefined) {
      throw new Error(`no such file: ${path}`);
    }
    return { size: byteLength(contents) };
  },
  writeTextFile: async (path: string, contents: string, options?: { append?: boolean }) => {
    const previous = options?.append ? files.get(path) ?? '' : '';
    files.set(path, previous + contents);
    writeCount += 1;
    notifyWrite?.();
  },
}));

// Resolves once `count` further writes have reached the fake fs. The disk path
// is a promise chain with no external signal, so the writes themselves are what
// we wait on — call this before the action that triggers them.
function afterWrites(count: number): Promise<void> {
  const target = writeCount + count;
  return new Promise((resolve) => {
    notifyWrite = () => {
      if (writeCount >= target) {
        notifyWrite = null;
        resolve();
      }
    };
  });
}

// Fills a file with plausible JSONL to `approxBytes`, within one line's length.
function seedFile(path: string, approxBytes: number): void {
  const line = `${JSON.stringify({ at: 1, kind: 'resize', pane: 'pane-before-rotate' })}\n`;
  files.set(path, line.repeat(Math.floor(approxBytes / byteLength(line))));
}

// Fills a file to exactly `bytes`, as one padded JSONL line, for the cases that
// need the projected size to land on an exact boundary. The pad is ASCII, so
// JSON adds no escapes and the length is predictable — the self-check keeps it
// honest if that ever stops holding.
function seedExactly(path: string, bytes: number): void {
  const shell = (pad: string) =>
    `${JSON.stringify({ at: 1, kind: 'resize', pane: 'pane-before-rotate', pad })}\n`;
  const contents = shell('x'.repeat(bytes - byteLength(shell(''))));
  if (byteLength(contents) !== bytes) {
    throw new Error(`seedExactly produced ${byteLength(contents)} bytes, wanted ${bytes}`);
  }
  files.set(path, contents);
}

// A real capture at the ring's caps: a full 512KB restore snapshot plus 512KB of
// retained writes. This is what an actual model_fault record carries, and the
// reason a rotation decision made after the append can overshoot by megabytes.
function buildModelFaultSizedCapture(): ModelFaultCapture {
  const ring = createGhosttyModelOpRing();
  ring.beginEpoch(80, 24);
  ring.noteRestoreChunk(new Uint8Array(512 * 1024).fill(0x41), 80, 24);
  for (let index = 0; index < 512; index += 1) {
    ring.noteWrite(new Uint8Array(1024).fill(0x42));
  }
  return ring.capture();
}

async function loadModule() {
  vi.resetModules();
  return import('./terminalDiagnosticsLog');
}

describe('diagnostics file rotation', () => {
  beforeEach(() => {
    files.clear();
    writeCount = 0;
    notifyWrite = null;
    window.localStorage.setItem('attn:terminal-diagnostics', '1');
  });

  afterEach(() => {
    // The boundary case stubs Date.now; a leaked stub would freeze time for the
    // rest of the file.
    vi.restoreAllMocks();
  });

  it('rotates before appending, so a near-cap file plus a model_fault capture stays under the cap', async () => {
    const { noteModelFault, FILE_SIZE_CAP_BYTES } = await loadModule();
    seedFile(LIFECYCLE_PATH, FILE_SIZE_CAP_BYTES - 1024);
    const capture = buildModelFaultSizedCapture();

    const written = afterWrites(1);
    noteModelFault('pane-fault', {
      session: 's-1',
      operation: 'render',
      error: 'Out of bounds memory access',
      model: 7,
      rendererEpoch: 2,
      capture,
    });
    await written;

    const contents = files.get(LIFECYCLE_PATH) ?? '';
    expect(byteLength(contents)).toBeLessThanOrEqual(FILE_SIZE_CAP_BYTES);

    // The record really is model-fault-sized: without this the case could decay
    // into a small-line test that the pre-append bug would also pass.
    const lines = contents.split('\n').filter(Boolean);
    expect(byteLength(lines[1] ?? '')).toBeGreaterThan(1024 * 1024);

    // Rotation replaced the file and the capture landed whole in it — neither
    // split across the rotation nor dropped.
    expect(contents).not.toContain('pane-before-rotate');
    expect(lines).toHaveLength(2);
    expect(JSON.parse(lines[0] ?? '{}').kind).toBe('rotate');
    const record = JSON.parse(lines[1] ?? '{}');
    expect(record.kind).toBe('model_fault');
    expect(record.capture).toEqual(capture);
  });

  // The two sides of the comparison itself: filling the cap exactly must append,
  // one byte more must rotate. Both legs need the written line's true length, so
  // a probe write measures it instead of rebuilding the JSON here — the module
  // decides what a record looks like, and a hand-built copy would drift.
  it('appends when the write fills the cap exactly and rotates one byte past it', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(1_700_000_000_000);
    const event = { kind: 'resize', pane: 'pane-boundary' } as const;

    const probe = await loadModule();
    const measured = afterWrites(1);
    probe.recordDiag(event);
    await measured;
    // No file existed, so nothing rotated and the file is exactly the line.
    const lineBytes = byteLength(files.get(LIFECYCLE_PATH) ?? '');
    const { FILE_SIZE_CAP_BYTES } = probe;

    files.clear();
    const atCap = await loadModule();
    seedExactly(LIFECYCLE_PATH, FILE_SIZE_CAP_BYTES - lineBytes);
    let written = afterWrites(1);
    atCap.recordDiag(event);
    await written;

    let contents = files.get(LIFECYCLE_PATH) ?? '';
    expect(contents).not.toContain('"kind":"rotate"');
    expect(contents).toContain('pane-before-rotate');
    expect(byteLength(contents)).toBe(FILE_SIZE_CAP_BYTES);

    files.clear();
    const overCap = await loadModule();
    seedExactly(LIFECYCLE_PATH, FILE_SIZE_CAP_BYTES - lineBytes + 1);
    written = afterWrites(1);
    overCap.recordDiag(event);
    await written;

    contents = files.get(LIFECYCLE_PATH) ?? '';
    expect(contents).not.toContain('pane-before-rotate');
    const lines = contents.split('\n').filter(Boolean);
    expect(JSON.parse(lines[0] ?? '{}').kind).toBe('rotate');
    expect(JSON.parse(lines[1] ?? '{}').pane).toBe('pane-boundary');
    expect(byteLength(contents)).toBeLessThanOrEqual(FILE_SIZE_CAP_BYTES);
  });

  it('applies the same projected-size rule to the incident stream', async () => {
    const { recordPaint, FILE_SIZE_CAP_BYTES } = await loadModule();
    seedFile(INCIDENT_PATH, FILE_SIZE_CAP_BYTES - 64);

    // An under-drawn paint writes a lifecycle marker and the full incident
    // record (which carries ring context, so it is far larger than 64 bytes).
    const written = afterWrites(2);
    recordPaint({
      pane: 'pane-incident',
      session: 's-1',
      cols: 80,
      rows: 24,
      force: false,
      offset: 0,
      modelPrintable: 500,
      quads: 3,
      cellsArrayLen: null,
      skipNull: null,
      skipZeroWidth: null,
    });
    await written;

    const contents = files.get(INCIDENT_PATH) ?? '';
    expect(byteLength(contents)).toBeLessThanOrEqual(FILE_SIZE_CAP_BYTES);
    expect(contents).not.toContain('pane-before-rotate');
    const lines = contents.split('\n').filter(Boolean);
    expect(JSON.parse(lines[0] ?? '{}').kind).toBe('rotate');
    expect(JSON.parse(lines[1] ?? '{}').reason).toBe('paint_underdraw');
  });
});
