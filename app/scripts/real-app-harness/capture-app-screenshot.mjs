#!/usr/bin/env node

import { UiAutomationClient } from './uiAutomationClient.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';

function printHelp() {
  console.log(`Usage: node scripts/real-app-harness/capture-app-screenshot.mjs [options]

Options:
  --path <file>      Save a native macOS window screenshot to an explicit path
  --launch           Launch the selected packaged app before capturing
  --fresh-launch     Quit and relaunch the selected packaged app before capturing
  --crop x,y,WxH     Crop to a window-relative region (also accepts x,y,w,h)
  --max-dim N        Downscale the PNG in place if its larger side exceeds N px
  --run-against-prod Explicitly allow targeting the production app
  --help, -h         Show this help

Examples:
  node scripts/real-app-harness/capture-app-screenshot.mjs
  node scripts/real-app-harness/capture-app-screenshot.mjs --path /tmp/attn.png
  node scripts/real-app-harness/capture-app-screenshot.mjs --crop 0,0,800x600 --max-dim 1024
  make app-screenshot SCREENSHOT_PATH=/tmp/attn.png
`);
}

// Parses a crop spec string into a window-relative {x, y, width, height} rect.
// Accepts "x,y,WxH" (e.g. "0,0,800x600") and the all-comma form "x,y,w,h".
export function parseCropSpec(str) {
  if (typeof str !== 'string' || str.trim() === '') {
    throw new Error(`Invalid --crop value: ${JSON.stringify(str)}`);
  }

  const parts = str.split(',').map((value) => value.trim());
  let x;
  let y;
  let width;
  let height;

  if (parts.length === 3) {
    const [xPart, yPart, sizePart] = parts;
    const sizeMatch = /^(\d+(?:\.\d+)?)x(\d+(?:\.\d+)?)$/.exec(sizePart || '');
    if (!sizeMatch) {
      throw new Error(`Invalid --crop value: ${JSON.stringify(str)}`);
    }
    x = Number(xPart);
    y = Number(yPart);
    width = Number(sizeMatch[1]);
    height = Number(sizeMatch[2]);
  } else if (parts.length === 4) {
    [x, y, width, height] = parts.map(Number);
  } else {
    throw new Error(`Invalid --crop value: ${JSON.stringify(str)}`);
  }

  if (![x, y, width, height].every(Number.isFinite) || width <= 0 || height <= 0) {
    throw new Error(`Invalid --crop value: ${JSON.stringify(str)}`);
  }

  return { x, y, width, height };
}

async function main() {
  const args = [...process.argv.slice(2)];
  let launch = false;
  let freshLaunch = false;
  let outputPath = '';
  let crop = null;
  let maxDim = null;

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
    if (arg === '--run-against-prod') {
      // The shared UiAutomationClient guard reads this explicit acknowledgement.
      continue;
    }
    if (arg === '--path') {
      outputPath = args.shift() || '';
      if (!outputPath) {
        throw new Error('--path requires a value');
      }
      continue;
    }
    if (arg === '--crop') {
      const value = args.shift() || '';
      if (!value) {
        throw new Error('--crop requires a value');
      }
      crop = parseCropSpec(value);
      continue;
    }
    if (arg === '--max-dim') {
      const value = args.shift() || '';
      if (!value || !/^\d+$/.test(value)) {
        throw new Error('--max-dim requires a positive integer value');
      }
      maxDim = Number.parseInt(value, 10);
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

  const targetPath = outputPath || '/tmp/attn-app-window.png';
  const result = await captureFrontWindowScreenshot(targetPath, { client, crop, maxDim });
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
