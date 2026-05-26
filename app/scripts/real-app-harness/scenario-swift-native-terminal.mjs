#!/usr/bin/env node

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { execFile, spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import WebSocket from 'ws';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { manifestPathForNativeProfile } from './nativeHarnessProfile.mjs';
import { MacOSDriver } from './macosDriver.mjs';

const execFileAsync = promisify(execFile);
const HARNESS_DIR = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HARNESS_DIR, '../../..');
const DEFAULT_APP_PATH = path.join(REPO_ROOT, 'native-ui', '.build', 'debug', 'attn-native-dev.app');
const FOREGROUND_HELPER = path.join(HARNESS_DIR, 'ForegroundApplication.swift');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function waitFor(check, label, timeoutMs = 20_000) {
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() < deadline) {
    last = await Promise.resolve().then(check).catch(() => null);
    if (last) return last;
    await delay(100);
  }
  throw new Error(`Timed out waiting for ${typeof label === 'function' ? label() : label}; last=${JSON.stringify(last)}`);
}

async function submitTerminalCommand(client, runtimeID, command) {
  await client.request('type_terminal', { runtime_id: runtimeID, text: command });
  await client.request('press_terminal_enter', { runtime_id: runtimeID });
}

async function reservePort() {
  const server = net.createServer();
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const { port } = server.address();
  await new Promise((resolve) => server.close(resolve));
  return port;
}

async function frontmostPID() {
  const { stdout } = await execFileAsync('/usr/bin/xcrun', ['swift', FOREGROUND_HELPER]);
  return Number.parseInt(stdout.split('\t')[0], 10);
}

async function withBriefForegroundInput(physicalDriver, action) {
  const displacedPID = await frontmostPID();
  await physicalDriver.activateApp();
  try {
    await action();
  } finally {
    if (displacedPID && displacedPID !== physicalDriver.pid) {
      await physicalDriver.activateProcessId(displacedPID);
    }
  }
}

async function writeClipboard(text) {
  await new Promise((resolve, reject) => {
    const child = spawn('/usr/bin/pbcopy', [], { stdio: ['pipe', 'ignore', 'pipe'] });
    let stderr = '';
    child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
    child.once('error', reject);
    child.once('exit', (code) => {
      if (code === 0) resolve();
      else reject(new Error(`pbcopy failed (${code}): ${stderr}`));
    });
    child.stdin.end(text);
  });
}

async function readClipboard() {
  const { stdout } = await execFileAsync('/usr/bin/pbpaste');
  return stdout;
}

class DaemonCommands {
  constructor(url) {
    this.url = url;
    this.events = [];
    this.socket = null;
  }

  async connect(timeoutMs = 15_000) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      try {
        await this.connectOnce();
        this.send({ cmd: 'client_hello', client_kind: 'swift-native-test', version: 'protocol-66', capabilities: [] });
        return;
      } catch {
        await delay(150);
      }
    }
    throw new Error(`could not connect to isolated daemon ${this.url}`);
  }

  connectOnce() {
    return new Promise((resolve, reject) => {
      const socket = new WebSocket(this.url);
      socket.once('open', () => {
        this.socket = socket;
        socket.on('message', (raw) => this.events.push(JSON.parse(raw.toString())));
        resolve();
      });
      socket.once('error', reject);
    });
  }

  send(command) {
    this.socket.send(JSON.stringify(command));
  }

  async bootstrapWorkspace({ workspaceID, runtimeID, agent, executable, directory = REPO_ROOT }) {
    this.send({
      cmd: 'bootstrap_workspace',
      id: workspaceID,
      title: `${agent} terminal test`,
      directory,
      initial_session: {
        id: runtimeID,
        cwd: directory,
        kind: agent === 'shell' ? 'shell' : 'agent',
        agent,
        executable,
        cols: 100,
        rows: 30,
        label: agent,
      },
    });
    const result = await waitFor(
      () => this.events.find((event) => event.event === 'bootstrap_workspace_result' && event.workspace_id === workspaceID),
      `daemon bootstrap result for ${agent}`,
    );
    assert.equal(result.success, true, result.error || `daemon failed to bootstrap ${agent}`);
  }

  async unregisterSession(runtimeID) {
    this.send({ cmd: 'unregister', id: runtimeID });
    await waitFor(
      () => this.events.find((event) => event.event === 'session_unregistered' && event.session?.id === runtimeID),
      `daemon unregister event for ${runtimeID}`,
    );
  }

  async spawnSession({ workspaceID, runtimeID, targetPaneID, agent, executable, directory = REPO_ROOT }) {
    this.send({
      cmd: 'spawn_session',
      id: runtimeID,
      cwd: directory,
      workspace_id: workspaceID,
      target_pane_id: targetPaneID,
      direction: 'vertical',
      agent,
      executable,
      cols: 100,
      rows: 30,
      label: agent,
    });
    const result = await waitFor(
      () => this.events.find((event) => event.event === 'spawn_result' && event.id === runtimeID),
      `daemon spawn result for ${agent} in existing workspace`,
    );
    assert.equal(result.success, true, result.error || `daemon failed to spawn ${agent} in existing workspace`);
  }

  async closePane({ workspaceID, paneID }) {
    this.send({
      cmd: 'workspace_layout_close_pane',
      workspace_id: workspaceID,
      pane_id: paneID,
    });
    const result = await waitFor(
      () => this.events.find((event) =>
        event.event === 'workspace_layout_action_result'
          && event.action === 'workspace_layout_close_pane'
          && event.pane_id === paneID),
      `daemon close result for pane ${paneID}`,
    );
    assert.equal(result.success, true, result.error || `daemon failed to close pane ${paneID}`);
  }

  close() {
    this.socket?.close();
  }
}

function deterministicAgentExecutable(directory) {
  const executable = path.join(directory, 'terminal-agent');
  fs.writeFileSync(executable, '#!/bin/sh\nexec /bin/sh -l\n', { mode: 0o755 });
  return executable;
}

