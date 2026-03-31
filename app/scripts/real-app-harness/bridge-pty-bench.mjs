#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
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
    if (arg === '--chunk-bytes' || arg === '--chunk-count') {
      index += 1;
      continue;
    }
    commonArgv.push(arg);
  }

  const options = parseCommonArgs(commonArgv);
  options.chunkBytes = 16 * 1024;
  options.chunkCount = 128;

  for (let index = 0; index < filteredArgv.length; index += 1) {
    const arg = filteredArgv[index];
    if (arg === '--chunk-bytes') options.chunkBytes = Number(filteredArgv[index + 1]);
    if (arg === '--chunk-count') options.chunkCount = Number(filteredArgv[index + 1]);
  }

  return options;
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

async function waitForNewWebKitPids(baselinePids, timeoutMs = 8_000) {
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

function summarizeProcessSamples(samples) {
  const byCommand = new Map();
  const totals = samples.map((sample) => ({
    totalCpuPct: sample.processes.reduce((sum, processInfo) => sum + processInfo.cpuPct, 0),
    totalRssKb: sample.processes.reduce((sum, processInfo) => sum + processInfo.rssKb, 0),
  }));

  for (const sample of samples) {
    for (const processInfo of sample.processes) {
      const label = classifyProcess(processInfo);
      const entry = byCommand.get(label) || {
        samples: 0,
        cpuTotal: 0,
        cpuMax: 0,
        rssMaxKb: 0,
      };
      entry.samples += 1;
      entry.cpuTotal += processInfo.cpuPct;
      entry.cpuMax = Math.max(entry.cpuMax, processInfo.cpuPct);
      entry.rssMaxKb = Math.max(entry.rssMaxKb, processInfo.rssKb);
      byCommand.set(label, entry);
    }
  }

  return {
    totalCpuPctMax: totals.length > 0 ? Math.max(...totals.map((item) => item.totalCpuPct)) : 0,
    totalRssKbMax: totals.length > 0 ? Math.max(...totals.map((item) => item.totalRssKb)) : 0,
    byCommand: Object.fromEntries(
      [...byCommand.entries()].map(([label, entry]) => [
        label,
        {
          cpuPctAvg: entry.samples > 0 ? entry.cpuTotal / entry.samples : 0,
          cpuPctMax: entry.cpuMax,
          rssMaxKb: entry.rssMaxKb,
        },
      ]),
    ),
  };
}

async function sampleWhilePending(rootPid, extraPids, promise, intervalMs = 100) {
  const samples = [];
  let settled = false;
  promise.finally(() => {
    settled = true;
  });

  while (!settled) {
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

  const result = await promise;
  return { result, processSummary: summarizeProcessSamples(samples) };
}

function compactResult(mode, bench, processSummary) {
  return {
    mode,
    flushEvery: bench.flushEvery || 1,
    totalMs: Number(bench.totalMs.toFixed(2)),
    throughputMiBPerSec: Number((bench.throughputMiBPerSec || 0).toFixed(2)),
    totalCpuPctMax: Number(processSummary.totalCpuPctMax.toFixed(1)),
    totalRssMbMax: Number((processSummary.totalRssKbMax / 1024).toFixed(1)),
    wsJsonParseMs: Number((bench.pty.wsJsonParseMs || 0).toFixed(3)),
    ptyJsonParseMs: Number((bench.pty.ptyJsonParseMs || 0).toFixed(3)),
    decodeMs: Number((bench.pty.decodeMs || 0).toFixed(3)),
    terminalWriteCallMs: Number((bench.pty.terminalWriteCallMs || 0).toFixed(3)),
    ptyOutputCount: bench.pty.ptyOutputCount || 0,
    terminalWriteCount: bench.pty.terminalWriteCount || 0,
    totalPayloadBytes: bench.totalPayloadBytes,
  };
}

async function main() {
  const options = parseArgs(process.argv.slice(2));
  if (options.help) {
    printCommonHelp('scripts/real-app-harness/bridge-pty-bench.mjs');
    console.log('  --chunk-bytes <n>          Payload bytes per chunk (default: 16384)');
    console.log('  --chunk-count <n>          Number of chunks per mode (default: 128)');
    return;
  }

  const { runId, runDir, sessionDir } = createRunContext(options, 'bridge-pty-bench');
  const sessionLabel = `attn-pty-bench-${runId}`;

  fs.mkdirSync(sessionDir, { recursive: true });

  const client = new UiAutomationClient({ appPath: options.appPath });
  const observer = new DaemonObserver({ wsUrl: options.wsUrl });

  try {
    const baselineWebKitPids = await captureRelevantWebKitPids();
    const daemonPids = await captureRelevantDaemonPids();
    await client.launchFreshApp();
    await client.waitForManifest(20_000);
    await client.waitForReady(20_000);
    await client.waitForFrontendResponsive(20_000);
    await observer.connect();
    await observer.unregisterMatchingSessions(
      (session) => typeof session.label === 'string' && session.label.startsWith('attn-pty-bench-'),
      20_000,
    );
    await client.waitForFrontendResponsive(20_000);

    const manifest = client.readManifest();
    const launchedWebKitPids = await waitForNewWebKitPids(baselineWebKitPids, 8_000);
    const extraPids = new Set([...launchedWebKitPids, ...daemonPids]);

    const createResult = await client.request('create_session', {
      cwd: sessionDir,
      label: sessionLabel,
      agent: 'claude',
    }, { timeoutMs: 60_000 });
    const sessionId = createResult.sessionId;
    await observer.waitForSession({
      label: sessionLabel,
      directory: sessionDir,
      timeoutMs: 30_000,
    });

    const initialWorkspace = await client.request('get_workspace', { sessionId }, { timeoutMs: 10_000 });
    await client.request('split_pane', {
      sessionId,
      targetPaneId: initialWorkspace.activePaneId || 'main',
      direction: 'vertical',
    }, { timeoutMs: 30_000 });

    const workspace = await observer.waitForWorkspace(
      sessionId,
      (entry) => (entry.panes || []).some((pane) => pane.kind === 'shell' && pane.runtime_id),
      `utility pane for ${sessionId}`,
      20_000,
    );
    const utilityPane = (workspace.panes || []).find((pane) => pane.kind === 'shell' && pane.runtime_id);
    if (!utilityPane?.runtime_id) {
      throw new Error('Utility pane not found');
    }

    const modes = [
      { name: 'json_base64_x1', mode: 'json_base64', flushEvery: 1 },
      { name: 'json_base64_x8', mode: 'json_base64', flushEvery: 8 },
      { name: 'json_base64_x32', mode: 'json_base64', flushEvery: 32 },
    ];
    const results = [];
    for (const entry of modes) {
      const benchPromise = client.request('benchmark_pty_transport', {
        sessionId,
        paneId: utilityPane.pane_id,
        mode: entry.mode,
        chunkBytes: options.chunkBytes,
        chunkCount: options.chunkCount,
        flushEvery: entry.flushEvery,
      }, { timeoutMs: 120_000 });
      const measured = await sampleWhilePending(manifest.pid, extraPids, benchPromise, 100);
      results.push({
        name: entry.name,
        mode: entry.mode,
        flushEvery: entry.flushEvery,
        bench: measured.result,
        processSummary: measured.processSummary,
      });
      await delay(500);
    }

    const compact = results.map((entry) => ({
      name: entry.name,
      ...compactResult(entry.mode, entry.bench, entry.processSummary),
    }));
    const json1 = compact.find((entry) => entry.name === 'json_base64_x1');
    const json8 = compact.find((entry) => entry.name === 'json_base64_x8');
    const json32 = compact.find((entry) => entry.name === 'json_base64_x32');

    const deltas = {
      x1VsX8Ms: json1 && json8 ? Number((json1.totalMs - json8.totalMs).toFixed(2)) : null,
      x1VsX32Ms: json1 && json32 ? Number((json1.totalMs - json32.totalMs).toFixed(2)) : null,
      x8VsX32Ms: json8 && json32 ? Number((json8.totalMs - json32.totalMs).toFixed(2)) : null,
    };

    const summary = {
      ok: true,
      runId,
      sessionId,
      paneId: utilityPane.pane_id,
      runtimeId: utilityPane.runtime_id,
      chunkBytes: options.chunkBytes,
      chunkCount: options.chunkCount,
      results,
      compact,
      deltas,
    };

    fs.writeFileSync(path.join(runDir, 'summary.json'), `${JSON.stringify(summary, null, 2)}\n`, 'utf8');
    console.log(JSON.stringify({ compact, deltas }, null, 2));
  } finally {
    await observer.close();
  }
}

main().catch((error) => {
  console.error('[PTYBench] Failed.');
  console.error(error instanceof Error ? error.stack || error.message : error);
  process.exitCode = 1;
});
