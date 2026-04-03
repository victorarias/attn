#!/usr/bin/env node

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { execFile } from 'node:child_process';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { UiAutomationClient } from './uiAutomationClient.mjs';

const execFileAsync = promisify(execFile);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function parseArgs(argv) {
  const filteredArgv = argv.filter((arg) => arg !== '--');
  const commonArgv = [];
  for (let index = 0; index < filteredArgv.length; index += 1) {
    const arg = filteredArgv[index];
    if (arg === '--terminal-line-count' || arg === '--terminal-steady-ms') {
      index += 1;
      continue;
    }
    commonArgv.push(arg);
  }

  const options = parseCommonArgs(commonArgv);
  options.terminalLineCount = 2500;
  options.terminalSteadyMs = 1500;

  for (let index = 0; index < filteredArgv.length; index += 1) {
    const arg = filteredArgv[index];
    if (arg === '--terminal-line-count') {
      options.terminalLineCount = Number(filteredArgv[index + 1]);
    }
    if (arg === '--terminal-steady-ms') {
      options.terminalSteadyMs = Number(filteredArgv[index + 1]);
    }
  }

  return options;
}

function writeFile(filePath, contents) {
  fs.writeFileSync(filePath, `${contents.endsWith('\n') ? contents : `${contents}\n`}`, 'utf8');
}

function runGit(args, cwd, extraEnv = {}) {
  execFileSync('git', args, {
    cwd,
    stdio: 'pipe',
    env: {
      ...process.env,
      GIT_AUTHOR_NAME: 'attn perf harness',
      GIT_AUTHOR_EMAIL: 'perf-harness@example.com',
      GIT_COMMITTER_NAME: 'attn perf harness',
      GIT_COMMITTER_EMAIL: 'perf-harness@example.com',
      ...extraEnv,
    },
  });
}

function buildFileContents(prefix, lineCount, width = 96) {
  const filler = 'x'.repeat(Math.max(width - prefix.length - 16, 8));
  const lines = [];
  for (let index = 0; index < lineCount; index += 1) {
    lines.push(`${prefix} line ${String(index).padStart(4, '0')} ${filler}`);
  }
  return `${lines.join('\n')}\n`;
}

function setupPerfRepo(sessionDir) {
  const remoteDir = `${sessionDir}-remote.git`;
  fs.mkdirSync(sessionDir, { recursive: true });
  execFileSync('git', ['init', '--bare', remoteDir], { stdio: 'pipe' });
  runGit(['init', '-b', 'main'], sessionDir);
  runGit(['config', 'user.name', 'attn perf harness'], sessionDir);
  runGit(['config', 'user.email', 'perf-harness@example.com'], sessionDir);
  runGit(['remote', 'add', 'origin', remoteDir], sessionDir);

  fs.mkdirSync(path.join(sessionDir, 'src'), { recursive: true });
  writeFile(path.join(sessionDir, 'src', 'large.ts'), buildFileContents('main', 2200, 120));
  writeFile(path.join(sessionDir, 'README.md'), '# perf repo\n');
  runGit(['add', '.'], sessionDir);
  runGit(['commit', '-m', 'main base'], sessionDir);
  runGit(['push', '-u', 'origin', 'main'], sessionDir);

  runGit(['checkout', '-b', 'feature/perf-review'], sessionDir);
  writeFile(path.join(sessionDir, 'src', 'large.ts'), buildFileContents('feature', 3200, 120));
  writeFile(path.join(sessionDir, 'src', 'added.ts'), buildFileContents('added', 1200, 100));
  runGit(['add', '.'], sessionDir);
  runGit(['commit', '-m', 'feature diff payload'], sessionDir);
}