function deterministicTerminalInputFixture(directory, name, armedScreen, inputHandler = '') {
  const executable = path.join(directory, name);
  const source = `#!/usr/bin/env node
process.stdin.setRawMode(true);
process.stdin.resume();
let armed = false;
let pending = '';
process.stdout.write('fixture awaiting arm');
process.stdin.on('data', (chunk) => {
  if (!armed) {
    armed = true;
    process.stdout.write(${JSON.stringify(`\u001b[2J\u001b[H${armedScreen}`)});
    return;
  }
  pending += chunk.toString('latin1');
  ${inputHandler}
});
`;
  fs.writeFileSync(executable, source, { mode: 0o755 });
  return executable;
}

async function attachFixture(client, daemon, { suffix, label, executable }) {
  const workspaceID = `swift-native-${label}-${suffix}`;
  const runtimeID = `swift-runtime-${label}-${suffix}`;
  await daemon.bootstrapWorkspace({ workspaceID, runtimeID, agent: 'pi', executable });
  await waitFor(async () => {
    await client.request('select_workspace', { workspace_id: workspaceID });
    const panes = await client.request('list_panes');
    return panes.panes.find((pane) => pane.runtimeId === runtimeID && pane.attached) ?? null;
  }, `${label} fixture surface attachment`);
  await client.request('type_terminal', { runtime_id: runtimeID, text: '\r' });
  return { workspaceID, runtimeID };
}

async function exerciseSelectionAndCapturedMouse(client, daemon, temporaryDirectory, suffix) {
  const selectableExecutable = deterministicTerminalInputFixture(
    temporaryDirectory,
    'selectable-terminal',
    'OLD_ANCHOR_LINE\r\nSECOND_LINE\r\nSELECT_TARGET_VALUE\r\n',
  );
  const selectable = await attachFixture(client, daemon, {
    suffix,
    label: 'selection',
    executable: selectableExecutable,
  });
  await waitFor(async () => {
    const pane = await client.request('read_pane_text', { runtime_id: selectable.runtimeID });
    return pane.text.includes('SELECT_TARGET_VALUE');
  }, 'selectable text fixture output');

  await client.request('move_terminal_pointer', {
    runtime_id: selectable.runtimeID,
    column: 1,
    row: 0,
  });
  await client.request('drag_terminal_selection', {
    runtime_id: selectable.runtimeID,
    start_column: 0,
    start_row: 2,
    end_column: 18,
    end_row: 2,
  });
  const selection = await client.request('read_terminal_selection', { runtime_id: selectable.runtimeID });
  assert.ok(
    selection.text?.includes('SELECT_TARGET'),
    `selection must start in the dragged row, got ${JSON.stringify(selection.text)}`,
  );
  assert.equal(
    selection.text?.includes('OLD_ANCHOR_LINE'),
    false,
    `selection must not reuse the previously hovered row, got ${JSON.stringify(selection.text)}`,
  );
  const previousClipboard = await readClipboard();
  let backgroundCopy = false;
  try {
    await writeClipboard(`clipboard-sentinel-${suffix}`);
    await client.request('copy_terminal_selection', { runtime_id: selectable.runtimeID });
    const copied = await readClipboard();
    assert.ok(
      copied.includes('SELECT_TARGET'),
      `Ghostty copy action should write its selected text to the clipboard, got ${JSON.stringify(copied)}`,
    );
    backgroundCopy = true;
  } finally {
    await writeClipboard(previousClipboard);
  }

  const mouseExecutable = deterministicTerminalInputFixture(
    temporaryDirectory,
    'mouse-reporting-tui',
    '\u001b[?1000h\u001b[?1002h\u001b[?1006hMOUSE_TUI_READY\r\nclick or drag',
    `for (const match of pending.matchAll(/\\u001b\\[<(\\d+);(\\d+);(\\d+)([Mm])/g)) {
    process.stdout.write('\\r\\nMOUSE_EVENT:' + match[1] + ':' + match[2] + ':' + match[3] + ':' + match[4]);
  }
  pending = pending.slice(-80);`,
  );
  const mouseTUI = await attachFixture(client, daemon, {
    suffix,
    label: 'mouse-tui',
    executable: mouseExecutable,
  });
  await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    const pane = panes.find((candidate) => candidate.runtimeId === mouseTUI.runtimeID);
    const text = await client.request('read_pane_text', { runtime_id: mouseTUI.runtimeID });
    return pane?.mouseCaptured && text.text.includes('MOUSE_TUI_READY') ? pane : null;
  }, 'mouse-reporting TUI to enable captured mouse mode');

  await client.request('move_terminal_pointer', {
    runtime_id: mouseTUI.runtimeID,
    column: 1,
    row: 0,
  });
  await client.request('click_terminal_cell', {
    runtime_id: mouseTUI.runtimeID,
    column: 8,
    row: 1,
  });
  await waitFor(async () => {
    const text = (await client.request('read_pane_text', { runtime_id: mouseTUI.runtimeID })).text;
    return text.includes('MOUSE_EVENT:0:9:2:M') ? text : null;
  }, 'captured TUI click to use clicked cell rather than prior pointer cell');
  await client.request('drag_terminal_selection', {
    runtime_id: mouseTUI.runtimeID,
    start_column: 3,
    start_row: 2,
    end_column: 12,
    end_row: 2,
  });
  const reportedMouse = await waitFor(async () => {
    const text = (await client.request('read_pane_text', { runtime_id: mouseTUI.runtimeID })).text;
    return text.includes('MOUSE_EVENT:32:13:3:M') ? text : null;
  }, 'captured TUI drag motion to be forwarded rather than selected by the terminal');

  return {
    terminalSelection: true,
    backgroundCopy,
    capturedMouseTUI: true,
    reportedMouse: reportedMouse.includes('MOUSE_EVENT:32:13:3:M'),
  };
}

