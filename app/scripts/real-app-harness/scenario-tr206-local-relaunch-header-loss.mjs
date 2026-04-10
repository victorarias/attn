#!/usr/bin/env node

import fs from 'node:fs';

import { parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { createScenarioRunner } from './scenarioRunner.mjs';
import {
  captureSessionArtifacts,
  scrollPaneToTop,
  waitForNewShellPane,
  waitForPaneInputFocus,
  waitForPaneTextChange,
  waitForPaneVisible,
  waitForSessionWorkspace,
} from './scenarioAssertions.mjs';
import { ensureCodexMainPromptReady } from './scenarioAgents.mjs';

const PROFILE_SEED = 0x206ca11;

function parseArgs(argv) {
  const args = [...argv];
  if (args[0] === '--') {
    args.shift();
  }

  const options = {
    ...parseCommonArgs([]),
    profileCount: 8,
  };

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--ws-url') options.wsUrl = args[++index];
    else if (arg === '--app-path') options.appPath = args[++index];
    else if (arg === '--artifacts-dir') options.artifactsDir = args[++index];
    else if (arg === '--session-root-dir') options.sessionRootDir = args[++index];
    else if (arg === '--profile-count') options.profileCount = Number(args[++index]);
    else if (arg === '--help' || arg === '-h') options.help = true;
    else throw new Error(`Unknown argument: ${arg}`);
  }

  if (!Number.isFinite(options.profileCount) || options.profileCount <= 0) {
    throw new Error(`Invalid --profile-count value: ${options.profileCount}`);
  }

  return {
    options,
    help: Boolean(options.help),
  };
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function normalizeText(text) {
  return String(text || '').replace(/\s+/g, '');
}

function hasCodexHeader(text) {
  const source = String(text || '');
  return source.includes('OpenAI Codex') || normalizeText(source).includes('OpenAICodex');
}

function createDeterministicProfiles(count) {
  let seed = PROFILE_SEED >>> 0;
  const nextFloat = () => {
    seed = ((1664525 * seed) + 1013904223) >>> 0;
    return seed / 0x100000000;
  };
  const nextInt = (min, max) => Math.floor(nextFloat() * (max - min + 1)) + min;

  return Array.from({ length: count }, (_, index) => ({
    id: `profile-${index + 1}`,
    closeAction: nextFloat() < 0.5 ? 'click' : 'focus',
    delays: {
      postReady: nextInt(50, 700),
      postInitialSplit: nextInt(50, 1200),
      postQuitBeforeLaunch: nextInt(100, 1200),
      postRelaunchBeforeSelect: nextFloat() < 0.5 ? nextInt(50, 1500) : 0,
      postRelaunchVisible: nextInt(50, 1000),
      preClose: nextInt(20, 500),
      postClose: nextInt(20, 900),
      postResplit: nextInt(20, 1200),
      postType: nextInt(100, 1200),
      postScroll: nextInt(100, 1200),
    },
  }));
}

function summarizeHeaderSnapshot(snapshot) {
  return {
    headerInText: snapshot.headerInText,
    viewportY: snapshot.viewportY,
    firstNonEmptyLine: snapshot.firstNonEmptyLine,
    textHead: snapshot.textHead,
  };
}

async function mainHeaderSnapshot(client, sessionId) {
  const [textPayload, state] = await Promise.all([
    client.request('read_pane_text', { sessionId, paneId: 'main' }, { timeoutMs: 20_000 }),
    client.request('get_pane_state', { sessionId, paneId: 'main' }, { timeoutMs: 20_000 }),
  ]);
  const text = typeof textPayload?.text === 'string' ? textPayload.text : '';
  return {
    text,
    textHead: text.slice(0, 4000),
    headerInText: hasCodexHeader(text),
    viewportY: state?.pane?.visibleContent?.viewportY ?? null,
    firstNonEmptyLine: state?.pane?.visibleContent?.summary?.firstNonEmptyLine ?? null,
  };
}

function classifyHeaderFailure(afterResplit, afterType, afterScroll) {
  if (!afterScroll.headerInText) {
    return 'nonrecovering_header_loss';
  }
  if (!afterResplit.headerInText || !afterType.headerInText) {
    return 'viewport_only_header_loss';
  }
  return 'unknown_header_failure';
}

async function withTimeout(promise, timeoutMs) {
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      setTimeout(() => reject(new Error(`Timed out after ${timeoutMs}ms`)), timeoutMs);
    }),
  ]);
}

