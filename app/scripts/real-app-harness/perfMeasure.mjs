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

// Region types worth naming in a memory receipt, and why each one is here.
// `ps` RSS sums these into one number, so a change that moves memory between
// them — releasing a GPU surface, bounding allocator churn — is invisible
// without the split.
const REGION_SLICES = {
  // Per-pane GPU surfaces: the WebGL drawing buffer and glyph atlas of every
  // MOUNTED terminal, visible or not. Owned by this process, mapped in the GPU
  // process, so it lands here rather than in any malloc zone.
  graphics: ['owned unmapped (graphics)', 'VM_ALLOCATE (graphics)'],
  // bmalloc: WebKit's C++ allocator. Ingestion-path churn high-water lives here,
  // NOT in the JS heap, and the scavenger returns it lazily.
  webkitMalloc: ['WebKit Malloc', 'WebKit Malloc metadata'],
  // The JS object heap proper. Measured small on attn (~7 MB) — kept so a claim
  // of "JS leak" can be checked rather than assumed.
  jsHeap: ['JS VM Gigacage', 'JS JIT generated code'],
  // The wasm linear memory of every Ghostty model plus other large buffers.
  malloc: ['MALLOC_LARGE', 'MALLOC_SMALL', 'MALLOC_TINY'],
};

function parseVmmapSize(token) {
  const match = /^([\d.]+)([KMGT])?$/.exec(token);
  if (!match) return null;
  const value = Number(match[1]);
  if (!Number.isFinite(value)) return null;
  const scale = { K: 1 / 1024, M: 1, G: 1024, T: 1024 * 1024 };
  return match[2] ? value * scale[match[2]] : value / (1024 * 1024);
}

// Locates the measurement window in a summary row: 7 size columns
// (VIRTUAL/RESIDENT/DIRTY/SWAPPED/VOLATILE/NONVOL/EMPTY) followed by an integer
// REGION COUNT. Neither end of the row can be trusted as an anchor — the name
// may end in a bare number ("Memory Tag 241") and the row may carry trailing
// prose ("see MALLOC ZONE table below"), so the window is found rather than
// counted from an edge. Returns the index the sizes start at, or -1.
function findSizeWindow(columns) {
  for (let start = 1; start + 7 < columns.length + 1; start += 1) {
    if (!/^\d+$/.test(columns[start + 7] ?? '')) continue;
    const sizes = columns.slice(start, start + 7).map(parseVmmapSize);
    if (sizes.length === 7 && sizes.every((size) => size !== null)) return start;
  }
  return -1;
}

// Pure: parses `vmmap --summary` into per-region-type resident/dirty megabytes.
// Parsing stops at the MALLOC ZONE table below, whose rows have a different
// arity and would otherwise mis-parse.
export function parseVmmapSummary(text) {
  const byRegion = {};
  // Unrounded, so a slice sums raw values and rounds once. Rounding each region
  // first and then adding lets the per-region error accumulate into the slice.
  const exactDirtyMb = {};
  let totalDirtyMb = 0;
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trimEnd();
    if (/^\s*MALLOC ZONE/.test(line)) break;
    if (!line || /^[=\s]+$/.test(line) || /^REGION TYPE/.test(line)) continue;
    const columns = line.trim().split(/\s+/);
    if (columns.length < 9) continue;
    const start = findSizeWindow(columns);
    if (start < 0) continue;
    const sizes = columns.slice(start, start + 7).map(parseVmmapSize);
    const name = columns.slice(0, start).join(' ');
    // Both summary rows: `TOTAL` and `TOTAL, minus reserved VM space`.
    if (!name || name.startsWith('TOTAL')) continue;
    const [residentMb, dirtyMb] = [sizes[1], sizes[2]];
    byRegion[name] = {
      residentMb: Number(residentMb.toFixed(1)),
      dirtyMb: Number(dirtyMb.toFixed(1)),
    };
    exactDirtyMb[name] = dirtyMb;
    totalDirtyMb += dirtyMb;
  }

  const slices = {};
  for (const [slice, names] of Object.entries(REGION_SLICES)) {
    slices[slice] = Number(
      names.reduce((sum, name) => sum + (exactDirtyMb[name] ?? 0), 0).toFixed(1),
    );
  }
  return {
    byRegion,
    slices,
    totalDirtyMb: Number(totalDirtyMb.toFixed(1)),
    footprintMb: parseFootprint(text),
  };
}

