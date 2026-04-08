import fs from 'node:fs';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

async function runAppleScript(script) {
  const { stdout } = await execFileAsync('osascript', ['-e', script], {
    timeout: 10_000,
  });
  return stdout.trim();
}

async function resolveProcessName(bundleId) {
  try {
    const name = await runAppleScript(`tell application id "${bundleId}" to get name`);
    if (name) {
      return name;
    }
  } catch {
    // Fall back to the default Tauri binary name below.
  }
  return 'app';
}

export async function getFrontWindowBounds(bundleId = 'com.attn.manager') {
  const processName = await resolveProcessName(bundleId);
  const output = await runAppleScript(`
tell application "System Events"
  tell application process "${processName}"
    if (count of windows) is 0 then
      error "No windows found for ${processName}"
    end if
    set p to position of front window
    set s to size of front window
    return (item 1 of p as text) & "," & (item 2 of p as text) & "," & (item 1 of s as text) & "," & (item 2 of s as text)
  end tell
end tell
`);

  const [x, y, width, height] = output
    .split(',')
    .map((value) => Number.parseInt(value.trim(), 10));

  if (![x, y, width, height].every((value) => Number.isFinite(value))) {
    throw new Error(`Failed to parse window bounds for ${bundleId}: ${output}`);
  }

  return { x, y, width, height, processName };
}

export async function captureFrontWindowScreenshot(outputPath, options = {}) {
  const bundleId = options.bundleId || 'com.attn.manager';
  const bounds = await getFrontWindowBounds(bundleId);
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
