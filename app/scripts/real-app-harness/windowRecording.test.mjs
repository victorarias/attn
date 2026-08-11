import { EventEmitter } from 'node:events';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createScenarioRecorder, recordingEnabled, startWindowRecording } from './windowRecording.mjs';

const FAKE_RECORDER = '/fake/attn-window-recorder';

class FakeChild extends EventEmitter {
  constructor() {
    super();
    this.stderr = new EventEmitter();
    this.signals = [];
  }

  kill(signal) {
    this.signals.push(signal);
  }
}

function makeSpawnFn() {
  const calls = [];
  const spawnFn = (command, args) => {
    const child = new FakeChild();
    calls.push({ command, args, child });
    return child;
  };
  return { calls, spawnFn };
}

let tmpDir;

beforeEach(() => {
  tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'window-recording-test-'));
});

afterEach(() => {
  fs.rmSync(tmpDir, { recursive: true, force: true });
  vi.useRealTimers();
});

describe('recordingEnabled', () => {
  it('is off by default and for falsy spellings', () => {
    expect(recordingEnabled({})).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: '' })).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: '0' })).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'false' })).toBe(false);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'off' })).toBe(false);
  });

  it('accepts 1/true/on in any case', () => {
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: '1' })).toBe(true);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'true' })).toBe(true);
    expect(recordingEnabled({ ATTN_HARNESS_RECORD: 'ON' })).toBe(true);
  });
});

describe('startWindowRecording', () => {
  it('records the window id and SIGINTs the child on stop', async () => {
    const { calls, spawnFn } = makeSpawnFn();
    const outputPath = path.join(tmpDir, 'segment.mp4');
    const handle = startWindowRecording({ windowId: 42, outputPath, command: FAKE_RECORDER, spawnFn });

    expect(calls).toHaveLength(1);
    expect(calls[0].command).toBe(FAKE_RECORDER);
    expect(calls[0].args).toEqual(['42', outputPath]);

    fs.writeFileSync(outputPath, 'movie-bytes');
    const stopPromise = handle.stop();
    expect(calls[0].child.signals).toEqual(['SIGINT']);
    calls[0].child.emit('exit', 0);

    const result = await stopPromise;
    expect(result).toMatchObject({ windowId: 42, outputPath, exitCode: 0, failure: null });
    expect(result.bytes).toBeGreaterThan(0);
  });

  it('reports a failure when the child exits with no output file', async () => {
    const { calls, spawnFn } = makeSpawnFn();
    const handle = startWindowRecording({
      windowId: 7,
      outputPath: path.join(tmpDir, 'missing.mp4'),
      command: FAKE_RECORDER,
      spawnFn,
    });

    const stopPromise = handle.stop();
    calls[0].child.stderr.emit('data', 'no such window');
    calls[0].child.emit('exit', 1);

    const result = await stopPromise;
    expect(result.bytes).toBe(0);
    expect(result.failure).toContain('no output');
    expect(result.failure).toContain('no such window');
  });

  it('SIGKILLs a child that ignores SIGINT and names the tripwire', async () => {
    vi.useFakeTimers();
    const { calls, spawnFn } = makeSpawnFn();
    const handle = startWindowRecording({
      windowId: 7,
      outputPath: path.join(tmpDir, 'stuck.mp4'),
      command: FAKE_RECORDER,
      spawnFn,
    });

    const stopPromise = handle.stop();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(calls[0].child.signals).toEqual(['SIGINT', 'SIGKILL']);
    calls[0].child.emit('exit', null);

    const result = await stopPromise;
    expect(result.failure).toContain('SIGKILL');
  });

  it('reports a spawn failure instead of throwing', async () => {
    const { calls, spawnFn } = makeSpawnFn();
    const handle = startWindowRecording({
      windowId: 7,
      outputPath: path.join(tmpDir, 'spawn-fail.mp4'),
      command: FAKE_RECORDER,
      spawnFn,
    });

    const stopPromise = handle.stop();
    calls[0].child.emit('error', new Error('ENOENT'));

    const result = await stopPromise;
    expect(result.failure).toContain('failed to spawn');
  });
});

