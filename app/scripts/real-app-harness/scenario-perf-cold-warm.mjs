#!/usr/bin/env node

// Cold vs warm RSS scenario.
//
// scenario-perf-baseline.mjs's single RSS number self-baselines against its
// OWN prior history: a freshly launched app and one that has cycled sessions
// for a while land at very different footprints, so its headline number is
// not reproducible run-to-run on the same machine. This scenario captures TWO
// reproducible numbers instead, each preceded by a full data-dir wipe so
// neither depends on prior app history:
//
//   cold = fresh app + N sessions, measured right after settle (no history)
//   warm = fresh app + N sessions + a bounded per-pane warmup burst (grows the
//          Ghostty WASM heaps/atlas), then settle -- the worked-then-idle
//          footprint
//
// Requires a dedicated non-prod profile app bundle (one-time
// `make install PROFILE=perf`) and is driven with `ATTN_HARNESS_PROFILE=perf`
// -- wiping ~/.attn-dev or (far worse) ~/.attn between phases is not
// acceptable, so this scenario refuses to run against the dev sibling or prod.
//
// Usage:
//   ATTN_HARNESS_PROFILE=perf pnpm run real-app:scenario-perf-cold-warm -- --sessions 8

import fs from 'node:fs';
import path from 'node:path';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, createSessionAndWaitForInitialPane, emitVerdict, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { profileForAppPath, assertProductionRunAllowed } from './harnessProfile.mjs';
import { getMachineFingerprint, loadBaseline, saveBaseline } from './machineRegistry.mjs';
import { buildColdWarmVerdict, evaluateRssBaseline } from './rssBaselineVerdict.mjs';
import { delay, captureWebKitPids, snapshot, classRssMb, readLiveDaemonPid, paneIdForSession, closeSessions, fillAllPanes, teardownProfileState } from './perfMeasure.mjs';

