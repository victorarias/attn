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
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, createSessionAndWaitForInitialPane, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';
import { daemonPidFilePathForProfile, profileForAppPath } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

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
    else passthrough.push(arg);
  }
  const options = parseCommonArgs(passthrough);
  return Object.assign(options, extras);
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

async function readProcessTable() {
  const { stdout } = await execFileAsync('ps', ['-axo', 'pid=,ppid=,%cpu=,rss=,comm=,command=']);
  return stdout
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const match = line.match(/^(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(\S+)\s+(.*)$/);
      if (!match) return null;
      return {
        pid: Number(match[1]),
        ppid: Number(match[2]),
        cpuPct: Number(match[3]),
        rssKb: Number(match[4]),
        comm: match[5],
        command: match[6],
      };
    })
    .filter(Boolean);
}

function collectDescendantPids(processes, rootPid) {
  const childrenByParent = new Map();
  for (const proc of processes) {
    const siblings = childrenByParent.get(proc.ppid) || [];
    siblings.push(proc.pid);
    childrenByParent.set(proc.ppid, siblings);
  }
  const visited = new Set([rootPid]);
  const queue = [rootPid];
  while (queue.length > 0) {
    const pid = queue.shift();
    for (const childPid of childrenByParent.get(pid) || []) {
      if (visited.has(childPid)) continue;
      visited.add(childPid);
      queue.push(childPid);
    }
  }
  return visited;
}

// WebKit content/GPU/networking processes are reparented to launchd, so they
// are NOT descendants of the app pid and must be matched by command.
function isRelevantWebKitProcess(proc) {
  return proc.command.includes('com.apple.WebKit.WebContent')
    || proc.command.includes('com.apple.WebKit.Networking')
    || proc.command.includes('com.apple.WebKit.GPU');
}

async function captureWebKitPids() {
  const table = await readProcessTable();
  return new Set(table.filter(isRelevantWebKitProcess).map((proc) => proc.pid));
}

function classify(proc) {
  const command = proc.command;
  if (command.includes('pty-worker')) return 'pty_worker';
  if (command.includes('attn daemon')) return 'daemon';
  if (command.includes('/Contents/MacOS/app')) return 'app';
  if (command.includes('com.apple.WebKit.WebContent')) return 'webkit_webcontent';
  if (command.includes('com.apple.WebKit.Networking')) return 'webkit_networking';
  if (command.includes('com.apple.WebKit.GPU')) return 'webkit_gpu';
  // Login shell spawned inside each pty-worker (the session's own workload, not
  // attn overhead). Track it separately so per-session attn cost stays clear.
  if (/\/(fish|zsh|bash|dash|tcsh|ksh)( |$)|\/sh( |$)/.test(command)) return 'shell';
  return proc.comm;
}

// Snapshot the RSS of the dev app tree: descendants of the app pid (WebKit) plus
// descendants of the daemon pid (one pty-worker per session) plus any explicitly
// known pids. Targeting these specific roots isolates the dev tree from a
// possibly-running prod daemon (both share the `attn daemon` command string).
async function snapshot(appPid, daemonPid, webkitBaseline = new Set(), extraPids = []) {
  const table = await readProcessTable();
  const pidSet = new Set();
  for (const pid of collectDescendantPids(table, appPid)) pidSet.add(pid);
  if (daemonPid) for (const pid of collectDescendantPids(table, daemonPid)) pidSet.add(pid);
  // Attribute any WebKit process that appeared after the pre-launch baseline to
  // this app (they reparent to launchd, so a tree walk misses them). This is
  // where the WS-1 atlas canvas + GPU texture memory lives.
  for (const proc of table) {
    if (isRelevantWebKitProcess(proc) && !webkitBaseline.has(proc.pid)) pidSet.add(proc.pid);
  }
  for (const pid of extraPids) pidSet.add(pid);

  const procs = table.filter((proc) => pidSet.has(proc.pid));
  const byClass = {};
  let totalRssKb = 0;
  for (const proc of procs) {
    const label = classify(proc);
    const entry = byClass[label] || { count: 0, rssKb: 0, rssMaxKb: 0, pids: [] };
    entry.count += 1;
    entry.rssKb += proc.rssKb;
    entry.rssMaxKb = Math.max(entry.rssMaxKb, proc.rssKb);
    entry.pids.push({ pid: proc.pid, rssKb: proc.rssKb });
    byClass[label] = entry;
    totalRssKb += proc.rssKb;
  }
  return {
    totalRssMb: Number((totalRssKb / 1024).toFixed(1)),
    procCount: procs.length,
    byClass: Object.fromEntries(
      Object.entries(byClass).map(([label, entry]) => [label, {
        count: entry.count,
        rssMb: Number((entry.rssKb / 1024).toFixed(1)),
        rssMaxMb: Number((entry.rssMaxKb / 1024).toFixed(1)),
        pids: entry.pids,
      }]),
    ),
  };
}

