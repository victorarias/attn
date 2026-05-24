#!/usr/bin/env node

import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { manifestPathForNativeProfile } from './nativeHarnessProfile.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');
const NATIVE_ROOT = path.join(REPO_ROOT, 'native-ui');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function reservePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  const port = typeof address === 'object' && address ? address.port : null;
  await new Promise((resolve) => server.close(resolve));
  if (!port) throw new Error('Could not reserve native harness websocket port');
  return port;
}

async function waitFor(check, label, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let latest = null;
  while (Date.now() < deadline) {
    latest = await check();
    if (latest) return latest;
    await delay(120);
  }
  throw new Error(`Timed out waiting for ${label}; last value=${JSON.stringify(latest)}`);
}

function ownedProcess(command, args, options) {
  const child = spawn(command, args, {
    ...options,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let log = '';
  const capture = (chunk) => {
    log = `${log}${chunk.toString()}`.slice(-6000);
  };
  child.stdout.on('data', capture);
  child.stderr.on('data', capture);
  return { child, output: () => log };
}

async function stopOwnedProcess(proc) {
  if (!proc || proc.exitCode !== null) return;
  proc.kill('SIGTERM');
  await Promise.race([
    new Promise((resolve) => proc.once('exit', resolve)),
    delay(2000).then(() => {
      if (proc.exitCode === null) proc.kill('SIGKILL');
    }),
  ]);
}

function profilePaths(profile) {
  const manifestPath = manifestPathForNativeProfile(profile);
  return {
    manifestPath,
    appDataDir: path.dirname(path.dirname(manifestPath)),
    automationLogPath: path.join(path.dirname(manifestPath), 'ui-automation-server.log'),
    daemonDataDir: path.join(os.homedir(), `.attn-${profile}`),
  };
}

function cleanupProfileArtifacts(profile) {
  if (process.env.ATTN_NATIVE_KEEP_ARTIFACTS === '1') return;
  const paths = profilePaths(profile);
  fs.rmSync(paths.appDataDir, { recursive: true, force: true });
  fs.rmSync(paths.daemonDataDir, { recursive: true, force: true });
}

async function main() {
  const backgroundMode = process.env.ATTN_NATIVE_FOREGROUND !== '1';
  const physicalInputMode = !backgroundMode && process.env.ATTN_NATIVE_PHYSICAL_INPUT === '1';
  const suffix = crypto.randomBytes(3).toString('hex');
  const profile = `nt${process.pid.toString(36)}${suffix}`.slice(0, 15);
  const port = await reservePort();
  const wsUrl = `ws://127.0.0.1:${port}/ws`;
  const daemonBinary = process.env.ATTN_NATIVE_DAEMON_BINARY || path.join(REPO_ROOT, 'attn');
  const isolatedEnv = { ...process.env };
  for (const key of [
    'ATTN_SOCKET_PATH',
    'ATTN_DB_PATH',
    'ATTN_CONFIG_PATH',
    'ATTN_WRAPPER_PATH',
    'ATTN_INSIDE_APP',
    'ATTN_DAEMON_MANAGED',
    'ATTN_PTY_WORKER',
    'ATTN_SESSION_ID',
    'ATTN_AGENT',
  ]) {
    delete isolatedEnv[key];
  }
  const commonEnv = {
    ...isolatedEnv,
    ATTN_PROFILE: profile,
    ATTN_WS_PORT: String(port),
  };
  let daemon;
  let native;
  let workspaceId;
  let parkingWorkspaceId;
  let client;
  let nativePid = null;
  const foregroundSamples = [];
  const focusDriver = new MacOSDriver();
  const priorForegroundPid = backgroundMode ? await focusDriver.frontmostProcessId() : null;
  const paths = profilePaths(profile);

  try {
    daemon = ownedProcess(daemonBinary, ['daemon'], {
      cwd: REPO_ROOT,
      env: commonEnv,
    });
    native = ownedProcess('cargo', ['run', '--quiet', '--bin', 'attn-native'], {
      cwd: NATIVE_ROOT,
      env: {
        ...commonEnv,
        ATTN_AUTOMATION: '1',
        ATTN_AUTOMATION_BACKGROUND: backgroundMode ? '1' : '0',
        ATTN_AUTOMATION_START_EMPTY: '1',
        ATTN_WS_URL: wsUrl,
      },
    });

    client = new UiAutomationClient({
      manifestPath: paths.manifestPath,
    });
    await client.waitForManifest(40_000);
    nativePid = client.readManifest().pid;
    const assertRunsInBackground = async (label) => {
      const frontmostPid = await focusDriver.frontmostProcessId();
      const sample = { label, frontmostPid, nativePid };
      foregroundSamples.push(sample);
      if (frontmostPid === nativePid) {
        throw new Error(`Native automation stole foreground focus at ${label}`);
      }
    };
    await client.waitForReady(20_000);
    await waitFor(async () => {
      const state = await client.request('get_state');
      return state.daemon?.connected ? state : null;
    }, 'isolated daemon connection');

    workspaceId = `native-smoke-${suffix}`;
    const runtimeId = `native-pane-${suffix}`;
    await client.request('create_workspace', {
      id: workspaceId,
      title: 'Native smoke',
      directory: '/tmp',
    });
    await waitFor(async () => {
      const state = await client.request('get_state');
      return state.workspaces.some((workspace) => workspace.id === workspaceId) ? state : null;
    }, 'workspace registration');
    await client.request('select_workspace', { workspace_id: workspaceId });
    await client.request('spawn_session', {
      id: runtimeId,
      workspace_id: workspaceId,
      cwd: '/tmp',
      agent: 'pi',
      executable: '/bin/sh',
    });

    const target = await waitFor(async () => {
      const state = await client.request('get_state');
      const layout = state.layouts.find((candidate) => candidate.workspace_id === workspaceId);
      const pane = layout?.panes?.find((candidate) => candidate.runtime_id === runtimeId);
      return pane ? { paneId: pane.pane_id, runtimeId } : null;
    }, 'visible test terminal pane');
    await waitFor(async () => {
      const health = await client.request('capture_render_health');
      return health.panes.find(
        (pane) => pane.runtimeId === runtimeId && pane.flags?.terminalReady,
      );
    }, 'terminal attach');
    if (backgroundMode) {
      if (priorForegroundPid && priorForegroundPid !== nativePid) {
        await focusDriver.activateProcessId(priorForegroundPid);
      }
      await assertRunsInBackground('terminal-bootstrap-complete');
    }

    const directMarker = `__ATTN_NATIVE_DIRECT_${suffix}__`;
    await client.request('write_pane', {
      workspaceId,
      paneId: target.paneId,
      text: `printf '${directMarker}\\n'\r`,
      submit: false,
    });
    const directText = await waitFor(async () => {
      const value = await client.request('read_pane_text', {
        workspaceId,
        paneId: target.paneId,
      });
      return value.text.includes(directMarker) ? value : null;
    }, 'direct PTY output in current window mode');
    let physicalText = null;
    if (physicalInputMode) {
      const physicalDriver = new MacOSDriver({ pid: nativePid, actionDelayMs: 10 });
      const physicalMarker = `phys${suffix}`;
      await physicalDriver.clickWindow(0.6, 0.4);
      for (const key of `echo ${physicalMarker}`) {
        await physicalDriver.pressKey(key);
      }
      await physicalDriver.pressEnter();
      physicalText = await waitFor(async () => {
        const value = await client.request('read_pane_text', {
          workspaceId,
          paneId: target.paneId,
        });
        return value.text.includes(physicalMarker) ? value : null;
      }, 'physical mouse-and-key terminal input');
    }
    const inputEventCursor = (await client.request('tail_events', { since_id: 0 })).next_cursor;
    const marker = `__ATTN_NATIVE_UI_${suffix}__`;
    await client.request('focus_pane', {
      workspace_id: workspaceId,
      pane_id: target.paneId,
    });
    await client.request('type_pane_via_ui', {
      workspaceId,
      paneId: target.paneId,
      text: `printf '${marker}\\n'\n`,
    });
    const inputCallback = await waitFor(async () => {
      const events = await client.request('tail_events', { since_id: inputEventCursor });
      return events.events.find(
        (event) =>
          event.category === 'terminal_input_callback' &&
          event.payload.runtime_id === runtimeId &&
          event.payload.bytes > 0,
      );
    }, 'Ghostty external-I/O input callback', 5_000);
    const text = await waitFor(async () => {
      const value = await client.request('read_pane_text', {
        workspaceId,
        paneId: target.paneId,
      });
      return value.text.includes(marker) ? value : null;
    }, 'typed UI output');
    if (backgroundMode) await assertRunsInBackground('typed-input');
    let reattachEvent = null;
    let beforeSplitCols = null;
    let splitSnapshot = null;
    if (!backgroundMode) {
      const eventTail = await client.request('tail_events', { since_id: 0 });
      const eventCursor = eventTail.next_cursor;
      parkingWorkspaceId = `native-parking-${suffix}`;
      await client.request('create_workspace', {
        id: parkingWorkspaceId,
        title: 'Native parking',
        directory: '/tmp',
      });
      await waitFor(async () => {
        const state = await client.request('get_state');
        return state.workspaces.some((workspace) => workspace.id === parkingWorkspaceId)
          ? state
          : null;
      }, 'parking workspace registration');
      await client.request('select_workspace', { workspace_id: parkingWorkspaceId });
      await waitFor(async () => {
        const state = await client.request('get_state');
        return state.selected_workspace_id === parkingWorkspaceId &&
          !state.visible_terminal_runtime_ids.includes(runtimeId)
          ? state
          : null;
      }, 'terminal release on workspace switch');
      await client.request('select_workspace', { workspace_id: workspaceId });
      await waitFor(async () => {
        const health = await client.request('capture_render_health');
        return health.panes.find(
          (pane) => pane.runtimeId === runtimeId && pane.flags?.terminalReady,
        );
      }, 'terminal reattach after workspace switch');
      await waitFor(async () => {
        const value = await client.request('read_pane_text', {
          workspaceId,
          paneId: target.paneId,
        });
        return value.text.includes(marker) ? value : null;
      }, 'replayed terminal output after workspace switch');
      reattachEvent = await waitFor(async () => {
        const events = await client.request('tail_events', { since_id: eventCursor });
        return events.events.find(
          (event) =>
            event.category === 'terminal_attach_processed' &&
            event.payload.runtime_id === runtimeId &&
            event.payload.success === true,
        );
      }, 'terminal attach processing after workspace switch');
      const beforeSplit = await client.request('capture_structured_snapshot', {
        includePaneText: false,
      });
      beforeSplitCols = beforeSplit.panes.find((pane) => pane.paneId === target.paneId)?.size?.cols;
      if (!beforeSplitCols) {
        throw new Error('Visible main pane did not expose its pre-split terminal dimensions');
      }
      await client.request('split_pane', {
        workspaceId,
        targetPaneId: target.paneId,
        direction: 'vertical',
      });
      splitSnapshot = await waitFor(async () => {
        const value = await client.request('capture_structured_snapshot', {
          includePaneText: false,
        });
        return value.panes.length === 2 &&
          value.panes.every((pane) => pane.attached && pane.size?.cols < beforeSplitCols)
          ? value
          : null;
      }, 'split terminal panes resized from pane bounds');
    }
    const snapshot = await client.request('capture_structured_snapshot', {
      includePaneText: false,
    });
    if (backgroundMode) await assertRunsInBackground('terminal-actions-complete');
    const serverLog = fs.readFileSync(
      paths.automationLogPath,
      'utf8',
    );
    if (!serverLog.includes('action=type_pane_via_ui') ||
      (!backgroundMode && !serverLog.includes('action=split_pane'))) {
      throw new Error('Native automation request log does not contain driven actions');
    }

    console.log(JSON.stringify({
      ok: true,
      profile,
      workspaceId,
      paneId: target.paneId,
      runtimeId,
      renderedMarker: text.text.includes(marker),
      backgroundOutputReadback: directText.text.includes(directMarker),
      ghosttyInputCallback: Boolean(inputCallback),
      physicalInputCovered: physicalInputMode ? Boolean(physicalText) : false,
      reattachedAfterSwitch: backgroundMode ? null : Boolean(reattachEvent),
      splitPanesAttached: splitSnapshot?.panes.length ?? null,
      perPaneResize: splitSnapshot
        ? splitSnapshot.panes.every((pane) => pane.size.cols < beforeSplitCols)
        : null,
      surfaceRecreationCovered: !backgroundMode,
      requestLog: true,
      backgroundMode,
      backgroundFocusPreserved: backgroundMode
        ? foregroundSamples.every((sample) => sample.frontmostPid !== sample.nativePid)
        : null,
      foregroundSamples,
      visiblePanes: snapshot.panes.length,
    }, null, 2));
  } catch (error) {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    if (native) console.error(`[native tail]\n${native.output()}`);
    if (daemon) console.error(`[daemon tail]\n${daemon.output()}`);
    if (fs.existsSync(paths.automationLogPath)) {
      console.error(`[automation log]\n${fs.readFileSync(paths.automationLogPath, 'utf8')}`);
    }
    process.exitCode = 1;
  } finally {
    if (client && workspaceId) {
      try {
        await client.request('destroy_workspace', { id: workspaceId });
      } catch {}
    }
    if (client && parkingWorkspaceId) {
      try {
        await client.request('destroy_workspace', { id: parkingWorkspaceId });
      } catch {}
    }
    await stopOwnedProcess(native?.child);
    await stopOwnedProcess(daemon?.child);
    cleanupProfileArtifacts(profile);
  }
}

await main();