async function exerciseRenderedTerminal(client, daemon, spec, suffix, executable, physicalDriver) {
  const workspaceID = `swift-native-${spec.agent}-${suffix}`;
  const runtimeID = spec.real ? crypto.randomUUID() : `swift-runtime-${spec.agent}-${suffix}`;
  await daemon.bootstrapWorkspace({ workspaceID, runtimeID, agent: spec.agent, executable });
  await waitFor(async () => {
    await client.request('select_workspace', { workspace_id: workspaceID });
    const panes = await client.request('list_panes');
    return panes.panes.find((pane) => pane.runtimeId === runtimeID && pane.attached) ?? null;
  }, `${spec.agent} Ghostty surface attachment`, spec.timeoutMs);

  const geometry = await waitFor(async () => {
    const current = await client.request('get_surface_geometry', { runtime_id: runtimeID });
    return current.cols > 0 && current.rows > 0 ? current : null;
  }, `${spec.agent} surface live grid`, spec.timeoutMs);

  if (spec.real) {
    let lastText = '';
    const rendered = await waitFor(async () => {
      const pane = await client.request('read_pane_text', { runtime_id: runtimeID });
      lastText = pane.text;
      return spec.readyPattern.test(pane.text) ? pane.text : null;
    }, () => `${spec.agent} startup UI rendered by Ghostty (last text: ${JSON.stringify(lastText.slice(-200))})`, spec.timeoutMs);
    return { workspaceID, runtimeID, geometry, rendered: rendered.slice(-160), real: true };
  }

  const marker = `__SWIFT_GHOSTTY_${spec.agent.toUpperCase()}_${suffix}__`;
  await submitTerminalCommand(client, runtimeID, `printf '${marker}\\n'`);
  const rendered = await waitFor(async () => {
    const pane = await client.request('read_pane_text', { runtime_id: runtimeID });
    return pane.text.includes(marker) ? pane.text : null;
  }, `${spec.agent} input/output rendered by Ghostty`, spec.timeoutMs);
  let physicalInput = false;
  let backgroundPaste = false;
  if (spec.agent === 'shell') {
    const previousClipboard = await readClipboard();
    const pasteMarker = `paste${suffix}`;
    try {
      await writeClipboard(`echo ${pasteMarker}`);
      await client.request('paste_terminal_clipboard', { runtime_id: runtimeID });
      await client.request('press_terminal_enter', { runtime_id: runtimeID });
      await waitFor(async () => {
        const pane = await client.request('read_pane_text', { runtime_id: runtimeID });
        return pane.text.includes(pasteMarker) ? pane.text : null;
      }, 'background Ghostty clipboard paste into daemon-backed shell', spec.timeoutMs);
      backgroundPaste = true;
    } finally {
      await writeClipboard(previousClipboard);
    }
  }
  if (spec.agent === 'shell' && physicalDriver) {
    const physicalMarker = `phys${suffix}`;
    await withBriefForegroundInput(physicalDriver, async () => {
      await client.request('focus_pane', { runtime_id: runtimeID, key_window: true });
      for (const key of `echo ${physicalMarker}`) {
        await physicalDriver.pressKey(key);
      }
      await physicalDriver.pressEnter();
    });
    await waitFor(async () => {
      const pane = await client.request('read_pane_text', { runtime_id: runtimeID });
      return pane.text.includes(physicalMarker) ? pane.text : null;
    }, 'physical AppKit keyboard input through Ghostty', spec.timeoutMs);
    physicalInput = true;
  }
  return { workspaceID, runtimeID, geometry, rendered: rendered.includes(marker), physicalInput, backgroundPaste, real: false };
}

async function exerciseAddedAgentDoesNotRenderProfileBanner(client, daemon, temporaryDirectory, suffix, executable) {
  const workspaceID = `swift-native-managed-agent-${suffix}`;
  const shellRuntimeID = `swift-runtime-managed-shell-${suffix}`;
  const agentRuntimeID = `swift-runtime-managed-codex-${suffix}`;
  await daemon.bootstrapWorkspace({
    workspaceID,
    runtimeID: shellRuntimeID,
    agent: 'shell',
    directory: temporaryDirectory,
  });
  await daemon.spawnSession({
    workspaceID,
    runtimeID: agentRuntimeID,
    targetPaneID: 'main',
    agent: 'codex',
    executable,
    directory: temporaryDirectory,
  });
  await waitFor(async () => {
    await client.request('select_workspace', { workspace_id: workspaceID });
    const panes = await client.request('list_panes');
    return panes.panes.find((pane) => pane.runtimeId === agentRuntimeID && pane.attached) ?? null;
  }, 'agent added to an existing workspace');

  const marker = `__MANAGED_AGENT_${suffix}__`;
  await submitTerminalCommand(client, agentRuntimeID, `printf '${marker}\\n'`);
  const text = await waitFor(async () => {
    const pane = await client.request('read_pane_text', { runtime_id: agentRuntimeID });
    return pane.text.includes(marker) ? pane.text : null;
  }, 'agent added to existing workspace terminal output');
  assert.equal(
    text.includes('[attn profile='),
    false,
    `daemon-managed agent bootstrap leaked the profile routing banner into terminal output: ${JSON.stringify(text.slice(0, 180))}`,
  );
  return true;
}

