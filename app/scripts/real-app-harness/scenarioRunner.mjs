import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { assertPackagedAppBuildMatchesCurrentSource } from './buildPreflight.mjs';
import { createRunContext } from './common.mjs';

function writeJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function normalizeError(error) {
  if (error instanceof Error) {
    return error.stack || error.message;
  }
  return String(error);
}

function processExists(pid) {
  if (!Number.isInteger(pid) || pid <= 0) {
    return false;
  }

  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function packagedAppScenarioLockPath() {
  return process.env.ATTN_REAL_APP_SCENARIO_LOCK_PATH || path.join(os.tmpdir(), 'attn-real-app-harness-scenario.lock');
}

function readLockOwner(lockDir) {
  const ownerPath = path.join(lockDir, 'owner.json');
  return JSON.parse(fs.readFileSync(ownerPath, 'utf8'));
}

function removeDirIfPresent(dirPath) {
  try {
    fs.rmSync(dirPath, { recursive: true, force: true });
  } catch {}
}

function acquireScenarioLock({ scenarioId, tier, runId, runDir, appPath }) {
  const lockDir = packagedAppScenarioLockPath();
  const ownerPath = path.join(lockDir, 'owner.json');
  const owner = {
    pid: process.pid,
    scenarioId,
    tier,
    runId,
    runDir,
    appPath: appPath || null,
    startedAt: new Date().toISOString(),
    command: process.argv.join(' '),
  };

  while (true) {
    try {
      fs.mkdirSync(lockDir);
      writeJson(ownerPath, owner);
      break;
    } catch (error) {
      if (!error || error.code !== 'EEXIST') {
        throw error;
      }

      let existingOwner = null;
      try {
        existingOwner = readLockOwner(lockDir);
      } catch {
        removeDirIfPresent(lockDir);
        continue;
      }

      if (!processExists(existingOwner?.pid)) {
        removeDirIfPresent(lockDir);
        continue;
      }

      const activeScenario = existingOwner.scenarioId || 'unknown';
      const activePid = Number.isInteger(existingOwner.pid) ? existingOwner.pid : 'unknown';
      const activeRunId = existingOwner.runId || 'unknown';
      const activeStartedAt = existingOwner.startedAt || 'unknown';
      throw new Error(
        `invalid run: packaged-app scenarios are single-tenant; ${activeScenario} is already active ` +
        `(pid ${activePid}, run ${activeRunId}, started ${activeStartedAt})`
      );
    }
  }

  let released = false;
  const release = () => {
    if (released) {
      return;
    }
    released = true;
    try {
      const existingOwner = readLockOwner(lockDir);
      if (existingOwner?.pid === process.pid && existingOwner?.runId === runId) {
        removeDirIfPresent(lockDir);
      }
    } catch {
      removeDirIfPresent(lockDir);
    }
  };

  const signalHandlers = new Map();
  const exitHandler = () => {
    release();
  };
  process.once('exit', exitHandler);

  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
    const handler = () => {
      release();
      process.removeListener(signal, handler);
      process.kill(process.pid, signal);
    };
    signalHandlers.set(signal, handler);
    process.once(signal, handler);
  }

  return () => {
    release();
    process.removeListener('exit', exitHandler);
    for (const [signal, handler] of signalHandlers.entries()) {
      process.removeListener(signal, handler);
    }
  };
}

export function createScenarioRunner(options, {
  scenarioId,
  tier,
  prefix,
  metadata = {},
  preflightLaunchEnv = null,
} = {}) {
  assertPackagedAppBuildMatchesCurrentSource({
    appPath: options?.appPath,
    launchEnv: preflightLaunchEnv,
  });
  const { runId, runDir, sessionDir } = createRunContext(options, prefix || scenarioId.toLowerCase());
  let releaseScenarioLock = null;
  try {
    releaseScenarioLock = acquireScenarioLock({
      scenarioId,
      tier,
      runId,
      runDir,
      appPath: options?.appPath,
    });
  } catch (error) {
    removeDirIfPresent(runDir);
    removeDirIfPresent(sessionDir);
    throw error;
  }
  const tracePath = path.join(runDir, 'trace.log');
  const steps = [];
  const assertions = [];

  const appendTrace = (message, details) => {
    const line = `[${new Date().toISOString()}] ${message}${details ? ` ${JSON.stringify(details)}` : ''}\n`;
    fs.appendFileSync(tracePath, line, 'utf8');
    process.stdout.write(line);
  };

  const runner = {
    scenarioId,
    tier,
    runId,
    runDir,
    sessionDir,
    metadata,
    tracePath,
    steps,
    assertions,
    log(message, details) {
      appendTrace(message, details);
    },
    writeJson(name, value) {
      writeJson(path.join(runDir, name), value);
    },
    writeText(name, value) {
      fs.writeFileSync(path.join(runDir, name), value, 'utf8');
    },
    async step(name, details, fn) {
      const actualDetails = typeof details === 'function' ? null : (details || null);
      const actualFn = typeof details === 'function' ? details : fn;
      if (typeof actualFn !== 'function') {
        throw new Error(`Scenario step ${name} is missing a function`);
      }
      const startedAt = Date.now();
      appendTrace(`step:start ${name}`, actualDetails || undefined);
      const record = {
        name,
        startedAt: new Date(startedAt).toISOString(),
        endedAt: null,
        durationMs: null,
        status: 'running',
        details: actualDetails,
      };
      steps.push(record);
      try {
        const result = await actualFn();
        const endedAt = Date.now();
        record.endedAt = new Date(endedAt).toISOString();
        record.durationMs = endedAt - startedAt;
        record.status = 'ok';
        appendTrace(`step:ok ${name}`, { durationMs: record.durationMs });
        return result;
      } catch (error) {
        const endedAt = Date.now();
        record.endedAt = new Date(endedAt).toISOString();
        record.durationMs = endedAt - startedAt;
        record.status = 'error';
        record.error = normalizeError(error);
        appendTrace(`step:error ${name}`, {
          durationMs: record.durationMs,
          error: normalizeError(error),
        });
        throw error;
      }
    },
    assert(condition, message, details = null) {
      const assertion = {
        ok: Boolean(condition),
        message,
        details,
        at: new Date().toISOString(),
      };
      assertions.push(assertion);
      appendTrace(condition ? 'assert:ok' : 'assert:fail', { message, details });
      if (!condition) {
        throw new Error(message);
      }
    },
    finishSuccess(summary = {}) {
      const finalSummary = {
        ok: true,
        scenarioId,
        tier,
        runId,
        runDir,
        sessionDir,
        metadata,
        steps,
        assertions,
        ...summary,
      };
      writeJson(path.join(runDir, 'summary.json'), finalSummary);
      releaseScenarioLock?.();
      return finalSummary;
    },
    finishFailure(error, summary = {}) {
      const finalSummary = {
        ok: false,
        scenarioId,
        tier,
        runId,
        runDir,
        sessionDir,
        metadata,
        steps,
        assertions,
        error: normalizeError(error),
        ...summary,
      };
      writeJson(path.join(runDir, 'failure.json'), finalSummary);
      releaseScenarioLock?.();
      return finalSummary;
    },
    close() {
      releaseScenarioLock?.();
    },
  };

  runner.writeJson('scenario.json', {
    scenarioId,
    tier,
    runId,
    runDir,
    sessionDir,
    metadata,
  });

  return runner;
}
