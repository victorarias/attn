#!/usr/bin/env node

import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  createSessionAndWaitForInitialPane,
  assertCommonTargetAllowed,
  DEFAULT_REMOTE_SSH_TARGET,
  launchFreshAppAndConnect,
  parseCommonArgs,
  printCommonHelp,
  relaunchAppAndConnect,
} from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { DaemonObserver } from './daemonObserver.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import { cleanupSessionViaAppClose } from './scenarioCleanup.mjs';
import {
  assertPaneCoverage,
  assertPaneNativePaintCoverage,
  assertPaneNativePaintRecovered,
  assertPaneVisibleContent,
  assertPaneVisibleContentPreserved,
  captureSessionArtifacts,
  sleep,
  waitForFirstWorkspacePane,
  waitForPaneShellReady,
  waitForPaneText,
  waitForNewShellPane,
  waitForPaneState,
  waitForPaneVisible,
  waitForSessionWorkspace,
  tokenAnchorIgnorePatterns,
} from './scenarioAssertions.mjs';
import {
  buildRemoteHarnessPaths,
  cleanupRemoteHarnessProcesses,
  chooseRemoteWSPort,
  getRemoteHome,
  removeStaleHarnessEndpoints,
  removeStaleHarnessScenarioSessions,
  waitForEndpointConnected,
} from './scenarioRemote.mjs';
import { currentHarnessProfile } from './harnessProfile.mjs';

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
    sshTarget: process.env.ATTN_REMOTE_RELAUNCH_CLOSE_REDRAW_SSH_TARGET || DEFAULT_REMOTE_SSH_TARGET,
    remoteDirectory: process.env.ATTN_REMOTE_RELAUNCH_CLOSE_REDRAW_REMOTE_DIRECTORY || '',
    remoteAgent: process.env.ATTN_REMOTE_RELAUNCH_CLOSE_REDRAW_REMOTE_AGENT || 'probe:codex',
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
    else if (arg === '--run-against-prod') options.runAgainstProd = true;
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  if (!options.help) assertCommonTargetAllowed(options, args);
  return {
    options,
    help: Boolean(options.help),
  };
}

function minRecoveredWidth(previousWidth) {
  if (!Number.isFinite(previousWidth) || previousWidth <= 0) {
    return 0;
  }
  // No absolute px floor: unreachable when 3 panes share the ~560px harness main area;
  // growth over the pre-close width is the recovery signal.
  return Math.floor(previousWidth * 1.18);
}

const PROBE_AGENT_PREFIX = 'probe:';

// `--remote-agent probe:codex` / `probe:claude` select the deterministic
// `attn _probe-tui` fixture — the only agents this scenario drives. The
// login-dependent live-agent legs were removed (they needed credentials
// seeded in the VM and could never run unattended); anything else is an error.
function parseProbeStyle(remoteAgent) {
  const raw = String(remoteAgent || '');
  if (!raw.startsWith(PROBE_AGENT_PREFIX)) {
    throw new Error(`--remote-agent must be probe:codex or probe:claude (got '${remoteAgent}')`);
  }
  const style = raw.slice(PROBE_AGENT_PREFIX.length).trim().toLowerCase();
  if (style !== 'codex' && style !== 'claude') {
    throw new Error(`Unsupported probe style in --remote-agent '${remoteAgent}' (expected probe:codex or probe:claude)`);
  }
  return style;
}

// Mirrors remoteBinaryName in internal/hub/ssh.go:29 — default profile
// installs as "attn", named profiles install as "attn-<profile>".
export function remoteProbeBinaryName(profile) {
  const trimmed = String(profile || '').trim();
  return trimmed === '' ? 'attn' : `attn-${trimmed}`;
}

