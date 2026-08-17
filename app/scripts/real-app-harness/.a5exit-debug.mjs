#!/usr/bin/env node
// Stepwise dock debug: ⌘K, check the action menu opened, type, pick.
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const delay = (ms) => new Promise((r) => setTimeout(r, ms));

async function present(client, selector) {
  try {
    await client.request('capture_screenshot_data', { selector });
    return true;
  } catch (error) {
    if (String(error).includes('Screenshot selector not found in DOM')) return false;
    return true;
  }
}

async function main() {
  const client = new UiAutomationClient();
  await client.waitForManifest(20_000);
  await client.waitForFrontendResponsive(20_000, 'get_state');
  const driver = new MacOSDriver({ bundleId: client.bundleId, appPath: client.appPath });

  console.log('frontmost before:', await driver.frontmostBundleId());
  await driver.activateApp();
  await delay(600);
  console.log('frontmost after:', await driver.frontmostBundleId());
  console.log('menu before:', await present(client, '.action-menu'));

  await driver.pressKey('k', { command: true });
  await delay(700);
  console.log('menu after ⌘K:', await present(client, '.action-menu'));

  await driver.typeText('Sessions');
  await delay(700);
  console.log('menu after typing:', await present(client, '.action-menu'));
}

main().catch((e) => { console.error(e); process.exitCode = 1; });
