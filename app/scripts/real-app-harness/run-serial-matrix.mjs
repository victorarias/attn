#!/usr/bin/env node

import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { assertPackagedAppBuildMatchesCurrentSource } from './buildPreflight.mjs';

const scenarioCatalog = [
  {
    id: 'tr205-codex',
    label: 'TR-205 remote codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr205'],
    preflightLaunchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
    },
  },
  {
    id: 'tr205-claude',
    label: 'TR-205 remote claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr205', '--', '--remote-agent', 'claude'],
    preflightLaunchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
    },
  },
  {
    id: 'tr502',
    label: 'TR-502 remote relaunch splits',
    command: ['pnpm', 'run', 'real-app:scenario-tr502'],
    preflightLaunchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
    },
  },
  {
    id: 'tr504',
    label: 'TR-504 remote cleanup',
    command: ['pnpm', 'run', 'real-app:scenario-tr504'],
    preflightLaunchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
    },
  },
  {
    id: 'tr402-local-codex',
    label: 'TR-402 local codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-codex'],
  },
  {
    id: 'tr402-local-claude',
    label: 'TR-402 local claude',
    command: ['pnpm', 'run', 'real-app:scenario-tr402-local-claude'],
  },
  {
    id: 'tr201-local-claude',
    label: 'TR-201 local claude existing split relaunch',
    command: ['pnpm', 'run', 'real-app:scenario-tr201'],
  },
  {
    id: 'tr204-local-claude',
    label: 'TR-204 local claude relaunch formatting',
    command: ['pnpm', 'run', 'real-app:scenario-tr204'],
  },
  {
    id: 'tr301-local-claude',
    label: 'TR-301 local claude utility focus',
    command: ['pnpm', 'run', 'real-app:scenario-tr301'],
  },
  {
    id: 'tr401-local-claude',
    label: 'TR-401 local claude resize',
    command: ['pnpm', 'run', 'real-app:scenario-tr401'],
  },
  {
    id: 'tr303-local-codex',
    label: 'TR-303 local codex',
    command: ['pnpm', 'run', 'real-app:scenario-tr303-local-codex'],
  },
];

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const selected = [];
  let failFast = false;
  let timeoutMs = 120_000;

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--scenario') {
      selected.push(String(args[++index] || '').trim());
    } else if (arg === '--fail-fast') {
      failFast = true;
    } else if (arg === '--timeout-ms') {
      timeoutMs = Number(args[++index]);
    } else if (arg === '--help' || arg === '-h') {
      return { help: true, selected: [], failFast: false, timeoutMs: 120_000 };
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }

  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    throw new Error(`Invalid --timeout-ms value: ${timeoutMs}`);
  }

  return { help: false, selected, failFast, timeoutMs };
}

function printHelp() {
  console.log(`Usage:
  node scripts/real-app-harness/run-serial-matrix.mjs
  node scripts/real-app-harness/run-serial-matrix.mjs --scenario tr205-codex --scenario tr504
  node scripts/real-app-harness/run-serial-matrix.mjs --fail-fast
  node scripts/real-app-harness/run-serial-matrix.mjs --timeout-ms 180000

Available scenarios:
${scenarioCatalog.map((scenario) => `  - ${scenario.id}: ${scenario.label}`).join('\n')}
`);
}

function resolveScenarios(selected) {
  if (!selected.length) {
    return scenarioCatalog;
  }
  const byId = new Map(scenarioCatalog.map((scenario) => [scenario.id, scenario]));
  return selected.map((id) => {
    const scenario = byId.get(id);
    if (!scenario) {
      throw new Error(`Unknown scenario id: ${id}`);
    }
    return scenario;
  });
}

function runScenario(scenario, timeoutMs) {
  return new Promise((resolve) => {
    const startedAt = Date.now();
    const child = spawn(scenario.command[0], scenario.command.slice(1), {
      cwd: process.cwd(),
      stdio: 'inherit',
      env: process.env,
    });
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGTERM');
      setTimeout(() => child.kill('SIGKILL'), 5_000).unref();
    }, timeoutMs);
    child.on('exit', (code, signal) => {
      clearTimeout(timer);
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
  const { help, selected, failFast, timeoutMs } = parseArgs(process.argv.slice(2));
  if (help) {
    printHelp();
    return;
  }

  const scenarios = resolveScenarios(selected);
  const appPath = process.env.ATTN_REAL_APP_PATH || path.join(os.homedir(), 'Applications', 'attn.app');
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
    const result = await runScenario(scenario, timeoutMs);
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
  if (failed.length > 0) {
    process.exitCode = 1;
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
