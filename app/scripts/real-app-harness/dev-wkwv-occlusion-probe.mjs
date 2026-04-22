#!/usr/bin/env node
// Research probe (NOT a scenario): does WKWebView deliver fresh frames to
// WindowServer while attn's NSWindow is occluded? Iteration tool for designing
// a focus-free capture path. Intentionally prefixed `dev-` so scenario runners
// and `real-app:serial-matrix` do not pick it up.
//
// Procedure:
//   1. Launch attn, connect bridge, create a session.
//   2. Split a shell pane, focus it, type BEFORE_TOKEN, wait for echo.
//   3. Capture-A: screencapture -l <winid> (attn frontmost).
//   4. Activate ghostty to occlude attn (no focus change on the pane PTY).
//   5. Capture-B immediately (nothing drew; expected to match A).
//   6. write_pane AFTER_TOKEN (PTY-direct — independent of DOM focus). Wait
//      for echo via read_pane_text (confirms xterm saw the bytes).
//   7. Capture-C while attn is still occluded.
//   8. Compare SHA-256 hashes.
//      - If C ≠ B: the compositor kept delivering frames under occlusion.
//      - If C == B: WKWebView paused composite delivery; any focus-free
//        screencap path captures stale backing store.
//
// Usage:
//   node scripts/real-app-harness/dev-wkwv-occlusion-probe.mjs
//
// Requires: /tmp/find-window-id (see notes in docs/research/wkwv-occlusion.md
// or rebuild from app/scripts/real-app-harness/InputDriver.swift's
// mainWindowBounds helper if missing).

// This probe deliberately occludes attn, so opt out of the default always-on-
// top harness mode — otherwise the window refuses to be covered.
process.env.ATTN_HARNESS_ALWAYS_ON_TOP = '0';

import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { PNG } from 'pngjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import {
  launchFreshAppAndConnect,
  createSessionAndWaitForMain,
} from './common.mjs';
import {
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneState,
  waitForPaneVisible,
  compactTerminalText,
  sleep,
} from './scenarioAssertions.mjs';

const execFileAsync = promisify(execFile);

const runId = `probe-${Date.now()}`;
const runDir = path.join(os.tmpdir(), 'wkwebview-occlusion', runId);
fs.mkdirSync(runDir, { recursive: true });
console.log(`[probe] runDir=${runDir}`);

async function findWindowId(bundleId = 'com.attn.manager') {
  const { stdout } = await execFileAsync('/tmp/find-window-id', [bundleId], { timeout: 5_000 });
  const line = stdout.trim().split('\n')[0] || '';
  const match = line.match(/wid=(\d+)/);
  if (!match) {
    throw new Error(`Could not parse window id from: ${stdout}`);
  }
  return Number(match[1]);
}

async function capture(name, wid) {
  const outPath = path.join(runDir, `${name}.png`);
  await execFileAsync('/usr/sbin/screencapture', ['-x', '-l', String(wid), '-o', outPath], {
    timeout: 8_000,
  });
  const buf = fs.readFileSync(outPath);
  const sha = crypto.createHash('sha256').update(buf).digest('hex').slice(0, 16);
  const img = PNG.sync.read(buf);
  return { path: outPath, bytes: buf.length, sha, width: img.width, height: img.height, data: img.data };
}

function countDistinctPixels(a, b) {
  if (!a || !b || a.data.length !== b.data.length) return -1;
  let diffs = 0;
  for (let i = 0; i < a.data.length; i += 4) {
    const dr = Math.abs(a.data[i] - b.data[i]);
    const dg = Math.abs(a.data[i + 1] - b.data[i + 1]);
    const db = Math.abs(a.data[i + 2] - b.data[i + 2]);
    if (dr + dg + db > 24) diffs += 1;
  }
  return diffs;
}

async function activateBundle(bundleId) {
  await execFileAsync('osascript', ['-e', `tell application id "${bundleId}" to activate`], {
    timeout: 5_000,
  }).catch(() => {});
}

async function frontmostBundle() {
  const { stdout } = await execFileAsync('osascript', [
    '-e',
    'tell application "System Events" to bundle identifier of first application process whose frontmost is true',
  ], { timeout: 5_000 });
  return stdout.trim();
}

async function typeAndWaitForEcho(client, sessionId, paneId, token, { useUi = true } = {}) {
  if (useUi) {
    await client.request('type_pane_via_ui', { sessionId, paneId, text: `echo ${token}` });
    await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
  } else {
    // PTY-direct path: does not depend on DOM activeElement, so it works even
    // when attn has lost key window status and the textarea is blurred.
    await client.request('write_pane', { sessionId, paneId, text: `echo ${token}\r`, submit: false });
  }
  const deadline = Date.now() + 12_000;
  while (Date.now() < deadline) {
    const payload = await client.request('read_pane_text', { sessionId, paneId });
    const text = payload?.text || '';
    if (text.includes(token) || compactTerminalText(text).includes(token)) {
      return text;
    }
    await sleep(250);
  }
  throw new Error(`token ${token} never echoed in pane ${paneId}`);
}

