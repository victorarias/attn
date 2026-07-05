#!/usr/bin/env node

// Leak-soak RSS scenario.
//
// scenario-perf-cold-warm.mjs takes two single-cycle snapshots, each preceded
// by a full data-dir wipe, so it can compare a reproducible cold footprint
// against a reproducible warm footprint -- but it can never see a leak that
// only accumulates WITHIN a single continuous process. This scenario runs N
// create -> workload -> close cycles inside ONE long-lived app instance (NO
// wipe between cycles -- the leak only accumulates within a single continuous
// process), sampling RETAINED RSS after a decay hold each cycle (the LAST
// sample of a decay window, never the peak -- macOS scavenges freed pages
// lazily, so only a settled sample reflects what the process is actually
// holding onto). It then fits the least-squares slope of retained-RSS-vs-cycle
// across the post-warmup cycles: a flat slope is healthy churn, a positive
// staircase is a leak.
//
// Requires a dedicated non-prod profile app bundle (one-time
// `make install PROFILE=perf`) and is driven with `ATTN_HARNESS_PROFILE=perf`
// -- like scenario-perf-cold-warm.mjs, this scenario refuses to run against
// the dev sibling or prod.
//
// Usage:
//   ATTN_HARNESS_PROFILE=perf pnpm run real-app:scenario-perf-leak-soak -- --cycles 12

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, createSessionAndWaitForInitialPane, emitVerdict, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { profileForAppPath, assertProductionRunAllowed } from './harnessProfile.mjs';
import { getMachineFingerprint, loadBaseline, recordOrCompareBaseline } from './machineRegistry.mjs';
import { buildLeakSoakVerdict, evaluateRssBaseline, fitSlope } from './rssBaselineVerdict.mjs';
import { captureWebKitPids, readLiveDaemonPid, closeSessions, fillAllPanes, sampleWindow, teardownProfileState } from './perfMeasure.mjs';

