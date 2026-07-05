// Shared measurement + lifecycle primitives for the perf scenarios
// (scenario-perf-baseline.mjs, scenario-perf-cold-warm.mjs). Side-effect-free:
// importing this module runs nothing.

import fs from 'node:fs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { daemonPidFilePathForProfile, dataDirForProfile } from './harnessProfile.mjs';

const execFileAsync = promisify(execFile);
export const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

export async function readProcessTable() {
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

export function collectDescendantPids(processes, rootPid) {
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
export function isRelevantWebKitProcess(proc) {
  return proc.command.includes('com.apple.WebKit.WebContent')
    || proc.command.includes('com.apple.WebKit.Networking')
    || proc.command.includes('com.apple.WebKit.GPU');
}

export async function captureWebKitPids() {
  const table = await readProcessTable();
  return new Set(table.filter(isRelevantWebKitProcess).map((proc) => proc.pid));
}

export function classify(proc) {
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
export async function snapshot(appPid, daemonPid, webkitBaseline = new Set(), extraPids = []) {
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

export function classRssMb(snap, label) {
  return snap?.byClass?.[label]?.rssMb ?? 0;
}

// Sample RSS repeatedly over a window and return the peak (by total RSS) and the
// last sample. Used to catch the transient/retained spike from heavy output.
export async function sampleWindow(appPid, daemonPid, webkitBaseline, windowMs, intervalMs = 1000) {
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
export function readLiveDaemonPid(profile) {
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

// Stop the detached daemon for the given profile via its pid file. Only ever
// touches ~/.attn-<profile> (or ~/.attn for prod), never another profile's.
export async function stopDaemon(profile) {
  const pid = readLiveDaemonPid(profile);
  if (pid == null) return null;
  try { process.kill(pid, 'SIGTERM'); } catch { return null; }
  for (let i = 0; i < 50; i += 1) {
    try { process.kill(pid, 0); } catch { return pid; }
    await delay(200);
  }
  try { process.kill(pid, 'SIGKILL'); } catch {}
  return pid;
}

// Tear a NON-PROD profile down to empty on-disk state: quit its app (so the app
// cannot auto-respawn the daemon after we kill it), stop the detached daemon,
// then wipe its data dir (SQLite store, pid file, socket, worker logs). Does NOT
// relaunch — the caller captures a WebKit-pid baseline (captureWebKitPids) after
// teardown and before launchFreshApp, exactly like scenario-perf-baseline does.
// Hard-refuses prod: wiping ~/.attn would destroy the user's real data.
export async function teardownProfileState({ client, profile, wipe = true }) {
  if (!profile || profile === 'default') {
    throw new Error(`teardownProfileState refuses an empty/prod profile (got ${JSON.stringify(profile)})`);
  }
  const dataDir = dataDirForProfile(profile);
  if (dataDir === dataDirForProfile('')) {
    throw new Error(`teardownProfileState refuses to wipe the prod data dir ${dataDir}`);
  }
  await client.quitApp();
  await stopDaemon(profile);
  if (wipe) {
    try { fs.rmSync(dataDir, { recursive: true, force: true }); } catch {}
  }
}

export async function paneIdForSession(client, sessionId) {
  const ws = await client.request('get_workspace', { sessionId }, { timeoutMs: 10_000 });
  return ws.activePaneId || ws.panes?.[0]?.paneId || null;
}

// Close sessions through the automation bridge (the daemon-level close). The
// observer's WS `unregister` is rejected without the workspace_sessions
// capability, so close_session is the supported cleanup path.
export async function closeSessions(client, ids) {
  for (const sessionId of ids) {
    await client.request('close_session', { sessionId }, { timeoutMs: 15_000 }).catch(() => {});
  }
}

// Run `cmd` in every pane, one at a time, to grow each Ghostty WASM heap + atlas
// so the warm-set sweep measures realistic (used) idle panes rather than the
// empty floor. Sequential with a per-pane settle to avoid overrunning the
// websocket's 256-message buffer (see AGENTS.md) by flooding all panes at once.
export async function fillAllPanes(client, sessionIds, cmd, perPaneSettleMs) {
  let filled = 0;
  for (const sessionId of sessionIds) {
    const paneId = await paneIdForSession(client, sessionId);
    if (!paneId) {
      console.warn(`[perf] fill: no pane for session ${sessionId}`);
      continue;
    }
    await client.request('write_pane', { sessionId, paneId, text: cmd }, { timeoutMs: 30_000 })
      .catch((error) => console.warn(`[perf] fill write_pane ${sessionId} failed: ${error.message}`));
    filled += 1;
    await delay(perPaneSettleMs);
  }
  console.log(`[perf] filled ${filled}/${sessionIds.length} panes with \`${cmd}\``);
}
