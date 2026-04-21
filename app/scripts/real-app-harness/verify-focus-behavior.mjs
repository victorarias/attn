#!/usr/bin/env node
// Dev harness — runs a real-app scenario as a child process while polling
// `NSWorkspace.frontmostApplication` once per second. Reports, per timestamp,
// which bundle identifier is frontmost, and at the end prints a condensed
// transition log ("caller → attn at Xs, attn → caller at Ys").
//
// Usage:
//   node scripts/real-app-harness/verify-focus-behavior.mjs \
//     --scenario tr301
//   node scripts/real-app-harness/verify-focus-behavior.mjs \
//     --command 'pnpm --dir app run real-app:scenario-tr204'
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const ATTN_BUNDLE_ID = 'com.attn.manager';

const scenarioCommands = {
  tr201: ['pnpm', ['--dir', 'app', 'run', 'real-app:scenario-tr201']],
  tr204: ['pnpm', ['--dir', 'app', 'run', 'real-app:scenario-tr204']],
  tr301: ['pnpm', ['--dir', 'app', 'run', 'real-app:scenario-tr301']],
  tr303: ['pnpm', ['--dir', 'app', 'run', 'real-app:scenario-tr303-local-codex']],
  tr401: ['pnpm', ['--dir', 'app', 'run', 'real-app:scenario-tr401']],
  tr402: ['pnpm', ['--dir', 'app', 'run', 'real-app:scenario-tr402-local-claude']],
};

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') args.shift();
  const options = { scenario: null, command: null, intervalMs: 1000 };
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (arg === '--scenario') options.scenario = args[++i];
    else if (arg === '--command') options.command = args[++i];
    else if (arg === '--interval-ms') options.intervalMs = Number(args[++i]);
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }
  return options;
}

async function getFrontmostBundleId() {
  try {
    const { stdout } = await execFileAsync('osascript', [
      '-e',
      'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
    ]);
    return stdout.trim();
  } catch {
    return '';
  }
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    console.log(`Usage:
  node verify-focus-behavior.mjs --scenario <id>
  node verify-focus-behavior.mjs --command '<shell>'

Known scenario ids: ${Object.keys(scenarioCommands).join(', ')}
`);
    return;
  }

  let spawnCmd;
  let spawnArgs;
  if (options.scenario) {
    const entry = scenarioCommands[options.scenario];
    if (!entry) throw new Error(`Unknown scenario id: ${options.scenario}`);
    [spawnCmd, spawnArgs] = entry;
  } else if (options.command) {
    spawnCmd = 'bash';
    spawnArgs = ['-lc', options.command];
  } else {
    throw new Error('Provide --scenario <id> or --command <cmd>');
  }

  const callerBundleId = await getFrontmostBundleId();
  const startedAt = Date.now();
  console.log(`[focus-verify] caller frontmost=${callerBundleId} scenario=${options.scenario || options.command}`);

  const child = spawn(spawnCmd, spawnArgs, { stdio: ['ignore', 'inherit', 'inherit'] });

  const samples = [];
  let lastBundleId = callerBundleId;
  const transitions = [];

  const poller = setInterval(async () => {
    const t = Math.round((Date.now() - startedAt) / 1000);
    const bundleId = await getFrontmostBundleId();
    samples.push({ t, bundleId });
    if (bundleId !== lastBundleId) {
      transitions.push({ t, from: lastBundleId, to: bundleId });
      console.log(`[focus-verify] t=${t}s transition ${lastBundleId || '(none)'} → ${bundleId}`);
      lastBundleId = bundleId;
    }
  }, options.intervalMs);

  const exitCode = await new Promise((resolve) => {
    child.on('exit', (code, signal) => resolve(code ?? (signal ? 1 : 0)));
  });

  clearInterval(poller);
  const finalBundleId = await getFrontmostBundleId();
  const endedAt = Math.round((Date.now() - startedAt) / 1000);

  const attnEverFrontmost = samples.some((s) => s.bundleId === ATTN_BUNDLE_ID);
  const secondsAttnFrontmost = samples.filter((s) => s.bundleId === ATTN_BUNDLE_ID).length;
  const callerRestored = finalBundleId === callerBundleId;

  console.log('');
  console.log(`[focus-verify] scenario exit=${exitCode}`);
  console.log(`[focus-verify] caller before=${callerBundleId}`);
  console.log(`[focus-verify] frontmost after=${finalBundleId}`);
  console.log(`[focus-verify] attn ever frontmost=${attnEverFrontmost} seconds=${secondsAttnFrontmost} of ${endedAt}`);
  console.log(`[focus-verify] caller restored after quit=${callerRestored}`);
  console.log(`[focus-verify] transitions=${transitions.length}`);
  for (const t of transitions) {
    console.log(`  t=${t.t}s ${t.from || '(none)'} → ${t.to}`);
  }

  process.exit(exitCode === 0 && callerRestored ? 0 : exitCode || 2);
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
