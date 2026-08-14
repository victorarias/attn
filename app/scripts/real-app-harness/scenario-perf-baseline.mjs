#!/usr/bin/env node

// Performance baseline scenario.
//
// Drives the packaged dev app to a fixed number of shell sessions and captures
// the per-process RSS of the whole attn tree (app + WebKit + daemon + one
// pty-worker subprocess per session). Shell sessions are deliberate: the memory
// workstreams target attn's OWN footprint (frontend terminals, worker scrollback
// buffers, daemon heap), not the external claude/codex agent processes, so a
// shell isolates exactly what we optimize and stays deterministic.
//
// Worker ring buffers are committed eagerly at spawn, and the frontend mounts a
// terminal per visible/warm workspace, so the IDLE N-session snapshot already
// captures the memory levers. Streaming (via benchmark_pty_transport) is only
// used to drive a CPU profile, and is best-effort.
//
// The daemon is detached from the app, so its pid (and therefore its pty-worker
// children) is resolved from the profile pid file (~/.attn-dev/attn.pid for the
// dev install). That happens on every run, including the default one, so daemon
// + worker RSS is always part of the baseline; if the pid file is missing/stale
// the run warns loudly rather than silently reporting daemon: 0.
//
// When ATTN_PPROF is set in this process's environment, the scenario also
// restarts the dev daemon so the freshly spawned one inherits the flag, then
// pulls /debug/vars (authoritative daemon pid + worker pids) and heap/CPU pprof
// profiles from the loopback diagnostics endpoint.
//
// Usage:
//   pnpm run real-app:scenario-perf-baseline -- --sessions 8 --stream 2
//   ATTN_PPROF=6060 pnpm run real-app:scenario-perf-baseline -- --sessions 8 --stream 2

import fs from 'node:fs';
import path from 'node:path';
import http from 'node:http';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, createSessionAndWaitForInitialPane, emitVerdict, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { profileForAppPath, socketPathForProfile } from './harnessProfile.mjs';
import { getMachineFingerprint, loadBaseline, recordOrCompareBaseline } from './machineRegistry.mjs';
import { buildBaselineVerdict, evaluateRssBaseline } from './rssBaselineVerdict.mjs';
import { captureFrontWindowScreenshot, getFrontWindowBounds, setFrontWindowBounds } from './nativeWindowCapture.mjs';
import { delay, captureWebKitPids, snapshot, classRssMb, sampleWindow, readLiveDaemonPid, stopDaemon, paneIdForSession, closeSessions, fillAllPanes, readRegionFootprint, readGraphicsRegions, readAppFootprint } from './perfMeasure.mjs';

const execFileAsync = promisify(execFile);

// The app's own WebContent: the largest of the attributed WebContent pids. A
// second one appears for an auxiliary web view (the in-app browser), and it is
// never the one holding the terminal panes.
function webContentPid(snap) {
  const pids = snap?.byClass?.webkit_webcontent?.pids ?? [];
  if (pids.length === 0) return null;
  return pids.reduce((best, entry) => (entry.rssKb > best.rssKb ? entry : best)).pid;
}

function parseArgs(argv) {
  const filtered = argv.filter((arg) => arg !== '--');
  const passthrough = [];
  const extras = {
    sessions: 8,
    stream: 2,
    chunkBytes: 64 * 1024,
    chunkCount: 256,
    settleMs: 4000,
    restartDaemon: true,
    cpuSeconds: 20,
    realCmd: null,
    realWindowMs: 25000,
    window: null,
    warm: null,
    fillCmd: null,
    fillSettleMs: 3000,
    reclaimHoldMs: 0,
    reclaimHoldIntervalMs: 15000,
    pressure: false,
    closeProbe: false,
    churnRounds: 0,
    dockProbe: null,
    switchProbe: false,
    rssTolerancePct: 15,
    recordBaseline: false,
  };
  for (let index = 0; index < filtered.length; index += 1) {
    const arg = filtered[index];
    if (arg === '--sessions') extras.sessions = Number(filtered[++index]);
    else if (arg === '--stream') extras.stream = Number(filtered[++index]);
    else if (arg === '--chunk-bytes') extras.chunkBytes = Number(filtered[++index]);
    else if (arg === '--chunk-count') extras.chunkCount = Number(filtered[++index]);
    else if (arg === '--settle-ms') extras.settleMs = Number(filtered[++index]);
    else if (arg === '--cpu-seconds') extras.cpuSeconds = Number(filtered[++index]);
    else if (arg === '--real-cmd') extras.realCmd = filtered[++index];
    else if (arg === '--real-window-ms') extras.realWindowMs = Number(filtered[++index]);
    else if (arg === '--no-restart-daemon') extras.restartDaemon = false;
    else if (arg === '--warm') extras.warm = filtered[++index];
    else if (arg === '--window') extras.window = filtered[++index];
    else if (arg === '--fill-cmd') extras.fillCmd = filtered[++index];
    else if (arg === '--fill-settle-ms') extras.fillSettleMs = Number(filtered[++index]);
    else if (arg === '--reclaim-hold-ms') extras.reclaimHoldMs = Number(filtered[++index]);
    else if (arg === '--reclaim-hold-interval-ms') extras.reclaimHoldIntervalMs = Number(filtered[++index]);
    else if (arg === '--rss-tolerance-pct') extras.rssTolerancePct = Number(filtered[++index]);
    else if (arg === '--pressure') extras.pressure = true;
    else if (arg === '--close-probe') extras.closeProbe = true;
    else if (arg === '--churn') extras.churnRounds = Number(filtered[++index]);
    else if (arg === '--dock-probe') extras.dockProbe = filtered[++index];
    else if (arg === '--switch-probe') extras.switchProbe = true;
    else if (arg === '--record-baseline') extras.recordBaseline = true;
    else passthrough.push(arg);
  }
  const options = parseCommonArgs(passthrough);
  return Object.assign(options, extras);
}

// The harness parks the window nearly off-screen, and `screencapture -R` grabs a
// screen region -- so a shot taken as-is catches the parked sliver, or whatever
// window happens to sit in front. Put the window fully on screen and raise the
// app before capturing anything.
async function bringWindowForward(client) {
  const parked = await getFrontWindowBounds(client.bundleId, { client }).catch(() => null);
  if (parked) {
    await setFrontWindowBounds({ x: 0, y: 25, width: parked.width, height: parked.height }, { client })
      .catch((error) => console.warn(`[perf] unpark failed: ${error.message}`));
    await delay(1500);
  }
  await execFileAsync('open', ['-b', client.bundleId]).catch(() => {});
  await delay(1200);
}

