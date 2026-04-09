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
  scrollPaneToTop,
  shellPanes,
  waitForPaneText,
  waitForNewShellPane,
  waitForPaneState,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import {
  ensureClaudeMainPromptReady,
  ensureCodexMainPromptReady,
  promptClaudeForStructuredBlock,
} from './scenarioAgents.mjs';
import {
  buildRemoteHarnessPaths,
  chooseRemoteWSPort,
  getRemoteHome,
  removeStaleHarnessEndpoints,
  removeStaleHarnessScenarioSessions,
  waitForEndpointConnected,
} from './scenarioRemote.mjs';

function isNativeCaptureUnavailable(error) {
  const message = error instanceof Error ? error.message : String(error || '');
  return (
    message.includes('screencapture exited with status') ||
    message.includes('No windows found for') ||
    message.includes('capture_window_screenshot')
  );
}

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
    sshTarget: process.env.ATTN_REMOTE_RELAUNCH_CLOSE_REDRAW_SSH_TARGET || 'ai-sandbox',
    remoteDirectory: process.env.ATTN_REMOTE_RELAUNCH_CLOSE_REDRAW_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_RELAUNCH_CLOSE_REDRAW_REMOTE_AGENT || 'codex',
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

function minRecoveredWidth(previousWidth) {
  if (!Number.isFinite(previousWidth) || previousWidth <= 0) {
    return 0;
  }
  return Math.max(240, Math.floor(previousWidth * 1.18));
}

function buildTranscriptAnchorPrompt(token, lineCount = 4) {
  return Array.from({ length: lineCount }, (_, index) =>
    `${token} line ${index + 1} width repaint recovery anchor payload ${index + 1} for relaunch close redraw verification`
  ).join('\n');
}

async function seedTranscriptAnchor(client, sessionId, anchorPrompt, anchorToken) {
  await client.request('select_session', { sessionId });
  await client.request('write_pane', { sessionId, paneId: 'main', text: anchorPrompt });
  await waitForPaneText(
    client,
    sessionId,
    'main',
    (text) => text.includes(anchorToken),
    `main transcript anchor ${anchorToken}`,
    30_000,
  );
}

async function prepareRemoteAgentBaseline(client, runner, sessionId, remoteAgent, transcriptAnchorToken, transcriptAnchorPrompt) {
  const agent = String(remoteAgent || 'codex').toLowerCase();
  if (agent === 'claude') {
    await ensureClaudeMainPromptReady(client, sessionId, 45_000);
    const fixture = await promptClaudeForStructuredBlock(client, sessionId, transcriptAnchorToken, 4);
    runner.writeJson('agent-fixture.json', fixture);
    return transcriptAnchorToken;
  }

  await ensureCodexMainPromptReady(client, sessionId, 45_000);
  await seedTranscriptAnchor(client, sessionId, transcriptAnchorPrompt, transcriptAnchorToken);
  return transcriptAnchorToken;
}

async function captureMainHealthyState(client, runner, sessionId, prefix, descriptionBase, requiredVisibleText = null) {
  await waitForPaneVisible(client, sessionId, 'main', 30_000);
  await scrollPaneToTop(client, sessionId, 'main');
  const state = await assertPaneVisibleContent(client, sessionId, 'main', {
    contains: requiredVisibleText,
    allowWrappedContains: Boolean(requiredVisibleText),
    minNonEmptyLines: 2,
    minDenseLines: 0,
    minCharCount: 20,
    minMaxLineLength: 12,
    timeoutMs: 30_000,
    description: `${descriptionBase} visible content`,
  });
  await assertPaneCoverage(client, sessionId, 'main', {
    minWidthRatio: 0.8,
    minHeightRatio: 0.7,
    timeoutMs: 20_000,
    description: `${descriptionBase} coverage`,
  });
  let nativeMetrics = null;
  try {
    nativeMetrics = await assertPaneNativePaintCoverage(
      client,
      runner.runDir,
      prefix,
      sessionId,
      'main',
      {
        target: 'paneBody',
        minBusyColumnRatio: 0.35,
        minBusyRowRatio: 0.1,
        minBBoxWidthRatio: 0.35,
        minBBoxHeightRatio: 0.12,
        description: `${descriptionBase} native paint coverage`,
      },
    );
  } catch (error) {
    if (!isNativeCaptureUnavailable(error)) {
      throw error;
    }
    runner.writeText(`${prefix}-native-unavailable.txt`, error instanceof Error ? error.stack || error.message : String(error));
  }
  return {
    state,
    nativeMetrics,
  };
}

