#!/usr/bin/env node

import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { promisify } from 'node:util';

import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { captureFrontWindowScreenshot } from './nativeWindowCapture.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function saveJson(filePath, value) {
  fs.writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

async function runSSH(target, command, timeoutMs = 30_000) {
  const { stdout } = await execFileAsync(
    'ssh',
    [
      '-o', 'BatchMode=yes',
      '-o', 'StrictHostKeyChecking=accept-new',
      '-o', 'ConnectTimeout=10',
      target,
      `bash -lc ${shellQuote(command)}`,
    ],
    {
      timeout: timeoutMs,
      maxBuffer: 1024 * 1024 * 8,
    },
  );
  return stdout;
}

async function getRemoteHome(target) {
  return (await runSSH(target, 'printf %s "$HOME"')).trim();
}

function chooseRemoteWSPort() {
  return 19000 + Math.floor(Math.random() * 2000);
}

async function waitForEndpointConnected(observer, name, timeoutMs = 120_000) {
  const startedAt = Date.now();
  let lastEndpoint = null;
  while (Date.now() - startedAt < timeoutMs) {
    const endpoint = observer.findEndpointByName(name);
    if (endpoint) {
      lastEndpoint = endpoint;
      if (endpoint.status === 'connected') {
        return endpoint;
      }
      if (endpoint.status === 'error') {
        throw new Error(`Endpoint ${name} entered error state: ${endpoint.status_message || 'unknown error'}`);
      }
    }
    await sleep(500);
  }
  throw new Error(
    `Timed out waiting for endpoint ${name} to connect. Last endpoint state:\n${JSON.stringify(lastEndpoint, null, 2)}`
  );
}

async function removeStaleHarnessEndpoints(observer, timeoutMs = 20_000) {
  const staleEndpoints = [...observer.endpointsById.values()].filter((endpoint) =>
    typeof endpoint?.name === 'string' && endpoint.name.startsWith('harness-')
  );
  for (const endpoint of staleEndpoints) {
    observer.removeEndpoint(endpoint.id);
  }
  if (staleEndpoints.length === 0) {
    return;
  }
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    const remaining = staleEndpoints.filter((endpoint) => observer.getEndpoint(endpoint.id));
    if (remaining.length === 0) {
      return;
    }
    await sleep(250);
  }
}

async function waitForPaneState(client, sessionId, paneId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastState = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastState = await client.request('get_pane_state', { sessionId, paneId }, { timeoutMs: 20_000 });
    if (predicate(lastState)) {
      return lastState;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last pane state:\n${JSON.stringify(lastState, null, 2)}`
  );
}

async function waitForPaneVisible(client, sessionId, paneId, timeoutMs = 20_000) {
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(
      state?.pane?.bounds &&
      state.pane.bounds.width >= 120 &&
      state.pane.bounds.height >= 80 &&
      state?.renderHealth?.flags?.terminalVisible !== false
    ),
    `pane ${paneId} to be visible`,
    timeoutMs,
  );
}

async function waitForPaneInputFocus(client, sessionId, paneId, timeoutMs = 12_000) {
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => Boolean(state?.inputFocused),
    `pane ${paneId} input focus`,
    timeoutMs,
  );
}

async function waitForPaneText(client, sessionId, paneId, needle, timeoutMs = 15_000) {
  const startedAt = Date.now();
  let lastPayload = null;
  let lastText = '';
  const compactNeedle = needle.replace(/\s+/g, '');

  while (Date.now() - startedAt < timeoutMs) {
    lastPayload = await client.request('read_pane_text', { sessionId, paneId }, { timeoutMs: 20_000 });
    lastText = lastPayload?.text || '';
    if (lastText.includes(needle) || lastText.replace(/\s+/g, '').includes(compactNeedle)) {
      return lastPayload;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for pane ${paneId} text to contain ${JSON.stringify(needle)}. Last pane text tail:\n${lastText.slice(-600)}`
  );
}

async function waitForSessionWorkspace(client, sessionId, predicate, description, timeoutMs = 20_000) {
  const startedAt = Date.now();
  let lastWorkspace = null;

  while (Date.now() - startedAt < timeoutMs) {
    lastWorkspace = await client.request('get_workspace', { sessionId }, { timeoutMs: 20_000 });
    if (predicate(lastWorkspace)) {
      return lastWorkspace;
    }
    await sleep(200);
  }

  throw new Error(
    `Timed out waiting for ${description}. Last workspace:\n${JSON.stringify(lastWorkspace, null, 2)}`
  );
}

