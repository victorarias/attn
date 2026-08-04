#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { DaemonObserver } from './daemonObserver.mjs';
import { createRunContext, parseCommonArgs, printCommonHelp } from './common.mjs';
import { setFrontWindowBounds } from './nativeWindowCapture.mjs';
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
    if (
      arg === '--chunk-bytes'
      || arg === '--chunk-count'
      || arg === '--chunk-delay-ms'
      || arg === '--mode'
      || arg === '--payload'
      || arg === '--flush-every'
      || arg === '--window-width'
      || arg === '--window-height'
    ) {
      index += 1;
      continue;
    }
    commonArgv.push(arg);
  }

  const options = parseCommonArgs(commonArgv);
  options.chunkBytes = 16 * 1024;
  options.chunkCount = 128;
  options.chunkDelayMs = 0;
  options.mode = null;
  options.payload = 'scroll';
  options.flushEvery = null;
  options.windowWidth = null;
  options.windowHeight = null;

  for (let index = 0; index < filteredArgv.length; index += 1) {
    const arg = filteredArgv[index];
    if (arg === '--chunk-bytes') options.chunkBytes = Number(filteredArgv[index + 1]);
    if (arg === '--chunk-count') options.chunkCount = Number(filteredArgv[index + 1]);
    if (arg === '--chunk-delay-ms') options.chunkDelayMs = Number(filteredArgv[index + 1]);
    if (arg === '--mode') options.mode = filteredArgv[index + 1];
    if (arg === '--payload') options.payload = filteredArgv[index + 1];
    if (arg === '--flush-every') options.flushEvery = Number(filteredArgv[index + 1]);
    if (arg === '--window-width') options.windowWidth = Number(filteredArgv[index + 1]);
    if (arg === '--window-height') options.windowHeight = Number(filteredArgv[index + 1]);
  }

  if (!Number.isFinite(options.chunkBytes) || options.chunkBytes <= 0) {
    throw new Error('--chunk-bytes must be a positive number');
  }
  if (!Number.isFinite(options.chunkCount) || options.chunkCount <= 0) {
    throw new Error('--chunk-count must be a positive number');
  }
  if (!Number.isFinite(options.chunkDelayMs) || options.chunkDelayMs < 0) {
    throw new Error('--chunk-delay-ms must be a non-negative number');
  }
  if (options.flushEvery !== null && (!Number.isFinite(options.flushEvery) || options.flushEvery <= 0)) {
    throw new Error('--flush-every must be a positive number');
  }
  if (!['scroll', 'progress'].includes(options.payload)) {
    throw new Error('--payload must be scroll or progress');
  }
  if ((options.windowWidth === null) !== (options.windowHeight === null)) {
    throw new Error('--window-width and --window-height must be provided together');
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
  const cpuTotals = totals.map((item) => item.totalCpuPct).sort((a, b) => a - b);
  const p95Index = Math.max(0, Math.ceil(cpuTotals.length * 0.95) - 1);

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
    totalCpuPctAvg: totals.length > 0
      ? totals.reduce((sum, item) => sum + item.totalCpuPct, 0) / totals.length
      : 0,
    totalCpuPctP95: cpuTotals.length > 0 ? cpuTotals[p95Index] : 0,
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
    payload: bench.payload || 'scroll',
    flushEvery: bench.flushEvery || 1,
    totalMs: Number(bench.totalMs.toFixed(2)),
    throughputMiBPerSec: Number((bench.throughputMiBPerSec || 0).toFixed(2)),
    totalCpuPctMax: Number(processSummary.totalCpuPctMax.toFixed(1)),
    totalCpuPctAvg: Number(processSummary.totalCpuPctAvg.toFixed(1)),
    totalCpuPctP95: Number(processSummary.totalCpuPctP95.toFixed(1)),
    totalRssMbMax: Number((processSummary.totalRssKbMax / 1024).toFixed(1)),
    wsJsonParseMs: Number((bench.pty.wsJsonParseMs || 0).toFixed(3)),
    ptyJsonParseMs: Number((bench.pty.ptyJsonParseMs || 0).toFixed(3)),
    decodeMs: Number((bench.pty.decodeMs || 0).toFixed(3)),
    terminalWriteCallMs: Number((bench.pty.terminalWriteCallMs || 0).toFixed(3)),
    rendererPaintCount: bench.renderer?.renderCount || 0,
    rendererCpuSubmitMs: Number((bench.renderer?.cpuSubmitMs || 0).toFixed(3)),
    rendererAvgFrameMs: bench.renderer?.renderCount > 0
      ? Number((bench.renderer.cpuSubmitMs / bench.renderer.renderCount).toFixed(3))
      : 0,
    rendererFullPaintCount: bench.renderer?.fullPaintCount || 0,
    rendererPartialPaintCount: bench.renderer?.partialPaintCount || 0,
    rendererRowsPainted: bench.renderer?.rowsPainted || 0,
    rendererSubmittedQuads: bench.renderer?.submittedQuads || 0,
    rendererRetainedRowVertexMb: Number(((bench.renderer?.retainedRowVertexBytes || 0) / (1024 * 1024)).toFixed(3)),
    rendererRetainedStagingMb: Number(((bench.renderer?.retainedStagingBytes || 0) / (1024 * 1024)).toFixed(3)),
    fixturePrintable: bench.renderer?.fixturePrintable || 0,
    finalModelPrintable: bench.renderer?.finalModelPrintable || 0,
    finalPaintQuads: bench.renderer?.finalPaintQuads || 0,
    scheduledRenderRequests: bench.renderer?.scheduledRequests || 0,
    scheduledRenderCoalesced: bench.renderer?.scheduledCoalesced || 0,
    scheduledRenderDeferred: bench.renderer?.scheduledDeferred || 0,
    writeParsedCount: bench.renderer?.writeParsedCount || 0,
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
    console.log('  --chunk-delay-ms <n>       Delay between chunks for sustained-output measurements');
    console.log('  --mode <name>              Run only bytes, base64, or json_base64');
    console.log('  --payload <name>           Run scrolling output or in-place progress updates');
    console.log('  --flush-every <n>           Run only one batching level');
    console.log('  --window-width <n>          Resize the isolated benchmark window before creating panes');
    console.log('  --window-height <n>         Resize the isolated benchmark window before creating panes');
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
    if (options.windowWidth !== null && options.windowHeight !== null) {
      await setFrontWindowBounds(
        { x: 60, y: 60, width: options.windowWidth, height: options.windowHeight },
        { client },
      );
      await client.waitForFrontendResponsive(20_000);
    }
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
    const targetPaneId = initialWorkspace.activePaneId || initialWorkspace.panes?.[0]?.paneId;
    if (!targetPaneId) {
      throw new Error(`No pane available to split in workspace ${sessionId}`);
    }
    await client.request('split_pane', {
      sessionId,
      targetPaneId,
      direction: 'vertical',
    }, { timeoutMs: 30_000 });

    const utilityPane = await observer.waitForUtilityPane(
      sessionId,
      20_000,
      new Set([targetPaneId]),
    );
    if (!utilityPane?.runtime_id) {
      throw new Error('Utility pane not found');
    }

    const selectedMode = options.mode || 'json_base64';
    if (!['bytes', 'base64', 'json_base64'].includes(selectedMode)) {
      throw new Error(`Unsupported benchmark mode: ${selectedMode}`);
    }
    const batchSizes = options.flushEvery === null ? [1, 8, 32] : [options.flushEvery];
    const modes = batchSizes.map((flushEvery) => ({
      name: `${selectedMode}_x${flushEvery}`,
      mode: selectedMode,
      flushEvery,
    }));
    const results = [];
    for (const entry of modes) {
      const benchPromise = client.request('benchmark_pty_transport', {
        sessionId,
        paneId: utilityPane.pane_id,
        mode: entry.mode,
        chunkBytes: options.chunkBytes,
        chunkCount: options.chunkCount,
        payload: options.payload,
        interChunkDelayMs: options.chunkDelayMs,
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
    const json1 = compact.find((entry) => entry.flushEvery === 1);
    const json8 = compact.find((entry) => entry.flushEvery === 8);
    const json32 = compact.find((entry) => entry.flushEvery === 32);

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
      chunkDelayMs: options.chunkDelayMs,
      payload: options.payload,
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
