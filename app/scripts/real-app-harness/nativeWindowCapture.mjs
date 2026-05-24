import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const NATIVE_WINDOW_ID_SOURCE = path.join(SCRIPT_DIR, 'NativeWindowID.swift');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function runAppleScript(script) {
  const { stdout } = await execFileAsync('osascript', ['-e', script], {
    timeout: 10_000,
  });
  return stdout.trim();
}

async function activateBundle(bundleId) {
  try {
    await runAppleScript(`tell application id "${bundleId}" to activate`);
  } catch {
    // Best effort only.
  }
}

function parseWindowBoundsOutput(output, bundleId) {
  const [x, y, width, height, processName] = String(output || '')
    .split(',')
    .map((value) => value.trim());

  const numericBounds = [x, y, width, height].map((value) => Number.parseInt(value, 10));
  if (!numericBounds.every((value) => Number.isFinite(value))) {
    throw new Error(`Failed to parse window bounds for bundle ${bundleId}: ${output}`);
  }

  return {
    x: numericBounds[0],
    y: numericBounds[1],
    width: numericBounds[2],
    height: numericBounds[3],
    processName: processName || null,
  };
}

function normalizeLogicalBounds(bounds) {
  if (!bounds) {
    return null;
  }
  const x = Number(bounds.x);
  const y = Number(bounds.y);
  const width = Number(bounds.width);
  const height = Number(bounds.height);
  if (![x, y, width, height].every(Number.isFinite) || width <= 0 || height <= 0) {
    return null;
  }
  return {
    x: Math.round(x),
    y: Math.round(y),
    width: Math.round(width),
    height: Math.round(height),
    processName: 'attn-app-window',
  };
}

async function readUiAutomationWindowBounds(client) {
  if (!client || typeof client.request !== 'function') {
    return null;
  }
  try {
    const result = await client.request('get_window_bounds', {}, { timeoutMs: 5_000 });
    if (result?.minimized === true) {
      return null;
    }
    return normalizeLogicalBounds(result?.logicalBounds);
  } catch {
    return null;
  }
}

async function readFrontWindowBoundsForBundle(bundleId) {
  const output = await runAppleScript(`
tell application "System Events"
  set targetProcess to first application process whose bundle identifier is "${bundleId}"
  if (count of windows of targetProcess) is 0 then
    error "No windows found for ${bundleId}"
  end if
  set p to position of front window of targetProcess
  set s to size of front window of targetProcess
  return (item 1 of p as text) & "," & (item 2 of p as text) & "," & (item 1 of s as text) & "," & (item 2 of s as text) & "," & (name of targetProcess as text)
end tell
`);
  return parseWindowBoundsOutput(output, bundleId);
}

export async function getFrontWindowBounds(bundleId = 'com.attn.manager', options = {}) {
  const automationBounds = await readUiAutomationWindowBounds(options.client);
  if (automationBounds) {
    return automationBounds;
  }

  let lastError = null;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if (attempt > 0) {
      await activateBundle(bundleId);
      await delay(250);
    }
    try {
      return await readFrontWindowBoundsForBundle(bundleId);
    } catch (error) {
      lastError = error;
    }
  }

  throw lastError instanceof Error
    ? lastError
    : new Error(`Failed to resolve front window bounds for ${bundleId}`);
}

function boundsWithinTolerance(actual, target, tolerancePx = 6) {
  if (!actual || !target) {
    return false;
  }
  return (
    Math.abs(actual.x - target.x) <= tolerancePx &&
    Math.abs(actual.y - target.y) <= tolerancePx &&
    Math.abs(actual.width - target.width) <= tolerancePx &&
    Math.abs(actual.height - target.height) <= tolerancePx
  );
}

async function setFrontWindowBoundsForBundle(bundleId, bounds) {
  const output = await runAppleScript(`
tell application "System Events"
  set targetProcess to first application process whose bundle identifier is "${bundleId}"
  if (count of windows of targetProcess) is 0 then
    error "No windows found for ${bundleId}"
  end if
  set position of front window of targetProcess to {${bounds.x}, ${bounds.y}}
  set size of front window of targetProcess to {${bounds.width}, ${bounds.height}}
  set p to position of front window of targetProcess
  set s to size of front window of targetProcess
  return (item 1 of p as text) & "," & (item 2 of p as text) & "," & (item 1 of s as text) & "," & (item 2 of s as text) & "," & (name of targetProcess as text)
end tell
`);
  return parseWindowBoundsOutput(output, bundleId);
}

