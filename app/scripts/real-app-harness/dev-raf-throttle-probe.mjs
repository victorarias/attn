#!/usr/bin/env node
// Research probe (NOT a scenario): measures the actual requestAnimationFrame
// callback rate inside attn's WKWebView under three window states: frontmost
// & key, visible & non-key (parked corner panel), and fully occluded.
//
// Previous "visible-non-key probe" was misleading — it looked at whether
// `terminalReady` and `runtimeAttached` eventually became true, which tolerates
// partial throttling (3 fps still passes a 20-second timeout). This probe
// counts real rAF ticks over 2-second windows and reports fps directly, so we
// can tell the difference between 60 fps, 3 fps, and 0 fps.
//
// Expected readings:
//   - Frontmost key window: ~60 fps (full speed)
//   - Visible non-key corner panel: ??? (that's what we want to measure)
//   - Fully occluded (behind another window): ~0–1 fps
//
// Decision rule:
//   If non-key ≈ key (both 50–60 fps): parking is a viable path for tests.
//   If non-key ≪ key: parking partially throttles; tests with tight round-
//     trip timeouts (tr204, tr401) will fail intermittently. Fall back to the
//     capture-and-restore pattern.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import {
  createSessionAndWaitForMain,
  launchFreshAppAndConnect,
} from './common.mjs';
import { sleep } from './scenarioAssertions.mjs';

const execFileAsync = promisify(execFile);
const ATTN_BUNDLE_ID = 'com.attn.manager';

async function frontmostBundle() {
  try {
    const { stdout } = await execFileAsync('osascript', [
      '-e',
      'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
    ], { timeout: 5_000 });
    return stdout.trim();
  } catch {
    return '';
  }
}

async function activateBundle(bundleId) {
  await execFileAsync('osascript', [
    '-e',
    `tell application id "${bundleId}" to activate`,
  ], { timeout: 5_000 }).catch(() => {});
}

// Measures rAF callback rate inside attn's webview via the automation bridge.
// The `dispatch_shortcut` path has access to the renderer's `window.requestAnimationFrame`,
// but the bridge doesn't expose arbitrary JS eval. So instead we poll a frame-
// counter that we increment via the bridge's read_pane_state heartbeat logging.
//
// Simpler approach: use an existing bridge action that internally calls
// `window.requestAnimationFrame`, and count how many completions happen in a
// fixed window by sampling xterm's `renderCount` (which only advances inside
// onRender → driven by rAF). If attn is at 60fps, a pane that has steady PTY
// output (e.g. `yes` running in the background) should see dozens of renders
// per second; if throttled to 3fps, the counter creeps.
//
// To avoid flooding the user's machine, we start a small `head -c 100000 /dev/urandom`
// loop in a pane (bounded output, triggers continuous xterm writes), then sample
// renderCount at t=0 and t=2000ms to compute fps.
async function sampleRenderRate(client, sessionId, paneId, windowMs = 2000) {
  const startSample = await client.request('get_pane_state', { sessionId, paneId });
  const startRender = startSample?.renderHealth?.terminal?.renderCount ?? 0;
  const startWriteParsed = startSample?.renderHealth?.terminal?.writeParsedCount ?? 0;
  const t0 = Date.now();

  await sleep(windowMs);

  const endSample = await client.request('get_pane_state', { sessionId, paneId });
  const endRender = endSample?.renderHealth?.terminal?.renderCount ?? 0;
  const endWriteParsed = endSample?.renderHealth?.terminal?.writeParsedCount ?? 0;
  const elapsedMs = Date.now() - t0;

  return {
    renderDelta: endRender - startRender,
    writeParsedDelta: endWriteParsed - startWriteParsed,
    elapsedMs,
    renderFps: ((endRender - startRender) / elapsedMs) * 1000,
    writeFps: ((endWriteParsed - startWriteParsed) / elapsedMs) * 1000,
  };
}