// The number Activity Monitor prints in its Memory column, and the only one that
// counts `owned unmapped (graphics)` — those pages are charged to this process
// but mapped in another, so `ps` RSS cannot see them at all. An app measured by
// RSS alone is missing its GPU surfaces entirely.
export function parseFootprint(text) {
  const match = /^Physical footprint:\s+(\S+)/m.exec(text);
  if (!match) return null;
  const mb = parseVmmapSize(match[1]);
  return mb === null ? null : Number(mb.toFixed(1));
}

// The app is the Tauri process plus the WebKit processes it drives. The daemon,
// its pty-workers, the session shells, and anything an agent spawns are separate
// programs that happen to share a process tree — counting them as "the app"
// attributes a 450MB headless classifier to the UI.
export const APP_PROCESS_CLASSES = ['app', 'webkit_webcontent', 'webkit_gpu', 'webkit_networking'];

export function appPids(snap) {
  return APP_PROCESS_CLASSES.flatMap((label) => (snap?.byClass?.[label]?.pids ?? []).map((entry) => entry.pid));
}

// Physical footprint of the app alone, per process and summed. Returns null
// entries for any process vmmap could not read rather than dropping it silently,
// so a partial total is visible as partial.
export async function readAppFootprint(snap) {
  const byPid = {};
  let totalMb = 0;
  let missing = 0;
  for (const label of APP_PROCESS_CLASSES) {
    for (const entry of snap?.byClass?.[label]?.pids ?? []) {
      const regions = await readRegionFootprint(entry.pid);
      const mb = regions?.footprintMb ?? null;
      byPid[entry.pid] = { label, footprintMb: mb };
      if (mb === null) missing += 1;
      else totalMb += mb;
    }
  }
  return { totalMb: Number(totalMb.toFixed(1)), missing, byPid };
}

// A GPU surface below this is chrome (compositing tiles, small layers), not a
// pane-sized buffer. At a 1710x1073 window a full-width surface is ~22-30 MB,
// and WebKit's 512x512@2x compositing tiles are ~4 MB, so 10 MB separates them
// cleanly. Reported alongside the count so a shifted window size is visible
// rather than silently re-bucketing.
const LARGE_GRAPHICS_SURFACE_MB = 10;

// Pure: the individual `owned unmapped (graphics)` regions from a full `vmmap`
// (NOT --summary, which pre-aggregates them). The summary says how much GPU
// surface a process holds; this says how many surfaces and what size, which is
// what distinguishes "one buffer per pane" from "a fixed cost per window".
export function parseGraphicsRegions(text) {
  const surfaces = [];
  for (const line of text.split('\n')) {
    if (!line.includes('owned unmapped (graphics)')) continue;
    const match = /\[\s*(\S+)\s+(\S+)\s+(\S+)\s+(\S+)\s*\]/.exec(line);
    if (!match) continue;
    const virtualMb = parseVmmapSize(match[1]);
    const dirtyMb = parseVmmapSize(match[3]);
    if (virtualMb === null || dirtyMb === null) continue;
    surfaces.push({ virtualMb, dirtyMb });
  }
  const large = surfaces.filter((surface) => surface.virtualMb >= LARGE_GRAPHICS_SURFACE_MB);
  const histogram = {};
  for (const surface of large) {
    const key = surface.virtualMb.toFixed(1);
    histogram[key] = (histogram[key] ?? 0) + 1;
  }
  return {
    regionCount: surfaces.length,
    largeCount: large.length,
    largeDirtyMb: Number(large.reduce((sum, s) => sum + s.dirtyMb, 0).toFixed(1)),
    largeVirtualMb: Number(large.reduce((sum, s) => sum + s.virtualMb, 0).toFixed(1)),
    histogram,
  };
}

export async function readGraphicsRegions(pid) {
  if (!pid) return null;
  try {
    const { stdout } = await execFileAsync('vmmap', [String(pid)], { maxBuffer: 128 * 1024 * 1024 });
    return parseGraphicsRegions(stdout);
  } catch {
    return null;
  }
}

// Dirty-page breakdown of one process. Returns null (never throws) when vmmap
// is unavailable or the process is gone: a region receipt is an enrichment of
// the RSS numbers, and losing it must not fail a run that measured fine.
export async function readRegionFootprint(pid) {
  if (!pid) return null;
  try {
    const { stdout } = await execFileAsync('vmmap', ['--summary', String(pid)], {
      maxBuffer: 32 * 1024 * 1024,
    });
    return parseVmmapSummary(stdout);
  } catch {
    return null;
  }
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