function parseArgs(argv) {
  const filtered = argv.filter((arg) => arg !== '--');
  const passthrough = [];
  const extras = {
    sessions: 8,
    settleMs: 4000,
    warmupCmd: 'seq 1 60000',
    warmupSettleMs: 3000,
    rssTolerancePct: 15,
    recordBaseline: false,
  };
  for (let index = 0; index < filtered.length; index += 1) {
    const arg = filtered[index];
    if (arg === '--sessions') extras.sessions = Number(filtered[++index]);
    else if (arg === '--settle-ms') extras.settleMs = Number(filtered[++index]);
    else if (arg === '--warmup-cmd') extras.warmupCmd = filtered[++index];
    else if (arg === '--warmup-settle-ms') extras.warmupSettleMs = Number(filtered[++index]);
    else if (arg === '--rss-tolerance-pct') extras.rssTolerancePct = Number(filtered[++index]);
    else if (arg === '--record-baseline') extras.recordBaseline = true;
    else passthrough.push(arg);
  }
  const options = parseCommonArgs(passthrough);
  return Object.assign(options, extras);
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-perf-cold-warm.mjs');
    console.log('  --sessions <n>            Shell sessions to create per phase (default: 8)');
    console.log('  --settle-ms <n>           Settle time before each snapshot (default: 4000)');
    console.log('  --warmup-cmd <cmd>        Per-pane warmup command for the warm phase, run once');
    console.log('                            per pane to grow its Ghostty WASM heap (default: "seq 1 60000")');
    console.log('  --warmup-settle-ms <n>    Settle time after the warmup burst, before the warm');
    console.log('                            snapshot (default: 3000)');
    console.log('  --rss-tolerance-pct <n>   Allowed growth over each phase\'s per-machine baseline');
    console.log('                            before the verdict fails (default: 15)');
    console.log('  --record-baseline         Overwrite both the cold and warm per-machine baselines');
    console.log('                            with this run\'s RSS instead of comparing against them');
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
      'scenario-perf-cold-warm needs a DEDICATED non-prod profile, distinct from the shared dev '
      + 'sibling -- each phase wipes its data dir, so it must never be prod (~/.attn) or the dev '
      + 'world (~/.attn-dev) that attn-on-attn iteration depends on. Set ATTN_HARNESS_PROFILE=perf '
      + 'and run `make install PROFILE=perf` once if you haven\'t already.',
    );
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'perf-cold-warm');
  const client = new UiAutomationClient({ appPath: options.appPath });

  async function runPhase({ warmup }) {
    await teardownProfileState({ client, profile });
    const webkitBaseline = await captureWebKitPids();

    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);

    const observer = new DaemonObserver({ wsUrl: options.wsUrl });
    await observer.connect();
    try {
      const appPid = client.readManifest().pid;
      const daemonPid = readLiveDaemonPid(profile);
      if (!daemonPid) {
        console.warn('[perf] WARNING: no live daemon pid file found; daemon + pty-worker RSS are NOT included in this phase');
      }

      const sessionIds = [];
      for (let i = 0; i < options.sessions; i += 1) {
        const label = `perf-cold-warm-${runId}-${i}`;
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
        console.log(`[perf] created session ${i + 1}/${options.sessions} (${sessionId})`);
      }

      await delay(options.settleMs);

      if (warmup) {
        await fillAllPanes(client, sessionIds, options.warmupCmd, options.warmupSettleMs);
        await delay(options.settleMs);
      }

      const snap = await snapshot(appPid, daemonPid, webkitBaseline);
      console.log(`[perf] ${warmup ? 'WARM' : 'COLD'} @ ${options.sessions} sessions: ${snap.totalRssMb} MB (${snap.procCount} procs)`);

      await closeSessions(client, sessionIds);
      return snap;
    } finally {
      await observer.close();
    }
  }

  const coldSnap = await runPhase({ warmup: false });
  const warmSnap = await runPhase({ warmup: true });

  // Compare each phase against its own key in the per-machine registry (see
  // machineRegistry.mjs). Never affects the process exit code -- a regression
  // is a trend signal, not a harness error.
  const fingerprint = getMachineFingerprint();
  const recordedAt = new Date().toISOString();
  const coldKey = `${fingerprint.key}-cold`;
  const warmKey = `${fingerprint.key}-warm`;
  const coldEval = evaluateRssBaseline({
    totalRssMb: coldSnap.totalRssMb,
    fingerprint,
    baseline: loadBaseline(coldKey),
    tolerancePct: options.rssTolerancePct,
    record: options.recordBaseline,
    recordedAt,
  });
  const warmEval = evaluateRssBaseline({
    totalRssMb: warmSnap.totalRssMb,
    fingerprint,
    baseline: loadBaseline(warmKey),
    tolerancePct: options.rssTolerancePct,
    record: options.recordBaseline,
    recordedAt,
  });
  if (coldEval.baselineToSave) saveBaseline(coldKey, coldEval.baselineToSave);
  if (warmEval.baselineToSave) saveBaseline(warmKey, warmEval.baselineToSave);

  if (coldEval.baselineToSave) {
    console.log(`[perf] recorded cold baseline for machine ${coldKey}: ${coldSnap.totalRssMb} MB`);
  } else {
    console.log(
      `[perf] compared to cold baseline for machine ${coldKey}: ${coldEval.comparison.value} MB `
      + `vs ${coldEval.comparison.baseline} MB (${coldEval.comparison.reason}, tolerance ${coldEval.comparison.tolerancePct}%)`,
    );
  }
  if (warmEval.baselineToSave) {
    console.log(`[perf] recorded warm baseline for machine ${warmKey}: ${warmSnap.totalRssMb} MB`);
  } else {
    console.log(
      `[perf] compared to warm baseline for machine ${warmKey}: ${warmEval.comparison.value} MB `
      + `vs ${warmEval.comparison.baseline} MB (${warmEval.comparison.reason}, tolerance ${warmEval.comparison.tolerancePct}%)`,
    );
  }

  const summary = {
    ok: coldEval.ok && warmEval.ok,
    runId,
    runDir,
    sessions: options.sessions,
    warmupCmd: options.warmupCmd,
    cold: { totalRssMb: coldSnap.totalRssMb, byClass: coldSnap.byClass },
    warm: { totalRssMb: warmSnap.totalRssMb, byClass: warmSnap.byClass },
    coldComparison: coldEval.comparison,
    warmComparison: warmEval.comparison,
    headline: {
      coldRssMb: coldSnap.totalRssMb,
      warmRssMb: warmSnap.totalRssMb,
      deltaMb: Number((warmSnap.totalRssMb - coldSnap.totalRssMb).toFixed(1)),
    },
  };
  const summaryPath = path.join(runDir, 'summary.json');
  fs.writeFileSync(summaryPath, `${JSON.stringify(summary, null, 2)}\n`, 'utf8');

  console.log(JSON.stringify({ headline: summary.headline, coldByClass: coldSnap.byClass, warmByClass: warmSnap.byClass, runDir }, null, 2));

  // A regression against either phase's machine baseline is a trend signal,
  // not a harness error: it surfaces as verdict.ok:false but never sets a
  // non-zero exit code (only real errors do that, via main().catch below).
  emitVerdict(buildColdWarmVerdict({
    cold: coldEval.comparison,
    warm: warmEval.comparison,
    scenarioId: 'perf-cold-warm',
    runId,
    artifactsDir: runDir,
    summaryPath,
    durationMs: Date.now() - startedAt,
  }));
}

main().catch((error) => {
  console.error('[perf] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
