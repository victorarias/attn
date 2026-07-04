#!/usr/bin/env node

import { spawn } from 'node:child_process';
import { assertPackagedAppBuildMatchesCurrentSource } from './buildPreflight.mjs';
import { emitVerdict } from './common.mjs';
import {
  assertProductionRunAllowed,
  defaultAppPathForProfile,
  defaultWSURLForProfile,
} from './harnessProfile.mjs';
import { resolveScenarios as resolveScenariosFromCatalog, scenarioCatalog } from './scenarioCatalog.mjs';

// Matrix runs against the safe dev install by default — the whole point of the
// profile feature is to iterate on attn-on-attn scenarios without ever taking
// over the live prod app. But honor the one knob: if the shell already selected
// a profile via ATTN_PROFILE (e.g. agent7), let it win so the matrix drives that
// profile's app/daemon. Only pin the dev sibling when NEITHER knob is set; an
// unset/empty ATTN_PROFILE never targets prod by omission (currentHarnessProfile
// falls back to dev). Opt into prod with ATTN_HARNESS_PROFILE= plus
// --run-against-prod. This must happen before any import that reads the env var
// at module-load time.
if (process.env.ATTN_HARNESS_PROFILE === undefined && !process.env.ATTN_PROFILE) {
  process.env.ATTN_HARNESS_PROFILE = 'dev';
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const selected = [];
  let failFast = false;
  let timeoutMs = 120_000;
  let runAgainstProd = false;

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--scenario') {
      selected.push(String(args[++index] || '').trim());
    } else if (arg === '--fail-fast') {
      failFast = true;
    } else if (arg === '--timeout-ms') {
      timeoutMs = Number(args[++index]);
    } else if (arg === '--run-against-prod') {
      runAgainstProd = true;
    } else if (arg === '--help' || arg === '-h') {
      return { help: true, selected: [], failFast: false, timeoutMs: 120_000, runAgainstProd: false };
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new Error(`Invalid --timeout-ms value: ${timeoutMs}`);
  }

  return { help: false, selected, failFast, timeoutMs, runAgainstProd };
}

function printHelp() {
  console.log(`Usage:
  node scripts/real-app-harness/run-serial-matrix.mjs
  node scripts/real-app-harness/run-serial-matrix.mjs --scenario tr205-codex --scenario tr504
  node scripts/real-app-harness/run-serial-matrix.mjs --fail-fast
  node scripts/real-app-harness/run-serial-matrix.mjs --timeout-ms 180000
  ATTN_HARNESS_PROFILE= node scripts/real-app-harness/run-serial-matrix.mjs --run-against-prod

Target: defaults to the dev install (~/Applications/attn-dev.app, port 29849)
  so the matrix never takes over your live prod app. Run \`make dev\` first
  if you haven't built one. Production additionally requires the explicit
  --run-against-prod acknowledgement.

Available scenarios:
${scenarioCatalog.map((scenario) => `  - ${scenario.id}: ${scenario.label}`).join('\n')}
`);
}

function resolveScenarios(selected) {
  return resolveScenariosFromCatalog(selected, scenarioCatalog);
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

function runScenario(scenario, timeoutMs, runAgainstProd) {
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
      stdio: 'inherit',
      env: process.env,
    });
    activeChild = child;
    let timedOut = false;
    // A scenario may declare a larger budget than the matrix default (e.g.
    // multi-shell sweeps); an explicit --timeout-ms still wins when larger.
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
      resolve({
        id: scenario.id,
        label: scenario.label,
        code: timedOut ? 124 : (code ?? (signal ? 1 : 0)),
        signal: signal || null,
        durationMs: Date.now() - startedAt,
        timedOut,
      });
    });
  });
}

async function main() {
  const matrixStartedAt = Date.now();
  const { help, selected, failFast, timeoutMs, runAgainstProd } = parseArgs(process.argv.slice(2));
  if (help) {
    printHelp();
    return;
  }

  const scenarios = resolveScenarios(selected);
  const appPath = process.env.ATTN_REAL_APP_PATH || defaultAppPathForProfile();
  const wsUrl = process.env.ATTN_REAL_APP_WS_URL || defaultWSURLForProfile();
  assertProductionRunAllowed(
    { appPath, wsUrl },
    runAgainstProd ? ['--run-against-prod'] : process.argv.slice(2),
  );
  console.log(`Matrix target: ${appPath} (ATTN_HARNESS_PROFILE=${process.env.ATTN_HARNESS_PROFILE || '<default>'})`);
  const preflightKeys = new Set();
  for (const scenario of scenarios) {
    const preflightLaunchEnv = scenario.preflightLaunchEnv || null;
    const preflightKey = JSON.stringify(preflightLaunchEnv || {});
    if (preflightKeys.has(preflightKey)) {
      continue;
    }
    preflightKeys.add(preflightKey);
    assertPackagedAppBuildMatchesCurrentSource({
      appPath,
      launchEnv: preflightLaunchEnv,
    });
  }
  const results = [];

  for (const scenario of scenarios) {
    console.log(`\n=== ${scenario.label} (${scenario.id}) ===`);
    const result = await runScenario(scenario, timeoutMs, runAgainstProd);
    results.push(result);
    const status = result.code === 0 ? 'ok' : (result.timedOut ? 'timed-out' : 'failed');
    console.log(`--- ${scenario.id}: ${status} (${result.durationMs}ms) ---`);
    if (failFast && result.code !== 0) {
      break;
    }
  }

  const failed = results.filter((result) => result.code !== 0);
  const summary = {
    ok: failed.length === 0,
    scenarioCount: results.length,
    failedCount: failed.length,
    results,
  };
  console.log(`\nSerial matrix summary:\n${JSON.stringify(summary, null, 2)}`);
  emitVerdict({
    ok: failed.length === 0,
    scenarioId: 'serial-matrix',
    runId: '',
    failureCount: failed.length,
    firstFailure: failed.length ? `${failed[0].id} exit ${failed[0].code}` : null,
    artifactsDir: '',
    summaryPath: '',
    durationMs: Date.now() - matrixStartedAt,
  });
  if (failed.length > 0) {
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
