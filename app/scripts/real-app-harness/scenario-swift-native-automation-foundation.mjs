#!/usr/bin/env node

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { execFile, spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { manifestPathForNativeProfile } from './nativeHarnessProfile.mjs';

const execFileAsync = promisify(execFile);
const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');
const DEFAULT_APP_PATH = path.join(REPO_ROOT, 'native-ui', '.build', 'debug', 'attn-native-dev.app');
const FOREGROUND_HELPER = path.join(HARNESS_DIR, 'ForegroundApplication.swift');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitForProcessExit(pid, timeoutMs = 5_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      process.kill(pid, 0);
    } catch {
      return;
    }
    await delay(50);
  }
  assert.fail(`native process ${pid} did not terminate after its launcher was interrupted`);
}

async function frontmostApplication() {
  const { stdout } = await execFileAsync('/usr/bin/xcrun', ['swift', FOREGROUND_HELPER]);
  const [pid, bundleIdentifier] = stdout.trim().split('\t');
  return {
    pid: Number.parseInt(pid, 10),
    bundleIdentifier: bundleIdentifier || null,
  };
}

async function main() {
  const suffix = crypto.randomBytes(3).toString('hex');
  const profile = `ns${process.pid.toString(36)}${suffix}`.slice(0, 15);
  const manifestPath = manifestPathForNativeProfile(profile);
  const appDataDir = path.dirname(path.dirname(manifestPath));
  const guardedProfile = `lg${process.pid.toString(36)}${crypto.randomBytes(3).toString('hex')}`.slice(0, 15);
  const guardedManifestPath = manifestPathForNativeProfile(guardedProfile);
  const guardedAppDataDir = path.dirname(path.dirname(guardedManifestPath));
  const screenshotPath = path.join(os.tmpdir(), `attn-swift-native-automation-${profile}.png`);
  const appPath = process.env.ATTN_NATIVE_APP_PATH || DEFAULT_APP_PATH;
  const foregroundBefore = await frontmostApplication();
  const previousForegroundPID = foregroundBefore.pid;
  let client;
  let nativePID;
  let guardedClient;
  let guardedNativePID;
  let guardedLauncher;

  assert.ok(previousForegroundPID, 'could not determine foreground process before native automation launch');
  assert.ok(fs.existsSync(appPath), `signed Swift native app not found: ${appPath}`);

  try {
    await execFileAsync('open', [
      '-n',
      '--env', `ATTN_PROFILE=${profile}`,
      '--env', 'ATTN_AUTOMATION=1',
      '--env', 'ATTN_AUTOMATION_BACKGROUND=1',
      '--env', `ATTN_AUTOMATION_RESTORE_FOREGROUND_PID=${previousForegroundPID}`,
      '--env', 'ATTN_NATIVE_WS_URL=ws://127.0.0.1:1/ws',
      appPath,
    ]);

    client = new UiAutomationClient({ manifestPath });
    await client.waitForManifest(20_000);
    nativePID = client.readManifest().pid;
    await client.waitForReady(20_000);
    await delay(250);

    const state = await client.request('get_state');
    assert.equal(state.app, 'swift-native');
    assert.equal(state.backgroundMode, true);
    assert.equal(state.daemonReady, false);

    const parked = await client.request('park_window', { visible_px: 20 });
    assert.equal(parked.parked, true);
    assert.equal(parked.visiblePx, 20);
    const bounds = await client.request('get_window_bounds');
    assert.ok(bounds.windowId > 0, 'native window must be inspectable');
    assert.ok(bounds.logicalBounds.width > 0 && bounds.logicalBounds.height > 0);
    assert.equal(bounds.logicalBounds.x, parked.logicalBounds.x, 'parked bounds must be observable through automation');

    const screenshot = await client.request('screenshot_window', { path: screenshotPath });
    assert.equal(screenshot.path, screenshotPath);
    assert.ok(fs.statSync(screenshotPath).size > 0, 'native screenshot must be non-empty');

    await assert.rejects(
      client.request('type_terminal', { text: 'echo ready\n' }),
      /terminal surface not found/,
    );

    const foregroundAfter = await frontmostApplication();
    assert.notEqual(foregroundAfter.pid, nativePID, 'background automation must not leave attn frontmost');
    assert.equal(foregroundAfter.pid, previousForegroundPID, 'background automation must restore foreground ownership');

    await execFileAsync('open', [
      '-n',
      '--env', `ATTN_PROFILE=${profile}`,
      '--env', 'ATTN_AUTOMATION=1',
      '--env', 'ATTN_AUTOMATION_BACKGROUND=1',
      '--env', `ATTN_AUTOMATION_RESTORE_FOREGROUND_PID=${previousForegroundPID}`,
      '--env', 'ATTN_NATIVE_WS_URL=ws://127.0.0.1:1/ws',
      appPath,
    ]);
    await delay(500);
    assert.equal(
      client.readManifest().pid,
      nativePID,
      'second native client for one profile must terminate without replacing the existing owner',
    );
    process.kill(nativePID, 0);

    guardedLauncher = spawn('/bin/sh', [path.join(REPO_ROOT, 'scripts', 'run-native-dev.sh')], {
      cwd: REPO_ROOT,
      detached: true,
      stdio: 'ignore',
      env: {
        ...process.env,
        ATTN_PROFILE: guardedProfile,
        ATTN_AUTOMATION: '1',
        ATTN_AUTOMATION_BACKGROUND: '1',
        ATTN_AUTOMATION_RESTORE_FOREGROUND_PID: String(previousForegroundPID),
        ATTN_NATIVE_WS_URL: 'ws://127.0.0.1:1/ws',
        ATTN_NATIVE_APP_BUNDLE: appPath,
      },
    });
    guardedClient = new UiAutomationClient({ manifestPath: guardedManifestPath });
    await guardedClient.waitForManifest(20_000);
    guardedNativePID = guardedClient.readManifest().pid;
    await guardedClient.waitForReady(20_000);
    const guardedDialog = await guardedClient.request('open_new_workspace_dialog');
    assert.equal(guardedDialog.presented, 'true');
    assert.equal(guardedDialog.mode, 'new_workspace');
    process.kill(-guardedLauncher.pid, 'SIGINT');
    await waitForProcessExit(guardedNativePID);

    const closeDialog = await client.request('open_new_workspace_dialog');
    assert.equal(closeDialog.presented, 'true');
    assert.equal(closeDialog.mode, 'new_workspace');
    await client.request('close_window').catch(() => {});
    await waitForProcessExit(nativePID);

    console.log(JSON.stringify({
      profile,
      nativePID,
      previousForegroundPID,
      previousForegroundBundleIdentifier: foregroundBefore.bundleIdentifier,
      parkedWindow: parked,
      windowId: bounds.windowId,
      screenshotPath,
      terminalSurfaceAbsentWithoutDaemon: true,
      singleClientPerProfile: true,
      interruptedLauncherStopsOwnedClientWithOpenDialog: true,
      closeWindowStopsClientWithOpenDialog: true,
    }, null, 2));
  } finally {
    if (nativePID) {
      try {
        process.kill(nativePID, 'SIGTERM');
      } catch {}
    }
    if (guardedNativePID) {
      try {
        process.kill(guardedNativePID, 'SIGTERM');
      } catch {}
    }
    if (guardedLauncher && guardedLauncher.exitCode === null) {
      try {
        process.kill(-guardedLauncher.pid, 'SIGTERM');
      } catch {}
    }
    fs.rmSync(screenshotPath, { force: true });
    fs.rmSync(appDataDir, { recursive: true, force: true });
    fs.rmSync(guardedAppDataDir, { recursive: true, force: true });
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
