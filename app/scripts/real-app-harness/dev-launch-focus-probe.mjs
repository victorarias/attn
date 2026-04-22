#!/usr/bin/env node
// Dev probe (NOT a scenario): verifies that the spawn-with-env launch path
// in UiAutomationClient.launchApp() restores the caller's frontmost app. The
// tr502 scenario is the only production user of this path today — this probe
// exercises the same code without paying for a full remote-harness round
// trip. `dev-` prefix keeps it out of scenario discovery.

// This probe deliberately exercises focus-stealing + caller-restore; opt out
// of the default always-on-top mode so launch behaves like production.
process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';

import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { MacOSDriver } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

async function frontmost() {
  const { stdout } = await execFileAsync('osascript', [
    '-e',
    'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
  ]);
  return stdout.trim();
}

async function activateBundle(id) {
  await execFileAsync('osascript', ['-e', `tell application id "${id}" to activate`]).catch(() => {});
}

async function main() {
  const callerCandidates = ['com.mitchellh.ghostty', 'com.apple.Terminal', 'com.googlecode.iterm2'];
  let callerBundle = null;
  for (const id of callerCandidates) {
    try {
      await execFileAsync('osascript', ['-e', `tell application id "${id}" to launch`]);
      await activateBundle(id);
      await new Promise((resolve) => setTimeout(resolve, 600));
      if ((await frontmost()) === id) {
        callerBundle = id;
        break;
      }
    } catch {}
  }
  if (!callerBundle) {
    throw new Error('Could not get a terminal app to frontmost. Is any installed?');
  }
  console.log(`[probe] caller frontmost=${callerBundle}`);

  // Minimal launchEnv triggers the spawn-with-env path without caring what
  // env vars land in the app process; this probe only checks focus.
  const client = new UiAutomationClient({
    launchEnv: { ATTN_FOCUS_PROBE: '1' },
  });

  await client.quitApp().catch(() => {});
  await activateBundle(callerBundle);
  await new Promise((resolve) => setTimeout(resolve, 400));
  console.log(`[probe] before launch frontmost=${await frontmost()}`);

  // Sample frontmost from a detached child during launch so we catch any
  // transient steal window that launchApp closes before returning.
  const sampler = execFile('node', ['-e', `
    const { execFile } = require('node:child_process');
    const { promisify } = require('node:util');
    const run = promisify(execFile);
    const start = Date.now();
    (async () => {
      while (Date.now() - start < 4000) {
        try {
          const { stdout } = await run('osascript', [
            '-e',
            'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
          ]);
          console.log('[sampler] t=' + (Date.now() - start) + 'ms ' + stdout.trim());
        } catch {}
        await new Promise(r => setTimeout(r, 60));
      }
    })();
  `]);
  sampler.stdout?.pipe(process.stdout);

  await client.launchApp();
  const afterFrontmost = await frontmost();
  console.log(`[probe] after launch frontmost=${afterFrontmost}`);

  await new Promise((resolve) => setTimeout(resolve, 2500));
  sampler.kill('SIGKILL');

  // Sample a few more times — the caller restore lands after the window gate
  // fires, which is ~200ms typical but can race on a loaded machine.
  for (let i = 0; i < 5; i += 1) {
    await new Promise((resolve) => setTimeout(resolve, 200));
    const now = await frontmost();
    console.log(`[probe] T+${(i + 1) * 200}ms frontmost=${now}`);
  }

  const finalFrontmost = await frontmost();
  const attnDriver = new MacOSDriver({ bundleId: 'com.attn.manager' });
  const attnWid = await attnDriver.mainWindowId();
  console.log(`[probe] attn window id after launch=${attnWid}`);
  await client.quitApp().catch(() => {});

  if (finalFrontmost === callerBundle) {
    console.log(`[probe] PASS: caller ${callerBundle} retained frontmost after spawn-with-env launch`);
  } else {
    console.log(`[probe] FAIL: expected ${callerBundle}, got ${finalFrontmost}`);
    process.exitCode = 1;
  }
}

main().catch((err) => {
  console.error('[probe] FAILED');
  console.error(err instanceof Error ? err.stack || err.message : err);
  process.exitCode = 1;
});