async function readProcessTable() {
  const { stdout } = await execFileAsync('ps', ['-axo', 'pid=,ppid=,%cpu=,rss=,comm=,command=']);
  return stdout
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const match = line.match(/^(\d+)\s+(\d+)\s+([\d.]+)\s+(\d+)\s+(\S+)\s+(.*)$/);
      if (!match) {
        return null;
      }
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

function isRelevantWebKitProcess(processInfo) {
  return processInfo.command.includes('com.apple.WebKit.WebContent')
    || processInfo.command.includes('com.apple.WebKit.Networking')
    || processInfo.command.includes('com.apple.WebKit.GPU');
}

async function captureRelevantWebKitPids() {
  const table = await readProcessTable();
  return new Set(
    table
      .filter((processInfo) => isRelevantWebKitProcess(processInfo))
      .map((processInfo) => processInfo.pid),
  );
}

async function captureRelevantDaemonPids() {
  const table = await readProcessTable();
  return new Set(
    table
      .filter((processInfo) => processInfo.command.includes('attn daemon'))
      .map((processInfo) => processInfo.pid),
  );
}

async function waitForNewWebKitPids(baselinePids, timeoutMs = 10_000) {
  const startedAt = Date.now();
  while (Date.now() - startedAt < timeoutMs) {
    const table = await readProcessTable();
    const newPids = table
      .filter((processInfo) => isRelevantWebKitProcess(processInfo) && !baselinePids.has(processInfo.pid))
      .map((processInfo) => processInfo.pid);
    if (newPids.length > 0) {
      return new Set(newPids);
    }
    await delay(200);
  }
  return new Set();
}

function collectDescendantPids(processes, rootPid) {
  const childrenByParent = new Map();
  for (const processInfo of processes) {
    const siblings = childrenByParent.get(processInfo.ppid) || [];
    siblings.push(processInfo.pid);
    childrenByParent.set(processInfo.ppid, siblings);
  }

  const visited = new Set([rootPid]);
  const queue = [rootPid];
  while (queue.length > 0) {
    const pid = queue.shift();
    const children = childrenByParent.get(pid) || [];
    for (const childPid of children) {
      if (visited.has(childPid)) continue;
      visited.add(childPid);
      queue.push(childPid);
    }
  }
  return visited;
}

function summarizeProcessSamples(samples) {
  const byCommand = new Map();
  const totals = samples.map((sample) => ({
    at: sample.at,
    totalCpuPct: sample.processes.reduce((sum, processInfo) => sum + processInfo.cpuPct, 0),
    totalRssKb: sample.processes.reduce((sum, processInfo) => sum + processInfo.rssKb, 0),
  }));

  for (const sample of samples) {
    const currentCounts = new Map();
    for (const processInfo of sample.processes) {
      currentCounts.set(processInfo.comm, (currentCounts.get(processInfo.comm) || 0) + 1);
      const label = classifyProcess(processInfo);
      const entry = byCommand.get(label) || {
        samples: 0,
        cpuTotal: 0,
        cpuMax: 0,
        rssMaxKb: 0,
        instanceMax: 0,
      };
      entry.samples += 1;
      entry.cpuTotal += processInfo.cpuPct;
      entry.cpuMax = Math.max(entry.cpuMax, processInfo.cpuPct);
      entry.rssMaxKb = Math.max(entry.rssMaxKb, processInfo.rssKb);
      entry.instanceMax = Math.max(entry.instanceMax, currentCounts.get(processInfo.comm));
      byCommand.set(label, entry);
    }
  }

  return {
    sampleCount: samples.length,
    totalCpuPctAvg: totals.length > 0
      ? totals.reduce((sum, value) => sum + value.totalCpuPct, 0) / totals.length
      : 0,
    totalCpuPctMax: totals.length > 0
      ? Math.max(...totals.map((value) => value.totalCpuPct))
      : 0,
    totalRssKbAvg: totals.length > 0
      ? Math.round(totals.reduce((sum, value) => sum + value.totalRssKb, 0) / totals.length)
      : 0,
    totalRssKbMax: totals.length > 0
      ? Math.max(...totals.map((value) => value.totalRssKb))
      : 0,
    byCommand: Object.fromEntries(
      [...byCommand.entries()]
        .sort((a, b) => b[1].rssMaxKb - a[1].rssMaxKb)
        .map(([comm, entry]) => [
          comm,
          {
            cpuPctAvg: entry.samples > 0 ? entry.cpuTotal / entry.samples : 0,
            cpuPctMax: entry.cpuMax,
            rssMaxKb: entry.rssMaxKb,
            instanceMax: entry.instanceMax,
          },
        ]),
    ),
  };
}

function classifyProcess(processInfo) {
  if (processInfo.command.includes('/Applications/attn.app/Contents/MacOS/app')) {
    return 'app';
  }
  if (processInfo.command.includes('attn daemon')) {
    return 'daemon';
  }
  if (processInfo.command.includes('com.apple.WebKit.WebContent')) {
    return 'webkit_webcontent';
  }
  if (processInfo.command.includes('com.apple.WebKit.Networking')) {
    return 'webkit_networking';
  }
  if (processInfo.command.includes('com.apple.WebKit.GPU')) {
    return 'webkit_gpu';
  }
  return processInfo.comm;
}

async function sampleProcessTree(rootPid, durationMs = 2000, intervalMs = 250, extraPids = new Set()) {
  const startedAt = Date.now();
  const samples = [];
  while (Date.now() - startedAt < durationMs) {
    const table = await readProcessTable();
    const pidSet = collectDescendantPids(table, rootPid);
    for (const pid of extraPids) {
      pidSet.add(pid);
    }
    samples.push({
      at: new Date().toISOString(),
      processes: table.filter((processInfo) => pidSet.has(processInfo.pid)),
    });
    await delay(intervalMs);
  }
  return {
    samples,
    summary: summarizeProcessSamples(samples),
  };
}

async function captureCheckpoint(client, rootPid, stage, options = {}) {
  const [frontend, processTree] = await Promise.all([
    client.request(
      'capture_perf_snapshot',
      {
        settleFrames: options.settleFrames ?? 2,
        includeMemory: options.includeMemory === true,
      },
      { timeoutMs: options.captureTimeoutMs ?? 120_000 },
    ),
    sampleProcessTree(
      rootPid,
      options.sampleDurationMs ?? 2000,
      options.sampleIntervalMs ?? 250,
      options.extraPids ?? new Set(),
    ),
  ]);
  return {
    stage,
    frontend,
    processTree,
  };
}

async function waitForCondition(fn, description, timeoutMs = 20_000, intervalMs = 250) {
  const startedAt = Date.now();
  let lastValue = null;
  while (Date.now() - startedAt < timeoutMs) {
    lastValue = await fn();
    if (lastValue) {
      return lastValue;
    }
    await delay(intervalMs);
  }
  throw new Error(`Timed out waiting for ${description}. Last value: ${JSON.stringify(lastValue, null, 2)}`);
}

async function waitForScrollbackBytes(observer, runtimeId, minBytes, timeoutMs = 30_000) {
  return waitForCondition(async () => {
    try {
      const scrollback = await observer.readScrollback(runtimeId, 8_000);
      return scrollback.length >= minBytes ? scrollback.length : null;
    } catch {
      return null;
    }
  }, `scrollback for ${runtimeId} to reach ${minBytes} bytes`, timeoutMs, 400);
}

function formatCheckpointSummary(checkpoint) {
  const rssMb = (checkpoint.processTree.summary.totalRssKbMax / 1024).toFixed(1);
  const cpuPct = checkpoint.processTree.summary.totalCpuPctMax.toFixed(1);
  const terminalLines = checkpoint.frontend.terminals.reduce((sum, terminal) => sum + terminal.bufferLength, 0);
  const writeQueueBytes = checkpoint.frontend.terminals.reduce((sum, terminal) => sum + terminal.writeQueueBytes, 0);
  const diffLines = checkpoint.frontend.review.editor?.lineCount || 0;
  const diffBuildMs = checkpoint.frontend.review.editor?.buildDocumentDurationMs || 0;
  const pty = checkpoint.frontend.pty || {};
  const terminalLoad = checkpoint.terminalLoad || null;
  return {
    stage: checkpoint.stage,
    totalCpuPctMax: Number(cpuPct),
    totalRssMbMax: Number(rssMb),
    terminalLines,
    writeQueueBytes,
    diffLines,
    diffBuildMs: Number(diffBuildMs.toFixed(2)),
    ptyJsonParseMs: Number((pty.ptyJsonParseMs || 0).toFixed(2)),
    ptyDecodeMs: Number((pty.decodeMs || 0).toFixed(2)),
    terminalWriteCallMs: Number((pty.terminalWriteCallMs || 0).toFixed(2)),
    terminalWriteCount: pty.terminalWriteCount || 0,
    ptyOutputCount: pty.ptyOutputCount || 0,
    terminalLoadBytes: terminalLoad?.totalScrollbackBytes || 0,
    terminalLoadLines: terminalLoad?.totalObservedLineCount || 0,
    terminalCompletionMsMax: Number((terminalLoad?.completionMsMax || 0).toFixed(2)),
    terminalCompletionMsAvg: Number((terminalLoad?.completionMsAvg || 0).toFixed(2)),
    terminalSteadyMs: terminalLoad?.steadyMs || 0,
  };
}

function getShellPanes(workspace) {
  return (workspace.panes || []).filter((pane) => pane.kind === 'shell' && pane.runtime_id);
}

async function createUtilityPanes(client, observer, sessionId, count) {
  let workspace = await client.request('get_workspace', { sessionId });
  for (let index = 0; index < count; index += 1) {
    await client.request('split_pane', {
      sessionId,
      targetPaneId: workspace.activePaneId || 'main',
      direction: 'vertical',
    });
    workspace = await observer.waitForWorkspace(
      sessionId,
      (entry) => getShellPanes(entry).length >= index + 1,
      `shell pane count >= ${index + 1}`,
      20_000,
    );
  }
  return getShellPanes(workspace);
}

function countOccurrences(haystack, needle) {
  if (!needle) {
    return 0;
  }
  let count = 0;
  let offset = 0;
  while (offset < haystack.length) {
    const next = haystack.indexOf(needle, offset);
    if (next === -1) {
      break;
    }
    count += 1;
    offset = next + needle.length;
  }
  return count;
}

async function waitForTerminalCompletion(observer, runtimeId, token, doneToken, expectedLineCount, timeoutMs = 45_000) {
  const startedAt = Date.now();
  return waitForCondition(async () => {
    try {
      const scrollback = await observer.readScrollback(runtimeId, 8_000);
      if (!scrollback.includes(doneToken)) {
        return null;
      }
      const observedLineCount = countOccurrences(scrollback, `${token} line `);
      return {
        completionMs: Date.now() - startedAt,
        expectedLineCount,
        observedLineCount,
        scrollbackBytes: scrollback.length,
        doneTokenCount: countOccurrences(scrollback, doneToken),
      };
    } catch {
      return null;
    }
  }, `terminal completion for ${runtimeId}`, timeoutMs, 300);
}

async function runTerminalLoad(client, observer, sessionId, shellPanes, lineCount = 2500) {
  const paneRuns = [];
  for (let index = 0; index < shellPanes.length; index += 1) {
    const pane = shellPanes[index];
    const token = `__ATTN_PERF_PANE_${index}_${Date.now()}__`;
    const doneToken = `${token} DONE`;
    const python = [
      `token=${JSON.stringify(token)}`,
      `done=${JSON.stringify(doneToken)}`,
      `line_count=${lineCount}`,
      `[print(f"{token} line {i:04d} " + ("x"*120)) for i in range(line_count)]`,
      `print(done)`,
    ].join('; ');
    const command = `/usr/bin/python3 -c '${python}'`;
    await client.request('write_pane', {
      sessionId,
      paneId: pane.pane_id,
      text: command,
      submit: true,
    });
    paneRuns.push({
      paneId: pane.pane_id,
      runtimeId: pane.runtime_id,
      token,
      doneToken,
      expectedLineCount: lineCount,
    });
  }

  const panes = await Promise.all(paneRuns.map(async (paneRun) => {
    const completion = await waitForTerminalCompletion(
      observer,
      paneRun.runtimeId,
      paneRun.token,
      paneRun.doneToken,
      paneRun.expectedLineCount,
      45_000,
    );
    return {
      paneId: paneRun.paneId,
      runtimeId: paneRun.runtimeId,
      ...completion,
    };
  }));

  return {
    lineCount,
    paneCount: panes.length,
    totalExpectedLineCount: panes.reduce((sum, pane) => sum + pane.expectedLineCount, 0),
    totalObservedLineCount: panes.reduce((sum, pane) => sum + pane.observedLineCount, 0),
    totalScrollbackBytes: panes.reduce((sum, pane) => sum + pane.scrollbackBytes, 0),
    completionMsMax: Math.max(...panes.map((pane) => pane.completionMs)),
    completionMsAvg: panes.reduce((sum, pane) => sum + pane.completionMs, 0) / Math.max(panes.length, 1),
    panes,
  };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-perf.mjs');
    console.log('  --terminal-line-count <n>            Lines per utility pane terminal load (default: 2500)');
    console.log('  --terminal-steady-ms <n>             Extra settle time after terminal completion before snapshot (default: 1500)');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'bridge-perf');
  const sessionLabel = `attn-perf-${runId}`;

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  console.log(`[RealAppPerf] runDir=${runDir}`);
  console.log(`[RealAppPerf] sessionDir=${sessionDir}`);

  setupPerfRepo(sessionDir);

  try {
    const baselineWebKitPids = await captureRelevantWebKitPids();
    const daemonPids = await captureRelevantDaemonPids();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();
    await observer.unregisterMatchingSessions(
      (session) => typeof session.label === 'string' && session.label.startsWith('attn-perf-bridge-perf-'),
      20_000,
    );
    await client.waitForFrontendResponsive(20_000);

    const manifest = client.readManifest();
    const launchedWebKitPids = await waitForNewWebKitPids(baselineWebKitPids, 8_000);
    const sampledExtraPids = new Set([...launchedWebKitPids, ...daemonPids]);
    const checkpoints = [];

    await client.request('clear_perf_counters');
    checkpoints.push(await captureCheckpoint(client, manifest.pid, 'app_ready', {
      extraPids: sampledExtraPids,
    }));

    await client.request('clear_perf_counters');
    const createResult = await client.request('create_session', {
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
    });
    const sessionId = createResult.sessionId;
    await observer.waitForSession({
      label: sessionLabel,
      directory: sessionDir,
      timeoutMs: 30_000,
    });

    checkpoints.push(await captureCheckpoint(client, manifest.pid, 'session_open', {
      extraPids: sampledExtraPids,
    }));

    await client.request('clear_perf_counters');
    const shellPanes = await createUtilityPanes(client, observer, sessionId, 2);
    checkpoints.push(await captureCheckpoint(client, manifest.pid, 'two_shell_panes', {
      extraPids: sampledExtraPids,
    }));

    await client.request('clear_perf_counters');
    const terminalLoad = await runTerminalLoad(client, observer, sessionId, shellPanes, options.terminalLineCount);
    await delay(options.terminalSteadyMs);
    const terminalOutputCheckpoint = await captureCheckpoint(client, manifest.pid, 'terminal_output', {
      extraPids: sampledExtraPids,
    });
    terminalOutputCheckpoint.terminalLoad = {
      ...terminalLoad,
      steadyMs: options.terminalSteadyMs,
    };
    checkpoints.push(terminalOutputCheckpoint);

    await client.request('clear_perf_counters');
    await client.request('dispatch_shortcut', { shortcutId: 'dock.diffDetail' });
    await waitForCondition(async () => {
      const snapshot = await client.request('capture_perf_snapshot', {
        settleFrames: 2,
        includeMemory: false,
      }, { timeoutMs: 60_000 });
      return snapshot.review?.editor?.active ? snapshot : null;
    }, 'diff detail editor to become active', 25_000);
    checkpoints.push(await captureCheckpoint(client, manifest.pid, 'diff_detail_open', {
      extraPids: sampledExtraPids,
    }));

    const summary = {
      ok: true,
      runId,
      sessionId,
      appPid: manifest.pid,
      checkpoints,
      compact: checkpoints.map(formatCheckpointSummary),
    };

    writeFile(path.join(runDir, 'summary.json'), JSON.stringify(summary, null, 2));
    console.log('[RealAppPerf] Perf harness completed.');
    console.log(JSON.stringify(summary.compact, null, 2));
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[RealAppPerf] Perf harness failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