function pprofPort() {
  const raw = (process.env.ATTN_PPROF || '').trim().toLowerCase();
  if (!raw || ['0', 'off', 'false', 'no'].includes(raw)) return null;
  if (['1', 'on', 'true', 'yes'].includes(raw)) return 6060;
  const match = raw.match(/(\d+)\s*$/);
  if (match) {
    const port = Number(match[1]);
    if (port > 0 && port <= 65535) return port;
  }
  return null;
}

function httpGetJson(port, urlPath, timeoutMs = 3000) {
  return new Promise((resolve, reject) => {
    const req = http.get({ host: '127.0.0.1', port, path: urlPath, timeout: timeoutMs }, (res) => {
      let data = '';
      res.on('data', (chunk) => { data += chunk; });
      res.on('end', () => {
        if (res.statusCode !== 200) { reject(new Error(`status ${res.statusCode}`)); return; }
        try { resolve(JSON.parse(data)); } catch (error) { reject(error); }
      });
    });
    req.on('error', reject);
    req.on('timeout', () => req.destroy(new Error('timeout')));
  });
}

function httpGetToFile(port, urlPath, outPath, timeoutMs = 60_000) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(outPath);
    const req = http.get({ host: '127.0.0.1', port, path: urlPath, timeout: timeoutMs }, (res) => {
      if (res.statusCode !== 200) { reject(new Error(`status ${res.statusCode}`)); return; }
      res.pipe(file);
      file.on('finish', () => file.close(() => resolve(outPath)));
    });
    req.on('error', reject);
    req.on('timeout', () => req.destroy(new Error('timeout')));
  });
}

async function waitForSessionsGone(observer, predicate, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (![...observer.sessionsById.values()].some(predicate)) return true;
    await delay(300);
  }
  return false;
}

async function streamBurst(client, sessionId, paneId, options) {
  return client.request('benchmark_pty_transport', {
    sessionId,
    paneId,
    mode: 'json_base64',
    chunkBytes: options.chunkBytes,
    chunkCount: options.chunkCount,
    flushEvery: 1,
  }, { timeoutMs: 120_000 });
}

// Parse the --warm value (comma-separated ints, e.g. "-1,3,2,1,0") into the list
// of warm-workspace limits to sweep. Returns null when --warm was not passed.
function parseWarmLevels(raw) {
  if (raw == null) return null;
  const levels = String(raw)
    .split(',')
    .map((part) => Number(part.trim()))
    .filter((value) => Number.isInteger(value));
  return levels.length > 0 ? levels : null;
}

// Live (mounted) panes for a warm limit: active + `limit` recent, or all `n` when
// the limit is negative (virtualization disabled). Used to order the sweep so it
// only ever tears panes down.
function warmLiveCount(limit, sessions) {
  return limit < 0 ? sessions : Math.min(sessions, limit + 1);
}

// Drive each session to `idle` through the real state-report path so the warm-set
// can reclaim its (now cold) workspace. `attn _hook-state <id> idle` is exactly
// what an agent's hook emits; the daemon broadcasts the state change and the
// frontend drops the live-runtime protection. stdin is /dev/null so the binary's
// optional hook-input JSON read hits EOF immediately instead of blocking.
function reportSessionState(bin, socketPath, sessionId, state) {
  return new Promise((resolve) => {
    const child = spawn(bin, ['_hook-state', sessionId, state], {
      env: { ...process.env, ATTN_SOCKET_PATH: socketPath },
      stdio: ['ignore', 'ignore', 'ignore'],
    });
    child.on('close', () => resolve());
    child.on('error', () => resolve());
  });
}

