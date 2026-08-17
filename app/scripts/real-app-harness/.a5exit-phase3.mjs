#!/usr/bin/env node
// A5 exit proof, phase 3: break the view on purpose — the boundary holds, the
// terminal beside it still runs — then fix it and the tile comes back.
import { readFileSync, writeFileSync } from 'fs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const delay = (ms) => new Promise((r) => setTimeout(r, ms));
const VIEW = '/tmp/a5exit-proof/src/views/Sessions.tsx';
const SESSION = '24ddeb2a-f453-4667-8bac-7cf6f6c907bc';
const WORKSPACE = `workspace-${SESSION}`;
const THROW = `  if (Math.random() < 2) {
    throw new Error("exit-proof broke this tile on purpose")
  }
`;

async function hostState(client) {
  const st = await client.request('app_view_get_state', { workspaceId: WORKSPACE });
  return st.hosts?.find((h) => h.view === 'exit-proof/sessions') ?? null;
}

async function main() {
  const client = new UiAutomationClient();
  await client.waitForManifest(20_000);
  await client.waitForFrontendResponsive(20_000, 'get_state');
  const driver = new MacOSDriver({ bundleId: client.bundleId, appPath: client.appPath });

  await delay(2500); // recording shows the healthy tile
  const good = readFileSync(VIEW, 'utf8');
  writeFileSync(VIEW, good.replace('export default function Sessions({ params }: ViewProps): ReactElement {\n', `export default function Sessions({ params }: ViewProps): ReactElement {\n${THROW}`));
  console.log('broke the view');

  for (let i = 0; i < 20; i++) {
    await delay(500);
    const host = await hostState(client);
    if (host?.placeholder === 'crashed') break;
  }
  console.log('boundary held: tile crashed in place');

  // Everything else still runs: the terminal beside the tile takes input.
  await driver.activateApp();
  await delay(400);
  await client.request('click_pane', { sessionId: SESSION, paneId: `pane-${SESSION}` });
  await delay(400);
  await driver.typeText('echo the boundary holds');
  await delay(300);
  await driver.pressEnter();
  await delay(2500);

  // Fix it: same file, throw removed — dev re-applies, the tile comes back.
  writeFileSync(VIEW, good);
  console.log('fixed the view');
  for (let i = 0; i < 20; i++) {
    await delay(500);
    const host = await hostState(client);
    if (host && !host.placeholder) {
      console.log('tile came back');
      await delay(3500);
      return;
    }
  }
  throw new Error('view did not come back after the fix');
}

main().catch((e) => { console.error(e); process.exitCode = 1; });