function parseArgs(argv) {
  const filtered = argv.filter((arg) => arg !== '--');
  const passthrough = [];
  const extras = {
    cycles: 12,
    sessions: 6,
    workloadCmd: 'seq 1 60000',
    reclaimHoldMs: 8000,
    perPaneSettleMs: 3000,
    warmupCycles: 2,
    slopeThresholdMb: 5,
    recordBaseline: false,
  };
  for (let index = 0; index < filtered.length; index += 1) {
    const arg = filtered[index];
    if (arg === '--cycles') extras.cycles = Number(filtered[++index]);
    else if (arg === '--sessions') extras.sessions = Number(filtered[++index]);
    else if (arg === '--workload-cmd') extras.workloadCmd = filtered[++index];
    else if (arg === '--reclaim-hold-ms') extras.reclaimHoldMs = Number(filtered[++index]);
    else if (arg === '--per-pane-settle-ms') extras.perPaneSettleMs = Number(filtered[++index]);
    else if (arg === '--warmup-cycles') extras.warmupCycles = Number(filtered[++index]);
    else if (arg === '--slope-threshold-mb') extras.slopeThresholdMb = Number(filtered[++index]);
    else if (arg === '--record-baseline') extras.recordBaseline = true;
    else passthrough.push(arg);
  }
  const options = parseCommonArgs(passthrough);
  return Object.assign(options, extras);
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-perf-leak-soak.mjs');
    console.log('  --cycles <n>              Create->workload->close cycles to run in one app instance (default: 12)');
    console.log('  --sessions <n>            Shell sessions to create per cycle (default: 6)');
    console.log('  --workload-cmd <cmd>      Per-pane workload command run once per pane each cycle');
    console.log('                            (default: "seq 1 60000")');
    console.log('  --reclaim-hold-ms <n>     Decay-hold window after closing each cycle\'s sessions, before');
    console.log('                            sampling the retained RSS for that cycle (default: 8000)');
    console.log('  --per-pane-settle-ms <n>  Settle time after writing the workload command into each pane');
    console.log('                            (default: 3000)');
    console.log('  --warmup-cycles <n>       Leading cycles dropped from the slope fit -- they include');
    console.log('                            one-time WASM heap growth, not a leak (default: 2)');
    console.log('  --slope-threshold-mb <n>  Max allowed retained-RSS slope in MB/cycle before the verdict');
    console.log('                            fails (default: 5)');
    console.log('  --record-baseline         Overwrite this machine\'s leak-floor baseline with this run\'s');
    console.log('                            first post-warmup retained RSS instead of comparing against it');
    console.log('');
    console.log('Requires a dedicated non-prod profile: run `make install PROFILE=perf` once, then');
    console.log('drive this scenario with ATTN_HARNESS_PROFILE=perf.');
    return;
  }

  const startedAt = Date.now();
  const profile = profileForAppPath(options.appPath);
  assertProductionRunAllowed({ appPath: options.appPath, wsUrl: options.wsUrl });
  if (!profile || profile === 'dev') {
    throw new Error(
      'scenario-perf-leak-soak needs a DEDICATED non-prod profile, distinct from the shared dev '
      + 'sibling -- it runs many cycles inside one long-lived app instance, so it must never be prod '
      + '(~/.attn) or the dev world (~/.attn-dev) that attn-on-attn iteration depends on. Set '
      + 'ATTN_HARNESS_PROFILE=perf and run `make install PROFILE=perf` once if you haven\'t already.',
    );
  }

  if (options.cycles - options.warmupCycles < 2) {
    throw new Error(
      `scenario-perf-leak-soak needs at least 2 post-warmup cycles to fit a slope, but got `
      + `cycles=${options.cycles} warmupCycles=${options.warmupCycles} (${options.cycles - options.warmupCycles} left)`,
    );
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'perf-leak-soak');
  const client = new UiAutomationClient({ appPath: options.appPath });

  await teardownProfileState({ client, profile });
  const webkitBaseline = await captureWebKitPids();

  await client.launchFreshApp();
  await client.waitForManifest(20_000);
  await client.waitForReady(20_000);
  await client.waitForFrontendResponsive(20_000);

  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  await observer.connect();
  const retainedByCycle = [];
  try {
    const appPid = client.readManifest().pid;
    const daemonPid = readLiveDaemonPid(profile);
    if (!daemonPid) {
      console.warn('[perf] WARNING: no live daemon pid file found; daemon + pty-worker RSS are NOT included in this run');
    }

    for (let cycle = 0; cycle < options.cycles; cycle += 1) {
      const sessionIds = [];
      for (let i = 0; i < options.sessions; i += 1) {
        const label = `perf-leak-soak-${runId}-c${cycle}-${i}`;
        const sessionId = await createSessionAndWaitForInitialPane({
          client,
          observer,
          cwd: sessionDir,
          label,
          agent: 'shell',
          sessionWaitMs: 30_000,
          waitForInitialPaneVisible: true,
          initialPaneWaitMs: 25_000,
        });
        sessionIds.push(sessionId);
      }

      await fillAllPanes(client, sessionIds, options.workloadCmd, options.perPaneSettleMs);
      await closeSessions(client, sessionIds);

      // Decay hold: sample repeatedly over the window and keep only the LAST
      // sample (retained), never the peak -- macOS scavenges freed pages
      // lazily, so a sample taken right after close would still show the
      // transient workload spike, not what the process actually retains.
      const win = await sampleWindow(appPid, daemonPid, webkitBaseline, options.reclaimHoldMs);
      retainedByCycle.push(win.last.totalRssMb);
      console.log(`[perf] cycle ${cycle + 1}/${options.cycles}: retained ${win.last.totalRssMb} MB (peak ${win.peak.totalRssMb})`);
    }
  } finally {
    await observer.close();
  }

  const post = retainedByCycle.slice(options.warmupCycles);
  const { slope } = fitSlope(post);

  // Registry comparison is an informational trend signal (mirrors cold/warm's
  // usage of evaluateRssBaseline) -- it is NEVER what gates this scenario's
  // verdict; the slope-vs-threshold check above is.
  const fingerprint = getMachineFingerprint();
  const key = `${fingerprint.key}-leak-floor`;
  const floorEval = evaluateRssBaseline({
    totalRssMb: post[0],
    fingerprint,
    baseline: loadBaseline(key),
    tolerancePct: 15,
    record: options.recordBaseline,
    recordedAt: new Date().toISOString(),
  });
  recordOrCompareBaseline({ evaluation: floorEval, key, label: 'leak-floor ' });

  const verdict = buildLeakSoakVerdict({
    retainedByCycle,
    warmupCycles: options.warmupCycles,
    slope,
    slopeThresholdMb: options.slopeThresholdMb,
    scenarioId: 'perf-leak-soak',
    runId,
    artifactsDir: runDir,
    summaryPath: path.join(runDir, 'summary.json'),
    durationMs: Date.now() - startedAt,
  });

  const summaryPath = path.join(runDir, 'summary.json');
  const summary = {
    ok: verdict.ok,
    runId,
    runDir,
    cycles: options.cycles,
    sessions: options.sessions,
    warmupCycles: options.warmupCycles,
    workloadCmd: options.workloadCmd,
    slopeThresholdMb: options.slopeThresholdMb,
    retainedByCycle,
    postWarmupRetained: post,
    slope,
    floorComparison: floorEval.comparison,
  };
  fs.writeFileSync(summaryPath, `${JSON.stringify(summary, null, 2)}\n`, 'utf8');

  console.log(JSON.stringify({ slope, slopeThresholdMb: options.slopeThresholdMb, retainedByCycle, runDir }, null, 2));

  // A slope regression is a trend signal, not a harness error: it surfaces as
  // verdict.ok:false but never sets a non-zero exit code (only real errors do
  // that, via main().catch below).
  emitVerdict(verdict);
}

main().catch((error) => {
  console.error('[perf] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