async function markSessionsIdle(client, options, sessionIds) {
  const profile = profileForAppPath(options.appPath);
  const bin = path.join(options.appPath, 'Contents', 'MacOS', 'attn');
  const socketPath = socketPathForProfile(profile);
  for (const sessionId of sessionIds) {
    await reportSessionState(bin, socketPath, sessionId, 'idle');
  }
  const target = new Set(sessionIds);
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    const state = await client.request('get_state', {}, { timeoutMs: 10_000 }).catch(() => null);
    const ours = (state?.sessions ?? []).filter((session) => target.has(session.id));
    if (ours.length === sessionIds.length && ours.every((session) => session.state === 'idle')) {
      console.log(`[perf] marked ${sessionIds.length} sessions idle (warm-set can now reclaim cold panes)`);
      return;
    }
    await delay(300);
  }
  console.warn('[perf] WARNING: not all sessions reached idle within 15s; warm-set virtualization may not engage');
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/scenario-perf-baseline.mjs');
    console.log('  --sessions <n>      Shell sessions to create (default: 8)');
    console.log('  --stream <n>        Sessions to stream into for the CPU profile (default: 2)');
    console.log('  --chunk-bytes <n>   Stream payload bytes per chunk (default: 65536)');
    console.log('  --chunk-count <n>   Stream chunks per burst (default: 256)');
    console.log('  --settle-ms <n>     Settle time before the idle snapshot (default: 4000)');
    console.log('  --cpu-seconds <n>   CPU profile duration when ATTN_PPROF set (default: 20)');
    console.log('  --real-cmd <cmd>    Run a heavy shell command in each stream target via the normal');
    console.log('                      pty path (real-output repro), e.g. "seq 1 5000000". Skips the');
    console.log('                      synthetic benchmark/CPU profile and samples peak + post RSS.');
    console.log('  --real-window-ms <n>  Sampling window for --real-cmd (default: 25000)');
    console.log('  --no-restart-daemon Do not restart the dev daemon for ATTN_PPROF');
    console.log('  --window <WxH>      Resize the app window before measuring (e.g. 1728x1080). A');
    console.log('                      pane\'s GPU surface is sized in device pixels, so the default');
    console.log('                      1200x800 launch window understates per-pane graphics memory.');
    console.log('  --warm <list>       Warm-workspace A/B (terminal virtualization). Drives every');
    console.log('                      session to idle (the real `attn _hook-state idle` path), then');
    console.log('                      sweeps each comma-separated limit, snapshotting retained RSS at');
    console.log('                      each. active + limit recent panes stay live, the rest tear down.');
    console.log('                      -1 keeps all live (ceiling). e.g. --warm -1,3,2,1,0');
    console.log('  --fill-cmd <cmd>    Before the warm sweep, run this command in every pane (one at a');
    console.log('                      time) to grow each Ghostty WASM heap / atlas, simulating idle');
    console.log('                      panes that have rendered real output. e.g. "seq 1 60000"');
    console.log('  --fill-settle-ms <n>  Per-pane settle after the fill command (default: 3000)');
    console.log('  --reclaim-hold-ms <n> After the warm sweep, hold the torn-down state and sample RSS');
    console.log('                      over this window (no pressure) to capture the reclaim decay');
    console.log('                      curve -- distinguishes soft-but-delayed from hard. Pair with');
    console.log('                      --fill-cmd + --warm <low>.');
    console.log('  --reclaim-hold-interval-ms <n>  Sample interval during the hold (default: 15000)');
    console.log('  --pressure          After the warm sweep, fire WebKit\'s low-memory notification and');
    console.log('                      re-measure. Separates surfaces a live layer owns (survive) from');
    console.log('                      surfaces WebKit is caching for reuse (dropped).');
    console.log('  --churn <rounds>    Open --sessions sessions, close them all, repeat <rounds> times,');
    console.log('                      measuring at rest each round. A rising at-rest line is memory');
    console.log('                      no session owns any more.');
    console.log('  --switch-probe      Walk every session with select_session and screenshot each one');
    console.log('                      after it is revealed. A pane that hands its GPU drawing buffer');
    console.log('                      back while hidden has to paint again on reveal; a blank or');
    console.log('                      stale window in a shot is that repaint missing. Pair with');
    console.log('                      --fill-cmd so each pane holds distinguishable content.');
    console.log('  --close-probe       Close every session after the sweep and re-measure, to see');
    console.log('                      whether full teardown releases what unmounting did not.');
    console.log('  --rss-tolerance-pct <n>  Allowed growth over the per-machine baseline before the');
    console.log('                      verdict fails (default: 15)');
    console.log('  --record-baseline   Overwrite the per-machine baseline with this run\'s RSS instead');
    console.log('                      of comparing against it');
    console.log('');
    console.log('Set ATTN_PPROF=<port> to also capture /debug/vars + heap/CPU pprof.');
    return;
  }

  const startedAt = Date.now();
  const port = pprofPort();
  const { runId, runDir, sessionDir } = createRunContext(options, 'perf-baseline');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const isPerfBaselineLabel = (session) => typeof session.label === 'string' && session.label.startsWith('perf-baseline-');
  const sessionIds = [];
  // Warm-workspace limit captured before the sweep perturbs it, restored in the
  // finally so this run does not leak its last swept value into localStorage.
  let initialWarmLimit = null;
  // Populated once the headline RSS is compared against the machine registry
  // (below); read again after the finally block to emit the ATTN_VERDICT line.
  let rssEvaluation = null;

  const summary = {
    ok: false,
    runId,
    runDir,
    sessions: options.sessions,
    warm: options.warm,
    warmSweep: null,
    reclaimHold: null,
    requestedStream: options.stream,
    chunkBytes: options.chunkBytes,
    chunkCount: options.chunkCount,
    pprofPort: port,
    appPid: null,
    daemonPid: null,
    diagUp: false,
    snapshots: {},
    vars: {},
    profiles: {},
  };

  try {
    if (port && options.restartDaemon) {
      const killed = await stopDaemon('dev');
      console.log(`[perf] stopped dev daemon pid=${killed ?? 'none'} so a fresh one inherits ATTN_PPROF=${port}`);
    }

    // Snapshot WebKit pids before relaunch so we can attribute the new ones to
    // the dev app (and exclude a possibly-running prod app's WebKit).
    const webkitBaseline = await captureWebKitPids();

    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();

    // A pane's GPU surface is sized in device pixels, so every graphics number
    // is only readable next to the window it was measured at. The harness
    // launches at Tauri's 1200x800 default, which is far smaller than a real
    // window and understates the per-pane surface accordingly.
    if (options.window) {
      const match = /^(\d+)x(\d+)$/.exec(options.window);
      if (!match) throw new Error(`--window expects WxH (e.g. 1728x1080), got: ${options.window}`);
      const current = await getFrontWindowBounds(client.bundleId, { client });
      await setFrontWindowBounds(
        { x: current?.x ?? 0, y: current?.y ?? 0, width: Number(match[1]), height: Number(match[2]) },
        { client },
      );
      await delay(1500);
    }
    summary.window = await getFrontWindowBounds(client.bundleId, { client }).catch(() => null);
    console.log(`[perf] window bounds: ${summary.window ? `${summary.window.width}x${summary.window.height}` : 'unknown'}`);

    // Clear detritus from prior runs. Sessions persist in the daemon's SQLite
    // store and are restored across daemon restarts, so a fresh daemon can still
    // surface stale perf-baseline sessions (with dead workers).
    const stale = [...observer.sessionsById.values()].filter(isPerfBaselineLabel);
    if (stale.length > 0) {
      console.log(`[perf] closing ${stale.length} stale perf-baseline session(s) from prior runs`);
      await closeSessions(client, stale.map((session) => session.id));
      await waitForSessionsGone(observer, isPerfBaselineLabel, 20_000);
    }
    await client.waitForFrontendResponsive(20_000);

    const manifest = client.readManifest();
    const appPid = manifest.pid;
    summary.appPid = appPid;

    let daemonPid = null;
    if (port) {
      for (let i = 0; i < 25; i += 1) {
        try {
          const vars = await httpGetJson(port, '/debug/vars');
          summary.diagUp = true;
          summary.vars.before = vars;
          daemonPid = vars.pid;
          break;
        } catch {
          await delay(400);
        }
      }
      if (summary.diagUp) {
        console.log(`[perf] diag endpoint live on :${port} daemonPid=${daemonPid} backend=${summary.vars.before.pty_backend}`);
      } else {
        console.warn(`[perf] diag endpoint not reachable on :${port}; continuing with ps-tree memory only`);
      }
    }

    // Default path (and pprof fallback): resolve the daemon pid from the profile
    // pid file so daemon + pty-worker RSS are always attributed -- not only when
    // ATTN_PPROF is set. The daemon is detached/reparented, so its workers are
    // NOT descendants of the app pid; without a daemon pid the headline silently
    // reports daemon: 0 / ptyWorkers: 0 on the documented default run.
    if (!daemonPid) {
      daemonPid = readLiveDaemonPid(profileForAppPath(options.appPath));
      if (daemonPid) {
        console.log(`[perf] resolved daemon pid=${daemonPid} from pid file (daemon + pty-workers included)`);
      } else {
        console.warn('[perf] WARNING: no live daemon pid file found; daemon + pty-worker RSS are NOT included in this baseline (app-tree + WebKit numbers remain valid)');
      }
    }
    summary.daemonPid = daemonPid;
    summary.daemonPidSource = daemonPid ? (summary.diagUp ? 'debug-vars' : 'pid-file') : 'none';

    summary.snapshots.empty = await snapshot(appPid, daemonPid, webkitBaseline);
    // The app's own floor, in the accounting the user sees. Everything else in
    // the tree -- daemon, pty-workers, session shells, agents the daemon spawns
    // -- is a different program, and summing them hides what the UI costs.
    summary.appFootprint = { empty: await readAppFootprint(summary.snapshots.empty) };
    const emptyWc = webContentPid(summary.snapshots.empty);
    summary.emptyWebContent = {
      regions: await readRegionFootprint(emptyWc),
      surfaces: await readGraphicsRegions(emptyWc),
    };
    console.log(
      `[perf] empty snapshot: ${summary.snapshots.empty.totalRssMb} MB rss `
      + `(${summary.snapshots.empty.procCount} procs) | APP FOOTPRINT `
      + `${summary.appFootprint.empty.totalMb} MB`,
    );
    console.log(
      `[perf] empty app webContent: ${JSON.stringify(summary.emptyWebContent.regions?.slices ?? {})} `
      + `footprint=${summary.emptyWebContent.regions?.footprintMb ?? 'n/a'}MB | surfaces=`
      + `${summary.emptyWebContent.surfaces?.largeCount ?? 'n/a'} `
      + JSON.stringify(summary.emptyWebContent.surfaces?.histogram ?? {}),
    );
    console.log(`[perf] empty app by pid: ${JSON.stringify(summary.appFootprint.empty.byPid)}`);

    // Pace creation: wait for each session's initial pane to mount before
    // creating the next, so the frontend main thread is free to reply to the
    // next create_session (firing them back-to-back hangs the bridge while a
    // terminal is mounting).
    for (let i = 0; i < options.sessions; i += 1) {
      const label = `perf-baseline-${runId}-${i}`;
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

    // Optionally grow each pane's WASM heap/atlas with real output before the
    // sweep, so retained-RSS reflects used idle panes (an agent that produced
    // output then went idle) instead of the empty-pane floor. Force all panes
    // LIVE first (the warm limit persists in localStorage across runs, so a fresh
    // app can launch already-virtualized) -- otherwise cold panes would only
    // ingest the daemon's capped replay on rehydrate, not the full live output,
    // and the sweep would compare unequal panes.
    // Capture the warm limit before fill (which forces all panes live) or the
    // sweep cycles through limits. It persists in localStorage, so leaving it
    // changed would bleed into the next run and the user's app; restored in the
    // finally.
    const warmLevels = parseWarmLevels(options.warm);
    if (options.fillCmd || warmLevels) {
      initialWarmLimit = await client
        .request('get_warm_workspace_limit', {}, { timeoutMs: 15_000 })
        .then((result) => (typeof result?.limit === 'number' ? result.limit : null))
        .catch((error) => {
          console.warn(`[perf] get_warm_workspace_limit failed: ${error.message}`);
          return null;
        });
    }

    if (options.fillCmd) {
      await client.request('set_warm_workspace_limit', { limit: -1 }, { timeoutMs: 15_000 })
        .catch((error) => console.warn(`[perf] pre-fill all-live failed: ${error.message}`));
      await delay(options.settleMs);
      await fillAllPanes(client, sessionIds, options.fillCmd, options.fillSettleMs);
      await delay(options.settleMs);
    }

    // Warm-set A/B sweep. The warm-workspace limit (terminal virtualization) only
    // reclaims IDLE workspaces: any workspace with a non-idle session is pinned
    // live so its PTY model can still answer terminal queries. Freshly created
    // shell sessions report `working`, so we first drive every session to `idle`
    // via the real state-report path (`attn _hook-state <id> idle`, exactly what an
    // agent's Stop/state hook does) -- otherwise the warm-set has nothing to tear
    // down. Then sweep the requested limits most-live-first so each step only frees
    // panes (monotonic teardown, never a rehydrate/regrow), giving clean
    // within-app retained-RSS deltas for the per-pane cost.
    if (warmLevels) {
      await markSessionsIdle(client, options, sessionIds);
      summary.warmSweep = [];
      const ordered = [...warmLevels].sort(
        (a, b) => warmLiveCount(b, options.sessions) - warmLiveCount(a, options.sessions),
      );
      for (const limit of ordered) {
        let warmState = null;
        try {
          warmState = await client.request('set_warm_workspace_limit', { limit }, { timeoutMs: 15_000 });
        } catch (error) {
          console.warn(`[perf] set_warm_workspace_limit(${limit}) failed: ${error.message}`);
        }
        await delay(options.settleMs);
        const snap = await snapshot(appPid, daemonPid, webkitBaseline);
        // Per-pane GPU surfaces do not appear as their own class in `ps` RSS, so
        // the sweep reads the WebContent region split too: a warm level that
        // mounts fewer panes should show `graphics` fall with `livePanes`.
        const pid = webContentPid(snap);
        const regions = await readRegionFootprint(pid);
        // How many pane-sized GPU surfaces exist at this warm level. If the
        // count tracks mounted panes, virtualization releases surfaces; if it
        // tracks sessions, something holds them past unmount.
        const surfaces = await readGraphicsRegions(pid);
        const appFootprint = await readAppFootprint(snap);
        const expectedVirtualized = limit < 0 ? 0 : Math.max(0, options.sessions - (limit + 1));
        const entry = {
          warm: limit,
          livePanes: options.sessions - expectedVirtualized,
          virtualizedPanes: warmState?.virtualizedPanes ?? null,
          expectedVirtualized,
          totalRssMb: snap.totalRssMb,
          webContentRssMb: classRssMb(snap, 'webkit_webcontent'),
          gpuRssMb: classRssMb(snap, 'webkit_gpu'),
          appRssMb: classRssMb(snap, 'app'),
          webContentDirtyMb: regions?.slices ?? null,
          graphicsSurfaces: surfaces ?? null,
          appFootprintMb: appFootprint.totalMb,
          appFootprintByPid: appFootprint.byPid,
        };
        summary.warmSweep.push(entry);
        console.log(
          `[perf] warm=${limit}: live=${entry.livePanes}/${options.sessions} `
          + `virtualized=${entry.virtualizedPanes} (expected ${expectedVirtualized}) | `
          + `APP FOOTPRINT ${entry.appFootprintMb}MB | `
          + `total=${entry.totalRssMb}MB webContent=${entry.webContentRssMb}MB gpu=${entry.gpuRssMb}MB`
          + (regions
            ? ` | dirty: graphics=${regions.slices.graphics}MB `
              + `webkitMalloc=${regions.slices.webkitMalloc}MB jsHeap=${regions.slices.jsHeap}MB`
            : ' | dirty: unavailable')
          + (surfaces
            ? ` | paneSizedSurfaces=${surfaces.largeCount} (${surfaces.largeDirtyMb}MB dirty) `
              + JSON.stringify(surfaces.histogram)
            : ''),
        );
        // The per-pane RSS slope is only meaningful if the warm-set actually
        // reached the intended live/virtual split. A mismatch means
        // virtualization did not engage as expected (e.g. a session never went
        // idle), so the retained-RSS deltas would be garbage — fail loudly
        // rather than report invalid perf numbers.
        if (entry.virtualizedPanes !== expectedVirtualized) {
          throw new Error(
            `[perf] warm=${limit}: virtualized ${entry.virtualizedPanes} != expected `
            + `${expectedVirtualized} — warm-set did not reach the intended live/virtual `
            + 'split; retained-RSS deltas would be invalid',
          );
        }
      }
    }

    // Does WebKit hand the pane-sized GPU surfaces back when asked? A surface a
    // live compositing layer still owns survives the low-memory notification; a
    // surface WebKit is merely caching for reuse is dropped by it. The two have
    // different fixes — mount fewer panes vs. cap or defeat the cache — so the
    // sweep alone cannot pick one.
    if (options.pressure) {
      const before = await snapshot(appPid, daemonPid, webkitBaseline);
      const pid = webContentPid(before);
      const beforeRegions = await readRegionFootprint(pid);
      const beforeSurfaces = await readGraphicsRegions(pid);
      await execFileAsync('notifyutil', ['-p', 'org.WebKit.lowMemory']);
      await delay(options.settleMs);
      const after = await snapshot(appPid, daemonPid, webkitBaseline);
      const afterRegions = await readRegionFootprint(pid);
      const afterSurfaces = await readGraphicsRegions(pid);
      summary.pressure = {
        before: {
          totalRssMb: before.totalRssMb,
          byClass: before.byClass,
          webContentDirtyMb: beforeRegions?.slices ?? null,
          graphicsSurfaces: beforeSurfaces ?? null,
        },
        after: {
          totalRssMb: after.totalRssMb,
          byClass: after.byClass,
          webContentDirtyMb: afterRegions?.slices ?? null,
          graphicsSurfaces: afterSurfaces ?? null,
        },
      };
      console.log(
        `[perf] pressure: total ${before.totalRssMb} -> ${after.totalRssMb}MB | `
        + `graphics ${beforeRegions?.slices.graphics ?? 'n/a'} -> ${afterRegions?.slices.graphics ?? 'n/a'}MB | `
        + `paneSizedSurfaces ${beforeSurfaces?.largeCount ?? 'n/a'} -> ${afterSurfaces?.largeCount ?? 'n/a'} `
        + `(${beforeSurfaces?.largeDirtyMb ?? 'n/a'} -> ${afterSurfaces?.largeDirtyMb ?? 'n/a'}MB dirty)`,
      );
    }

    // A dock panel costs a compositing layer only while it is open. Measures
    // both directions -- the layer must be absent when closed and present when
    // open -- and captures the open panel so a memory win that stopped the
    // drawer from rendering cannot pass as a win.
    if (options.dockProbe) {
      const closedPid = webContentPid(await snapshot(appPid, daemonPid, webkitBaseline));
      const closed = await readGraphicsRegions(closedPid);
      await client.request('open_dock_panel', { panelId: options.dockProbe }, { timeoutMs: 15_000 });
      await delay(options.settleMs);
      const openPid = webContentPid(await snapshot(appPid, daemonPid, webkitBaseline));
      const opened = await readGraphicsRegions(openPid);
      await bringWindowForward(client);
      const shot = path.join(runDir, `dock-${options.dockProbe}-open.png`);
      await captureFrontWindowScreenshot(shot).catch((error) => console.warn(`[perf] dock screenshot failed: ${error.message}`));
      summary.dockProbe = { panelId: options.dockProbe, closed, opened, screenshot: shot };
      console.log(
        `[perf] dock ${options.dockProbe}: closed surfaces=${closed?.largeCount ?? 'n/a'} `
        + JSON.stringify(closed?.histogram ?? {})
        + ` -> open surfaces=${opened?.largeCount ?? 'n/a'} `
        + JSON.stringify(opened?.histogram ?? {}) + ` | shot=${shot}`,
      );
    }

    // An off-screen pane hands its GPU drawing buffer back, so being revealed is
    // the moment it owes a repaint. Walk every session and photograph the window
    // right after each switch: the memory win is only a win if the pane the user
    // switched to still shows its own content.
    if (options.switchProbe && sessionIds.length > 0) {
      await bringWindowForward(client);
      summary.switchProbe = [];
      for (let index = 0; index < sessionIds.length; index += 1) {
        const sessionId = sessionIds[index];
        await client.request('select_session', { sessionId }, { timeoutMs: 15_000 });
        await delay(options.settleMs);
        const shot = path.join(runDir, `switch-${index + 1}.png`);
        await captureFrontWindowScreenshot(shot)
          .catch((error) => console.warn(`[perf] switch screenshot failed: ${error.message}`));
        const surfaces = await readGraphicsRegions(webContentPid(await snapshot(appPid, daemonPid, webkitBaseline)));
        summary.switchProbe.push({ sessionId, screenshot: shot, graphicsSurfaces: surfaces ?? null });
        console.log(
          `[perf] switch ${index + 1}/${sessionIds.length}: ${sessionId} | `
          + `paneSizedSurfaces=${surfaces?.largeCount ?? 'n/a'} `
          + JSON.stringify(surfaces?.histogram ?? {}) + ` | shot=${shot}`,
        );
      }
    }

    // What a day of real use does that a single snapshot cannot see: open a set
    // of sessions, close them, and do it again. A round that ends heavier than
    // it started is memory no session owns any more, so nothing will ever
    // release it — the shape that turns a working app into a 1GB one by evening.
    if (options.churnRounds > 0) {
      summary.churn = [];
      for (let round = 0; round < options.churnRounds; round += 1) {
        const roundIds = [];
        for (let i = 0; i < options.sessions; i += 1) {
          roundIds.push(await createSessionAndWaitForInitialPane({
            client,
            observer,
            cwd: sessionDir,
            label: `perf-churn-${runId}-${round}-${i}`,
            agent: 'shell',
            sessionWaitMs: 30_000,
            waitForInitialPaneVisible: true,
            initialPaneWaitMs: 25_000,
          }));
        }
        if (options.fillCmd) await fillAllPanes(client, roundIds, options.fillCmd, options.fillSettleMs);
        await delay(options.settleMs);
        const peak = await snapshot(appPid, daemonPid, webkitBaseline);
        const peakRegions = await readRegionFootprint(webContentPid(peak));
        await closeSessions(client, roundIds);
        await delay(options.settleMs);
        const rest = await snapshot(appPid, daemonPid, webkitBaseline);
        const pid = webContentPid(rest);
        const restRegions = await readRegionFootprint(pid);
        const restSurfaces = await readGraphicsRegions(pid);
        summary.churn.push({
          round,
          peak: { totalRssMb: peak.totalRssMb, webContentDirtyMb: peakRegions?.slices ?? null },
          rest: {
            totalRssMb: rest.totalRssMb,
            webContentDirtyMb: restRegions?.slices ?? null,
            graphicsSurfaces: restSurfaces ?? null,
          },
        });
        console.log(
          `[perf] churn round ${round + 1}/${options.churnRounds}: `
          + `at rest (0 sessions) graphics=${restRegions?.slices.graphics ?? 'n/a'}MB `
          + `webkitMalloc=${restRegions?.slices.webkitMalloc ?? 'n/a'}MB `
          + `jsHeap=${restRegions?.slices.jsHeap ?? 'n/a'}MB `
          + `total=${rest.totalRssMb}MB | peak webkitMalloc=${peakRegions?.slices.webkitMalloc ?? 'n/a'}MB`,
        );
      }
    }

    // Full teardown, not just unmount: close every session and re-measure. If
    // closing releases the pane-sized surfaces that virtualization did not, the
    // retention is attn's to fix; if it does not, WebKit is caching them past
    // any lifetime attn controls and only memory pressure returns them.
    if (options.closeProbe) {
      const before = await snapshot(appPid, daemonPid, webkitBaseline);
      const pid = webContentPid(before);
      const beforeRegions = await readRegionFootprint(pid);
      const beforeSurfaces = await readGraphicsRegions(pid);
      await closeSessions(client, sessionIds);
      sessionIds.length = 0;
      await delay(options.settleMs);
      const after = await snapshot(appPid, daemonPid, webkitBaseline);
      const afterRegions = await readRegionFootprint(pid);
      const afterSurfaces = await readGraphicsRegions(pid);
      summary.closeProbe = {
        before: {
          webContentDirtyMb: beforeRegions?.slices ?? null,
          graphicsSurfaces: beforeSurfaces ?? null,
        },
        after: {
          webContentDirtyMb: afterRegions?.slices ?? null,
          graphicsSurfaces: afterSurfaces ?? null,
        },
      };
      console.log(
        `[perf] close-all: graphics ${beforeRegions?.slices.graphics ?? 'n/a'} -> `
        + `${afterRegions?.slices.graphics ?? 'n/a'}MB | webkitMalloc `
        + `${beforeRegions?.slices.webkitMalloc ?? 'n/a'} -> ${afterRegions?.slices.webkitMalloc ?? 'n/a'}MB | `
        + `jsHeap ${beforeRegions?.slices.jsHeap ?? 'n/a'} -> ${afterRegions?.slices.jsHeap ?? 'n/a'}MB | `
        + `paneSizedSurfaces ${beforeSurfaces?.largeCount ?? 'n/a'} -> ${afterSurfaces?.largeCount ?? 'n/a'}`,
      );
    }

    // Reclaim decay sampler (no pressure, no GC nudge). Hold the torn-down
    // (most-virtualized) state and sample retained RSS over time. WebKit's
    // scavenger / periodic memory monitor reclaims freed WASM + heap on a delay
    // that is LONGER than a normal settle window, so a single short-settle
    // snapshot can read "teardown reclaimed nothing" when the memory does come
    // back given ~30-120s of quiet. This records the decay curve so we can tell
    // soft-but-delayed (declines on its own) from hard (flat forever). Sessions
    // stay open + idle so this measures warm-set teardown specifically.
    if (options.reclaimHoldMs > 0) {
      summary.reclaimHold = [];
      const holdStart = Date.now();
      let elapsed = 0;
      for (;;) {
        const snap = await snapshot(appPid, daemonPid, webkitBaseline);
        const regions = await readRegionFootprint(webContentPid(snap));
        const entry = {
          tMs: elapsed,
          totalRssMb: snap.totalRssMb,
          webContentRssMb: classRssMb(snap, 'webkit_webcontent'),
          gpuRssMb: classRssMb(snap, 'webkit_gpu'),
          webContentDirtyMb: regions?.slices ?? null,
        };
        summary.reclaimHold.push(entry);
        console.log(
          `[perf] HOLD t=${Math.round(elapsed / 1000)}s: webContent=${entry.webContentRssMb}MB `
          + `gpu=${entry.gpuRssMb}MB total=${entry.totalRssMb}MB`
          + (regions ? ` gfxDirty=${regions.slices.graphics}MB mallocDirty=${regions.slices.webkitMalloc}MB` : ''),
        );
        if (elapsed >= options.reclaimHoldMs) break;
        await delay(Math.min(options.reclaimHoldIntervalMs, options.reclaimHoldMs - elapsed));
        elapsed = Date.now() - holdStart;
      }
      const first = summary.reclaimHold[0];
      const last = summary.reclaimHold[summary.reclaimHold.length - 1];
      console.log(
        `[perf] HOLD DECAY over ${Math.round(last.tMs / 1000)}s: webContent ${first.webContentRssMb}->${last.webContentRssMb}MB `
        + `(${Number((last.webContentRssMb - first.webContentRssMb).toFixed(1))}MB), `
        + `total ${first.totalRssMb}->${last.totalRssMb}MB (${Number((last.totalRssMb - first.totalRssMb).toFixed(1))}MB)`,
      );
    }

    if (summary.diagUp) summary.vars.idle = await httpGetJson(port, '/debug/vars').catch(() => null);
    summary.snapshots.idle = await snapshot(appPid, daemonPid, webkitBaseline);
    const workerCount = summary.vars.idle ? Object.keys(summary.vars.idle.worker_pids || {}).length : null;
    console.log(`[perf] IDLE @ ${options.sessions} sessions: ${summary.snapshots.idle.totalRssMb} MB total; workers=${workerCount ?? 'n/a'}`);

    const streamN = Math.min(options.stream, sessionIds.length);
    if (options.realCmd && streamN > 0) {
      // Realistic high-output repro through the NORMAL pty path (not the
      // synthetic benchmark_pty_transport): run a heavy command in each stream
      // target and sample peak + post-settle RSS. This is the before/after used
      // to measure frontend-memory fixes.
      const targets = [];
      for (let i = 0; i < streamN; i += 1) {
        const sid = sessionIds[i];
        const paneId = await paneIdForSession(client, sid);
        if (!paneId) continue;
        targets.push({ sid, paneId });
        await client.request('write_pane', { sessionId: sid, paneId, text: options.realCmd }, { timeoutMs: 15_000 })
          .catch((error) => console.warn(`[perf] write_pane ${sid} failed: ${error.message}`));
      }
      console.log(`[perf] ran "${options.realCmd}" in ${targets.length} pane(s); sampling ${options.realWindowMs}ms`);
      const win = await sampleWindow(appPid, daemonPid, webkitBaseline, options.realWindowMs);
      summary.snapshots.realPeak = win.peak;
      await delay(3000);
      summary.snapshots.realPost = await snapshot(appPid, daemonPid, webkitBaseline);
      if (summary.diagUp) summary.vars.realPost = await httpGetJson(port, '/debug/vars').catch(() => null);
      console.log(`[perf] REAL-OUTPUT peak=${win.peak.totalRssMb} MB  post-settle=${summary.snapshots.realPost.totalRssMb} MB  (idle was ${summary.snapshots.idle.totalRssMb} MB)`);
    } else if (summary.diagUp && streamN > 0) {
      // Best-effort synthetic streaming + CPU profile. Never fails the measurement.
      try {
        const targets = [];
        for (let i = 0; i < streamN; i += 1) {
          const sid = sessionIds[i];
          const paneId = await paneIdForSession(client, sid);
          if (paneId) targets.push({ sid, paneId });
        }
        if (targets.length > 0) {
          console.log(`[perf] capturing ${options.cpuSeconds}s CPU profile under ${targets.length}-session stream`);
          const cpuPromise = httpGetToFile(port, `/debug/pprof/profile?seconds=${options.cpuSeconds}`, path.join(runDir, 'cpu.pb.gz'), (options.cpuSeconds + 15) * 1000);
          const burst = targets.map(({ sid, paneId }) => (async () => {
            const deadline = Date.now() + options.cpuSeconds * 1000;
            while (Date.now() < deadline) {
              await streamBurst(client, sid, paneId, options).catch(() => {});
            }
          })());
          summary.profiles.cpu = await cpuPromise.catch((error) => { console.warn(`[perf] cpu profile failed: ${error.message}`); return null; });
          await Promise.all(burst).catch(() => {});
          summary.snapshots.active = await snapshot(appPid, daemonPid, webkitBaseline);
          console.log(`[perf] ACTIVE (under stream): ${summary.snapshots.active.totalRssMb} MB total`);
        }
      } catch (error) {
        console.warn(`[perf] streaming/CPU phase skipped: ${error.message}`);
      }
    }

    if (summary.diagUp) {
      summary.profiles.heap = await httpGetToFile(port, '/debug/pprof/heap', path.join(runDir, 'heap.pb.gz')).catch((error) => { console.warn(`[perf] heap profile failed: ${error.message}`); return null; });
      summary.vars.post = await httpGetJson(port, '/debug/vars').catch(() => null);
    }
    summary.snapshots.post = await snapshot(appPid, daemonPid, webkitBaseline);

    summary.ok = true;
    const idle = summary.snapshots.idle;
    summary.headline = {
      sessions: options.sessions,
      totalRssMb: idle.totalRssMb,
      app: classRssMb(idle, 'app'),
      webkit: ['webkit_webcontent', 'webkit_gpu', 'webkit_networking'].reduce((sum, k) => sum + classRssMb(idle, k), 0),
      daemon: classRssMb(idle, 'daemon'),
      ptyWorkers: classRssMb(idle, 'pty_worker'),
      ptyWorkerCount: idle.byClass.pty_worker?.count ?? 0,
      perWorkerAvgMb: idle.byClass.pty_worker?.count ? Number((classRssMb(idle, 'pty_worker') / idle.byClass.pty_worker.count).toFixed(1)) : 0,
    };
    if (summary.snapshots.realPeak) {
      const peak = summary.snapshots.realPeak;
      const post = summary.snapshots.realPost;
      summary.headline.realOutput = {
        cmd: options.realCmd,
        idleWebContentMb: classRssMb(idle, 'webkit_webcontent'),
        peakTotalMb: peak.totalRssMb,
        peakWebContentMb: classRssMb(peak, 'webkit_webcontent'),
        postTotalMb: post?.totalRssMb ?? null,
        postWebContentMb: post ? classRssMb(post, 'webkit_webcontent') : null,
        retainedMb: post ? Number((post.totalRssMb - idle.totalRssMb).toFixed(1)) : null,
      };
    }
    if (summary.warmSweep && summary.warmSweep.length > 0) {
      // Per-pane cost = how much retained RSS each extra live (warm) pane holds,
      // derived from the slope across the swept levels (max-live minus min-live,
      // divided by the difference in live panes). This is the lever for the warm
      // default: it is the marginal memory each warm slot costs.
      const sweep = summary.warmSweep;
      const most = sweep[0];
      const least = sweep[sweep.length - 1];
      const paneSpan = most.livePanes - least.livePanes;
      summary.headline.warmSweep = {
        levels: sweep.map((entry) => ({
          warm: entry.warm,
          livePanes: entry.livePanes,
          virtualizedPanes: entry.virtualizedPanes,
          totalRssMb: entry.totalRssMb,
          webContentRssMb: entry.webContentRssMb,
          gpuRssMb: entry.gpuRssMb,
          webContentDirtyMb: entry.webContentDirtyMb,
        })),
        perLivePaneTotalMb: paneSpan > 0 ? Number(((most.totalRssMb - least.totalRssMb) / paneSpan).toFixed(1)) : null,
        perLivePaneWebContentMb: paneSpan > 0 ? Number(((most.webContentRssMb - least.webContentRssMb) / paneSpan).toFixed(1)) : null,
        // The per-pane GPU surface: what release-on-hide could actually reclaim.
        perLivePaneGraphicsMb: paneSpan > 0 && most.webContentDirtyMb && least.webContentDirtyMb
          ? Number(((most.webContentDirtyMb.graphics - least.webContentDirtyMb.graphics) / paneSpan).toFixed(1))
          : null,
      };
    }

    // Compare this run's headline RSS against the per-machine registry (see
    // machineRegistry.mjs) and record what the verdict line below reports.
    // This never affects summary.ok / the process exit code -- an RSS
    // regression is a trend signal for a driving agent to notice, not a
    // harness error.
    const fingerprint = getMachineFingerprint();
    const baseline = loadBaseline(fingerprint.key);
    rssEvaluation = evaluateRssBaseline({
      totalRssMb: summary.headline.totalRssMb,
      fingerprint,
      baseline,
      tolerancePct: options.rssTolerancePct,
      record: options.recordBaseline,
      recordedAt: new Date().toISOString(),
    });
    recordOrCompareBaseline({ evaluation: rssEvaluation, key: fingerprint.key });
    summary.baselineComparison = rssEvaluation.comparison;
  } finally {
    // Close every session we created so they don't persist into the next run.
    await closeSessions(client, sessionIds);
    // Restore the warm limit captured before the sweep so we don't leak this
    // run's last swept value into localStorage (next run / the user's app).
    if (initialWarmLimit !== null) {
      await client
        .request('set_warm_workspace_limit', { limit: initialWarmLimit }, { timeoutMs: 15_000 })
        .catch((error) => console.warn(`[perf] restore warm limit failed: ${error.message}`));
    }
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    await observer.close();
  }

  if (summary.headline?.warmSweep) {
    const { levels, perLivePaneTotalMb, perLivePaneWebContentMb } = summary.headline.warmSweep;
    console.log(`\n[perf] WARM-SET A/B (${options.sessions} idle sessions)`);
    console.log('  warm  live  virt  total(MB)  webContent(MB)  gpu(MB)  gfxDirty(MB)  mallocDirty(MB)');
    for (const lvl of levels) {
      const warmCol = String(lvl.warm).padStart(4);
      const liveCol = String(lvl.livePanes).padStart(4);
      const virtCol = String(lvl.virtualizedPanes).padStart(4);
      const totalCol = String(lvl.totalRssMb).padStart(9);
      const wcCol = String(lvl.webContentRssMb).padStart(14);
      const gpuCol = String(lvl.gpuRssMb).padStart(7);
      const gfxCol = String(lvl.webContentDirtyMb?.graphics ?? 'n/a').padStart(12);
      const mallocCol = String(lvl.webContentDirtyMb?.webkitMalloc ?? 'n/a').padStart(15);
      console.log(`  ${warmCol}  ${liveCol}  ${virtCol}  ${totalCol}  ${wcCol}  ${gpuCol}  ${gfxCol}  ${mallocCol}`);
    }
    console.log(`  per-live-pane: total ${perLivePaneTotalMb ?? 'n/a'} MB, webContent ${perLivePaneWebContentMb ?? 'n/a'} MB`);
    // The A/B this table exists for: graphics dirty is the per-pane GPU surface.
    // If it does not fall with livePanes, invisible panes are not holding it and
    // the release-on-hide hypothesis is wrong.
    const withGfx = levels.filter((lvl) => lvl.webContentDirtyMb?.graphics != null);
    if (withGfx.length >= 2) {
      const first = withGfx[0];
      const last = withGfx[withGfx.length - 1];
      const paneDelta = first.livePanes - last.livePanes;
      const gfxDelta = first.webContentDirtyMb.graphics - last.webContentDirtyMb.graphics;
      console.log(
        `  graphics dirty: ${first.webContentDirtyMb.graphics} MB @ ${first.livePanes} live `
        + `-> ${last.webContentDirtyMb.graphics} MB @ ${last.livePanes} live `
        + `(${gfxDelta >= 0 ? '-' : '+'}${Math.abs(gfxDelta).toFixed(1)} MB`
        + (paneDelta > 0 ? `, ${(gfxDelta / paneDelta).toFixed(1)} MB per pane` : '') + ')',
      );
    }
  }

  console.log(JSON.stringify({ headline: summary.headline, reclaimHold: summary.reclaimHold, idleByClass: summary.snapshots.idle?.byClass, profiles: summary.profiles, runDir }, null, 2));

  // A regression against the machine baseline is a trend signal, not a
  // harness error: it surfaces as verdict.ok:false but never sets a non-zero
  // exit code (see the try/finally above -- only real errors do that, via
  // main().catch below).
  if (rssEvaluation) {
    emitVerdict(buildBaselineVerdict({
      ok: rssEvaluation.ok,
      comparison: rssEvaluation.comparison,
      scenarioId: 'perf-baseline',
      runId,
      artifactsDir: runDir,
      summaryPath: path.join(runDir, 'summary.json'),
      durationMs: Date.now() - startedAt,
      extraMetrics: summary.headline?.realOutput?.retainedMb != null
        ? { retainedMb: summary.headline.realOutput.retainedMb }
        : {},
    }));
  }
}

main().catch((error) => {
  console.error('[perf] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
