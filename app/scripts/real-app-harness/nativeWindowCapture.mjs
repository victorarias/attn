import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

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

  const [x, y, width, height, processName] = output
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
