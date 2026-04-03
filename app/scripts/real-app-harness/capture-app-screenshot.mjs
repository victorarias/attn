#!/usr/bin/env node

import { UiAutomationClient } from './uiAutomationClient.mjs';

function printHelp() {
  console.log(`Usage: node scripts/real-app-harness/capture-app-screenshot.mjs [options]

Options:
  --path <file>      Save screenshot to an explicit path
  --launch           Launch ~/Applications/attn.app before capturing
  --fresh-launch     Quit and relaunch ~/Applications/attn.app before capturing
  --help, -h         Show this help

Examples:
  node scripts/real-app-harness/capture-app-screenshot.mjs
  node scripts/real-app-harness/capture-app-screenshot.mjs --path /tmp/attn.png
  make app-screenshot SCREENSHOT_PATH=/tmp/attn.png
`);
}

async function main() {
  const args = [...process.argv.slice(2)];
  let launch = false;
  let freshLaunch = false;
  let outputPath = '';

  while (args.length > 0) {
    const arg = args.shift();
    if (arg === '--launch') {
      launch = true;
      continue;
    }
    if (arg === '--fresh-launch') {
      freshLaunch = true;
      continue;
    }
    if (arg === '--path') {
      outputPath = args.shift() || '';
      if (!outputPath) {
        throw new Error('--path requires a value');
      }
      continue;
    }
    if (arg === '--help' || arg === '-h') {
      printHelp();
      return;
    }
    throw new Error(`Unknown argument: ${arg}`);
  }

  const client = new UiAutomationClient();
  if (freshLaunch) {
    await client.launchFreshApp();
  } else if (launch) {
    await client.launchApp();
  }
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);

  const payload = outputPath ? { path: outputPath } : {};
  const result = await client.request('capture_screenshot', payload, { timeoutMs: 20_000 });
  console.log(JSON.stringify(result, null, 2));
}

main().catch((error) => {
  const message = error instanceof Error ? error.stack || error.message : String(error);
  if (message.includes('screencapture exited with status')) {
    console.error(`${message}\nHint: enable macOS Screen Recording permission for attn.app and the terminal you are invoking this from, then retry.`);
  } else {
    console.error(message);
  }
  process.exitCode = 1;
});