async function runProfile(client, runner, profile) {
  const prefix = profile.id;
  const sessionDir = `${runner.sessionDir}/${profile.id}`;
  const label = `tr206-local-codex-${runner.runId}-${profile.id}`;
  let sessionId = null;
  const profileSummary = {
    profileId: profile.id,
    closeAction: profile.closeAction,
    delays: profile.delays,
  };

  const captureWindow = async (name) => {
    try {
      await client.request('capture_window_screenshot', {
        path: `${runner.runDir}/${name}`,
        bundleId: 'com.attn.manager',
      }, { timeoutMs: 20_000 });
    } catch {}
  };

  const failProfile = async (message, details) => {
    runner.writeJson(`${prefix}-failure-context.json`, details);
    await captureSessionArtifacts(client, runner.runDir, `${prefix}-failure`, sessionId);
    runner.assert(false, message, details);
  };

  try {
    await runner.step(`run_${profile.id}`, async () => {
      fs.mkdirSync(sessionDir, { recursive: true });
      await client.launchFreshApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);

      sessionId = (await client.request('create_session', {
        cwd: sessionDir,
        label,
        agent: 'codex',
      })).sessionId;
      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      await ensureCodexMainPromptReady(client, sessionId, 45_000);
      await delay(profile.delays.postReady);

      const baseline = await mainHeaderSnapshot(client, sessionId);
      runner.assert(baseline.headerInText, `${profile.id} baseline main Codex header visible`, summarizeHeaderSnapshot(baseline));

      const beforeSplitWorkspace = await client.request('get_workspace', { sessionId });
      const existingBeforeSplit = new Set((beforeSplitWorkspace.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', { sessionId, targetPaneId: 'main', direction: 'vertical' });
      const initialSplitPane = await waitForNewShellPane(client, sessionId, existingBeforeSplit, `${profile.id} initial split pane`, 30_000);
      const initialSplitPaneId = initialSplitPane.paneId;
      await waitForPaneVisible(client, sessionId, initialSplitPaneId, 30_000);
      await delay(profile.delays.postInitialSplit);

      await client.quitApp();
      await delay(profile.delays.postQuitBeforeLaunch);
      await client.launchApp();
      await client.waitForManifest(20_000);
      await client.waitForReady(20_000);
      await client.waitForFrontendResponsive(20_000);
      if (profile.delays.postRelaunchBeforeSelect > 0) {
        await delay(profile.delays.postRelaunchBeforeSelect);
      }

      await client.request('select_session', { sessionId });
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      await waitForPaneVisible(client, sessionId, initialSplitPaneId, 30_000);
      await delay(profile.delays.postRelaunchVisible);

      const afterRelaunch = await mainHeaderSnapshot(client, sessionId);
      if (!afterRelaunch.headerInText) {
        await failProfile(`${profile.id} main Codex header survives relaunch restore`, {
          sessionId,
          bugClass: 'header_missing_after_relaunch_restore',
          profile: profileSummary,
          baseline: summarizeHeaderSnapshot(baseline),
          afterRelaunch: summarizeHeaderSnapshot(afterRelaunch),
        });
      }
      runner.assert(true, `${profile.id} main Codex header survives relaunch restore`, summarizeHeaderSnapshot(afterRelaunch));

      if (profile.closeAction === 'click') {
        await client.request('click_pane', { sessionId, paneId: initialSplitPaneId });
      } else {
        await client.request('focus_pane', { sessionId, paneId: initialSplitPaneId });
      }
      await delay(profile.delays.preClose);
      await client.request('close_pane', { sessionId, paneId: initialSplitPaneId });
      await waitForSessionWorkspace(
        client,
        sessionId,
        (workspace) => (workspace.panes || []).length === 1,
        `${profile.id} workspace collapse after relaunch close`,
        20_000,
      );
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      await delay(profile.delays.postClose);

      const afterClose = await mainHeaderSnapshot(client, sessionId);
      if (!afterClose.headerInText) {
        await failProfile(`${profile.id} main Codex header survives relaunch split close`, {
          sessionId,
          bugClass: 'header_missing_after_relaunch_close',
          profile: profileSummary,
          baseline: summarizeHeaderSnapshot(baseline),
          afterRelaunch: summarizeHeaderSnapshot(afterRelaunch),
          afterClose: summarizeHeaderSnapshot(afterClose),
        });
      }
      runner.assert(true, `${profile.id} main Codex header survives relaunch split close`, summarizeHeaderSnapshot(afterClose));

      const beforeResplitWorkspace = await client.request('get_workspace', { sessionId });
      const existingBeforeResplit = new Set((beforeResplitWorkspace.panes || []).map((pane) => pane.paneId));
      await client.request('split_pane', { sessionId, targetPaneId: 'main', direction: 'vertical' });
      const secondSplitPane = await waitForNewShellPane(client, sessionId, existingBeforeResplit, `${profile.id} second split pane`, 30_000);
      const secondSplitPaneId = secondSplitPane.paneId;
      await waitForPaneVisible(client, sessionId, 'main', 30_000);
      await waitForPaneVisible(client, sessionId, secondSplitPaneId, 30_000);
      await delay(profile.delays.postResplit);

      await client.request('click_pane', { sessionId, paneId: 'main' });
      await waitForPaneInputFocus(client, sessionId, 'main', 15_000);
      const afterResplit = await mainHeaderSnapshot(client, sessionId);
      await captureWindow(`${prefix}-01-after-resplit.png`);

      if (!afterResplit.headerInText) {
        const afterScrollState = await scrollPaneToTop(client, sessionId, 'main', 12_000).catch((error) => ({ error: error.message }));
        await delay(profile.delays.postScroll);
        const afterScrollSnapshot = await mainHeaderSnapshot(client, sessionId);
        await captureWindow(`${prefix}-02-after-resplit-scroll.png`);
        await failProfile(`${profile.id} main Codex header survives relaunch close-resplit before typing`, {
          sessionId,
          bugClass: classifyHeaderFailure(afterResplit, afterResplit, afterScrollSnapshot),
          profile: profileSummary,
          baseline: summarizeHeaderSnapshot(baseline),
          afterRelaunch: summarizeHeaderSnapshot(afterRelaunch),
          afterClose: summarizeHeaderSnapshot(afterClose),
          afterResplit: summarizeHeaderSnapshot(afterResplit),
          afterScroll: {
            ...summarizeHeaderSnapshot(afterScrollSnapshot),
            scrollState: afterScrollState,
          },
        });
      }
      runner.assert(true, `${profile.id} main Codex header survives relaunch close-resplit before typing`, summarizeHeaderSnapshot(afterResplit));

      const token = `${profile.id.toUpperCase().replace(/-/g, '')}${Date.now()}`;
      await client.request('type_pane_via_ui', { sessionId, paneId: 'main', text: token });
      await waitForPaneTextChange(
        client,
        sessionId,
        'main',
        afterResplit.text,
        `${profile.id} main pane text change after typing`,
        15_000,
      );
      await delay(profile.delays.postType);
      const afterType = await mainHeaderSnapshot(client, sessionId);
      await captureWindow(`${prefix}-03-after-type.png`);

      if (!afterType.headerInText) {
        const scrollState = await scrollPaneToTop(client, sessionId, 'main', 12_000).catch((error) => ({ error: error.message }));
        await delay(profile.delays.postScroll);
        const afterScroll = await mainHeaderSnapshot(client, sessionId);
        await captureWindow(`${prefix}-04-after-type-scroll.png`);
        await failProfile(`${profile.id} typing keeps the main Codex header alive after relaunch close-resplit`, {
          sessionId,
          bugClass: classifyHeaderFailure(afterResplit, afterType, afterScroll),
          profile: profileSummary,
          token,
          baseline: summarizeHeaderSnapshot(baseline),
          afterRelaunch: summarizeHeaderSnapshot(afterRelaunch),
          afterClose: summarizeHeaderSnapshot(afterClose),
          afterResplit: summarizeHeaderSnapshot(afterResplit),
          afterType: summarizeHeaderSnapshot(afterType),
          afterScroll: {
            ...summarizeHeaderSnapshot(afterScroll),
            scrollState,
          },
        });
      }
      runner.assert(true, `${profile.id} typing keeps the main Codex header alive after relaunch close-resplit`, {
        token,
        ...summarizeHeaderSnapshot(afterType),
      });
    });
  } finally {
    try {
      await withTimeout(client.request('close_session', { sessionId }, { timeoutMs: 20_000 }), 10_000);
    } catch {}
    try {
      await withTimeout(client.quitApp(), 5_000);
    } catch {}
  }

  return {
    profileId: profile.id,
    sessionId,
  };
}

async function main() {
  const { options, help } = parseArgs(process.argv.slice(2));
  if (help) {
    printCommonHelp('scripts/real-app-harness/scenario-tr206-local-relaunch-header-loss.mjs');
    console.log(`Additional options:
  --profile-count <n>        Number of deterministic relaunch timing profiles to run (default: 8)
`);
    return;
  }

  const runner = createScenarioRunner(options, {
    scenarioId: 'TR-206',
    tier: 'tier2-local-real-agent',
    prefix: 'scenario-tr206-local-codex-relaunch-header',
    metadata: {
      agent: 'codex',
      focus: 'local relaunch close-resplit Codex header preservation',
      profileSeed: PROFILE_SEED,
      profileCount: options.profileCount,
    },
  });
  const client = new UiAutomationClient({ appPath: options.appPath });

  const profiles = createDeterministicProfiles(options.profileCount);
  runner.writeJson('profiles.json', profiles);

  try {
    const runResults = [];
    for (const profile of profiles) {
      runResults.push(await runProfile(client, runner, profile));
    }

    const summary = runner.finishSuccess({
      profiles,
      runResults,
    });
    console.log(JSON.stringify(summary, null, 2));
  } catch (error) {
    const summary = runner.finishFailure(error, {
      profiles,
    });
    console.error(summary.error);
    process.exitCode = 1;
  } finally {
    try {
      await withTimeout(client.quitApp(), 5_000);
    } catch {}
  }
}

main()
  .then(() => {
    process.exit(process.exitCode ?? 0);
  })
  .catch((error) => {
    console.error(error instanceof Error ? error.stack || error.message : String(error));
    process.exit(1);
  });
