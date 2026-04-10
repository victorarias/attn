#!/usr/bin/env node

import path from 'node:path';
import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  captureSessionArtifacts,
  waitForNewShellPane,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import {
  buildRemoteHarnessPaths,
  cleanupRemoteHarnessProcesses,
  chooseRemoteWSPort,
  getRemoteHome,
  listRemoteProcessesByHarnessRoot,
  removeStaleHarnessEndpoints,
  removeStaleHarnessScenarioSessions,
  runSSH,
  waitForEndpointConnected,
  waitForRemoteProcessesByHarnessRoot,
} from './scenarioRemote.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
    sshTarget: process.env.ATTN_REMOTE_CLEANUP_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_CLEANUP_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_CLEANUP_REMOTE_AGENT || 'codex',
  };

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--ssh-target') options.sshTarget = args[++index] || options.sshTarget;
    else if (arg === '--remote-directory') options.remoteDirectory = args[++index] || '';
    else if (arg === '--remote-agent') options.remoteAgent = args[++index] || options.remoteAgent;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  return {
    options,
    help: Boolean(options.help),
  };
}

function isRemoteHarnessTransportProcess(processInfo) {
  const cmdline = String(processInfo?.cmdline || '');
  return cmdline.includes('ws-relay') || /\battn daemon\b/.test(cmdline);
}