async function exerciseWorkspaceLauncher(client, daemon, temporaryDirectory, suffix, physicalDriver) {
  const workspaceDirectory = path.join(temporaryDirectory, 'dialog-workspace');
  fs.mkdirSync(workspaceDirectory);
  const resolvedWorkspaceDirectory = fs.realpathSync(workspaceDirectory);

  const opened = await client.request('open_new_workspace_dialog');
  assert.equal(opened.mode, 'new_workspace');
  assert.equal(opened.presented, 'true');
  await client.request('set_launcher_choice', { choice: 'terminal' });

  const filterRoot = path.join(temporaryDirectory, 'dialog-filter-root');
  const filterMatch = path.join(filterRoot, 'alpha-match');
  const filterSibling = path.join(filterRoot, 'beta-other');
  const nestedMatch = path.join(filterMatch, 'nested-location');
  fs.mkdirSync(nestedMatch, { recursive: true });
  fs.mkdirSync(filterSibling);
  await client.request('set_launcher_path', { path: path.join(filterRoot, 'alp') });
  await waitFor(async () => {
    const launcher = await client.request('get_launcher_state');
    return launcher.browsedDirectory === filterRoot
      && launcher.visibleDirectoryPaths === filterMatch
      ? launcher
      : null;
  }, 'typed launcher prefix filters immediate directory results');
  await client.request('set_launcher_path', { path: `${filterMatch}${path.sep}` });
  await waitFor(async () => {
    const launcher = await client.request('get_launcher_state');
    return launcher.browsedDirectory === filterMatch
      && launcher.visibleDirectoryPaths === nestedMatch
      ? launcher
      : null;
  }, 'typed launcher directory changes visible child results');
  await client.request('set_launcher_path', { path: path.join(filterRoot, 'missing-parent', 'leaf') });
  await waitFor(async () => {
    const launcher = await client.request('get_launcher_state');
    return launcher.visibleDirectoryPaths === '' ? launcher : null;
  }, 'invalid typed launcher intermediate clears directory results');
  await client.request('set_launcher_path', { path: path.join(filterRoot, 'alp') });
  await waitFor(async () => {
    const launcher = await client.request('get_launcher_state');
    return launcher.browsedDirectory === filterRoot
      && launcher.visibleDirectoryPaths === filterMatch
      ? launcher
      : null;
  }, 'launcher continues filtering after invalid typed intermediate');

  await client.request('set_launcher_path', { path: workspaceDirectory });
  await client.request('submit_launcher_location');

  const firstPane = await waitFor(async () => {
    const state = await client.request('get_state');
    if (state.launcher.presented !== 'false' || !state.selectedWorkspaceId) return null;
    const panes = await client.request('list_panes');
    return panes.panes.length === 1 && panes.panes[0].attached ? panes.panes[0] : null;
  }, 'New Workspace dialog-created Ghostty pane');

  const firstMarker = `__DIALOG_WORKSPACE_${suffix}__`;
  await submitTerminalCommand(client, firstPane.runtimeId, `printf '${firstMarker}\\n'`);
  await waitFor(async () => {
    const pane = await client.request('read_pane_text', { runtime_id: firstPane.runtimeId });
    return pane.text.includes(firstMarker);
  }, 'input in New Workspace initial terminal');

  const paneDirectory = path.join(temporaryDirectory, 'dialog-pane-current-directory');
  fs.mkdirSync(paneDirectory);
  const resolvedPaneDirectory = fs.realpathSync(paneDirectory);
  await submitTerminalCommand(client, firstPane.runtimeId, `cd "${resolvedPaneDirectory}"`);
  await waitFor(async () => {
    const panes = await client.request('list_panes');
    return panes.panes.find((pane) => pane.runtimeId === firstPane.runtimeId)?.reportedCurrentDirectory === resolvedPaneDirectory;
  }, 'Ghostty reported current directory after shell cd');

  const addOpened = await client.request('open_add_pane_dialog', { direction: 'vertical' });
  assert.equal(addOpened.mode, 'add_pane');
  assert.equal(addOpened.direction, 'vertical');
  assert.equal(
    addOpened.path,
    resolvedPaneDirectory,
    'Add Pane must prefer the focused Ghostty surface reported working directory over workspace default',
  );
  await client.request('set_launcher_choice', { choice: 'terminal' });
  await client.request('submit_launcher_location');

  const splitPanes = await waitFor(async () => {
    const state = await client.request('get_state');
    if (state.launcher.presented !== 'false') return null;
    const panes = await client.request('list_panes');
    const secondPane = panes.panes.find((pane) => pane.runtimeId !== firstPane.runtimeId && pane.attached);
    const firstStillMounted = panes.panes.some((pane) => pane.runtimeId === firstPane.runtimeId && pane.attached);
    return secondPane && firstStillMounted ? { panes: panes.panes, secondPane } : null;
  }, 'Add Pane dialog-created Ghostty pane');
  const secondPane = splitPanes.secondPane;
  const secondMarker = `__DIALOG_PANE_${suffix}__`;
  await submitTerminalCommand(client, secondPane.runtimeId, `printf '${secondMarker}\\n'`);
  await waitFor(async () => {
    const pane = await client.request('read_pane_text', { runtime_id: secondPane.runtimeId });
    return pane.text.includes(secondMarker);
  }, 'input in Add Pane terminal');
  let postDialogPhysicalInput = false;
  if (physicalDriver) {
    const physicalMarker = `dialogphys${suffix}`;
    await withBriefForegroundInput(physicalDriver, async () => {
      for (const key of `echo ${physicalMarker}`) {
        await physicalDriver.pressKey(key);
      }
      await physicalDriver.pressEnter();
    });
    await waitFor(async () => {
      const pane = await client.request('read_pane_text', { runtime_id: secondPane.runtimeId });
      return pane.text.includes(physicalMarker) ? pane.text : null;
    }, 'active Add Pane terminal receives keyboard focus after dialog closes');
    postDialogPhysicalInput = true;
  }

  if (physicalDriver) {
    await withBriefForegroundInput(physicalDriver, async () => {
      await client.request('focus_pane', { runtime_id: firstPane.runtimeId, key_window: true });
      await physicalDriver.pressKey('d', { command: true, shift: true });
    });
  } else {
    const quickSplit = await client.request('quick_split', { direction: 'horizontal' });
    assert.equal(quickSplit.direction, 'horizontal');
  }
  const nestedSplit = await waitFor(async () => {
    const panes = await client.request('list_panes');
    const mounted = panes.panes.filter((pane) => pane.attached);
    return mounted.length === 3 ? mounted : null;
  }, 'horizontal split mounted within selected vertical pane');
  const thirdPane = nestedSplit.find((pane) =>
    pane.runtimeId !== firstPane.runtimeId && pane.runtimeId !== secondPane.runtimeId);
  assert.ok(thirdPane, 'nested quick-split equivalent should create a third pane');
  const focusedPanes = nestedSplit.filter((pane) => pane.focused);
  assert.equal(
    focusedPanes.length,
    1,
    `exactly one mounted terminal must render a focused cursor; focused=${focusedPanes.map((pane) => pane.paneId).join(',')}`,
  );
  assert.equal(
    thirdPane.inputFocused,
    true,
    'the pane created by quick split must receive input focus automatically',
  );
  for (const pane of nestedSplit) {
    assert.equal(
      pane.inactiveOverlayOpacity,
      pane.inputFocused ? 0 : 0.3,
      `split pane ${pane.paneId} must mirror Ghostty inactive-split dimming`,
    );
  }
  let latestGeometry = null;
  const settledGeometry = await waitFor(async () => {
    const [firstGeometry, secondGeometry, thirdGeometry] = await Promise.all([
      client.request('get_surface_geometry', { runtime_id: firstPane.runtimeId }),
      client.request('get_surface_geometry', { runtime_id: secondPane.runtimeId }),
      client.request('get_surface_geometry', { runtime_id: thirdPane.runtimeId }),
    ]);
    latestGeometry = { firstGeometry, secondGeometry, thirdGeometry };
    const sameOuterColumns = Math.abs(firstGeometry.cols - secondGeometry.cols) <= 3;
    const splitRowsSettled = physicalDriver
      ? Math.abs(firstGeometry.rows - thirdGeometry.rows) <= 2 && secondGeometry.rows > firstGeometry.rows * 1.7
      : Math.abs(secondGeometry.rows - thirdGeometry.rows) <= 2 && firstGeometry.rows > secondGeometry.rows * 1.7;
    return sameOuterColumns && splitRowsSettled ? latestGeometry : null;
  }, () => `nested split geometry to settle (last=${JSON.stringify(latestGeometry)})`);
  const { firstGeometry, secondGeometry, thirdGeometry } = settledGeometry;
  if (physicalDriver) {
    assert.ok(
      Math.abs(firstGeometry.rows - thirdGeometry.rows) <= 2,
      `quick horizontal split must halve the selected pane; rows=${firstGeometry.rows}/${thirdGeometry.rows}`,
    );
    assert.ok(
      Math.abs(firstGeometry.cols - secondGeometry.cols) <= 3,
      `quick nested split must preserve equal outer vertical columns; cols=${firstGeometry.cols}/${secondGeometry.cols}`,
    );
    assert.ok(
      secondGeometry.rows > firstGeometry.rows * 1.7,
      `unselected sibling must retain full height after quick split; rows=${secondGeometry.rows}/${firstGeometry.rows}`,
    );
  } else {
    assert.ok(
      Math.abs(secondGeometry.rows - thirdGeometry.rows) <= 2,
      `horizontal nested split must halve the selected pane; rows=${secondGeometry.rows}/${thirdGeometry.rows}`,
    );
    assert.ok(
      firstGeometry.rows > secondGeometry.rows * 1.7,
      `unsplit sibling must retain full height; rows=${firstGeometry.rows}/${secondGeometry.rows}`,
    );
    assert.ok(
      Math.abs(firstGeometry.cols - secondGeometry.cols) <= 3,
      `nested split must preserve equal outer vertical columns; cols=${firstGeometry.cols}/${secondGeometry.cols}`,
    );
  }

  const launcherWorkspaceID = (await client.request('get_state')).selectedWorkspaceId;
  const expectedUpPane = physicalDriver ? firstPane : secondPane;
  const moveUp = await client.request('navigate', { direction: 'up' });
  assert.equal(moveUp.result, 'focus_pane', 'navigation within a split must focus a pane before changing workspace');
  assert.equal(moveUp.paneId, expectedUpPane.paneId, 'up navigation must choose the geometrically overlapping pane');
  await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    return panes.find((pane) => pane.paneId === expectedUpPane.paneId)?.inactiveOverlayOpacity === 0 ? panes : null;
  }, 'Cmd+Option+Up-equivalent pane focus');

  const horizontalSourcePane = physicalDriver ? firstPane : secondPane;
  const horizontalTargetPane = physicalDriver ? secondPane : firstPane;
  const horizontalDirection = physicalDriver ? 'right' : 'left';
  await client.request('focus_pane', { runtime_id: horizontalSourcePane.runtimeId });
  await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    return panes.find((pane) => pane.paneId === horizontalSourcePane.paneId)?.inactiveOverlayOpacity === 0 ? panes : null;
  }, 'horizontal navigation source pane focus');
  const moveHorizontal = await client.request('navigate', { direction: horizontalDirection });
  assert.equal(moveHorizontal.result, 'focus_pane', 'horizontal navigation must remain inside the current split when possible');
  assert.equal(moveHorizontal.paneId, horizontalTargetPane.paneId, 'horizontal navigation must choose the adjacent pane by layout geometry');

  await client.request('focus_pane', { runtime_id: firstPane.runtimeId });
  await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    return panes.find((pane) => pane.paneId === firstPane.paneId)?.inactiveOverlayOpacity === 0 ? panes : null;
  }, 'workspace edge source pane focus');
  const moveOut = await client.request('navigate', { direction: 'left' });
  assert.equal(moveOut.result, 'select_workspace', 'left movement at a pane edge must switch workspaces');
  assert.notEqual(moveOut.workspaceId, launcherWorkspaceID, 'edge movement must select a different workspace when available');
  await waitFor(async () => {
    const state = await client.request('get_state');
    return state.selectedWorkspaceId === moveOut.workspaceId ? state : null;
  }, 'edge navigation workspace selection');
  await client.request('select_workspace', { workspace_id: launcherWorkspaceID });
  const restoredNestedSplit = await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes.filter((pane) => pane.attached);
    return panes.length === 3 ? panes : null;
  }, 'restore launcher workspace after edge navigation');

  const survivorsBeforeClose = new Map(
    restoredNestedSplit
      .filter((pane) => pane.paneId !== thirdPane.paneId)
      .map((pane) => [pane.runtimeId, pane.surfaceIdentity]),
  );
  assert.equal(survivorsBeforeClose.size, 2, 'both surviving panes must be mounted before close');
  for (const identity of survivorsBeforeClose.values()) {
    assert.ok(identity, 'surviving panes must expose native terminal identities');
  }
  await client.request('focus_pane', { runtime_id: secondPane.runtimeId });
  await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    return panes.find((pane) => pane.paneId === secondPane.paneId)?.inactiveOverlayOpacity === 0 ? panes : null;
  }, 'previous pane before selected close');
  await client.request('focus_pane', { runtime_id: thirdPane.runtimeId });
  await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    return panes.find((pane) => pane.paneId === thirdPane.paneId)?.inactiveOverlayOpacity === 0 ? panes : null;
  }, 'active pane before selected close');
  await client.request('close_selected_content');
  const survivorsAfterClose = await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes.filter((pane) => pane.attached);
    return panes.length === 2 ? panes : null;
  }, 'nested sibling after closing adjacent pane');
  for (const pane of survivorsAfterClose) {
    assert.equal(
      pane.surfaceIdentity,
      survivorsBeforeClose.get(pane.runtimeId),
      `closing a pane must not remount surviving Ghostty terminal ${pane.runtimeId}`,
    );
  }
  assert.equal(
    survivorsAfterClose.find((pane) => pane.paneId === secondPane.paneId)?.inactiveOverlayOpacity,
    0,
    'closing the active pane must restore the previously selected pane in that workspace',
  );

  let newWorkspaceShortcut = false;
  let keyboardLocationSelection = false;
  let keyboardTabCompletion = false;
  let worktreeComposerPhysicalInput = false;
  let worktreeComposerEscape = false;
  let destinationEscapeFocusReturn = false;
  if (physicalDriver) {
    await withBriefForegroundInput(physicalDriver, () =>
      physicalDriver.pressKey('n', { command: true, shift: true }));
    const shortcutLauncher = await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.presented === 'true' ? launcher : null;
    }, 'Cmd+Shift+N launcher presentation');
    assert.equal(
      shortcutLauncher.mode,
      'new_workspace',
      `Cmd+Shift+N must open New Workspace, got ${shortcutLauncher.mode}`,
    );
    await client.request('cancel_launcher');
    newWorkspaceShortcut = true;

    const keyboardSelectionPath = path.join(temporaryDirectory, `keyboard-selection-${suffix}`);
    fs.mkdirSync(keyboardSelectionPath);
    const resolvedKeyboardSelectionPath = fs.realpathSync(keyboardSelectionPath);
    const selectionPrefix = path.join(temporaryDirectory, 'keyboard-selection-');
    await withBriefForegroundInput(physicalDriver, () =>
      physicalDriver.pressKey('n', { command: true }));
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.mode === 'add_pane' && launcher.presented === 'true' ? launcher : null;
    }, 'Cmd+N Add Pane launcher presentation');
    await client.request('set_launcher_choice', { choice: 'terminal' });
    await client.request('set_launcher_path', { path: selectionPrefix });
    await new Promise((resolve) => setTimeout(resolve, 200));
    const completionSuffix = keyboardSelectionPath.slice(selectionPrefix.length);
    const ghostState = await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.ghostCompletionSuffix === completionSuffix ? launcher : null;
    }, 'Add Pane ghost-text completion candidate');
    assert.equal(
      ghostState.ghostCompletion,
      keyboardSelectionPath,
      'the visible ghost suffix must complete the matching location path',
    );
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.pressKeyCode(48));
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.path === keyboardSelectionPath && launcher.highlightedLocationPath === '' ? launcher : null;
    }, 'Tab to apply Add Pane ghost-text location completion');
    keyboardTabCompletion = true;
    await client.request('set_launcher_path', { path: selectionPrefix });
    await new Promise((resolve) => setTimeout(resolve, 200));
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.pressKeyCode(125));
    const highlighted = await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.highlightedLocationPath ? launcher.highlightedLocationPath : null;
    }, 'launcher navigation to highlight a matching Add Pane location');
    assert.equal(
      highlighted,
      keyboardSelectionPath,
      'ArrowDown must highlight the matching Add Pane location without rewriting the input',
    );
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.pressEnter());
    const keyboardPane = await waitFor(async () => {
      const state = await client.request('get_state');
      if (state.launcher.presented !== 'false') return null;
      const panes = (await client.request('list_panes')).panes.filter((pane) => pane.attached);
      return panes.length === 3
        ? panes.find((pane) => pane.runtimeId !== firstPane.runtimeId && pane.runtimeId !== secondPane.runtimeId)
        : null;
    }, 'Return to open highlighted Add Pane location');
    const keyboardCwdMarker = `__KEYBOARD_CWD_${suffix}__`;
    await submitTerminalCommand(client, keyboardPane.runtimeId, `printf '${keyboardCwdMarker}'; pwd`);
    let observedKeyboardText = null;
    await waitFor(async () => {
      const pane = await client.request('read_pane_text', { runtime_id: keyboardPane.runtimeId });
      observedKeyboardText = pane.text;
      return pane.text.includes(`${keyboardCwdMarker}${resolvedKeyboardSelectionPath}`) ? pane.text : null;
    }, () => `keyboard-selected Add Pane terminal pwd (last=${JSON.stringify(observedKeyboardText?.slice(-200))}, expected=${JSON.stringify(resolvedKeyboardSelectionPath)})`);
    await client.request('close_selected_content');
    await waitFor(async () => {
      const panes = (await client.request('list_panes')).panes.filter((pane) => pane.attached);
      return panes.length === 2 ? panes : null;
    }, 'remove keyboard selection verification pane');
    keyboardLocationSelection = true;

    await client.request('open_new_workspace_dialog');
    await client.request('set_launcher_path', { path: REPO_ROOT });
    await client.request('submit_launcher_location');
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.stage === 'destinations' ? launcher : null;
    }, 'repository destinations for inline worktree input verification');
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.pressKeyCode(53));
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.presented === 'true' && launcher.stage === 'location'
        ? launcher
        : null;
    }, 'Escape returns from repository destinations to location input');
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.pressKeyCode(53));
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.presented === 'false' ? launcher : null;
    }, 'second Escape closes launcher after returning focus to location input');
    destinationEscapeFocusReturn = true;
    await client.request('open_new_workspace_dialog');
    await client.request('set_launcher_path', { path: REPO_ROOT });
    await client.request('submit_launcher_location');
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.stage === 'destinations' ? launcher : null;
    }, 'repository destinations reopened for inline worktree input verification');
    for (let i = 0; i < 64; i += 1) {
      await client.request('perform_launcher_action', { action: 'move_destination_down' });
    }
    await client.request('perform_launcher_action', { action: 'accept_destination' });
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.isCreatingWorktree === 'true' ? launcher : null;
    }, 'inline worktree composer presentation');
    const branchDraft = `focus-${suffix}`;
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.typeText(branchDraft));
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.newBranch === branchDraft ? launcher : null;
    }, 'inline worktree branch field receives keyboard focus automatically');
    worktreeComposerPhysicalInput = true;
    await withBriefForegroundInput(physicalDriver, () => physicalDriver.pressKeyCode(53));
    await waitFor(async () => {
      const launcher = await client.request('get_launcher_state');
      return launcher.presented === 'true'
        && launcher.stage === 'destinations'
        && launcher.isCreatingWorktree === 'false'
        ? launcher
        : null;
    }, 'Escape closes inline worktree composer without closing launcher');
    worktreeComposerEscape = true;
    await client.request('cancel_launcher');
    await client.request('cancel_launcher');
  }

  return {
    workspaceDirectory: resolvedWorkspaceDirectory,
    focusedTerminalDirectoryDefault: resolvedPaneDirectory,
    firstPaneRuntimeID: firstPane.runtimeId,
    secondPaneRuntimeID: secondPane.runtimeId,
    shellFirstWorkspace: true,
    typedPathFiltering: true,
    directionalAddPane: true,
    simultaneouslyMountedPanes: nestedSplit.length,
    nestedSplitGeometry: { firstGeometry, secondGeometry, thirdGeometry },
    spatialPaneNavigation: true,
    workspaceEdgeNavigation: true,
    survivingSurfaceRetainedAfterClose: true,
    restoresPreviousPaneAfterClose: true,
    postDialogPhysicalInput,
    newWorkspaceShortcut,
    keyboardLocationSelection,
    keyboardTabCompletion,
    worktreeComposerPhysicalInput,
    worktreeComposerEscape,
    destinationEscapeFocusReturn,
  };
}