function shellPanes(workspace) {
  return (workspace?.panes || []).filter((pane) => pane.kind === 'shell');
}

async function waitForNewShellPane(client, sessionId, existingPaneIds, description, timeoutMs = 20_000) {
  return waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => shellPanes(workspace).some((pane) => !existingPaneIds.has(pane.paneId)),
    description,
    timeoutMs,
  ).then((workspace) => {
    const newShells = shellPanes(workspace).filter((pane) => !existingPaneIds.has(pane.paneId));
    const activeShell = newShells.find((pane) => pane.paneId === workspace.activePaneId);
    return activeShell || newShells[0];
  });
}

async function tryCaptureNativeWindow(runDir, filename) {
  try {
    return await captureFrontWindowScreenshot(path.join(runDir, filename));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${filename}.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
    return null;
  }
}

async function captureSessionArtifacts(client, runDir, prefix, sessionId) {
  try {
    saveJson(path.join(runDir, `${prefix}-workspace.json`), await client.request('get_workspace', { sessionId }));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-workspace.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
  }

  try {
    saveJson(path.join(runDir, `${prefix}-session-ui-state.json`), await client.request('get_session_ui_state', { sessionId }));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-session-ui-state.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
  }

  try {
    saveJson(path.join(runDir, `${prefix}-structured-snapshot.json`), await client.request('capture_structured_snapshot', {
      sessionIds: [sessionId],
      includePaneText: false,
    }));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-structured-snapshot.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
  }

  try {
    saveJson(path.join(runDir, `${prefix}-render-health.json`), await client.request('capture_render_health', {
      sessionIds: [sessionId],
    }));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-render-health.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
  }

  try {
    saveJson(path.join(runDir, `${prefix}-perf-snapshot.json`), await client.request('capture_perf_snapshot', {
      sessionIds: [sessionId],
      settleFrames: 2,
      includeMemory: false,
    }));
  } catch (error) {
    fs.writeFileSync(
      path.join(runDir, `${prefix}-perf-snapshot.txt`),
      error instanceof Error ? error.stack || error.message : String(error),
      'utf8',
    );
  }

  await tryCaptureNativeWindow(runDir, `${prefix}-native-window.png`);
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    sshTarget: process.env.ATTN_REMOTE_RELAUNCH_SPLITS_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_RELAUNCH_SPLITS_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_RELAUNCH_SPLITS_REMOTE_AGENT || 'codex',
  };
  const remaining = [];

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ssh-target') {
      options.sshTarget = args[++index] || options.sshTarget;
    } else if (arg === '--remote-directory') {
      options.remoteDirectory = args[++index] || '';
    } else if (arg === '--remote-agent') {
      options.remoteAgent = args[++index] || options.remoteAgent;
    } else {
      remaining.push(arg);
    }
  }

  return {
    options: {
      ...parseCommonArgs(remaining),
      sshTarget: options.sshTarget,
      remoteDirectory: options.remoteDirectory,
      remoteAgent: options.remoteAgent,
    },
    help: remaining.includes('--help') || remaining.includes('-h'),
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/bridge-remote-relaunch-splits.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>      Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>         Agent for the remote session (default: codex)
`);
    return;
  }

  const { runId, runDir } = createRunContext(options, 'bridge-remote-relaunch-splits');
  const endpointName = `harness-${runId}`;
  const remoteHome = await getRemoteHome(options.sshTarget);
  const remoteDirectory = options.remoteDirectory || remoteHome;
  const remoteHarnessRoot = path.posix.join(remoteHome, '.attn', 'harness', runId);
  const remoteHarnessBinary = path.posix.join(remoteHarnessRoot, 'bin', 'attn');
  const remoteHarnessSocket = path.posix.join(remoteHarnessRoot, 'attn.sock');
  const remoteHarnessDB = path.posix.join(remoteHarnessRoot, 'attn.db');
  const remoteHarnessWSPort = String(chooseRemoteWSPort());
  const preRelaunchToken = `__REMOTE_RELAUNCH_PRE_${Date.now()}__`;
  const postRelaunchMainToken = `__REMOTE_RELAUNCH_MAIN_${Date.now()}__`;
  const postRelaunchShellToken = `__REMOTE_RELAUNCH_SHELL_${Date.now()}__`;

  const client = new UiAutomationClient({
    appPath: options.appPath,
    launchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
      ATTN_REMOTE_ATTN_BIN: remoteHarnessBinary,
      ATTN_REMOTE_SOCKET_PATH: remoteHarnessSocket,
      ATTN_REMOTE_DB_PATH: remoteHarnessDB,
      ATTN_REMOTE_WS_PORT: remoteHarnessWSPort,
    },
  });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let endpoint = null;
  let sessionId = null;
  let initialShellPaneId = null;
  let postRelaunchMainSplitPaneId = null;
  let postRelaunchShellSplitPaneId = null;

  try {
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();
    await removeStaleHarnessEndpoints(observer, 20_000);

    observer.addEndpoint(endpointName, options.sshTarget);
    endpoint = await waitForEndpointConnected(observer, endpointName, 120_000);
    saveJson(path.join(runDir, 'endpoint.json'), endpoint);

    sessionId = (await client.request('create_session', {
      cwd: remoteDirectory,
      label: `remote-relaunch-${runId}`,
      agent: options.remoteAgent,
      endpoint_id: endpoint.id,
    })).sessionId;
    await observer.waitForSession({ id: sessionId, timeoutMs: 30_000 });
    await observer.waitForWorkspace(
      sessionId,
      (workspace) => (workspace.panes || []).length >= 1,
      `initial workspace for ${sessionId}`,
      30_000,
    );

    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, 'main', 30_000);

    await client.request('split_pane', {
      sessionId,
      targetPaneId: 'main',
      direction: 'vertical',
    });
    const preRelaunchWorkspace = await waitForSessionWorkspace(
      client,
      sessionId,
      (workspace) => shellPanes(workspace).length >= 1,
      'initial pre-relaunch split pane',
      30_000,
    );
    const initialShellPane = shellPanes(preRelaunchWorkspace)[0];
    initialShellPaneId = initialShellPane?.paneId || null;
    if (!initialShellPaneId) {
      throw new Error('Initial shell pane was not created before relaunch');
    }

    await client.request('focus_pane', { sessionId, paneId: initialShellPaneId });
    await waitForPaneVisible(client, sessionId, initialShellPaneId, 20_000);
    await waitForPaneInputFocus(client, sessionId, initialShellPaneId, 20_000);
    await client.request('type_pane_via_ui', {
      sessionId,
      paneId: initialShellPaneId,
      text: preRelaunchToken,
    });
    await waitForPaneText(client, sessionId, initialShellPaneId, preRelaunchToken, 20_000);

    await captureSessionArtifacts(client, runDir, '01-pre-relaunch', sessionId);

    await client.quitApp();
    await sleep(750);
    await client.launchApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);

    await client.request('select_session', { sessionId });
    await waitForPaneVisible(client, sessionId, 'main', 30_000);
    await waitForPaneVisible(client, sessionId, initialShellPaneId, 30_000);

    await captureSessionArtifacts(client, runDir, '02-post-relaunch', sessionId);

    const beforeMainSplitWorkspace = await client.request('get_workspace', { sessionId });
    const beforeMainSplitPaneIds = new Set((beforeMainSplitWorkspace.panes || []).map((pane) => pane.paneId));
    await client.request('split_pane', {
      sessionId,
      targetPaneId: 'main',
      direction: 'vertical',
    });
    const postMainSplitPane = await waitForNewShellPane(
      client,
      sessionId,
      beforeMainSplitPaneIds,
      'new shell pane after relaunch split from main',
      30_000,
    );
    postRelaunchMainSplitPaneId = postMainSplitPane?.paneId || null;
    if (!postRelaunchMainSplitPaneId) {
      throw new Error('Post-relaunch split from main did not produce a new pane');
    }

    await client.request('focus_pane', { sessionId, paneId: postRelaunchMainSplitPaneId });
    await waitForPaneVisible(client, sessionId, postRelaunchMainSplitPaneId, 20_000);
    await waitForPaneInputFocus(client, sessionId, postRelaunchMainSplitPaneId, 20_000);
    await client.request('type_pane_via_ui', {
      sessionId,
      paneId: postRelaunchMainSplitPaneId,
      text: postRelaunchMainToken,
    });
    await waitForPaneText(client, sessionId, postRelaunchMainSplitPaneId, postRelaunchMainToken, 20_000);

    await captureSessionArtifacts(client, runDir, '03-after-main-split', sessionId);

    const beforeShellSplitWorkspace = await client.request('get_workspace', { sessionId });
    const beforeShellSplitPaneIds = new Set((beforeShellSplitWorkspace.panes || []).map((pane) => pane.paneId));
    await client.request('split_pane', {
      sessionId,
      targetPaneId: initialShellPaneId,
      direction: 'vertical',
    });
    const postShellSplitPane = await waitForNewShellPane(
      client,
      sessionId,
      beforeShellSplitPaneIds,
      'new shell pane after relaunch split from utility pane',
      30_000,
    );
    postRelaunchShellSplitPaneId = postShellSplitPane?.paneId || null;
    if (!postRelaunchShellSplitPaneId) {
      throw new Error('Post-relaunch split from utility did not produce a new pane');
    }

    await client.request('focus_pane', { sessionId, paneId: postRelaunchShellSplitPaneId });
    await waitForPaneVisible(client, sessionId, postRelaunchShellSplitPaneId, 20_000);
    await waitForPaneInputFocus(client, sessionId, postRelaunchShellSplitPaneId, 20_000);
    await client.request('type_pane_via_ui', {
      sessionId,
      paneId: postRelaunchShellSplitPaneId,
      text: postRelaunchShellToken,
    });
    await waitForPaneText(client, sessionId, postRelaunchShellSplitPaneId, postRelaunchShellToken, 20_000);

    await captureSessionArtifacts(client, runDir, '04-after-shell-split', sessionId);

    const finalWorkspace = await client.request('get_workspace', { sessionId });
    const finalUiState = await client.request('get_session_ui_state', { sessionId });
    const summary = {
      ok: true,
      scenarioId: 'TR-502',
      runId,
      sessionId,
      endpointId: endpoint?.id || null,
      sshTarget: options.sshTarget,
      remoteAgent: options.remoteAgent,
      remoteDirectory,
      tokens: {
        preRelaunchToken,
        postRelaunchMainToken,
        postRelaunchShellToken,
      },
      panes: {
        initialShellPaneId,
        postRelaunchMainSplitPaneId,
        postRelaunchShellSplitPaneId,
      },
      finalWorkspace: {
        activePaneId: finalWorkspace.activePaneId,
        paneIds: (finalWorkspace.panes || []).map((pane) => pane.paneId),
        shellPaneIds: shellPanes(finalWorkspace).map((pane) => pane.paneId),
      },
      finalUiState,
      artifacts: {
        runDir,
        files: [
          'endpoint.json',
          '01-pre-relaunch-workspace.json',
          '01-pre-relaunch-session-ui-state.json',
          '01-pre-relaunch-structured-snapshot.json',
          '01-pre-relaunch-render-health.json',
          '01-pre-relaunch-perf-snapshot.json',
          '01-pre-relaunch-native-window.png',
          '02-post-relaunch-workspace.json',
          '02-post-relaunch-session-ui-state.json',
          '02-post-relaunch-structured-snapshot.json',
          '02-post-relaunch-render-health.json',
          '02-post-relaunch-perf-snapshot.json',
          '02-post-relaunch-native-window.png',
          '03-after-main-split-workspace.json',
          '03-after-main-split-session-ui-state.json',
          '03-after-main-split-structured-snapshot.json',
          '03-after-main-split-render-health.json',
          '03-after-main-split-perf-snapshot.json',
          '03-after-main-split-native-window.png',
          '04-after-shell-split-workspace.json',
          '04-after-shell-split-session-ui-state.json',
          '04-after-shell-split-structured-snapshot.json',
          '04-after-shell-split-render-health.json',
          '04-after-shell-split-perf-snapshot.json',
          '04-after-shell-split-native-window.png',
          'summary.json',
        ],
      },
    };
    saveJson(path.join(runDir, 'summary.json'), summary);
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runDir, 'failure', sessionId);
    }
    saveJson(path.join(runDir, 'failure.json'), {
      ok: false,
      scenarioId: 'TR-502',
      runId,
      sessionId,
      error: error instanceof Error ? error.stack || error.message : String(error),
    });
    console.error(error instanceof Error ? error.stack || error.message : error);
    process.exitCode = 1;
  } finally {
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