function classRssMb(snap, label) {
  return snap?.byClass?.[label]?.rssMb ?? 0;
}

// Sample RSS repeatedly over a window and return the peak (by total RSS) and the
// last sample. Used to catch the transient/retained spike from heavy output.
async function sampleWindow(appPid, daemonPid, webkitBaseline, windowMs, intervalMs = 1000) {
  const samples = [];
  const deadline = Date.now() + windowMs;
  while (Date.now() < deadline) {
    samples.push(await snapshot(appPid, daemonPid, webkitBaseline));
    await delay(intervalMs);
  }
  if (samples.length === 0) samples.push(await snapshot(appPid, daemonPid, webkitBaseline));
  const peak = samples.reduce((best, current) => (current.totalRssMb > best.totalRssMb ? current : best), samples[0]);
  return { peak, last: samples[samples.length - 1], count: samples.length };
}

// Read the authoritative daemon pid from the profile's pid file, returning it
// only if that process is still alive. This is pprof-independent: it is how the
// default (non-ATTN_PPROF) baseline still attributes daemon + pty-worker RSS,
// since the detached daemon and its workers are not descendants of the app pid.
function readLiveDaemonPid(profile) {
  let pid = null;
  try {
    pid = Number(fs.readFileSync(daemonPidFilePathForProfile(profile), 'utf8').trim());
  } catch {
    return null;
  }
  if (!Number.isInteger(pid) || pid <= 0) return null;
  try { process.kill(pid, 0); } catch { return null; } // stale pid file
  return pid;
}

async function stopDevDaemon() {
  // Only ever touches the dev profile pid file (~/.attn-dev), never prod (~/.attn).
  const pid = readLiveDaemonPid('dev');
  if (pid == null) return null;
  try { process.kill(pid, 'SIGTERM'); } catch { return null; }
  for (let i = 0; i < 50; i += 1) {
    try { process.kill(pid, 0); } catch { return pid; }
    await delay(200);
  }
  try { process.kill(pid, 'SIGKILL'); } catch {}
  return pid;
}

async function paneIdForSession(client, sessionId) {
  const ws = await client.request('get_workspace', { sessionId }, { timeoutMs: 10_000 });
  return ws.activePaneId || ws.panes?.[0]?.paneId || null;
}

// Close sessions through the automation bridge (the daemon-level close). The
// observer's WS `unregister` is rejected without the workspace_sessions
// capability, so close_session is the supported cleanup path.
async function closeSessions(client, ids) {
  for (const sessionId of ids) {
    await client.request('close_session', { sessionId }, { timeoutMs: 15_000 }).catch(() => {});
  }
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
    console.log('');
    console.log('Set ATTN_PPROF=<port> to also capture /debug/vars + heap/CPU pprof.');
    return;
  }

  const port = pprofPort();
  const { runId, runDir, sessionDir } = createRunContext(options, 'perf-baseline');
  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });
  const isPerfBaselineLabel = (session) => typeof session.label === 'string' && session.label.startsWith('perf-baseline-');
  const sessionIds = [];

  const summary = {
    ok: false,
    runId,
    runDir,
    sessions: options.sessions,
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
      const killed = await stopDevDaemon();
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
    console.log(`[perf] empty snapshot: ${summary.snapshots.empty.totalRssMb} MB (${summary.snapshots.empty.procCount} procs)`);

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
  } finally {
    // Close every session we created so they don't persist into the next run.
    await closeSessions(client, sessionIds);
    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    await observer.close();
  }

  console.log(JSON.stringify({ headline: summary.headline, idleByClass: summary.snapshots.idle?.byClass, profiles: summary.profiles, runDir }, null, 2));
}

main().catch((error) => {
  console.error('[perf] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