async function writeUiAutomationWindowBounds(client, targetBounds) {
  if (!client || typeof client.request !== 'function') {
    return null;
  }
  try {
    const result = await client.request(
      'set_window_bounds',
      { logicalBounds: targetBounds },
      { timeoutMs: 5_000 },
    );
    return normalizeLogicalBounds(result?.logicalBounds);
  } catch {
    return null;
  }
}

export async function setFrontWindowBounds(targetBounds, options = {}) {
  const bundleId = options.bundleId || 'com.attn.manager';
  const normalizedTarget = normalizeLogicalBounds(targetBounds);
  if (!normalizedTarget) {
    throw new Error(`Invalid target window bounds: ${JSON.stringify(targetBounds)}`);
  }

  const automationResult = await writeUiAutomationWindowBounds(options.client, normalizedTarget);
  if (
    automationResult &&
    boundsWithinTolerance(automationResult, normalizedTarget, options.settleTolerancePx ?? 8)
  ) {
    return automationResult;
  }

  let lastError = null;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    if (attempt > 0) {
      await delay(250);
    }
    await activateBundle(bundleId);
    try {
      const result = await setFrontWindowBoundsForBundle(bundleId, normalizedTarget);
      if (boundsWithinTolerance(result, normalizedTarget, options.settleTolerancePx ?? 8)) {
        return result;
      }
      break;
    } catch (error) {
      lastError = error;
    }
  }

  if (lastError) {
    throw lastError;
  }

  const settleTimeoutMs = options.settleTimeoutMs ?? 6_000;
  const retryIntervalMs = options.retryIntervalMs ?? 150;
  const settleTolerancePx = options.settleTolerancePx ?? 8;
  const startedAt = Date.now();
  let lastBounds = null;

  while (Date.now() - startedAt < settleTimeoutMs) {
    lastBounds = await getFrontWindowBounds(bundleId, options);
    if (boundsWithinTolerance(lastBounds, normalizedTarget, settleTolerancePx)) {
      return lastBounds;
    }
    await delay(retryIntervalMs);
  }

  throw new Error(
    `Timed out waiting for ${bundleId} window bounds to settle at ${JSON.stringify(normalizedTarget)}. ` +
    `Last bounds: ${JSON.stringify(lastBounds)}`
  );
}

export async function captureFrontWindowScreenshot(outputPath, options = {}) {
  const bundleId = options.bundleId || 'com.attn.manager';
  const bounds = await getFrontWindowBounds(bundleId, options);
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });
  await execFileAsync('/usr/sbin/screencapture', [
    '-x',
    '-R',
    `${bounds.x},${bounds.y},${bounds.width},${bounds.height}`,
    outputPath,
  ], {
    timeout: 10_000,
  });
  return {
    source: 'native_window',
    bundleId,
    bounds,
    path: outputPath,
  };
}

export async function getProcessWindowId(pid) {
  if (!Number.isInteger(pid) || pid <= 0) {
    throw new Error(`Invalid native app pid: ${pid}`);
  }
  const { stdout } = await execFileAsync('/usr/bin/xcrun', [
    'swift',
    NATIVE_WINDOW_ID_SOURCE,
    String(pid),
  ], {
    timeout: 20_000,
  });
  const windowId = Number.parseInt(stdout.trim(), 10);
  if (!Number.isInteger(windowId) || windowId <= 0) {
    throw new Error(`Failed to resolve native app window id for pid ${pid}: ${stdout}`);
  }
  return windowId;
}

export async function captureProcessWindowScreenshot(outputPath, pid) {
  const windowId = await getProcessWindowId(pid);
  fs.mkdirSync(path.dirname(outputPath), { recursive: true });
  await execFileAsync('/usr/sbin/screencapture', [
    '-x',
    '-l',
    String(windowId),
    '-o',
    outputPath,
  ], {
    timeout: 10_000,
  });
  return {
    source: 'native_window_id',
    pid,
    windowId,
    path: outputPath,
  };
}