function sessionWorkerProcesses(processes) {
  return (processes || []).filter((processInfo) => !isRemoteHarnessTransportProcess(processInfo));
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr504-remote-cleanup.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>      Remote cwd for the spawned session (default: unique harness workdir)
  --remote-agent <agent>         Agent for the remote session (default: codex)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-504',
    tier: 'tier3-remote-real-agent',
    prefix: 'scenario-tr504-remote-cleanup',
    metadata: {
      sshTarget: options.sshTarget,
      agent: options.remoteAgent,
      focus: 'remote session close tears down worker-side processes',
    },
  });

  const remoteHome = await getRemoteHome(options.sshTarget);
  const remoteHarnessBase = path.posix.join(remoteHome, '.attn', 'harness');
  const remoteDirectory = options.remoteDirectory || path.posix.join(remoteHome, '.attn', 'harness', runner.runId, 'workspace');
  const remotePaths = buildRemoteHarnessPaths(remoteHome, runner.runId);
  const remoteHarnessWSPort = String(chooseRemoteWSPort());
  const client = new UiAutomationClient({
    appPath: options.appPath,
    launchEnv: {
      ATTN_PREFER_LOCAL_DAEMON: '1',
      ATTN_REMOTE_ATTN_BIN: remotePaths.remoteHarnessBinary,
      ATTN_REMOTE_SOCKET_PATH: remotePaths.remoteHarnessSocket,
      ATTN_REMOTE_DB_PATH: remotePaths.remoteHarnessDB,
      ATTN_REMOTE_WS_PORT: remoteHarnessWSPort,
    },
  });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let endpoint = null;
  let sessionId = null;
  let splitPaneId = null;
  let activeProcessSnapshot = [];
  let postCloseProcessSnapshot = [];
  let cleanedProcessSnapshot = [];
  let postEndpointRemovalProcessSnapshot = [];
  let endpointRemoved = false;

  try {
    await runner.step('cleanup_stale_remote_harness_state', async () => {
      const cleanupResult = await cleanupRemoteHarnessProcesses(options.sshTarget, remoteHarnessBase, 60_000);
      runner.writeJson('00-remote-harness-preflight-cleanup.json', cleanupResult);
      runner.assert((cleanupResult.leftover || []).length === 0, 'remote harness preflight cleanup leaves no stale harness-root processes', {
        remoteHarnessBase,
        cleanupResult,
      });
    });

    await runner.step('launch_app_and_connect_daemon', async () => {
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      await observer.connect();
      await removeStaleHarnessEndpoints(observer, 20_000);
      await removeStaleHarnessScenarioSessions(observer, 30_000);
    });

    await runner.step('prepare_remote_workspace', async () => {
      await runSSH(options.sshTarget, `mkdir -p '${remoteDirectory.replace(/'/g, `'\\''`)}'`, 30_000);
      const initialProcesses = await listRemoteProcessesByHarnessRoot(options.sshTarget, remotePaths.remoteHarnessRoot, 30_000);
      runner.writeJson('00-initial-processes.json', initialProcesses);
      runner.assert(initialProcesses.length === 0, 'remote harness root starts without leaked descendant processes', {
        remoteHarnessRoot: remotePaths.remoteHarnessRoot,
        initialProcesses,
      });
    });

    endpoint = await runner.step('connect_remote_endpoint', async () => {
      const endpointName = `harness-${runner.runId}`;
      observer.addEndpoint(endpointName, options.sshTarget);
      const connected = await waitForEndpointConnected(observer, endpointName, 120_000);
      runner.writeJson('endpoint.json', connected);
      return connected;
    });

    sessionId = await runner.step('create_remote_session', async () => {
      const result = await client.request('create_session', {
        cwd: remoteDirectory,
        label: `tr504-${runner.runId}`,
        agent: options.remoteAgent,
        endpoint_id: endpoint.id,
      });
      await observer.waitForSession({ id: result.sessionId, timeoutMs: 30_000 });
      await observer.waitForWorkspace(
        result.sessionId,
        (workspace) => (workspace.panes || []).length >= 1,
        `initial workspace for ${result.sessionId}`,
        30_000,
      );
      return result.sessionId;
    });

    splitPaneId = await runner.step('create_remote_split', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 45_000);
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: 'main',
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'remote cleanup split',
        30_000,
      );
      await waitForPaneVisible(client, sessionId, newPane.paneId, 20_000);
      return newPane.paneId;
    });

    await runner.step('observe_remote_processes_before_cleanup', async () => {
      activeProcessSnapshot = await waitForRemoteProcessesByHarnessRoot(
        options.sshTarget,
        remotePaths.remoteHarnessRoot,
        (processes) => (processes.length > 0 ? processes : null),
        'remote harness-root processes to appear',
        45_000,
      );
      runner.writeJson('01-active-processes.json', activeProcessSnapshot);
    });

    await runner.step('close_session_and_verify_remote_cleanup', async () => {
      await client.request('close_session', { sessionId });
      postCloseProcessSnapshot = await listRemoteProcessesByHarnessRoot(options.sshTarget, remotePaths.remoteHarnessRoot, 30_000);
      runner.writeJson('02-post-close-processes.json', postCloseProcessSnapshot);
      await observer.waitFor(
        () => !observer.getSession(sessionId) && !observer.getWorkspace(sessionId),
        `session ${sessionId} to disappear after close`,
        30_000,
      );
      cleanedProcessSnapshot = await waitForRemoteProcessesByHarnessRoot(
        options.sshTarget,
        remotePaths.remoteHarnessRoot,
        (processes) => (sessionWorkerProcesses(processes).length === 0 ? processes : null),
        'remote session worker processes to exit after close_session',
        60_000,
      );
      runner.writeJson('02-cleaned-processes.json', cleanedProcessSnapshot);
    });

    await runner.step('remove_endpoint_and_verify_harness_root_cleanup', async () => {
      observer.removeEndpoint(endpoint.id);
      await observer.waitFor(() => !observer.getEndpoint(endpoint.id), `remove endpoint ${endpoint.id}`, 20_000);
      endpointRemoved = true;
      postEndpointRemovalProcessSnapshot = await waitForRemoteProcessesByHarnessRoot(
        options.sshTarget,
        remotePaths.remoteHarnessRoot,
        (processes) => (processes.length === 0 ? processes : null),
        'remote harness-root transport processes to exit after remove_endpoint',
        30_000,
      );
      runner.writeJson('03-post-endpoint-remove-processes.json', postEndpointRemovalProcessSnapshot);
    });

    const summary = runner.finishSuccess({
      sessionId,
      endpointId: endpoint?.id || null,
      splitPaneId,
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      activeProcessCount: activeProcessSnapshot.length,
      postCloseProcessCount: postCloseProcessSnapshot.length,
      cleanedProcessCount: cleanedProcessSnapshot.length,
      postEndpointRemovalProcessCount: postEndpointRemovalProcessSnapshot.length,
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId).catch(() => {});
    }
    runner.writeJson('failure-active-processes.json', activeProcessSnapshot);
    runner.writeJson('failure-post-close-processes.json', postCloseProcessSnapshot);
    runner.writeJson('failure-cleaned-processes.json', cleanedProcessSnapshot);
    runner.writeJson('failure-post-endpoint-remove-processes.json', postEndpointRemovalProcessSnapshot);
    const summary = runner.finishFailure(error, {
      sessionId,
      endpointId: endpoint?.id || null,
      splitPaneId,
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      activeProcessCount: activeProcessSnapshot.length,
      postCloseProcessCount: postCloseProcessSnapshot.length,
      cleanedProcessCount: cleanedProcessSnapshot.length,
      postEndpointRemovalProcessCount: postEndpointRemovalProcessSnapshot.length,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    if (sessionId) {
      await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
    }
    if (endpoint?.id && !endpointRemoved) {
      try {
        observer.removeEndpoint(endpoint.id);
        await observer.waitFor(() => !observer.getEndpoint(endpoint.id), `cleanup remove endpoint ${endpoint.id}`, 20_000).catch(() => {});
      } catch {
        // Best-effort cleanup only.
      }
    }
    const finalRemoteCleanup = await cleanupRemoteHarnessProcesses(
      options.sshTarget,
      remotePaths.remoteHarnessRoot,
      30_000,
    ).catch((error) => ({
      error: error instanceof Error ? error.stack || error.message : String(error),
    }));
    runner.writeJson('99-final-remote-harness-cleanup.json', finalRemoteCleanup);
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
