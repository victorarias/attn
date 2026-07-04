#!/usr/bin/env node

// Runs one packaged-app scenario N times, strictly serially (the packaged app
// is single-tenant — never parallelize), parses each run's ATTN_VERDICT line,
// and ends with its own aggregate ATTN_VERDICT line + JSON report. This turns
// "loop a scenario 30 times by hand" into one background command.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { assertPackagedAppBuildMatchesCurrentSource } from './buildPreflight.mjs';
import { ATTN_VERDICT_PREFIX, createRunContext, emitVerdict, ensureDir } from './common.mjs';
import {
  assertProductionRunAllowed,
  defaultAppPathForProfile,
  defaultWSURLForProfile,
} from './harnessProfile.mjs';
import { resolveScenario } from './scenarioCatalog.mjs';

// Same profile-pinning rule as run-serial-matrix.mjs: default to the safe dev
// sibling unless the shell (or an explicit override) already picked a
// profile. This must run before any import that reads the env var.
if (process.env.ATTN_HARNESS_PROFILE === undefined && !process.env.ATTN_PROFILE) {
  process.env.ATTN_HARNESS_PROFILE = 'dev';
}

const DEFAULT_TIMEOUT_MS = 120_000;
const DEFAULT_REPEAT = 10;

export function parseVerdictFromOutput(stdoutText) {
  if (typeof stdoutText !== 'string' || stdoutText.length === 0) {
    return null;
  }
  const lines = stdoutText.split('\n');
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const line = lines[index];
    if (!line.startsWith(ATTN_VERDICT_PREFIX)) {
      continue;
    }
    const payload = line.slice(ATTN_VERDICT_PREFIX.length);
    try {
      return JSON.parse(payload);
    } catch {
      return null;
    }
  }
  return null;
}

export function isRunFailure(record) {
  return (
    record.exitCode !== 0 ||
    record.timedOut === true ||
    record.verdict === null ||
    record.verdict === undefined ||
    record.verdict.ok === false
  );
}

export function summarizeSoak(records, { scenarioId, runDir, summaryPath, durationMs }) {
  const failed = records.filter((record) => isRunFailure(record));
  let firstFailure = null;
  if (failed.length > 0) {
    const first = failed[0];
    if (first.verdict && first.verdict.firstFailure) {
      firstFailure = first.verdict.firstFailure;
    } else {
      firstFailure = `iteration ${first.iteration} exit ${first.exitCode}`;
    }
  }
  return {
    ok: failed.length === 0,
    scenarioId: `soak:${scenarioId}`,
    runId: path.basename(runDir),
    failureCount: failed.length,
    firstFailure,
    artifactsDir: runDir,
    summaryPath,
    durationMs,
  };
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  let scenarioId = null;
  let repeat = DEFAULT_REPEAT;
  let untilViolation = false;
  let timeoutMs = DEFAULT_TIMEOUT_MS;
  let runAgainstProd = false;

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--scenario') {
      scenarioId = String(args[++index] || '').trim();
    } else if (arg === '--repeat') {
      repeat = Number(args[++index]);
    } else if (arg === '--until-violation') {
      untilViolation = true;
    } else if (arg === '--timeout-ms') {
      timeoutMs = Number(args[++index]);
    } else if (arg === '--run-against-prod') {
      runAgainstProd = true;
    } else if (arg === '--help' || arg === '-h') {
      return { help: true };
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  if (!scenarioId) {
    throw new Error('Missing required argument: --scenario <id>');
  }
  if (!Number.isFinite(repeat) || repeat <= 0 || !Number.isInteger(repeat)) {
    throw new Error(`Invalid --repeat value: ${repeat}`);
  }
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new Error(`Invalid --timeout-ms value: ${timeoutMs}`);
  }

  return { help: false, scenarioId, repeat, untilViolation, timeoutMs, runAgainstProd };
}

function printHelp() {
  console.log(`Usage:
  node scripts/real-app-harness/run-soak.mjs --scenario <id> --repeat 10
  node scripts/real-app-harness/run-soak.mjs --scenario <id> --repeat 30 --until-violation
  node scripts/real-app-harness/run-soak.mjs --scenario <id> --timeout-ms 180000
  ATTN_HARNESS_PROFILE= node scripts/real-app-harness/run-soak.mjs --scenario <id> --run-against-prod

Runs one packaged-app scenario repeatedly, strictly serially (the packaged
app is single-tenant — never parallelized), and reports an aggregate verdict.

Options:
  --scenario <id>       Required. Scenario id from run-serial-matrix.mjs's catalog.
  --repeat <n>           Number of iterations (default: ${DEFAULT_REPEAT}).
  --until-violation      Stop at the first failing run instead of running all --repeat iterations.
  --timeout-ms <n>       Per-run timeout in ms (default: ${DEFAULT_TIMEOUT_MS}).
  --run-against-prod     Explicitly allow targeting the production app.

Target: defaults to the dev install (~/Applications/attn-dev.app, port 29849)
  so the soak never takes over your live prod app.
`);
}

