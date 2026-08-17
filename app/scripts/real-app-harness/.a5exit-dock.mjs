#!/usr/bin/env node
// A5 exit proof driver: dock the exit-proof app's Sessions view via the real
// command menu (⌘K), leaving params empty, then poll for the mounted tile.
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const delay = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const client = new UiAutomationClient();
  await client.waitForManifest(20_000);
  await client.waitForFrontendResponsive(20_000, 'get_state');
  const driver = new MacOSDriver({ bundleId: client.bundleId, appPath: client.appPath });

  const state = await client.request('get_state');
  const sessionId = state.activeSessionId;
  if (!sessionId) throw new Error('no active session');
  const { workspaceId } = await client.request('get_workspace', { sessionId });
  if (!workspaceId) throw new Error('no workspaceId');

  await driver.activateApp();
  await delay(500);
  await driver.pressKeyCode(53);              // Esc — close anything left open
  await delay(300);
  await driver.pressKey('k', { command: true });   // command menu
  await delay(600);
  await driver.typeText('Sessions');           // find the view entry
  await delay(600);
  await driver.pressEnter();                   // pick it -> params prompt
  await delay(600);
  await driver.pressEnter();                   // leave params empty -> dock

  for (let i = 0; i < 20; i++) {
    await delay(500);
    const ws = await client.request('get_workspace_ui_state', { workspaceId });
    if ((ws.tileIds ?? []).some((id) => id.includes('app:exit-proof/sessions'))) {
      console.log('DOCKED');
      console.log(JSON.stringify(ws, null, 2));
      return;
    }
  }
  const ws = await client.request('get_workspace_ui_state', { workspaceId });
  console.error('NOT DOCKED; workspace state:');
  console.error(JSON.stringify(ws, null, 2));
  process.exitCode = 1;
}

main().catch((e) => { console.error(e); process.exitCode = 1; });
