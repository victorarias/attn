#!/usr/bin/env node

import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import {
  createSessionAndWaitForMain,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';

const execFileAsync = promisify(execFile);
const WITNESS_BUNDLE_ID = 'com.apple.Terminal';
const ATTN_BUNDLE_ID = 'com.attn.manager';

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function frontmostBundleId() {
  const { stdout } = await execFileAsync('osascript', [
    '-e',
    'tell application "System Events" to return bundle identifier of first application process whose frontmost is true',
  ]);
  return stdout.trim();
}

async function activateWitness(timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      await execFileAsync('osascript', ['-e', 'tell application "Terminal" to activate']);
    } catch (error) {
      lastError = error;
    }
    // Wait a beat for macOS to propagate the activation; re-fire osascript
    // each iteration because recently-launched apps can briefly refuse to
    // yield frontmost to an AppleEvent activate request.
    await sleep(400);
    if ((await frontmostBundleId()) === WITNESS_BUNDLE_ID) {
      return;
    }
  }
  throw new Error(
    `witness ${WITNESS_BUNDLE_ID} never became frontmost within ${timeoutMs}ms; focus probe preconditions cannot be established${
      lastError ? ` (last osascript error: ${lastError.message})` : ''
    }`,
  );
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }
  const options = parseCommonArgs(args);
  return {
    options,
    help: args.includes('--help') || args.includes('-h'),
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-focus-probe.mjs');
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'FOCUS-PROBE',
    tier: 'tier0-focus-regression',
    prefix: 'scenario-focus-probe',
    metadata: {
      focus: 'measure caller frontmost preservation across driver modes',
      witnessBundleId: WITNESS_BUNDLE_ID,
      attnBundleId: ATTN_BUNDLE_ID,
    },
  });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const driver = new MacOSDriver({ appPath: options.appPath });

  let sessionId = null;
  const observations = {
    witnessBundleId: WITNESS_BUNDLE_ID,
    attnBundleId: ATTN_BUNDLE_ID,
  };

  try {
    await runner.step('launch_app_via_bridge', async () => {
      await launchFreshAppAndConnect(client, observer);
    });

    sessionId = await runner.step('create_session_via_bridge', async () => {
      return createSessionAndWaitForMain({
        client,
        observer,
        cwd: runner.sessionDir,
        label: `focus-probe-${runner.runId}`,
        agent: 'claude',
        sessionWaitMs: 60_000,
      });
    });

    await runner.step('record_baseline_post_launch', async () => {
      observations.baselineAfterLaunchFrontmost = await frontmostBundleId();
      runner.log('baseline_post_launch', {
        bundleId: observations.baselineAfterLaunchFrontmost,
      });
    });

    await runner.step('exercise_mode_a_cgevent_driver', async () => {
      await activateWitness();
      observations.modeA_frontmostBefore = await frontmostBundleId();
      runner.log('mode_a:before', { bundleId: observations.modeA_frontmostBefore });

      // Current focus-stealing path: NSRunningApplication.activate(...) + CGEvent.post.
      await driver.activateApp();
      await driver.pressKey('d', { command: true });
      await sleep(500);

      observations.modeA_frontmostAfter = await frontmostBundleId();
      observations.modeA_stoleFocus =
        observations.modeA_frontmostBefore === WITNESS_BUNDLE_ID &&
        observations.modeA_frontmostAfter !== WITNESS_BUNDLE_ID;
      runner.log('mode_a:after', {
        bundleId: observations.modeA_frontmostAfter,
        stoleFocus: observations.modeA_stoleFocus,
      });
    });

    await runner.step('exercise_mode_b_bridge_only', async () => {
      await activateWitness();
      observations.modeB_frontmostBefore = await frontmostBundleId();
      runner.log('mode_b:before', { bundleId: observations.modeB_frontmostBefore });

      // Focus-free path: bridge TCP socket → useUiAutomationBridge → splitPane.
      await client.request('split_pane', {
        sessionId,
        targetPaneId: 'main',
        direction: 'vertical',
      });
      await sleep(500);

      observations.modeB_frontmostAfter = await frontmostBundleId();
      observations.modeB_stoleFocus =
        observations.modeB_frontmostBefore === WITNESS_BUNDLE_ID &&
        observations.modeB_frontmostAfter !== WITNESS_BUNDLE_ID;
      runner.log('mode_b:after', {
        bundleId: observations.modeB_frontmostAfter,
        stoleFocus: observations.modeB_stoleFocus,
      });
    });

    runner.writeJson('focus-probe.json', observations);

    runner.assert(
      observations.modeA_frontmostBefore === WITNESS_BUNDLE_ID,
      'mode A precondition: witness Terminal is frontmost before CGEvent action',
      observations,
    );
    runner.assert(
      observations.modeB_frontmostBefore === WITNESS_BUNDLE_ID,
      'mode B precondition: witness Terminal is frontmost before bridge action',
      observations,
    );
    runner.assert(
      observations.modeB_frontmostAfter === WITNESS_BUNDLE_ID,
      'mode B (bridge-only) must preserve caller frontmost; split_pane via bridge stole focus',
      observations,
    );
    runner.assert(
      observations.modeA_stoleFocus === true,
      'mode A (CGEvent) must demonstrably steal focus; otherwise the probe is not exercising the regression',
      observations,
    );

    const summary = runner.finishSuccess({ sessionId, observations });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, { sessionId, observations });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
    }
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