const signalExitCode = {
  SIGINT: 130,
  SIGTERM: 143,
  SIGHUP: 129,
};
let activeChild = null;
let interruptHandled = false;

function terminateActiveChild(signal) {
  if (!activeChild || activeChild.killed) {
    return;
  }
  activeChild.kill(signal);
  setTimeout(() => {
    if (activeChild && !activeChild.killed) {
      activeChild.kill('SIGKILL');
    }
  }, 5_000).unref();
}

for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.once(signal, () => {
    if (interruptHandled) {
      return;
    }
    interruptHandled = true;
    terminateActiveChild(signal);
    if (!activeChild) {
      process.exit(signalExitCode[signal] || 1);
      return;
    }
    activeChild.once('exit', () => {
      process.exit(signalExitCode[signal] || 1);
    });
  });
}

function runIteration(scenario, iteration, timeoutMs, runAgainstProd) {
  return new Promise((resolve) => {
    const startedAt = Date.now();
    const childArgs = [...scenario.command.slice(1)];
    if (runAgainstProd) {
      if (!childArgs.includes('--')) {
        childArgs.push('--');
      }
      childArgs.push('--run-against-prod');
    }
    const child = spawn(scenario.command[0], childArgs, {
      cwd: process.cwd(),
      stdio: ['inherit', 'pipe', 'pipe'],
      env: process.env,
    });
    activeChild = child;
    let stdoutBuffer = '';
    child.stdout.on('data', (chunk) => {
      stdoutBuffer += chunk.toString();
      process.stdout.write(chunk);
    });
    child.stderr.on('data', (chunk) => {
      process.stderr.write(chunk);
    });

    let timedOut = false;
    const effectiveTimeoutMs = Math.max(timeoutMs, scenario.timeoutMs ?? 0);
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGTERM');
      setTimeout(() => child.kill('SIGKILL'), 5_000).unref();
    }, effectiveTimeoutMs);

    child.on('exit', (code, signal) => {
      clearTimeout(timer);
      if (activeChild === child) {
        activeChild = null;
      }
      const exitCode = timedOut ? 124 : (code ?? (signal ? 1 : 0));
      resolve({
        iteration,
        exitCode,
        signal: signal || null,
        timedOut,
        durationMs: Date.now() - startedAt,
        verdict: parseVerdictFromOutput(stdoutBuffer),
      });
    });
  });
}

async function main() {
  const soakStartedAt = Date.now();
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printHelp();
    return;
  }

  const { scenarioId, repeat, untilViolation, timeoutMs, runAgainstProd } = options;
  const scenario = resolveScenario(scenarioId);

  const appPath = process.env.ATTN_REAL_APP_PATH || defaultAppPathForProfile();
  const wsUrl = process.env.ATTN_REAL_APP_WS_URL || defaultWSURLForProfile();
  assertProductionRunAllowed(
    { appPath, wsUrl },
    runAgainstProd ? ['--run-against-prod'] : process.argv.slice(2),
  );
  console.log(`Soak target: ${appPath} (ATTN_HARNESS_PROFILE=${process.env.ATTN_HARNESS_PROFILE || '<default>'})`);
  assertPackagedAppBuildMatchesCurrentSource({ appPath, launchEnv: scenario.preflightLaunchEnv || null });

  const artifactsRoot = process.env.ATTN_REAL_APP_ARTIFACTS_DIR || path.join(os.tmpdir(), 'attn-real-app-harness');
  ensureDir(artifactsRoot);
  const { runDir } = createRunContext({ artifactsDir: artifactsRoot, sessionRootDir: artifactsRoot }, `soak-${scenarioId}`);

  const records = [];
  for (let iteration = 1; iteration <= repeat; iteration += 1) {
    console.log(`\n=== soak ${scenario.id} iteration ${iteration}/${repeat} ===`);
    const record = await runIteration(scenario, iteration, timeoutMs, runAgainstProd);
    records.push(record);
    const failed = isRunFailure(record);
    console.log(`--- iteration ${iteration}: ${failed ? 'failed' : 'ok'} (${record.durationMs}ms) ---`);
    if (untilViolation && failed) {
      break;
    }
  }

  const summaryPath = path.join(runDir, 'soak-report.json');
  const report = {
    scenarioId,
    repeatRequested: repeat,
    untilViolation,
    runs: records,
  };
  fs.writeFileSync(summaryPath, JSON.stringify(report, null, 2));
  console.log(`\nSoak report:\n${JSON.stringify(report, null, 2)}`);

  const verdict = summarizeSoak(records, {
    scenarioId,
    runDir,
    summaryPath,
    durationMs: Date.now() - soakStartedAt,
  });
  emitVerdict(verdict);
  if (!verdict.ok) {
    process.exitCode = 1;
  }
}

// Only run the CLI entrypoint when this file is executed directly — the pure
// helpers above (parseVerdictFromOutput, isRunFailure, summarizeSoak) are
// imported directly by runSoak.test.mjs, and importing must not trigger a
// real soak run or process.exit.
const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1]);
if (isMainModule) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
}