async function exerciseRetainedEmptyWorkspace(client, daemon, temporaryDirectory, suffix) {
  async function createRetainedEmptyWorkspace(label) {
    const directory = path.join(temporaryDirectory, label);
    fs.mkdirSync(directory);
    const workspaceID = `swift-native-${label}-${suffix}`;
    const runtimeID = `swift-runtime-${label}-${suffix}`;
    await daemon.bootstrapWorkspace({ workspaceID, runtimeID, agent: 'shell', directory });
    await client.request('select_workspace', { workspace_id: workspaceID });
    await waitFor(async () => {
      const panes = (await client.request('list_panes')).panes;
      return panes.some((pane) => pane.runtimeId === runtimeID && pane.attached) ? panes : null;
    }, `${label} root pane attachment`);
    await daemon.unregisterSession(runtimeID);
    let emptyObservation = null;
    await waitFor(async () => {
      const state = await client.request('get_state');
      const panes = (await client.request('list_panes')).panes;
      emptyObservation = { selectedWorkspaceId: state.selectedWorkspaceId, panes };
      return state.selectedWorkspaceId === workspaceID && panes.length === 0 ? state : null;
    }, () => `${label} retained empty workspace layout (observed=${JSON.stringify(emptyObservation)})`);
    return { workspaceID, directory };
  }

  const closable = await createRetainedEmptyWorkspace('empty-close');
  await client.request('close_selected_content');
  await waitFor(async () => {
    const state = await client.request('get_state');
    return state.selectedWorkspaceId !== closable.workspaceID ? state : null;
  }, 'close retained empty workspace through Cmd+W controller path');

  const refillable = await createRetainedEmptyWorkspace('empty-refill');
  const escape = await client.request('navigate', { direction: 'right' });
  assert.equal(escape.result, 'select_workspace', 'empty workspace must remain keyboard-navigable');
  assert.notEqual(escape.workspaceId, refillable.workspaceID, 'navigation must leave the empty workspace');
  await client.request('select_workspace', { workspace_id: refillable.workspaceID });

  const opened = await client.request('open_add_pane_dialog', { direction: 'vertical' });
  assert.equal(opened.mode, 'add_pane', 'empty workspace must offer Add Pane instead of rejecting the request');
  assert.equal(opened.presented, 'true');
  assert.equal(opened.error, '');
  await client.request('set_launcher_choice', { choice: 'terminal' });
  await client.request('set_launcher_path', { path: refillable.directory });
  await client.request('submit_launcher_location');

  const pane = await waitFor(async () => {
    const state = await client.request('get_state');
    const panes = (await client.request('list_panes')).panes;
    return state.selectedWorkspaceId === refillable.workspaceID
      && state.launcher.presented === 'false'
      && panes.length === 1
      && panes[0].attached
      ? panes[0]
      : null;
  }, 'replacement root pane in retained workspace');
  const marker = `__EMPTY_REFILLED_${suffix}__`;
  await submitTerminalCommand(client, pane.runtimeId, `printf '${marker}\\n'`);
  await waitFor(async () => {
    const rendered = await client.request('read_pane_text', { runtime_id: pane.runtimeId });
    return rendered.text.includes(marker) ? rendered : null;
  }, 'input in refilled retained workspace');

  return {
    emptyWorkspaceClose: true,
    emptyWorkspaceKeyboardEscape: true,
    emptyWorkspaceAddPane: true,
  };
}

