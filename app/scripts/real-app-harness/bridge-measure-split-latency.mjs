#!/usr/bin/env node

/**
 * Measures shell split latency: time from split_pane to fish prompt visible.
 * Reports per-split timing to identify the fast/slow alternating pattern.
 *
 * Usage:
 *   pnpm exec node scripts/real-app-harness/bridge-measure-split-latency.mjs [--splits N]
 *
 * Requires the app running with UI automation enabled:
 *   ATTN_UI_AUTOMATION=1 VITE_UI_AUTOMATION=1 make install-app
 */

import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, parseCommonArgs } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function main() {
  const argv = process.argv.slice(2);
  let splitCount = 6;
  const filtered = [];
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--splits' && argv[i + 1]) {
      splitCount = parseInt(argv[++i], 10);
    } else {
      filtered.push(argv[i]);
    }
  }

  const options = parseCommonArgs(filtered);
  const { runId, sessionDir } = createRunContext(options, 'split-latency');
  const sessionLabel = `attn-split-latency-${runId}`;

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  try {
    console.log(`Connecting to running app (not launching)...`);
    await client.waitForManifest(5_000);
    await client.waitForReady(5_000);
    await observer.connect();

    // Create a session
    const createResult = await client.request('create_session', {
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
    });
    const sessionId = createResult.sessionId;
    await observer.waitForSession({ label: sessionLabel, timeoutMs: 15_000 });
    console.log(`Session created: ${sessionId}`);

    // Wait for initial workspace
    await observer.waitForWorkspace(sessionId, () => true, 'initial workspace', 10_000);
    await delay(1000); // let the main pane settle

    const results = [];

    for (let i = 0; i < splitCount; i++) {
      const workspace = await client.request('get_workspace', { sessionId });
      const targetPaneId = workspace.activePaneId || 'main';

      const t0 = performance.now();

      await client.request('split_pane', {
        sessionId,
        targetPaneId,
        direction: i % 2 === 0 ? 'vertical' : 'horizontal',
      });
      const tSplit = performance.now();

      // Wait for the new shell pane to appear in the workspace
      const updatedWorkspace = await observer.waitForWorkspace(
        sessionId,
        (entry) => (entry.panes || []).filter((p) => p.kind === 'shell').length >= i + 1,
        `shell pane #${i + 1}`,
        10_000,
      );
      const tWorkspace = performance.now();

      const shells = (updatedWorkspace.panes || []).filter((p) => p.kind === 'shell' && p.runtime_id);
      const newShell = shells[shells.length - 1];
      if (!newShell?.runtime_id) {
        console.log(`  split #${i + 1}: shell pane not found`);
        results.push({ split: i + 1, error: 'no shell pane' });
        continue;
      }

      // Wait up to 4s and check if the fish DA1 warning appears.
      // The warning means fish waited 2s for a DA1 response and gave up.
      let fishWarning = false;
      let firstOutputAt = null;
      const checkDeadline = performance.now() + 4_000;
      while (performance.now() < checkDeadline) {
        try {
          const scrollback = await observer.readScrollback(newShell.runtime_id, 2_000);
          if (scrollback.length > 0 && !firstOutputAt) {
            firstOutputAt = performance.now();
          }
          if (scrollback.includes('warning: fish could not read')) {
            fishWarning = true;
            break;
          }
          // If we have substantial output and no warning, shell is ready
          if (scrollback.length > 50 && !scrollback.includes('warning:')) {
            break;
          }
        } catch {
          // runtime not attached yet
        }
        await delay(50);
      }
      const tDone = performance.now();

      const splitMs = Math.round(tSplit - t0);
      const totalMs = Math.round(tDone - t0);
      const firstOutputMs = firstOutputAt ? Math.round(firstOutputAt - t0) : null;
      const status = fishWarning ? 'SLOW (DA1 warning)' : 'FAST';

      results.push({
        split: i + 1,
        runtimeId: newShell.runtime_id,
        paneId: newShell.pane_id,
        splitMs,
        firstOutputMs,
        totalMs,
        status,
      });

      console.log(`  split #${i + 1}: ${status}  split=${splitMs}ms  firstOutput=${firstOutputMs ?? '?'}ms  total=${totalMs}ms`);
      await delay(1000);
    }

    console.log('\n=== Summary ===');
    console.log(JSON.stringify(results, null, 2));
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