async function main() {
  const appPath = path.join(os.homedir(), 'Applications', 'attn.app');
  const observer = new DaemonObserver({ wsUrl: 'ws://127.0.0.1:9849/ws' });
  // Setup phase runs with attn frontmost so WKWebView initializes normally;
  // we deliberately occlude later to test paint delivery.
  const client = new UiAutomationClient({ appPath, backgroundLaunch: false });
  const sessionDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wkwv-probe-'));

  try {
    await launchFreshAppAndConnect(client, observer);

    const sessionId = await createSessionAndWaitForMain({
      client,
      observer,
      cwd: sessionDir,
      label: `wkwv-probe-${runId}`,
      agent: 'claude',
      sessionWaitMs: 60_000,
    });
    console.log(`[probe] sessionId=${sessionId}`);

    const ws0 = await client.request('get_workspace', { sessionId });
    const before = new Set((ws0.panes || []).map((p) => p.paneId));
    await client.request('split_pane', { sessionId, targetPaneId: 'main', direction: 'vertical' });
    const pane = await waitForNewShellPane(client, sessionId, before, 'shell pane for probe', 20_000);
    console.log(`[probe] pane=${pane.paneId}`);

    await client.request('focus_pane', { sessionId, paneId: pane.paneId });
    await waitForPaneVisible(client, sessionId, pane.paneId, 20_000);
    await waitForPaneInputFocus(client, sessionId, pane.paneId, 20_000, { stableMs: 400 });
    await waitForPaneState(
      client,
      sessionId,
      pane.paneId,
      (s) => Boolean(s?.renderHealth?.flags?.terminalReady),
      `pane ${pane.paneId} terminalReady`,
      30_000,
    );

    const beforeToken = `__WKWV_BEFORE_${Date.now()}__`;
    await typeAndWaitForEcho(client, sessionId, pane.paneId, beforeToken);
    console.log(`[probe] BEFORE echoed: ${beforeToken}`);

    const wid = await findWindowId();
    console.log(`[probe] attn wid=${wid}`);
    console.log(`[probe] frontmost=${await frontmostBundle()}`);
    const captureA = await capture('A-frontmost-before', wid);
    console.log(`[probe] Capture-A sha=${captureA.sha} size=${captureA.width}x${captureA.height}`);

    await activateBundle('com.mitchellh.ghostty');
    await sleep(500);
    console.log(`[probe] frontmost after occlude=${await frontmostBundle()}`);

    const captureB = await capture('B-occluded-before', wid);
    console.log(`[probe] Capture-B sha=${captureB.sha}`);

    const afterToken = `__WKWV_AFTER_${Date.now()}__`;
    await typeAndWaitForEcho(client, sessionId, pane.paneId, afterToken, { useUi: false });
    console.log(`[probe] AFTER echoed: ${afterToken}`);
    await sleep(400);
    console.log(`[probe] frontmost after type=${await frontmostBundle()}`);

    const captureC = await capture('C-occluded-after', wid);
    console.log(`[probe] Capture-C sha=${captureC.sha}`);

    const diffAB = captureA.sha === captureB.sha ? 'identical' : `different (${countDistinctPixels(captureA, captureB)} pixels)`;
    const diffBC = captureB.sha === captureC.sha ? 'identical' : `different (${countDistinctPixels(captureB, captureC)} pixels)`;
    const diffAC = captureA.sha === captureC.sha ? 'identical' : `different (${countDistinctPixels(captureA, captureC)} pixels)`;
    console.log(`[probe] diff A↔B: ${diffAB}`);
    console.log(`[probe] diff B↔C: ${diffBC}`);
    console.log(`[probe] diff A↔C: ${diffAC}`);

    const verdict = {
      beforeToken,
      afterToken,
      captureA: { sha: captureA.sha, width: captureA.width, height: captureA.height },
      captureB: { sha: captureB.sha },
      captureC: { sha: captureC.sha },
      diffAB,
      diffBC,
      diffAC,
      interpretation: captureB.sha === captureC.sha
        ? 'WKWebView DID NOT repaint under occlusion — focus-free capture needs a repaint nudge.'
        : 'WKWebView repainted under occlusion — focus-free capture is safe for paint-coverage assertions.',
    };
    fs.writeFileSync(path.join(runDir, 'verdict.json'), JSON.stringify(verdict, null, 2));
    console.log(`[probe] VERDICT: ${verdict.interpretation}`);
  } finally {
    await client.quitApp().catch(() => {});
    await observer.close();
  }
}

main().catch((err) => {
  console.error('[probe] FAILED');
  console.error(err instanceof Error ? err.stack || err.message : err);
  process.exitCode = 1;
});
