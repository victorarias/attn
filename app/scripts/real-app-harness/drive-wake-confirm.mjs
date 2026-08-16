#!/usr/bin/env node
// Drives the two-step crew wake in a running app. Attaches to whatever app is
// already up for the profile — it never launches or restarts one — so it is the
// way to watch the arm-and-confirm choreography in the packaged app.
//
//   node scripts/real-app-harness/drive-wake-confirm.mjs focus <member>
//   node scripts/real-app-harness/drive-wake-confirm.mjs click <member>
//   node scripts/real-app-harness/drive-wake-confirm.mjs story <asleep> <woken>
//   node scripts/real-app-harness/drive-wake-confirm.mjs pointer <asleep> <woken>
//
// `focus` reveals the row's controls through :focus-within — CSS :hover does
// not answer to dispatched pointer events, so a synthetic hover would leave the
// sun invisible. `story` is the whole choreography in one run: arm and let it
// stand down, then arm and confirm.
//
// `pointer` is the same choreography under the real cursor, for recording it.
// It costs an Accessibility grant and a frontmost app, and it is worth both: a
// bridge-driven run has to focus the row to see it, which paints a focus ring no
// pointer user ever sees, and it stands the arm down on a timeout rather than
// showing the outside click that is the rule people actually meet.

import { MacOSDriver } from './macosDriver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function connect() {
  const client = new UiAutomationClient({});
  await client.waitForManifest(10_000);
  await client.waitForReady(15_000);
  await client.waitForFrontendResponsive(15_000);
  return client;
}

const focus = (client, member) =>
  client.request('dom_focus', { selector: `[data-testid="queue-crew-select-${member}"]` });
const click = (client, member) =>
  client.request('dom_click', { selector: `[data-testid="queue-crew-wake-${member}"]` });

// The input driver aims at the AX window frame, which starts at the titlebar;
// the DOM measures from under it. Measured at 32 logical px on macOS by
// comparing a wake button's DOM bounds against where the same button lands in a
// window-relative screenshot, twice on different rows. A wrong offset does not
// fail quietly: the click misses the sun, the member stays asleep, and both the
// recording and `attn crew list` say so.
const WINDOW_CHROME_PX = 32;

async function pointerMapper(client) {
  const { logicalBounds } = await client.request('get_window_bounds', {});
  return (bounds) => ({
    x: (bounds.x + bounds.width / 2) / logicalBounds.width,
    y: (bounds.y + bounds.height / 2 + WINDOW_CHROME_PX) / logicalBounds.height,
  });
}

async function main() {
  const [verb, ...args] = process.argv.slice(2);
  const client = await connect();

  if (verb === 'focus') {
    console.log(JSON.stringify(await focus(client, args[0])));
    return;
  }
  if (verb === 'click') {
    console.log(JSON.stringify(await click(client, args[0])));
    return;
  }
  if (verb === 'story') {
    const [standDown, woken] = args;
    await focus(client, standDown);
    await wait(1600);
    await click(client, standDown);
    console.log(`armed ${standDown}`);
    await wait(4800);
    console.log(`${standDown} stood down`);

    await focus(client, woken);
    await wait(1000);
    await click(client, woken);
    console.log(`armed ${woken}`);
    await wait(1800);
    await click(client, woken);
    console.log(`confirmed ${woken}`);
    await wait(4500);
    return;
  }
  if (verb === 'pointer') {
    const [standDown, woken] = args;
    const driver = new MacOSDriver({ actionDelayMs: 120 });
    await driver.activateApp();
    const toWindow = await pointerMapper(client);

    // Hover first and click second, both on the sun: the controls only exist
    // under the pointer, so this is the order a person's hand takes.
    const sunOf = async (member) => {
      const { bounds } = await client.request('dom_hover', {
        selector: `[data-testid="queue-crew-wake-${member}"]`,
      });
      return toWindow(bounds);
    };
    const hover = async (point) => driver.movePointerInWindow(point.x, point.y);
    const press = async (point) => driver.clickWindow(point.x, point.y);

    const standDownSun = await sunOf(standDown);
    await hover(standDownSun);
    await wait(900);
    await press(standDownSun);
    console.log(`armed ${standDown}`);
    await wait(1500);

    // Empty sidebar below the roster: the outside click is the stand-down rule
    // a person meets first, and it reads faster on camera than four seconds of
    // nothing.
    await press({ x: 0.13, y: 0.7 });
    console.log(`${standDown} stood down`);
    await wait(1200);

    const wokenSun = await sunOf(woken);
    await hover(wokenSun);
    await wait(700);
    await press(wokenSun);
    console.log(`armed ${woken}`);
    await wait(1600);
    await press(wokenSun);
    console.log(`confirmed ${woken}`);
    await wait(3500);
    return;
  }
  throw new Error(`unknown verb ${verb ?? '(none)'}`);
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