async function closePaneAndAssertRecovery({
  client,
  runner,
  sessionId,
  paneId,
  baselineVisibleContent,
  baselineNativeMetrics,
  previousMainWidth,
  minPaneCountAfterClose,
  label,
  minNonEmptyLineRatio = 0.7,
  minCharCountRatio = 0.55,
  minAnchorMatches = 2,
  enforceNativeStability = true,
  requiredVisibleText = null,
}) {
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, 'main', 20_000);
  await client.request('focus_pane', { sessionId, paneId });
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await client.request('close_pane', { sessionId, paneId });
  await waitForSessionWorkspace(
    client,
    sessionId,
    (workspace) => (workspace.panes || []).length === minPaneCountAfterClose,
    `${label} workspace collapse`,
    20_000,
  );
  const recoveredMainState = await waitForPaneState(
    client,
    sessionId,
    'main',
    (state) => (state?.pane?.bounds?.width ?? 0) >= minRecoveredWidth(previousMainWidth),
    `${label} main width recovery`,
    20_000,
  );
  await scrollPaneToTop(client, sessionId, 'main');
  await assertPaneVisibleContentPreserved(
    client,
    sessionId,
    'main',
    baselineVisibleContent,
    {
      minNonEmptyLineRatio,
      minCharCountRatio,
      minAnchorMatches,
      timeoutMs: 20_000,
      description: `${label} main content recovery`,
    },
  );
  if (requiredVisibleText) {
    await assertPaneVisibleContent(client, sessionId, 'main', {
      contains: requiredVisibleText,
      allowWrappedContains: true,
      minNonEmptyLines: 2,
      minDenseLines: 0,
      minCharCount: 20,
      minMaxLineLength: 12,
      timeoutMs: 20_000,
      description: `${label} main anchor visibility`,
    });
  }
  await assertPaneCoverage(client, sessionId, 'main', {
    minWidthRatio: 0.85,
    minHeightRatio: 0.7,
    timeoutMs: 20_000,
    description: `${label} main coverage`,
  });
  let candidateNativeMetrics = null;
  try {
    candidateNativeMetrics = await assertPaneNativePaintCoverage(client, runner.runDir, `${label}-main`, sessionId, 'main', {
      target: 'paneBody',
      minBusyColumnRatio: 0.35,
      minBusyRowRatio: 0.1,
      minBBoxWidthRatio: 0.35,
      minBBoxHeightRatio: 0.12,
      description: `${label} main native paint coverage`,
    });
  } catch (error) {
    if (!isNativeCaptureUnavailable(error)) {
      throw error;
    }
    runner.writeText(`${label}-native-unavailable.txt`, error instanceof Error ? error.stack || error.message : String(error));
  }
  const finalMainState = await client.request('get_pane_state', { sessionId, paneId: 'main' });
  if (enforceNativeStability && baselineNativeMetrics && candidateNativeMetrics) {
    const widenedPastPreviousWidth = (finalMainState?.pane?.bounds?.width ?? 0) > previousMainWidth + 1;
    await assertPaneNativePaintRecovered(
      client,
      runner.runDir,
      `${label}-main-stability`,
      sessionId,
      'main',
      baselineNativeMetrics,
      {
        target: 'paneBody',
        maxBusyColumnRatioRegression: widenedPastPreviousWidth ? null : 0.12,
        maxBusyRowRatioRegression: widenedPastPreviousWidth ? null : 0.1,
        maxBBoxWidthRatioRegression: widenedPastPreviousWidth ? null : 0.12,
        maxBBoxHeightRatioRegression: widenedPastPreviousWidth ? null : 0.1,
        maxActivePixelRatioRegression: null,
        description: `${label} main native paint recovery`,
      },
    );
  }
  return {
    state: finalMainState,
    nativeMetrics: candidateNativeMetrics,
    widthState: recoveredMainState,
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr205-remote-relaunch-close-redraw.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: ai-sandbox)
  --remote-directory <path>      Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>         Agent for the remote session (default: codex)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-205',
    tier: 'tier3-remote-real-agent',
    prefix: 'scenario-tr205-remote-relaunch-close-redraw',
    metadata: {
      sshTarget: options.sshTarget,
      agent: options.remoteAgent,
      focus: 'relaunch split close recovery',
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
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  let endpoint = null;
  let sessionId = null;
  let initialShellPaneId = null;
  let postRelaunchMainSplitPaneId = null;
  let postRelaunchShellSplitPaneId = null;
  let baselineMainState = null;
  let initialSplitMainState = null;
  let restoredMainState = null;
  let finalMainState = null;
  const transcriptAnchorToken = `TR205ANCHOR${Date.now()}`;
  const transcriptAnchorPrompt = buildTranscriptAnchorPrompt(transcriptAnchorToken, 4);

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
      const cleanupResult = await removeStaleHarnessScenarioSessions(observer, 60_000);
      if (cleanupResult.sessions.length > 0 || cleanupResult.lingeringWorkspaceSessionIds.length > 0) {
        runner.writeJson('stale-harness-sessions-cleaned.json', cleanupResult);
      }
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
        label: `tr205-${runner.runId}`,
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
      const requiredVisibleText = await prepareRemoteAgentBaseline(
        client,
        runner,
        sessionId,
        options.remoteAgent,
        transcriptAnchorToken,
        transcriptAnchorPrompt,
      );
      baselineMainState = await captureMainHealthyState(
        client,
        runner,
        sessionId,
        '01-baseline-main',
        'baseline main before relaunch-close scenario',
        requiredVisibleText,
      );
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    initialShellPaneId = await runner.step('create_initial_split_before_relaunch', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
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
        'initial split before relaunch',
        30_000,
      );
      await assertPaneVisibleContent(client, sessionId, 'main', {
        contains: transcriptAnchorToken,
        allowWrappedContains: true,
        minNonEmptyLines: 8,
        minDenseLines: 3,
        minCharCount: 200,
        minMaxLineLength: 30,
        timeoutMs: 20_000,
        description: 'main transcript anchor preserved after initial split before relaunch',
      });
      initialSplitMainState = await captureMainHealthyState(
        client,
        runner,
        sessionId,
        '02-after-initial-split-main',
        'main after initial split before relaunch',
        transcriptAnchorToken,
      );
      await captureSessionArtifacts(client, runner.runDir, '02-after-initial-split', sessionId);
      return newPane?.paneId || null;
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
      restoredMainState = await captureMainHealthyState(
        client,
        runner,
        sessionId,
        '03-post-relaunch-main',
        'restored main after relaunch',
        transcriptAnchorToken,
      );
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        'main',
        initialSplitMainState?.state?.pane?.visibleContent || null,
        {
          minNonEmptyLineRatio: 0.7,
          minCharCountRatio: 0.55,
          minAnchorMatches: 2,
          timeoutMs: 20_000,
          description: 'restored main content matches pre-relaunch split state',
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '03-post-relaunch', sessionId);
    });

    postRelaunchMainSplitPaneId = await runner.step('split_from_main_after_relaunch', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
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
        'new shell after relaunch split from main',
        30_000,
      );
      await assertPaneVisibleContent(client, sessionId, 'main', {
        contains: transcriptAnchorToken,
        allowWrappedContains: true,
        minNonEmptyLines: 8,
        minDenseLines: 3,
        minCharCount: 200,
        minMaxLineLength: 30,
        timeoutMs: 20_000,
        description: 'main transcript anchor preserved after relaunch split from main',
      });
      await captureSessionArtifacts(client, runner.runDir, '04-after-main-split', sessionId);
      return newPane?.paneId || null;
    });

    postRelaunchShellSplitPaneId = await runner.step('split_from_existing_shell_after_relaunch', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, initialShellPaneId, 20_000);
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
        'new shell after relaunch split from existing shell',
        30_000,
      );
      await captureSessionArtifacts(client, runner.runDir, '05-after-shell-split', sessionId);
      return newPane?.paneId || null;
    });

    await runner.step('close_relaunched_splits_and_assert_recovery', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 20_000);
      let previousMainWidth = (await client.request('get_pane_state', { sessionId, paneId: 'main' }))?.pane?.bounds?.width ?? 0;
      const firstRecovered = await closePaneAndAssertRecovery({
        client,
        runner,
        sessionId,
        paneId: postRelaunchShellSplitPaneId,
        baselineVisibleContent: restoredMainState?.state?.pane?.visibleContent || null,
        baselineNativeMetrics: null,
        previousMainWidth,
        minPaneCountAfterClose: 3,
        label: '06-after-closing-shell-split',
        minNonEmptyLineRatio: 0.45,
        minCharCountRatio: 0.35,
        minAnchorMatches: 1,
        enforceNativeStability: false,
        requiredVisibleText: transcriptAnchorToken,
      });
      previousMainWidth = firstRecovered?.state?.pane?.bounds?.width ?? firstRecovered?.widthState?.pane?.bounds?.width ?? previousMainWidth;
      await captureSessionArtifacts(client, runner.runDir, '06-after-closing-shell-split', sessionId);

      const secondRecovered = await closePaneAndAssertRecovery({
        client,
        runner,
        sessionId,
        paneId: postRelaunchMainSplitPaneId,
        baselineVisibleContent: firstRecovered?.state?.pane?.visibleContent || restoredMainState?.state?.pane?.visibleContent || null,
        baselineNativeMetrics: firstRecovered?.nativeMetrics || restoredMainState?.nativeMetrics || null,
        previousMainWidth,
        minPaneCountAfterClose: 2,
        label: '07-after-closing-main-split',
        minNonEmptyLineRatio: 0.7,
        minCharCountRatio: 0.55,
        minAnchorMatches: 2,
        enforceNativeStability: true,
        requiredVisibleText: transcriptAnchorToken,
      });
      previousMainWidth = secondRecovered?.state?.pane?.bounds?.width ?? secondRecovered?.widthState?.pane?.bounds?.width ?? previousMainWidth;
      await captureSessionArtifacts(client, runner.runDir, '07-after-closing-main-split', sessionId);

      finalMainState = await closePaneAndAssertRecovery({
        client,
        runner,
        sessionId,
        paneId: initialShellPaneId,
        baselineVisibleContent:
          options.remoteAgent === 'claude'
            ? (baselineMainState?.state?.pane?.visibleContent || null)
            : (secondRecovered?.state?.pane?.visibleContent || baselineMainState?.state?.pane?.visibleContent || null),
        baselineNativeMetrics:
          options.remoteAgent === 'claude'
            ? (baselineMainState?.nativeMetrics || null)
            : (secondRecovered?.nativeMetrics || baselineMainState?.nativeMetrics || null),
        previousMainWidth,
        minPaneCountAfterClose: 1,
        label: '08-after-closing-initial-split',
        minNonEmptyLineRatio: 0.75,
        minCharCountRatio: 0.6,
        minAnchorMatches: 3,
        enforceNativeStability: true,
        requiredVisibleText: transcriptAnchorToken,
      });
      await captureSessionArtifacts(client, runner.runDir, '08-after-closing-initial-split', sessionId);
    });

    const finalWorkspace = await client.request('get_workspace', { sessionId });
    const summary = runner.finishSuccess({
      sessionId,
      endpointId: endpoint?.id || null,
      sshTarget: options.sshTarget,
      remoteDirectory,
      remoteAgent: options.remoteAgent,
      transcriptAnchorToken,
      panes: {
        initialShellPaneId,
        postRelaunchMainSplitPaneId,
        postRelaunchShellSplitPaneId,
      },
      widths: {
        baselineMainWidth: baselineMainState?.state?.pane?.bounds?.width ?? null,
        restoredMainWidth: restoredMainState?.state?.pane?.bounds?.width ?? null,
        finalMainWidth: finalMainState?.state?.pane?.bounds?.width ?? finalMainState?.widthState?.pane?.bounds?.width ?? null,
      },
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
      transcriptAnchorToken,
      widths: {
        baselineMainWidth: baselineMainState?.state?.pane?.bounds?.width ?? null,
        restoredMainWidth: restoredMainState?.state?.pane?.bounds?.width ?? null,
        finalMainWidth: finalMainState?.pane?.bounds?.width ?? null,
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