async function exerciseRealClaudeInactiveSplitTreatment(client, claudeResult) {
  await client.request('select_workspace', { workspace_id: claudeResult.workspaceID });
  const quickSplit = await client.request('quick_split', { direction: 'vertical' });
  assert.equal(quickSplit.direction, 'vertical');
  const splitPanes = await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes.filter((pane) => pane.attached);
    return panes.length === 2 ? panes : null;
  }, 'real Claude inactive split treatment');
  const claudePane = splitPanes.find((pane) => pane.runtimeId === claudeResult.runtimeID);
  const activePane = splitPanes.find((pane) => pane.runtimeId !== claudeResult.runtimeID);
  assert.ok(claudePane, 'real Claude pane must remain mounted after splitting');
  assert.ok(activePane, 'new active pane must mount beside real Claude');
  assert.equal(claudePane.focused, false, 'real Claude must yield Ghostty surface focus to the new split');
  assert.equal(
    claudePane.inactiveOverlayOpacity,
    0.3,
    'real Claude inactive pane must receive Ghostty.app-compatible dimming',
  );
  assert.equal(activePane.inactiveOverlayOpacity, 0, 'focused sibling must remain undimmed');
  const claudeNotifiedOfFocusLoss = await waitFor(async () => {
    const panes = (await client.request('list_panes')).panes;
    const current = panes.find((pane) => pane.runtimeId === claudeResult.runtimeID);
    return current && current.focusLossWrites > 0 ? current.focusLossWrites : null;
  }, 'directly launched real Claude to receive terminal focus-loss notification');
  assert.ok(
    claudeNotifiedOfFocusLoss > 0,
    'directly launched real Claude must receive focus loss after its startup modes are replayed',
  );
  return true;
}

