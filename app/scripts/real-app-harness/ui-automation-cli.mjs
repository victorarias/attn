#!/usr/bin/env node

import { UiAutomationClient } from './uiAutomationClient.mjs';

function printHelp() {
  console.log(`Usage: pnpm exec node scripts/real-app-harness/ui-automation-cli.mjs [options] <action> [json-payload]

Options:
  --launch         Launch ~/Applications/attn.app before connecting
  --fresh-launch   Quit and relaunch ~/Applications/attn.app before connecting
  --wait-ready     Wait for the frontend automation hook before sending the action

Examples:
  pnpm exec node scripts/real-app-harness/ui-automation-cli.mjs ping
  pnpm exec node scripts/real-app-harness/ui-automation-cli.mjs --wait-ready get_state
  pnpm exec node scripts/real-app-harness/ui-automation-cli.mjs --wait-ready capture_screenshot '{\"path\":\"/tmp/attn-shot.png\"}'
`);
}

async function main() {
  const args = [...process.argv.slice(2)];
  if (args[0] === '--') {
    args.shift();
  }
  let launch = false;
  let freshLaunch = false;
  let waitReady = false;

  while (args[0]?.startsWith('--')) {
    const flag = args.shift();
    if (flag === '--launch') launch = true;
    else if (flag === '--fresh-launch') freshLaunch = true;
    else if (flag === '--wait-ready') waitReady = true;
    else if (flag === '--help' || flag === '-h') {
      printHelp();
      return;
    } else {
      throw new Error(`Unknown flag: ${flag}`);
    }
  }

  const action = args.shift();
  if (!action) {
    printHelp();
    process.exitCode = 1;
    return;
  }

  const payloadArg = args.shift();
  const payload = payloadArg ? JSON.parse(payloadArg) : {};

  const client = new UiAutomationClient();
  if (freshLaunch) {
    await client.launchFreshApp();
  } else if (launch) {
    await client.launchApp();
  }
  await client.waitForManifest(20_000);
  if (waitReady) {
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
  }
  const result = await client.request(action, payload);
  console.log(JSON.stringify(result, null, 2));
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