// Mirrors the remote binary resolution in internal/hub/ssh.go:69 (used by
// remoteAttnCommand) so the probe launches from whichever location the hub
// actually installed to. This has to be resolved IN THE REMOTE SHELL, not
// precomputed in JS: the launchEnv ATTN_REMOTE_ATTN_BIN this scenario passes
// to the packaged app only reaches a daemon process it spawns fresh — an
// already-running daemon (the common case once the harness reuses a live
// app) never sees it, so the bootstrapper installs to the default
// $HOME/.local/bin/<binaryName> path instead. Precomputing the path in JS
// guesses which world the daemon is in and guesses wrong whenever a daemon
// was already running; asking the shell to fall back the same way
// remoteAttnCommand does is the only way to match the actual install
// location in both worlds.
export function buildProbeLaunchCommand(binaryName, style) {
  return `ATTN_BIN="\${ATTN_REMOTE_ATTN_BIN:-$HOME/.local/bin/${binaryName}}"; if [ ! -x "$ATTN_BIN" ] && [ -z "\${ATTN_REMOTE_ATTN_BIN:-}" ]; then ATTN_BIN="$(command -v ${binaryName} 2>/dev/null || true)"; fi; exec "$ATTN_BIN" _probe-tui --style ${style}`;
}

function escapeRegExp(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// The probe's style row, independent of geometry/seq — mirrors bannerStyleRow
// in internal/probetui/probetui.go ("style=<style> seq=<seq> READY").
function probeStyleRowRegex(style) {
  return new RegExp(`style=${escapeRegExp(style)} seq=\\d+ READY`);
}

// Truncation-tolerant style identity: just `style=<style>`, 11-12 chars, so it
// survives right-truncation in panes as narrow as ~20 cols where the full
// "style=<style> seq=<n> READY" row (24 chars) cannot fit. Used by the at-grid
// wait, where the geometry row already proves a fresh repaint; full-row
// readiness matching stays width-safe because it only runs pre-split at full
// pane width (prepareRemoteProbeBaseline).
export function probeStyleIdentityRegex(style) {
  return new RegExp(`style=${escapeRegExp(style)}(?=\\s|$)`);
}

// Geometry and style are painted as two SEPARATE rows (bannerGeometryRow /
// bannerStyleRow in internal/probetui/probetui.go) — split because a single
// combined banner line truncated past recognition in narrow (~20-31 col)
// panes. Both matchers must pass independently against the pane's joined
// visible text; neither row alone proves the fixture is both alive and
// painting the requested style.
export function probeBannerReadyMatchers(style) {
  return [/ATTN-PROBE \d+x\d+/, probeStyleRowRegex(style)];
}

// The probe's geometry row pinned to an exact grid — used to confirm the
// fixture has repainted at the pane's *current* geometry after a
// resize/relaunch, not a stale pre-resize frame. `(?!\d)` guards against a
// grid like 31x2 matching as a prefix of 31x25.
export function buildProbeBannerAtGridRegex(cols, rows) {
  return new RegExp(`ATTN-PROBE ${cols}x${rows}(?!\\d)`);
}

// Additional probe-mode-only geometry assertion: waits for the pane's visible
// content to contain the geometry row pinned to the pane's *current* grid
// (cols x rows) AND the style identity for the expected style, so a redraw
// claim is verified against ground truth instead of density heuristics alone.
// The style check uses the truncation-tolerant identity regex, not the full
// READY row, because narrow post-split panes truncate the style row.
async function waitForProbeBannerAtGrid(client, sessionId, paneId, style, timeoutMs = 20_000) {
  const styleRegex = probeStyleIdentityRegex(style);
  return waitForPaneState(
    client,
    sessionId,
    paneId,
    (state) => {
      const visibleContent = state?.pane?.visibleContent || null;
      const cols = visibleContent?.cols;
      const rows = Array.isArray(visibleContent?.lines) ? visibleContent.lines.length : null;
      if (typeof cols !== 'number' || typeof rows !== 'number') {
        return false;
      }
      const joined = (visibleContent.lines || []).join('\n');
      return buildProbeBannerAtGridRegex(cols, rows).test(joined) && styleRegex.test(joined);
    },
    `pane ${paneId} probe banner at current grid (style=${style})`,
    timeoutMs,
  );
}

// The session is spawned with agent 'shell' (not the probe style), so the
// initial pane is a plain shell. Launch the probe TUI in it and wait for its
// readiness banner — the probe reads no input, so there is no transcript-anchor
// prompt to seed; the banner itself is the anchor for the rest of the scenario.
async function prepareRemoteProbeBaseline(client, sessionId, style) {
  await client.request('select_session', { sessionId });
  const initialPane = await waitForFirstWorkspacePane(client, sessionId, `initial pane for probe session ${sessionId}`, 30_000);
  const paneId = initialPane.paneId;
  await waitForPaneVisible(client, sessionId, paneId, 20_000);
  await waitForPaneShellReady(client, sessionId, paneId, {
    timeoutMs: 30_000,
    description: `probe pane ${paneId} shell ready before launching probe TUI`,
  });
  // Text and the doorbell CR are sent as SEPARATE write_pane calls — a fast
  // burst ending in CR is treated as a bracketed paste and never submits.
  const probeCommand = buildProbeLaunchCommand(remoteProbeBinaryName(currentHarnessProfile()), style);
  await client.request('write_pane', { sessionId, paneId, text: probeCommand, submit: false });
  await sleep(300);
  await client.request('write_pane', { sessionId, paneId, text: '\r', submit: false });
  const readyMatchers = probeBannerReadyMatchers(style);
  await waitForPaneText(
    client,
    sessionId,
    paneId,
    (text) => readyMatchers.every((matcher) => matcher.test(text)),
    `probe TUI ready banner (style=${style}) in pane ${paneId}`,
    45_000,
  );
  // Stable across seq and relaunch — this text never scrolls out or gets
  // collapsed by redraw behavior. Just the geometry row's fixed prefix: the
  // style row can itself truncate in very narrow panes, so it must not be the
  // anchor other assertions key off of (see probeBannerReadyMatchers /
  // bannerStyleRow).
  return { paneId, requiredVisibleText: 'ATTN-PROBE' };
}

// Best-effort settle: waits until two consecutive pane snapshots render the same
// visible lines, so baselines aren't captured mid-stream while the agent TUI is
// still painting. Bounded — an animating TUI (spinner) proceeds after maxAttempts
// rather than failing, since the caller's own assertions are the real check.
async function waitForPaneContentStable(client, sessionId, paneId, { intervalMs = 1_500, maxAttempts = 8 } = {}) {
  let previous = null;
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    const state = await client.request('get_pane_state', { sessionId, paneId });
    const current = (state?.pane?.visibleContent?.lines || []).join('\n');
    if (previous !== null && current === previous && current.trim().length > 0) {
      return;
    }
    previous = current;
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
}

async function captureInitialPaneHealthyState(client, runner, sessionId, paneId, prefix, descriptionBase, requiredVisibleText = null, probeStyle = null) {
  await waitForPaneVisible(client, sessionId, paneId, 30_000);
  // No scroll: captures assert on the live bottom viewport — agent startup output has
  // outgrown one screen, so top-of-scrollback holds only banners, and the bottom is
  // what a post-redraw screen must reproduce.
  const state = await assertPaneVisibleContent(client, sessionId, paneId, {
    contains: requiredVisibleText,
    allowWrappedContains: Boolean(requiredVisibleText),
    minNonEmptyLines: 2,
    minDenseLines: 0,
    minCharCount: 20,
    minMaxLineLength: 12,
    timeoutMs: 30_000,
    description: `${descriptionBase} visible content`,
  });
  if (probeStyle) {
    // Verify the banner has repainted at the pane's current grid, not just that
    // dense content is present (ground truth on top of the density heuristic
    // above).
    await waitForProbeBannerAtGrid(client, sessionId, paneId, probeStyle, 30_000);
  }
  await assertPaneCoverage(client, sessionId, paneId, {
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
      paneId,
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
  initialPaneId,
  paneId,
  baselineNativeMetrics,
  previousInitialPaneWidth,
  minPaneCountAfterClose,
  label,
  enforceNativeStability = true,
  requiredVisibleText = null,
  probeStyle = null,
}) {
  await client.request('select_session', { sessionId });
  await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);
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
  const recoveredInitialPaneState = await waitForPaneState(
    client,
    sessionId,
    initialPaneId,
    (state) => (state?.pane?.bounds?.width ?? 0) >= minRecoveredWidth(previousInitialPaneWidth),
    `${label} initial pane width recovery`,
    20_000,
  );
  // Line-anchor preservation is skipped here: every probe row encodes the
  // current geometry/frame seq (by design), so baseline lines from a different
  // grid can never match after a pane close widens the initial pane. The
  // waitForProbeBannerAtGrid call below is the replacement — it proves a fresh
  // repaint at the recovered grid, which is strictly stronger evidence of
  // redraw recovery than stale-line matching.
  const anchorState = requiredVisibleText
    ? await assertPaneVisibleContent(client, sessionId, initialPaneId, {
        contains: requiredVisibleText,
        allowWrappedContains: true,
        minNonEmptyLines: 2,
        minDenseLines: 0,
        minCharCount: 20,
        minMaxLineLength: 12,
        timeoutMs: 20_000,
        description: `${label} initial pane anchor visibility`,
      })
    : null;
  if (probeStyle) {
    // Same ground-truth grid check as captureInitialPaneHealthyState, applied
    // at this close/recovery point.
    await waitForProbeBannerAtGrid(client, sessionId, initialPaneId, probeStyle, 20_000);
  }
  await assertPaneCoverage(client, sessionId, initialPaneId, {
    minWidthRatio: 0.85,
    minHeightRatio: 0.7,
    timeoutMs: 20_000,
    description: `${label} initial pane coverage`,
  });
  let candidateNativeMetrics = null;
  try {
    candidateNativeMetrics = await assertPaneNativePaintCoverage(client, runner.runDir, `${label}-initial-pane`, sessionId, initialPaneId, {
      target: 'paneBody',
      minBusyColumnRatio: 0.35,
      minBusyRowRatio: 0.1,
      minBBoxWidthRatio: 0.35,
      minBBoxHeightRatio: 0.12,
      description: `${label} initial pane native paint coverage`,
    });
  } catch (error) {
    if (!isNativeCaptureUnavailable(error)) {
      throw error;
    }
    runner.writeText(`${label}-native-unavailable.txt`, error instanceof Error ? error.stack || error.message : String(error));
  }
  const finalMainState = await client.request('get_pane_state', { sessionId, paneId: initialPaneId });
  if (enforceNativeStability && baselineNativeMetrics && candidateNativeMetrics) {
    const widenedPastPreviousWidth = (finalMainState?.pane?.bounds?.width ?? 0) > previousInitialPaneWidth + 1;
    await assertPaneNativePaintRecovered(
      client,
      runner.runDir,
      `${label}-initial-pane-stability`,
      sessionId,
      initialPaneId,
      baselineNativeMetrics,
      {
        target: 'paneBody',
        maxBusyColumnRatioRegression: widenedPastPreviousWidth ? null : 0.12,
        maxBusyRowRatioRegression: widenedPastPreviousWidth ? null : 0.1,
        maxBBoxWidthRatioRegression: widenedPastPreviousWidth ? null : 0.12,
        maxBBoxHeightRatioRegression: widenedPastPreviousWidth ? null : 0.1,
        maxActivePixelRatioRegression: null,
        description: `${label} initial pane native paint recovery`,
      },
    );
  }
  return {
    // The token-guaranteed snapshot from the requiredVisibleText assert above, not the
    // separate get_pane_state a few checks later — codex can repaint to a banner-top
    // frame in between, which would strip token lines from a chained baseline.
    state: anchorState || finalMainState,
    nativeMetrics: candidateNativeMetrics,
    widthState: recoveredInitialPaneState,
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr205-remote-relaunch-close-redraw.mjs');
    console.log(`Additional options:
  --ssh-target <target>          SSH target for the remote endpoint (default: attn-remote@orb — the provisioned OrbStack VM)
  --remote-directory <path>      Remote cwd for the spawned session (default: remote $HOME)
  --remote-agent <agent>         Probe style for the remote session: probe:codex (default)
                                  or probe:claude. Runs the deterministic
                                  'attn _probe-tui' fixture on the remote.
`);
    return;
  }

  const probeStyle = parseProbeStyle(options.remoteAgent);
  // The scenario spawns a plain shell and types the probe TUI command into it
  // itself, rather than asking the daemon to launch an agent.
  const spawnAgent = 'shell';

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-205',
    tier: 'tier3-remote-probe',
    prefix: 'scenario-tr205-remote-relaunch-close-redraw',
    metadata: {
      sshTarget: options.sshTarget,
      agent: options.remoteAgent,
      focus: 'relaunch split close recovery',
    },
  });

  const remoteHome = await getRemoteHome(options.sshTarget);
  const remoteHarnessBase = `${remoteHome}/.attn/harness`;
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
  let initialPaneId = null;
  let initialShellPaneId = null;
  let postRelaunchMainSplitPaneId = null;
  let postRelaunchShellSplitPaneId = null;
  let baselineMainState = null;
  let initialSplitMainState = null;
  let restoredMainState = null;
  let finalMainState = null;
  // The stable anchor text used for redraw/recovery assertions throughout the
  // rest of the run: the probe's ready banner prefix, set once
  // prepareRemoteProbeBaseline returns.
  let anchorText = null;
  let cleanupStarted = false;

  const runFinalCleanup = async () => {
    if (cleanupStarted) {
      return;
    }
    cleanupStarted = true;
    if (sessionId) {
      await cleanupSessionViaAppClose(client, observer, sessionId).catch(() => {});
    }
    if (endpoint?.id) {
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
    await client.quitApp().catch(() => {});
    await observer.close().catch(() => {});
  };
  runner.registerCleanup('remote relaunch close redraw teardown', runFinalCleanup);

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
      await launchFreshAppAndConnect(client, observer);
      await removeStaleHarnessEndpoints(observer, 20_000);
      const cleanupResult = await removeStaleHarnessScenarioSessions(observer, 60_000);
      if (cleanupResult.sessions.length > 0 || cleanupResult.lingeringWorkspaceSessionIds.length > 0) {
        runner.writeJson('stale-harness-sessions-cleaned.json', cleanupResult);
      }
    });

    endpoint = await runner.step('connect_remote_endpoint', async () => {
      const endpointName = `harness-${runner.runId}`;
      observer.addEndpoint(endpointName, options.sshTarget);
      const connected = await waitForEndpointConnected(observer, endpointName);
      runner.writeJson('endpoint.json', connected);
      return connected;
    });

    sessionId = await runner.step('create_remote_session', async () => {
      const resultSessionId = await createSessionAndWaitForInitialPane({
        client,
        observer,
        cwd: remoteDirectory,
        label: `tr205-${runner.runId}`,
        agent: spawnAgent,
        endpointId: endpoint.id,
        waitForInitialPaneVisible: false,
      });
      await observer.waitForWorkspace(
        resultSessionId,
        (workspace) => (workspace.panes || []).length >= 1,
        `initial workspace for ${resultSessionId}`,
        30_000,
      );
      return resultSessionId;
    });

    await runner.step('capture_baseline_initial_pane', async () => {
      await client.request('select_session', { sessionId });
      const baseline = await prepareRemoteProbeBaseline(client, sessionId, probeStyle);
      initialPaneId = baseline.paneId;
      anchorText = baseline.requiredVisibleText;
      baselineMainState = await captureInitialPaneHealthyState(
        client,
        runner,
        sessionId,
        initialPaneId,
        '01-baseline-initial-pane',
        'baseline initial pane before relaunch-close scenario',
        anchorText,
        probeStyle,
      );
      await captureSessionArtifacts(client, runner.runDir, '01-baseline', sessionId);
    });

    initialShellPaneId = await runner.step('create_initial_split_before_relaunch', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialPaneId,
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'initial split before relaunch',
        30_000,
      );
      await assertPaneVisibleContent(client, sessionId, initialPaneId, {
        contains: anchorText,
        allowWrappedContains: true,
        minNonEmptyLines: 8,
        minDenseLines: 3,
        minCharCount: 200,
        // Initial pane is ~31 cols after the split; the anchor payload word-wraps, so
        // trimmed lines top out under 30 chars.
        minMaxLineLength: 20,
        timeoutMs: 20_000,
        description: 'initial pane transcript anchor preserved after initial split before relaunch',
      });
      await waitForProbeBannerAtGrid(client, sessionId, initialPaneId, probeStyle, 20_000);
      // Settle the agent TUI before capturing the baseline the post-relaunch
      // redraw is compared against — capturing mid-stream picks anchors from a
      // transient frame that legitimately no longer exists after relaunch.
      await waitForPaneContentStable(client, sessionId, initialPaneId);
      initialSplitMainState = await captureInitialPaneHealthyState(
        client,
        runner,
        sessionId,
        initialPaneId,
        '02-after-initial-split-initial-pane',
        'initial pane after initial split before relaunch',
        anchorText,
        probeStyle,
      );
      await captureSessionArtifacts(client, runner.runDir, '02-after-initial-split', sessionId);
      return newPane?.paneId || null;
    });

    await runner.step('relaunch_and_restore_session', async () => {
      await relaunchAppAndConnect(client, observer);
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, initialPaneId, 30_000);
      await waitForPaneVisible(client, sessionId, initialShellPaneId, 30_000);
      restoredMainState = await captureInitialPaneHealthyState(
        client,
        runner,
        sessionId,
        initialPaneId,
        '03-post-relaunch-initial-pane',
        'restored initial pane after relaunch',
        anchorText,
        probeStyle,
      );
      await assertPaneVisibleContentPreserved(
        client,
        sessionId,
        initialPaneId,
        initialSplitMainState?.state?.pane?.visibleContent || null,
        {
          minNonEmptyLineRatio: 0.7,
          minCharCountRatio: 0.55,
          minAnchorMatches: 2,
          // Anchor only on token lines (claude echo/reflow flake). Safe here: the baseline
          // is initialSplitMainState, captured via captureInitialPaneHealthyState with
          // requiredVisibleText: anchorText, which asserts contains: token first.
          ignoreAnchorPatterns: tokenAnchorIgnorePatterns(anchorText),
          timeoutMs: 20_000,
          description: 'restored initial pane content matches pre-relaunch split state',
        },
      );
      await captureSessionArtifacts(client, runner.runDir, '03-post-relaunch', sessionId);
    });

    postRelaunchMainSplitPaneId = await runner.step('split_from_initial_pane_after_relaunch', async () => {
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);
      const workspaceBefore = await client.request('get_workspace', { sessionId });
      const existingPaneIds = new Set((workspaceBefore.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', {
        sessionId,
        targetPaneId: initialPaneId,
        direction: 'vertical',
      });
      const newPane = await waitForNewShellPane(
        client,
        sessionId,
        existingPaneIds,
        'new shell after relaunch split from initial pane',
        30_000,
      );
      await assertPaneVisibleContent(client, sessionId, initialPaneId, {
        contains: anchorText,
        allowWrappedContains: true,
        minNonEmptyLines: 8,
        // The pane can be ~20 cols once three panes share the main area after the
        // relaunch split; dense/max-line gates are unreachable at that width — the
        // wrapped contains + charCount carry this assertion.
        minDenseLines: 0,
        minCharCount: 200,
        minMaxLineLength: 12,
        timeoutMs: 20_000,
        description: 'initial pane transcript anchor preserved after relaunch split from initial pane',
      });
      await waitForProbeBannerAtGrid(client, sessionId, initialPaneId, probeStyle, 20_000);
      await captureSessionArtifacts(client, runner.runDir, '04-after-initial-pane-split', sessionId);
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
      await waitForPaneVisible(client, sessionId, initialPaneId, 20_000);
      let previousInitialPaneWidth = (await client.request('get_pane_state', { sessionId, paneId: initialPaneId }))?.pane?.bounds?.width ?? 0;
      const firstRecovered = await closePaneAndAssertRecovery({
        client,
        runner,
        sessionId,
        initialPaneId,
        paneId: postRelaunchShellSplitPaneId,
        baselineNativeMetrics: null,
        previousInitialPaneWidth,
        minPaneCountAfterClose: 3,
        label: '06-after-closing-shell-split',
        enforceNativeStability: false,
        requiredVisibleText: anchorText,
        probeStyle,
      });
      previousInitialPaneWidth = firstRecovered?.state?.pane?.bounds?.width ?? firstRecovered?.widthState?.pane?.bounds?.width ?? previousInitialPaneWidth;
      await captureSessionArtifacts(client, runner.runDir, '06-after-closing-shell-split', sessionId);

      const secondRecovered = await closePaneAndAssertRecovery({
        client,
        runner,
        sessionId,
        initialPaneId,
        paneId: postRelaunchMainSplitPaneId,
        baselineNativeMetrics: firstRecovered?.nativeMetrics || restoredMainState?.nativeMetrics || null,
        previousInitialPaneWidth,
        minPaneCountAfterClose: 2,
        label: '07-after-closing-initial-pane-split',
        enforceNativeStability: true,
        requiredVisibleText: anchorText,
        probeStyle,
      });
      previousInitialPaneWidth = secondRecovered?.state?.pane?.bounds?.width ?? secondRecovered?.widthState?.pane?.bounds?.width ?? previousInitialPaneWidth;
      await captureSessionArtifacts(client, runner.runDir, '07-after-closing-initial-pane-split', sessionId);

      finalMainState = await closePaneAndAssertRecovery({
        client,
        runner,
        sessionId,
        initialPaneId,
        paneId: initialShellPaneId,
        baselineNativeMetrics: secondRecovered?.nativeMetrics || baselineMainState?.nativeMetrics || null,
        previousInitialPaneWidth,
        minPaneCountAfterClose: 1,
        label: '08-after-closing-initial-split',
        enforceNativeStability: true,
        requiredVisibleText: anchorText,
        probeStyle,
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
      anchorText,
      probeStyle,
      panes: {
        initialPaneId,
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
        otherPaneIds: (finalWorkspace.panes || [])
          .map((pane) => pane.paneId)
          .filter((paneId) => paneId !== initialPaneId),
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
        initialPaneId,
        initialShellPaneId,
        postRelaunchMainSplitPaneId,
        postRelaunchShellSplitPaneId,
      },
      widths: {
        baselineMainWidth: baselineMainState?.state?.pane?.bounds?.width ?? null,
        restoredMainWidth: restoredMainState?.state?.pane?.bounds?.width ?? null,
        finalMainWidth: finalMainState?.pane?.bounds?.width ?? null,
      },
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    await runFinalCleanup();
  }
}

// Guards direct execution only — this module is imported directly by
// scenario-tr205.test.mjs for its pure helpers, and importing must not
// trigger a real scenario run or process.exit.
const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1]);
if (isMainModule) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
}