async function exerciseCloseCascade(client, physicalDriver, nativePID) {
  let closedPanes = 0;
  let closedWorkspaces = 0;
  const requestClose = physicalDriver
    ? () => withBriefForegroundInput(physicalDriver, () =>
      physicalDriver.pressKey('w', { command: true }))
    : () => client.request('close_selected_content');
  while (true) {
    const state = await client.request('get_state');
    if (!state.selectedWorkspaceId) break;
    const workspaceID = state.selectedWorkspaceId;
    const panes = (await client.request('list_panes')).panes;
    assert.ok(panes.length > 0, 'selected workspace must expose at least one pane before closing');
    await requestClose();
    if (panes.length > 1) {
      await waitFor(async () => {
        const current = await client.request('get_state');
        if (current.selectedWorkspaceId !== workspaceID) return null;
        const remaining = (await client.request('list_panes')).panes;
        return remaining.length === panes.length - 1 ? remaining : null;
      }, `Cmd+W to close one pane in workspace ${workspaceID}`);
      closedPanes += 1;
    } else {
      await waitFor(async () => {
        const current = await client.request('get_state');
        return current.selectedWorkspaceId !== workspaceID ? current : null;
      }, `Cmd+W to close workspace ${workspaceID}`);
      closedWorkspaces += 1;
    }
  }

  const emptyState = await client.request('get_state');
  assert.equal(emptyState.selectedWorkspaceId, null, 'all workspace content should be gone before closing the window');
  assert.deepEqual((await client.request('list_panes')).panes, [], 'empty window must render no panes before it closes');

  if (physicalDriver) {
    await requestClose();
    await waitFor(() => {
      try {
        process.kill(nativePID, 0);
        return null;
      } catch {
        return true;
      }
    }, 'native process exit after closing empty window');
  }
  return {
    closedPanes,
    closedWorkspaces,
    emptyBackgroundBeforeWindowClose: true,
    processExitedWithWindow: physicalDriver ? true : null,
  };
}

