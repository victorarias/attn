#!/usr/bin/env node

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  assertPaneCoverage,
  assertPaneNativePaintCoverage,
  assertPaneNativePaintRecovered,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  shellPanes,
  waitForNewShellPane,
  waitForPaneState,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import {
  buildRemoteHarnessPaths,
  chooseRemoteWSPort,
  getRemoteHome,
  removeStaleHarnessEndpoints,
  waitForEndpointConnected,
} from './scenarioRemote.mjs';

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
    sshTarget: process.env.ATTN_REMOTE_CLOSE_REDRAW_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_CLOSE_REDRAW_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_CLOSE_REDRAW_REMOTE_AGENT || 'codex',
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

function recoveredWidthThreshold(baselineWidth) {
  if (!Number.isFinite(baselineWidth) || baselineWidth <= 0) {
    return 0;
  }
  return Math.max(240, Math.floor(baselineWidth * 0.9));
}

function shrunkWidthThreshold(baselineWidth) {
  if (!Number.isFinite(baselineWidth) || baselineWidth <= 0) {
    return Number.POSITIVE_INFINITY;
  }
  return Math.ceil(baselineWidth * 0.75);
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr402-remote-close-redraw.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>      Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>         Agent for the remote session (default: codex)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-402',
    tier: 'tier3-remote-real-agent',
    prefix: 'scenario-tr402-remote-close-redraw',
    metadata: {
      sshTarget: options.sshTarget,
      agent: options.remoteAgent,
      focus: 'split close triggers targeted redraw and main pane recovery',
    },
  });

  const remoteHome = await getRemoteHome(options.sshTarget);
  const remoteDirectory = options.remoteDirectory || remoteHome;
  const remotePaths = buildRemoteHarnessPaths(remoteHome, runner.runId);
  const remoteHarnessWSPort = String(chooseRemoteWSPort());
  const client = new UiAutomationClient({
    appPath: options.appPath,
    launchEnv: {
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
  let baselineMainState = null;
  let baselineMainNativeMetrics = null;
  let splitMainState = null;
  let recoveredMainState = null;
  let splitOpenContentPreservation = null;

  try {
    await runner.step('launch_app_and_connect_daemon', async () => {
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      const paneDebugConfig = await client.request('set_pane_debug', { enabled: true });
      const terminalRuntimeTraceConfig = await client.request('set_terminal_runtime_trace', { enabled: true });
      runner.writeJson('ui-debug-config.json', {
        paneDebugConfig,
        terminalRuntimeTraceConfig,
      });
      await observer.connect();
      await removeStaleHarnessEndpoints(observer, 20_000);
    });

    endpoint = await runner.step('connect_remote_endpoint', async () => {
      const endpointName = `harness-${runner.runId}`;
      observer.addEndpoint(endpointName, options.sshTarget);
      const connected = await waitForEndpointConnected(observer, endpointName);
      runner.writeJson('endpoint.json', connected);
      return connected;
    });

    sessionId = await runner.step('create_remote_session', async () => {
      const result = await client.request('create_session', {
        cwd: remoteDirectory,
        label: `tr402-${runner.runId}`,
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

    await runner.step('capture_baseline_main', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 45_000);
      baselineMainState = await assertPaneVisibleContent(client, sessionId, 'main', {
        minNonEmptyLines: 2,
        minDenseLines: 0,
        minCharCount: 20,
        minMaxLineLength: 12,
        timeoutMs: 45_000,
        description: 'remote main pane visible content before split close scenario',
      });
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'remote main pane coverage before split close scenario',
      });
      baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-baseline-main',
        sessionId,
        'main',
        {
          target: 'paneBody',
          minBusyColumnRatio: 0.35,
          minBusyRowRatio: 0.12,
          minBBoxWidthRatio: 0.35,
          minBBoxHeightRatio: 0.12,
          description: 'remote main pane native paint coverage before split close scenario',
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    splitPaneId = await runner.step('split_from_main', async () => {
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
        'new utility pane after split from main for close redraw scenario',
        30_000,
      );
      if (!newPane?.paneId) {
        throw new Error('Split pane missing after split from main');
      }
      splitMainState = await waitForPaneState(
        client,
        sessionId,
        'main',
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          const baselineWidth = baselineMainState?.pane?.bounds?.width ?? 0;
          return width > 0 && width <= shrunkWidthThreshold(baselineWidth);
        },
        'main pane width to shrink after split',
        20_000,
      );
      try {
        await assertPaneVisibleContentPreserved(
          client,
          sessionId,
          'main',
          baselineMainState?.pane?.visibleContent || null,
          {
            minNonEmptyLineRatio: 0.5,
            minCharCountRatio: 0.35,
            minAnchorMatches: 2,
            timeoutMs: 20_000,
            description: 'remote main pane content preserved while split is open',
          },
        );
        splitOpenContentPreservation = {
          ok: true,
        };
      } catch (error) {
        splitOpenContentPreservation = {
          ok: false,
          error: error instanceof Error ? error.stack || error.message : String(error),
        };
        runner.log('observation:split_open_content_degraded', splitOpenContentPreservation);
      }
      await captureSessionArtifacts(client, runner.runDir, '02-after-split', sessionId);
      return newPane.paneId;
    });

    await runner.step('close_split_and_assert_main_recovers', async () => {
      await client.request('focus_pane', { sessionId, paneId: splitPaneId });
      await waitForPaneVisible(client, sessionId, splitPaneId, 20_000);
      await client.request('close_pane', { sessionId, paneId: splitPaneId });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => (workspace.panes || []).length === 1 && shellPanes(workspace).length === 0,
        'workspace to collapse back to one pane after closing split',
        20_000,
      );
      recoveredMainState = await waitForPaneState(
        client,
        sessionId,
        'main',
        (state) => {
          const width = state?.pane?.bounds?.width ?? 0;
          return width >= recoveredWidthThreshold(baselineMainState?.pane?.bounds?.width ?? 0);
        },
        'main pane width to recover after closing split',
        20_000,
      );
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        'main',
        baselineMainState?.pane?.visibleContent || null,
        {
          minNonEmptyLineRatio: 0.7,
          minCharCountRatio: 0.6,
          minAnchorMatches: 3,
          timeoutMs: 20_000,
          description: 'remote main pane content recovered after closing split',
        },
      );
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.85,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'remote main pane coverage after closing split',
      });
      await assertPaneNativePaintCoverage(client, runner.runDir, '03-after-close-main', sessionId, 'main', {
        target: 'paneBody',
        minBusyColumnRatio: 0.35,
        minBusyRowRatio: 0.12,
        minBBoxWidthRatio: 0.35,
        minBBoxHeightRatio: 0.12,
        description: 'remote main pane native paint coverage after closing split',
      });
      if (baselineMainNativeMetrics) {
        await assertPaneNativePaintRecovered(
          client,
          runner.runDir,
          '03-after-close-main-stability',
          sessionId,
          'main',
          baselineMainNativeMetrics,
          {
            target: 'paneBody',
            maxBusyColumnRatioRegression: 0.1,
            maxBusyRowRatioRegression: 0.08,
            maxBBoxWidthRatioRegression: 0.1,
            maxBBoxHeightRatioRegression: 0.08,
            maxActivePixelRatioRegression: null,
            description: 'remote main pane native paint recovery after closing split',
          },
        );
      }
      await captureSessionArtifacts(client, runner.runDir, '03-after-close', sessionId);
    });

    const summary = runner.finishSuccess({
      sessionId,
      endpointId: endpoint?.id || null,
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      splitPaneId,
      splitOpenContentPreservation,
      widths: {
        baselineMainWidth: baselineMainState?.pane?.bounds?.width ?? null,
        splitMainWidth: splitMainState?.pane?.bounds?.width ?? null,
        recoveredMainWidth: recoveredMainState?.pane?.bounds?.width ?? null,
      },
      artifacts: {
        runDir: runner.runDir,
        trace: runner.tracePath,
      },
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    if (sessionId) {
      await captureSessionArtifacts(client, runner.runDir, 'failure', sessionId);
    }
    const summary = runner.finishFailure(error, {
      sessionId,
      endpointId: endpoint?.id || null,
      splitPaneId,
      splitOpenContentPreservation,
      widths: {
        baselineMainWidth: baselineMainState?.pane?.bounds?.width ?? null,
        splitMainWidth: splitMainState?.pane?.bounds?.width ?? null,
        recoveredMainWidth: recoveredMainState?.pane?.bounds?.width ?? null,
      },
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : String(error));
  process.exit(1);
});
