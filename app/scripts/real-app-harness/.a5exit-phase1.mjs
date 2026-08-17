#!/usr/bin/env node
// A5 exit proof, phase 1: dock the view via the real command menu and show
// live data arriving (attn doc put → a row appears in the docked tile).
import { execFile } from 'child_process';
import { promisify } from 'util';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const execFileAsync = promisify(execFile);
const delay = (ms) => new Promise((r) => setTimeout(r, ms));
const ROOT = '/Users/victor/projects/victor/attn--delegate-pi-a5-exit-8360d126';
const SESSION = '24ddeb2a-f453-4667-8bac-7cf6f6c907bc';
const WORKSPACE = `workspace-${SESSION}`;

function cliEnv() {
  const env = { ...process.env, ATTN_PROFILE: 'a5exit' };
  for (const k of ['ATTN_DATA_DIR', 'ATTN_DB_PATH', 'ATTN_SOCKET_PATH', 'ATTN_WS_PORT', 'ATTN_CONFIG_PATH', 'ATTN_PLUGIN_DIR']) delete env[k];
  return env;
}
async function docPut(id, body) {
  const { stdout } = await execFileAsync(`${ROOT}/attn`, ['doc', 'put', 'app/exit-proof', 'seen', id, JSON.stringify(body)], { env: cliEnv() });
  console.log('doc put:', stdout.trim());
}

async function main() {
  const client = new UiAutomationClient();
  await client.waitForManifest(20_000);
  await client.waitForFrontendResponsive(20_000, 'get_state');
  const driver = new MacOSDriver({ bundleId: client.bundleId, appPath: client.appPath });

  // Dock through the real command menu.
  await driver.activateApp();
  await delay(600);
  await driver.pressKeyCode(53);                    // Esc — close anything open
  await delay(400);
  await driver.pressKey('k', { command: true });    // ⌘K command menu
  await delay(700);
  await driver.typeText('Sessions');                // find the view entry
  await delay(700);
  await driver.pressEnter();                        // pick it → params prompt
  await delay(700);
  await driver.pressEnter();                        // empty params → dock

  // Wait for the view to mount.
  for (let i = 0; i < 20; i++) {
    await delay(500);
    const st = await client.request('app_view_get_state', { workspaceId: WORKSPACE });
    if (st.hosts?.some((h) => h.view === 'exit-proof/sessions' && !h.placeholder)) break;
  }
  console.log('docked and mounted');
  await delay(2000);

  // Live data appears, twice, while the camera rolls.
  await docPut('proof-row-1', { state: 'live write via attn doc put', seq: 1 });
  await delay(3000);
  await docPut('proof-row-2', { state: 'a second live write', seq: 2 });
  await delay(4000);
}

main().catch((e) => { console.error(e); process.exitCode = 1; });
