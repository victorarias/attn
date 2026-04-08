import fs from 'node:fs';
import path from 'node:path';
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

export function createScenarioRunner(options, {
  scenarioId,
  tier,
  prefix,
  metadata = {},
} = {}) {
  const { runId, runDir, sessionDir } = createRunContext(options, prefix || scenarioId.toLowerCase());
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
      return finalSummary;
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