async function main() {
  const runId = `raf-${Date.now()}`;
  const runDir = path.join(os.tmpdir(), 'raf-throttle-probe', runId);
  fs.mkdirSync(runDir, { recursive: true });
  console.log(`[raf-probe] runDir=${runDir}`);

  const callerBundleId = await frontmostBundle();
  console.log(`[raf-probe] caller frontmost=${callerBundleId}`);

  const appPath = path.join(os.homedir(), 'Applications', 'attn.app');
  const observer = new DaemonObserver({ wsUrl: 'ws://127.0.0.1:9849/ws' });
  const client = new UiAutomationClient({ appPath });
  const sessionDir = fs.mkdtempSync(path.join(os.tmpdir(), 'raf-probe-'));

  const results = {
    callerBundleId,
    phases: {},
  };

  try {
    await launchFreshAppAndConnect(client, observer);
    const sessionId = await createSessionAndWaitForMain({
      client,
      observer,
      cwd: sessionDir,
      label: `raf-${runId}`,
      agent: 'claude',
      sessionWaitMs: 60_000,
    });
    const ws0 = await client.request('get_workspace', { sessionId });
    const before = new Set((ws0.panes || []).map((p) => p.paneId));
    await client.request('split_pane', { sessionId, targetPaneId: 'main', direction: 'vertical' });
    await sleep(1_000);
    const wsAfter = await client.request('get_workspace', { sessionId });
    const newPane = (wsAfter.panes || []).find((p) => !before.has(p.paneId));
    if (!newPane) throw new Error('no new pane after split');
    const paneId = newPane.paneId;
    console.log(`[raf-probe] paneId=${paneId}`);

    // Start bounded continuous output so each rAF tick renders.
    await client.request('write_pane', {
      sessionId,
      paneId,
      text: 'yes 2>/dev/null | head -c 200000 > /dev/null 2>&1; while :; do echo tick; sleep 0.1; done',
      submit: false,
    });
    await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
    await sleep(1_500);

    // Phase A: key & frontmost. Current launchApp hands key back to caller, so
    // explicitly activate attn for this phase.
    await activateBundle(ATTN_BUNDLE_ID);
    await sleep(800);
    results.phases.key_frontmost = {
      frontmost: await frontmostBundle(),
      sample: await sampleRenderRate(client, sessionId, paneId),
    };
    console.log('[raf-probe] key_frontmost:', results.phases.key_frontmost);

    // Phase B: non-key but still visible (caller gets focus).
    if (callerBundleId) {
      await activateBundle(callerBundleId);
    }
    await sleep(800);
    results.phases.visible_nonkey = {
      frontmost: await frontmostBundle(),
      sample: await sampleRenderRate(client, sessionId, paneId),
    };
    console.log('[raf-probe] visible_nonkey:', results.phases.visible_nonkey);

    // Phase C: restore attn foreground (control) to confirm throttle is reversible.
    await activateBundle(ATTN_BUNDLE_ID);
    await sleep(800);
    results.phases.key_frontmost_again = {
      frontmost: await frontmostBundle(),
      sample: await sampleRenderRate(client, sessionId, paneId),
    };
    console.log('[raf-probe] key_frontmost_again:', results.phases.key_frontmost_again);

    // Summary heuristic.
    const fpsKey = results.phases.key_frontmost.sample.renderFps;
    const fpsNonKey = results.phases.visible_nonkey.sample.renderFps;
    const ratio = fpsKey > 0 ? fpsNonKey / fpsKey : 0;
    results.summary = {
      fpsKey: Number(fpsKey.toFixed(2)),
      fpsNonKey: Number(fpsNonKey.toFixed(2)),
      ratio: Number(ratio.toFixed(3)),
      verdict: ratio >= 0.8
        ? 'NOT_THROTTLED (parking viable for tests)'
        : ratio >= 0.2
          ? 'PARTIALLY_THROTTLED (tests with tight timeouts may fail)'
          : 'HEAVILY_THROTTLED (parking not viable)',
    };
    console.log('[raf-probe] summary:', results.summary);
  } catch (error) {
    results.error = error instanceof Error ? (error.stack || error.message) : String(error);
    console.error('[raf-probe] failed:', results.error);
  } finally {
    fs.writeFileSync(path.join(runDir, 'results.json'), JSON.stringify(results, null, 2));
    await client.quitApp().catch(() => {});
    await observer.close();
  }

  if (callerBundleId) {
    await activateBundle(callerBundleId);
  }
  process.exit(results.summary?.ratio >= 0.8 ? 0 : 1);
}

main().catch((err) => {
  console.error('[raf-probe] fatal:', err instanceof Error ? (err.stack || err.message) : err);
  process.exit(1);
});