describe('createScenarioRecorder', () => {
  function makeRecorder({ windowIds }) {
    vi.useFakeTimers();
    const { calls, spawnFn } = makeSpawnFn();
    const ids = [...windowIds];
    const logs = [];
    const recorder = createScenarioRecorder({
      runDir: tmpDir,
      resolveWindowId: async () => (ids.length > 1 ? ids.shift() : ids[0]),
      log: (message, details) => logs.push({ message, details }),
      spawnFn,
      commandFn: async () => FAKE_RECORDER,
    });
    return { recorder, calls, logs };
  }

  it('starts a segment when the window appears and keeps it while the id is stable', async () => {
    const { recorder, calls } = makeRecorder({ windowIds: [null, 11, 11] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(3_000);

    expect(calls).toHaveLength(1);
    expect(calls[0].args).toContain('11');
    expect(calls[0].args).toContain(path.join(tmpDir, 'recording-01.mp4'));
  });

  it('rotates to a new segment when the window id changes', async () => {
    const { recorder, calls } = makeRecorder({ windowIds: [11, 22] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(500);
    expect(calls).toHaveLength(1);

    fs.writeFileSync(path.join(tmpDir, 'recording-01.mp4'), 'segment-one');
    const advance = vi.advanceTimersByTimeAsync(1_500);
    await Promise.resolve();
    calls[0].child.emit('exit', 0);
    await advance;

    expect(calls[0].child.signals).toEqual(['SIGINT']);
    expect(calls).toHaveLength(2);
    expect(calls[1].args).toContain('22');
    expect(calls[1].args).toContain(path.join(tmpDir, 'recording-02.mp4'));
  });

  it('finalizes segments on stop and writes the manifest', async () => {
    const { recorder, calls, logs } = makeRecorder({ windowIds: [11] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(500);
    expect(calls).toHaveLength(1);

    fs.writeFileSync(path.join(tmpDir, 'recording-01.mp4'), 'segment-one');
    const stopPromise = recorder.stop();
    await Promise.resolve();
    calls[0].child.emit('exit', 0);
    const segments = await stopPromise;

    expect(segments).toHaveLength(1);
    expect(segments[0].failure).toBeNull();
    const manifest = JSON.parse(fs.readFileSync(path.join(tmpDir, 'recording.json'), 'utf8'));
    expect(manifest.segments).toHaveLength(1);
    expect(logs.some((entry) => entry.message === 'recording:done')).toBe(true);
  });

  it('starts nothing after stop and writes no manifest without a window', async () => {
    const { recorder, calls } = makeRecorder({ windowIds: [null] });
    recorder.start();
    await vi.advanceTimersByTimeAsync(2_000);
    const segments = await recorder.stop();
    await vi.advanceTimersByTimeAsync(2_000);

    expect(calls).toHaveLength(0);
    expect(segments).toHaveLength(0);
    expect(fs.existsSync(path.join(tmpDir, 'recording.json'))).toBe(false);
  });

  it('disables itself when the recorder binary cannot build', async () => {
    vi.useFakeTimers();
    const { calls, spawnFn } = makeSpawnFn();
    const logs = [];
    const recorder = createScenarioRecorder({
      runDir: tmpDir,
      resolveWindowId: async () => 11,
      log: (message, details) => logs.push({ message, details }),
      spawnFn,
      commandFn: async () => {
        throw new Error('swiftc exploded');
      },
    });
    recorder.start();
    await vi.advanceTimersByTimeAsync(3_000);

    expect(calls).toHaveLength(0);
    expect(logs.filter((entry) => entry.message === 'recording:disabled')).toHaveLength(1);
    const segments = await recorder.stop();
    expect(segments).toHaveLength(0);
  });

  it('tolerates resolveWindowId failures and keeps polling', async () => {
    vi.useFakeTimers();
    const { calls, spawnFn } = makeSpawnFn();
    const logs = [];
    let attempts = 0;
    const recorder = createScenarioRecorder({
      runDir: tmpDir,
      resolveWindowId: async () => {
        attempts += 1;
        if (attempts === 1) throw new Error('driver not ready');
        return 33;
      },
      log: (message, details) => logs.push({ message, details }),
      spawnFn,
      commandFn: async () => FAKE_RECORDER,
    });
    recorder.start();
    await vi.advanceTimersByTimeAsync(2_500);

    expect(logs.some((entry) => entry.message === 'recording:poll-error')).toBe(true);
    expect(calls).toHaveLength(1);
    expect(calls[0].args).toContain('33');
  });
});
