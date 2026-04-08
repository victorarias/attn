#!/usr/bin/env node

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { MacOSDriver } from './macosDriver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  assertPaneCoverage,
  assertPaneNativePaintCoverage,
  assertPaneNativePaintStable,
  assertPaneVisibleContent,
  compactTerminalText,
  captureSessionArtifacts,
  shellPanes,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneText,
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
    sshTarget: process.env.ATTN_REMOTE_RELAUNCH_SPLITS_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_RELAUNCH_SPLITS_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_RELAUNCH_SPLITS_REMOTE_AGENT || 'codex',
    echoThresholdMs: Number.parseInt(process.env.ATTN_REMOTE_RELAUNCH_SPLITS_ECHO_THRESHOLD_MS || '2500', 10),
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
    else if (arg === '--echo-threshold-ms') options.echoThresholdMs = Number.parseInt(args[++index] || '2500', 10);
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  return {
    options,
    help: Boolean(options.help),
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr502-remote-relaunch-splits.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>      Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>         Agent for the remote session (default: codex)
  --echo-threshold-ms <ms>       Max acceptable shell echo latency per typed token (default: 2500)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-502',
    tier: 'tier3-remote-real-agent',
    prefix: 'scenario-tr502-remote-relaunch-splits',
    metadata: {
      sshTarget: options.sshTarget,
      agent: options.remoteAgent,
      focus: 'remote relaunch split persistence',
      note: 'visible agent-content proof is still partial in this first automation slice',
    },
  });

  const remoteHome = await getRemoteHome(options.sshTarget);
  const remoteDirectory = options.remoteDirectory || remoteHome;
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
  const driver = new MacOSDriver({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let endpoint = null;
  let sessionId = null;
  let initialShellPaneId = null;
  let postRelaunchMainSplitPaneId = null;
  let postRelaunchShellSplitPaneId = null;
  let baselineMainNativeMetrics = null;
  let baselineInitialShellNativeMetrics = null;
  const timings = {
    initialShellEchoMs: null,
    postRelaunchMainSplitEchoMs: null,
    postRelaunchShellSplitEchoMs: null,
  };
  const shellTypingReadiness = {
    initialShell: null,
    postRelaunchMainSplit: null,
    postRelaunchShellSplit: null,
  };
  const preRelaunchToken = `__TR502_PRE_${Date.now()}__`;
  const postRelaunchMainToken = `__TR502_MAIN_${Date.now()}__`;
  const postRelaunchShellToken = `__TR502_SHELL_${Date.now()}__`;

  function assertEchoLatency(label, latencyMs) {
    if (!Number.isFinite(latencyMs)) {
      throw new Error(`${label} did not produce a measurable echo latency`);
    }
    if (latencyMs > options.echoThresholdMs) {
      throw new Error(`${label} echo latency ${latencyMs}ms exceeded threshold ${options.echoThresholdMs}ms`);
    }
  }

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
      const connected = await waitForEndpointConnected(observer, endpointName, 120_000);
      runner.writeJson('endpoint.json', connected);
      return connected;
    });

    sessionId = await runner.step('create_remote_session', async () => {
      const result = await client.request('create_session', {
        cwd: remoteDirectory,
        label: `tr502-${runner.runId}`,
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

    initialShellPaneId = await runner.step('create_initial_split_before_relaunch', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      await assertPaneVisibleContent(client, sessionId, 'main', {
        minNonEmptyLines: 2,
        minDenseLines: 0,
        minCharCount: 20,
        minMaxLineLength: 12,
        timeoutMs: 30_000,
        description: 'remote main pane visible content before relaunch',
      });
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'remote main pane coverage before relaunch',
      });
      baselineMainNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-pre-relaunch-main',
        sessionId,
        'main',
        {
          target: 'paneBody',
          minBusyColumnRatio: 0.35,
          minBusyRowRatio: 0.12,
          minBBoxWidthRatio: 0.35,
          minBBoxHeightRatio: 0.12,
          description: 'remote main pane native paint coverage before relaunch',
        },
      );
      await client.request('split_pane', {
        sessionId,
        targetPaneId: 'main',
        direction: 'vertical',
      });
      const workspace = await waitForSessionWorkspace(
        client,
        sessionId,
        (entry) => shellPanes(entry).length >= 1,
        'initial remote split pane',
        30_000,
      );
      const initialPane = shellPanes(workspace)[0];
      if (!initialPane?.paneId) {
        throw new Error('Initial remote split pane missing');
      }
      return initialPane.paneId;
    });

    await runner.step('seed_initial_shell_before_relaunch', async () => {
      await client.request('focus_pane', { sessionId, paneId: initialShellPaneId });
      await waitForPaneVisible(client, sessionId, initialShellPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, initialShellPaneId, 20_000);
      const readyState = await client.request('get_pane_state', { sessionId, paneId: initialShellPaneId });
      shellTypingReadiness.initialShell = {
        terminalReadyAtTypeStart: Boolean(readyState?.renderHealth?.flags?.terminalReady),
        writeParsedCountAtTypeStart: readyState?.renderHealth?.terminal?.writeParsedCount ?? null,
      };
      runner.log('shell:type_start', {
        phase: 'initial',
        paneId: initialShellPaneId,
        ...shellTypingReadiness.initialShell,
      });
      const typeStartedAt = Date.now();
      await driver.activateApp();
      await driver.typeText(preRelaunchToken);
      await waitForPaneText(
        client,
        sessionId,
        initialShellPaneId,
        (text) => compactTerminalText(text).includes(preRelaunchToken),
        'initial remote shell token before relaunch',
        20_000,
      );
      timings.initialShellEchoMs = Date.now() - typeStartedAt;
      assertEchoLatency('initial remote shell', timings.initialShellEchoMs);
      await assertPaneVisibleContent(client, sessionId, initialShellPaneId, {
        contains: preRelaunchToken,
        allowWrappedContains: true,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: preRelaunchToken.length,
        minMaxLineLength: 8,
        timeoutMs: 20_000,
        description: 'initial remote shell content before relaunch',
      });
      baselineInitialShellNativeMetrics = await assertPaneNativePaintCoverage(
        client,
        runner.runDir,
        '01-pre-relaunch-shell',
        sessionId,
        initialShellPaneId,
        {
          target: 'paneBody',
          minBusyColumnRatio: 0.2,
          minBusyRowRatio: 0.04,
          minBBoxWidthRatio: 0.2,
          minBBoxHeightRatio: 0.08,
          description: 'initial remote shell native paint coverage before relaunch',
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '01-pre-relaunch', sessionId);
    });

    await runner.step('relaunch_and_restore_session', async () => {
      await client.quitApp();
      await client.launchApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      await waitForPaneVisible(client, sessionId, initialShellPaneId, 30_000);
      await assertPaneVisibleContent(client, sessionId, 'main', {
        minNonEmptyLines: 2,
        minDenseLines: 0,
        minCharCount: 20,
        minMaxLineLength: 12,
        timeoutMs: 30_000,
        description: 'remote main pane visible content after relaunch',
      });
      await assertPaneCoverage(client, sessionId, 'main', {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'remote main pane coverage after relaunch',
      });
      await assertPaneNativePaintCoverage(client, runner.runDir, '02-post-relaunch-main', sessionId, 'main', {
        target: 'paneBody',
        minBusyColumnRatio: 0.35,
        minBusyRowRatio: 0.12,
        minBBoxWidthRatio: 0.35,
        minBBoxHeightRatio: 0.12,
        description: 'remote main pane native paint coverage after relaunch',
      });
      if (!baselineMainNativeMetrics) {
        throw new Error('Missing baseline remote main-pane native metrics before relaunch');
      }
      await assertPaneNativePaintStable(
        client,
        runner.runDir,
        '02-post-relaunch-main-stability',
        sessionId,
        'main',
        baselineMainNativeMetrics,
        {
          target: 'paneBody',
          maxBusyColumnRatioDelta: 0.12,
          maxBusyRowRatioDelta: null,
          maxBBoxWidthRatioDelta: 0.1,
          maxBBoxHeightRatioDelta: 0.1,
          maxActivePixelRatioDelta: null,
          description: 'remote main pane native paint stability after relaunch with allowed content reflow',
        },
      );
      await assertPaneVisibleContent(client, sessionId, initialShellPaneId, {
        contains: preRelaunchToken,
        allowWrappedContains: true,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: preRelaunchToken.length,
        minMaxLineLength: 8,
        timeoutMs: 20_000,
        description: 'initial remote shell content after relaunch',
      });
      await assertPaneNativePaintCoverage(client, runner.runDir, '02-post-relaunch-shell', sessionId, initialShellPaneId, {
        target: 'paneBody',
        minBusyColumnRatio: 0.2,
        minBusyRowRatio: 0.04,
        minBBoxWidthRatio: 0.2,
        minBBoxHeightRatio: 0.08,
        description: 'initial remote shell native paint coverage after relaunch',
      });
      if (!baselineInitialShellNativeMetrics) {
        throw new Error('Missing baseline initial remote shell native metrics before relaunch');
      }
      await assertPaneNativePaintStable(
        client,
        runner.runDir,
        '02-post-relaunch-shell-stability',
        sessionId,
        initialShellPaneId,
        baselineInitialShellNativeMetrics,
        {
          target: 'paneBody',
          maxBusyColumnRatioDelta: 0.12,
          maxBusyRowRatioDelta: 0.16,
          maxBBoxWidthRatioDelta: 0.12,
          maxBBoxHeightRatioDelta: 0.12,
          maxActivePixelRatioDelta: 0.06,
          description: 'initial remote shell native paint stability after relaunch',
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '02-post-relaunch', sessionId);
    });

    postRelaunchMainSplitPaneId = await runner.step('split_from_main_after_relaunch', async () => {
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
        'new remote shell after relaunch split from main',
        30_000,
      );
      return newPane.paneId;
    });

    await runner.step('assert_main_split_after_relaunch', async () => {
      await client.request('focus_pane', { sessionId, paneId: postRelaunchMainSplitPaneId });
      await waitForPaneVisible(client, sessionId, postRelaunchMainSplitPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, postRelaunchMainSplitPaneId, 20_000);
      const readyState = await client.request('get_pane_state', { sessionId, paneId: postRelaunchMainSplitPaneId });
      shellTypingReadiness.postRelaunchMainSplit = {
        terminalReadyAtTypeStart: Boolean(readyState?.renderHealth?.flags?.terminalReady),
        writeParsedCountAtTypeStart: readyState?.renderHealth?.terminal?.writeParsedCount ?? null,
      };
      runner.log('shell:type_start', {
        phase: 'post-relaunch-main-split',
        paneId: postRelaunchMainSplitPaneId,
        ...shellTypingReadiness.postRelaunchMainSplit,
      });
      const typeStartedAt = Date.now();
      await driver.activateApp();
      await driver.typeText(postRelaunchMainToken);
      await waitForPaneText(
        client,
        sessionId,
        postRelaunchMainSplitPaneId,
        (text) => compactTerminalText(text).includes(postRelaunchMainToken),
        'remote shell token after main split',
        20_000,
      );
      timings.postRelaunchMainSplitEchoMs = Date.now() - typeStartedAt;
      assertEchoLatency('post-relaunch main split shell', timings.postRelaunchMainSplitEchoMs);
      await assertPaneVisibleContent(client, sessionId, postRelaunchMainSplitPaneId, {
        contains: postRelaunchMainToken,
        allowWrappedContains: true,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: postRelaunchMainToken.length,
        minMaxLineLength: 8,
        timeoutMs: 20_000,
        description: 'remote shell content after main split',
      });
      await assertPaneCoverage(client, sessionId, postRelaunchMainSplitPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'remote shell coverage after main split',
      });
      await captureSessionArtifacts(client, runner.runDir, '03-after-main-split', sessionId);
    });

    postRelaunchShellSplitPaneId = await runner.step('split_from_existing_shell_after_relaunch', async () => {
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialShellPaneId,
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'new remote shell after relaunch split from shell',
        30_000,
      );
      return newPane.paneId;
    });

    await runner.step('assert_shell_split_after_relaunch', async () => {
      await client.request('focus_pane', { sessionId, paneId: postRelaunchShellSplitPaneId });
      await waitForPaneVisible(client, sessionId, postRelaunchShellSplitPaneId, 20_000);
      await waitForPaneInputFocus(client, sessionId, postRelaunchShellSplitPaneId, 20_000);
      const readyState = await client.request('get_pane_state', { sessionId, paneId: postRelaunchShellSplitPaneId });
      shellTypingReadiness.postRelaunchShellSplit = {
        terminalReadyAtTypeStart: Boolean(readyState?.renderHealth?.flags?.terminalReady),
        writeParsedCountAtTypeStart: readyState?.renderHealth?.terminal?.writeParsedCount ?? null,
      };
      runner.log('shell:type_start', {
        phase: 'post-relaunch-shell-split',
        paneId: postRelaunchShellSplitPaneId,
        ...shellTypingReadiness.postRelaunchShellSplit,
      });
      const typeStartedAt = Date.now();
      await driver.activateApp();
      await driver.typeText(postRelaunchShellToken);
      await waitForPaneText(
        client,
        sessionId,
        postRelaunchShellSplitPaneId,
        (text) => compactTerminalText(text).includes(postRelaunchShellToken),
        'remote shell token after shell split',
        20_000,
      );
      timings.postRelaunchShellSplitEchoMs = Date.now() - typeStartedAt;
      assertEchoLatency('post-relaunch shell split shell', timings.postRelaunchShellSplitEchoMs);
      await assertPaneVisibleContent(client, sessionId, postRelaunchShellSplitPaneId, {
        contains: postRelaunchShellToken,
        allowWrappedContains: true,
        minNonEmptyLines: 1,
        minDenseLines: 0,
        minCharCount: postRelaunchShellToken.length,
        minMaxLineLength: 8,
        timeoutMs: 20_000,
        description: 'remote shell content after shell split',
      });
      await assertPaneCoverage(client, sessionId, postRelaunchShellSplitPaneId, {
        minWidthRatio: 0.8,
        minHeightRatio: 0.7,
        timeoutMs: 20_000,
        description: 'remote shell coverage after shell split',
      });
      await captureSessionArtifacts(client, runner.runDir, '04-after-shell-split', sessionId);
    });

    const finalWorkspace = await client.request('get_workspace', { sessionId });
    const summary = runner.finishSuccess({
      sessionId,
      endpointId: endpoint?.id || null,
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      panes: {
        initialShellPaneId,
        postRelaunchMainSplitPaneId,
        postRelaunchShellSplitPaneId,
      },
      tokens: {
        preRelaunchToken,
        postRelaunchMainToken,
        postRelaunchShellToken,
      },
      thresholds: {
        echoMs: options.echoThresholdMs,
      },
      timings,
      shellTypingReadiness,
      finalWorkspace: {
        activePaneId: finalWorkspace.activePaneId,
        paneIds: (finalWorkspace.panes || []).map((pane) => pane.paneId),
        shellPaneIds: shellPanes(finalWorkspace).map((pane) => pane.paneId),
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
      panes: {
        initialShellPaneId,
        postRelaunchMainSplitPaneId,
        postRelaunchShellSplitPaneId,
      },
      tokens: {
        preRelaunchToken,
        postRelaunchMainToken,
        postRelaunchShellToken,
      },
      thresholds: {
        echoMs: options.echoThresholdMs,
      },
      timings,
      shellTypingReadiness,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await observer.close().catch(() => {});
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