async function main() {
  const physicalInputEnabled = process.env.ATTN_NATIVE_PHYSICAL_INPUT === '1';
  if (physicalInputEnabled && process.env.ATTN_NATIVE_INTERACTIVE_TEST !== '1') {
    throw new Error(
      'ATTN_NATIVE_PHYSICAL_INPUT requires ATTN_NATIVE_INTERACTIVE_TEST=1 because HID input briefly takes foreground focus.',
    );
  }
  const suffix = crypto.randomBytes(3).toString('hex');
  const profile = `ng${process.pid.toString(36)}${suffix}`.slice(0, 15);
  const port = await reservePort();
  const wsURL = `ws://127.0.0.1:${port}/ws`;
  const manifestPath = manifestPathForNativeProfile(profile);
  const appDataDir = path.dirname(path.dirname(manifestPath));
  const daemonDataDir = path.join(os.homedir(), `.attn-${profile}`);
  const temporaryDirectory = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-native-terminal-'));
  const daemonProcessLogPath = path.join(temporaryDirectory, 'daemon-process.stderr.log');
  const keepArtifacts = process.env.ATTN_NATIVE_KEEP_ARTIFACTS === '1';
  const appPath = process.env.ATTN_NATIVE_APP_PATH || DEFAULT_APP_PATH;
  const daemonBinary = process.env.ATTN_NATIVE_DAEMON_BINARY || path.join(REPO_ROOT, 'attn');
  const previousForegroundPID = await frontmostPID();
  const agentExecutable = deterministicAgentExecutable(temporaryDirectory);
  let daemonProcess;
  let daemonExit = null;
  let nativePID;
  let controller;
  let scenarioFailed = false;

  assert.ok(fs.existsSync(appPath), `signed Swift native app not found: ${appPath}`);
  assert.ok(fs.existsSync(daemonBinary), `daemon binary not found: ${daemonBinary}`);

  const isolatedEnv = { ...process.env, ATTN_PROFILE: profile, ATTN_WS_PORT: String(port) };
  for (const key of ['ATTN_SOCKET_PATH', 'ATTN_DB_PATH', 'ATTN_CONFIG_PATH', 'ATTN_WRAPPER_PATH',
    'ATTN_INSIDE_APP', 'ATTN_DAEMON_MANAGED', 'ATTN_PTY_WORKER', 'ATTN_SESSION_ID', 'ATTN_AGENT']) {
    delete isolatedEnv[key];
  }

  try {
    const daemonProcessLogFD = fs.openSync(daemonProcessLogPath, 'a');
    daemonProcess = spawn(daemonBinary, ['daemon'], {
      cwd: REPO_ROOT,
      env: isolatedEnv,
      stdio: ['ignore', daemonProcessLogFD, daemonProcessLogFD],
      detached: true,
    });
    fs.closeSync(daemonProcessLogFD);
    daemonProcess.once('exit', (code, signal) => {
      daemonExit = { code, signal };
    });
    controller = new DaemonCommands(wsURL);
    await controller.connect();
    await execFileAsync('open', [
      '-n',
      '--env', `ATTN_PROFILE=${profile}`,
      '--env', 'ATTN_AUTOMATION=1',
      '--env', 'ATTN_AUTOMATION_BACKGROUND=1',
      '--env', `ATTN_AUTOMATION_RESTORE_FOREGROUND_PID=${previousForegroundPID}`,
      '--env', `ATTN_NATIVE_WS_URL=${wsURL}`,
      appPath,
    ]);
    const client = new UiAutomationClient({ manifestPath });
    await client.waitForManifest(20_000);
    nativePID = client.readManifest().pid;
    await client.waitForReady(20_000);
    await waitFor(async () => (await client.request('get_state')).daemonReady, 'native daemon connection');
    const parkedWindow = await client.request('park_window', { visible_px: 20 });
    assert.equal(parkedWindow.parked, true, 'native terminal harness should park its test window');
    assert.equal(parkedWindow.visiblePx, 20, 'native terminal harness should leave only a narrow visible strip');
    const physicalDriver = physicalInputEnabled
      ? new MacOSDriver({ pid: nativePID, actionDelayMs: 20 })
      : null;

    const results = [];
    for (const agent of ['shell', 'claude', 'codex']) {
      results.push(await exerciseRenderedTerminal(
        client,
        controller,
        { agent, real: false, timeoutMs: 20_000 },
        suffix,
        agent === 'shell' ? undefined : agentExecutable,
        physicalDriver,
      ));
    }
    const addedAgentBannerSuppressed = await exerciseAddedAgentDoesNotRenderProfileBanner(
      client,
      controller,
      temporaryDirectory,
      suffix,
      agentExecutable,
    );
    const mouseInput = await exerciseSelectionAndCapturedMouse(client, controller, temporaryDirectory, suffix);
    const launcher = await exerciseWorkspaceLauncher(client, controller, temporaryDirectory, suffix, physicalDriver);
    const retainedEmptyWorkspace = await exerciseRetainedEmptyWorkspace(client, controller, temporaryDirectory, suffix);
    const screenshotPath = path.join(temporaryDirectory, 'ghostty-terminal-window.png');
    await client.request('screenshot_window', { path: screenshotPath });
    assert.ok(fs.statSync(screenshotPath).size > 0, 'rendered terminal window screenshot must be non-empty');

    if (process.env.ATTN_NATIVE_REAL_AGENTS === '1') {
      const realClaude = await exerciseRenderedTerminal(
        client,
        controller,
        { agent: 'claude', real: true, readyPattern: /Claude|trust this folder|❯/u, timeoutMs: 60_000 },
        `${suffix}real`,
        undefined,
        null,
      );
      results.push(realClaude);
      const realClaudeInactiveSplitTreatment = await exerciseRealClaudeInactiveSplitTreatment(client, realClaude);
      results.push(await exerciseRenderedTerminal(
        client,
        controller,
        { agent: 'codex', real: true, readyPattern: /OpenAI Codex|Update available|model:|100% left/u, timeoutMs: 60_000 },
        `${suffix}real`,
        undefined,
        null,
      ));
      launcher.realClaudeInactiveSplitTreatment = realClaudeInactiveSplitTreatment;
    }

    const closeCascade = await exerciseCloseCascade(client, physicalDriver, nativePID);
    if (!physicalDriver) {
      assert.notEqual(await frontmostPID(), nativePID, 'native terminal automation must remain non-frontmost');
    }
    assert.equal(daemonExit, null, `daemon exited during native close cascade: ${JSON.stringify(daemonExit)}`);
    console.log(JSON.stringify({
      profile,
      nativePID,
      directDaemonBootstrap: true,
      addedAgentBannerSuppressed,
      parkedWindow,
      mouseInput,
      launcher,
      retainedEmptyWorkspace,
      terminalScreenshot: true,
      results,
      closeCascade,
    }, null, 2));
  } catch (error) {
    scenarioFailed = true;
    throw error;
  } finally {
    controller?.close();
    if (!nativePID && fs.existsSync(manifestPath)) {
      try {
        nativePID = JSON.parse(fs.readFileSync(manifestPath, 'utf8')).pid;
      } catch {
        // Ignore a partially written late manifest; daemon cleanup still runs.
      }
    }
    if (nativePID) {
      try { process.kill(nativePID, 'SIGTERM'); } catch {}
    }
    if (daemonProcess) {
      try {
        process.kill(-daemonProcess.pid, 'SIGTERM');
      } catch {
        daemonProcess.kill('SIGTERM');
      }
    }
    if (keepArtifacts || scenarioFailed) {
      console.error(`Retained native terminal harness artifacts: ${temporaryDirectory} ${daemonDataDir} ${appDataDir}`);
      console.error(`Captured daemon process output: ${daemonProcessLogPath}`);
    } else {
      fs.rmSync(appDataDir, { recursive: true, force: true });
      fs.rmSync(daemonDataDir, { recursive: true, force: true });
      fs.rmSync(temporaryDirectory, { recursive: true, force: true });
    }
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exitCode = 1;
});
