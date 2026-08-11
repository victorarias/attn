import { execFile, spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

// Records the app window of a scenario run to mp4 segments in the run's
// artifacts dir, via the compiled WindowRecorder.swift (ScreenCaptureKit
// desktop-independent window capture — see its header for why plain
// `screencapture -v` cannot record the harness's parked windows).
//
// An app relaunch means a new segment against the new window id — hence the
// window-id poll. Recording never fails a scenario: every failure path logs
// and continues.

const execFileAsync = promisify(execFile);
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const RECORDER_SOURCE = path.join(SCRIPT_DIR, 'WindowRecorder.swift');
const RECORDER_BUILD_DIR = path.join(SCRIPT_DIR, '.build');
const RECORDER_BINARY = path.join(RECORDER_BUILD_DIR, 'attn-window-recorder');
const CODESIGN_IDENTITY_SCRIPT = path.resolve(SCRIPT_DIR, '..', '..', '..', 'scripts', 'macos-codesign-identity.sh');

export function recordingEnabled(env = process.env) {
  const value = String(env.ATTN_HARNESS_RECORD ?? '').trim().toLowerCase();
  return value === '1' || value === 'true' || value === 'on';
}

// Same content-hash rebuild and best-effort codesign as InputDriver
// (macosDriver.mjs ensureInputDriver): a stable signature keeps the TCC
// screen-recording grant attached to the binary across rebuilds.
export async function ensureWindowRecorder() {
  fs.mkdirSync(RECORDER_BUILD_DIR, { recursive: true });
  const sourceHash = createHash('sha256').update(fs.readFileSync(RECORDER_SOURCE)).digest('hex');
  const fingerprintPath = `${RECORDER_BINARY}.fingerprint`;
  const builtFromHash = fs.existsSync(fingerprintPath)
    ? fs.readFileSync(fingerprintPath, 'utf8').trim()
    : null;
  if (!fs.existsSync(RECORDER_BINARY) || builtFromHash !== sourceHash) {
    await execFileAsync('/usr/bin/swiftc', ['-O', RECORDER_SOURCE, '-o', RECORDER_BINARY], {
      timeout: 120_000,
    });
    fs.writeFileSync(fingerprintPath, `${sourceHash}\n`);
    if (process.platform === 'darwin' && fs.existsSync(CODESIGN_IDENTITY_SCRIPT)) {
      const { stdout } = await execFileAsync('bash', [CODESIGN_IDENTITY_SCRIPT, 'find'], { timeout: 5_000 });
      const identity = stdout.toString().trim();
      if (identity && identity !== '-') {
        await execFileAsync('/usr/bin/codesign', ['--force', '--sign', identity, RECORDER_BINARY], {
          timeout: 10_000,
        });
      }
    }
  }
  return RECORDER_BINARY;
}

// SIGINT-to-exit was <1s in every measured finalization; 10s only catches a
// recorder that will never finalize, and then SIGKILL abandons the file.
const FINALIZE_TRIPWIRE_MS = 10_000;

export function startWindowRecording({ windowId, outputPath, command, spawnFn = spawn }) {
  const child = spawnFn(command, [String(windowId), outputPath], {
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '';
  child.stderr?.on('data', (chunk) => {
    if (stderr.length < 4096) stderr += chunk;
  });

  const exited = new Promise((resolve) => {
    child.once('error', (error) => resolve({ code: null, spawnError: error }));
    child.once('exit', (code) => resolve({ code, spawnError: null }));
  });

  return {
    windowId,
    outputPath,
    async stop() {
      let forced = false;
      try {
        child.kill('SIGINT');
      } catch {
        // Already dead; `exited` below settles from the buffered event.
      }
      const tripwire = setTimeout(() => {
        forced = true;
        try {
          child.kill('SIGKILL');
        } catch {}
      }, FINALIZE_TRIPWIRE_MS);
      tripwire.unref();
      const { code, spawnError } = await exited;
      clearTimeout(tripwire);

      let bytes = 0;
      try {
        bytes = fs.statSync(outputPath).size;
      } catch {}
      const failure = spawnError
        ? `recorder failed to spawn: ${spawnError.message}`
        : forced
          ? `recorder ignored SIGINT for ${FINALIZE_TRIPWIRE_MS}ms and was SIGKILLed; the file is likely unplayable`
          : bytes === 0
            ? `recorder exited ${code} with no output${stderr ? `: ${stderr.trim()}` : ''}`
            : code !== 0
              ? `recorder exited ${code} leaving a possibly unplayable file${stderr ? `: ${stderr.trim()}` : ''}`
              : null;
      return { windowId, outputPath, bytes, exitCode: code, failure };
    },
  };
}

// Follows a scenario's app window across launches: polls resolveWindowId and
// keeps exactly one recording running against the current window id, rotating
// to a new numbered segment when the id changes (app relaunch) and finalizing
// the old one. stop() is idempotent and safe to call without awaiting — the
// recorder child keeps the event loop alive until it has finalized.
export function createScenarioRecorder({
  runDir,
  resolveWindowId,
  log = () => {},
  pollIntervalMs = 1_000,
  spawnFn = spawn,
  commandFn = ensureWindowRecorder,
}) {
  let active = null;
  let segmentIndex = 0;
  let pollPromise = null;
  let stopped = false;
  const segments = [];
  let timer = null;
  let commandPromise = null;

  const finalizeActive = async () => {
    if (!active) return;
    const handle = active;
    active = null;
    const result = await handle.stop();
    segments.push(result);
    if (result.failure) {
      log('recording:segment-failed', { path: result.outputPath, failure: result.failure });
    } else {
      log('recording:segment', { path: result.outputPath, bytes: result.bytes });
    }
  };

  const poll = async () => {
    if (pollPromise || stopped) return;
    pollPromise = (async () => {
      try {
        commandPromise ??= commandFn();
        let command;
        try {
          command = await commandPromise;
        } catch (error) {
          // A recorder binary that cannot build will never build this run;
          // disable instead of logging a failed compile once per poll.
          log('recording:disabled', { error: error instanceof Error ? error.message : String(error) });
          stopped = true;
          if (timer) {
            clearInterval(timer);
            timer = null;
          }
          return;
        }
        const windowId = await resolveWindowId();
        if (stopped) return;
        if (active && active.windowId === windowId) return;
        await finalizeActive();
        if (windowId && !stopped) {
          segmentIndex += 1;
          const outputPath = path.join(runDir, `recording-${String(segmentIndex).padStart(2, '0')}.mp4`);
          active = startWindowRecording({ windowId, outputPath, command, spawnFn });
          log('recording:start', { path: outputPath, windowId });
        }
      } catch (error) {
        log('recording:poll-error', { error: error instanceof Error ? error.message : String(error) });
      } finally {
        pollPromise = null;
      }
    })();
    await pollPromise;
  };

  return {
    start() {
      if (timer || stopped) return;
      timer = setInterval(poll, pollIntervalMs);
      void poll();
    },
    async stop() {
      if (stopped) return segments;
      stopped = true;
      if (timer) {
        clearInterval(timer);
        timer = null;
      }
      if (pollPromise) {
        await pollPromise.catch(() => {});
      }
      await finalizeActive();
      const usable = segments.filter((s) => !s.failure);
      if (usable.length > 0) {
        try {
          fs.writeFileSync(
            path.join(runDir, 'recording.json'),
            `${JSON.stringify({ segments }, null, 2)}\n`,
            'utf8'
          );
        } catch {}
      }
      log('recording:done', { segments: segments.length, usable: usable.length });
      return segments;
    },
  };
}
