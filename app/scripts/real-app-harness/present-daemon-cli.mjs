#!/usr/bin/env node

import { getPresentations, getPresentationRound, submitPresentationRound } from './presentDaemon.mjs';

function printHelp() {
  console.log(`Usage: node scripts/real-app-harness/present-daemon-cli.mjs <get_presentations|get_round|submit_round> [json-payload]

Examples:
  node scripts/real-app-harness/present-daemon-cli.mjs get_presentations
  node scripts/real-app-harness/present-daemon-cli.mjs get_round '{"presentationId":"pres-1"}'
  node scripts/real-app-harness/present-daemon-cli.mjs submit_round '{"presentationId":"pres-1","comments":[{"filepath":"a.go","line_start":1,"line_end":1,"side":"new","content":"nit"}]}'
`);
}

async function main() {
  const args = process.argv.slice(2);
  const command = args.shift();
  if (!command || command === '--help' || command === '-h') {
    printHelp();
    process.exitCode = command ? 0 : 1;
    return;
  }

  const payloadArg = args.shift();
  const payload = payloadArg ? JSON.parse(payloadArg) : {};

  let result;
  switch (command) {
    case 'get_presentations':
      result = await getPresentations({ port: payload.port });
      break;
    case 'get_round': {
      const { presentationId } = payload;
      if (!presentationId) {
        throw new Error('get_round requires "presentationId" in the JSON payload');
      }
      result = await getPresentationRound(presentationId, { port: payload.port, seq: payload.seq });
      break;
    }
    case 'submit_round':
      result = await submitPresentationRound(payload, { port: payload.port });
      break;
    default:
      printHelp();
      process.exitCode = 1;
      return;
  }
  console.log(JSON.stringify(result, null, 2));
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
