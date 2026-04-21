import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);
const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));
const INPUT_DRIVER_SOURCE = path.join(SCRIPT_DIR, 'InputDriver.swift');
const INPUT_DRIVER_BUILD_DIR = path.join(SCRIPT_DIR, '.build');
const INPUT_DRIVER_BINARY = path.join(INPUT_DRIVER_BUILD_DIR, 'attn-real-input-driver');

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export class MacOSDriver {
  constructor({
    bundleId = 'com.attn.manager',
    appPath = path.join(os.homedir(), 'Applications', 'attn.app'),
    actionDelayMs = 250,
  } = {}) {
    this.bundleId = bundleId;
    this.appPath = appPath;
    this.actionDelayMs = actionDelayMs;
  }

  async launchApp() {
    await execFileAsync('open', ['-a', this.appPath]);
    await delay(800);
  }

  async openDeepLink(url) {
    await execFileAsync('open', [url]);
    await delay(500);
  }

  async activateApp() {
    await this.runInputDriver(['activate']);
    await delay(this.actionDelayMs);
  }

  async activateBackground() {
    // Verify the app is running without changing frontmost. Silent success
    // means the target process is resolvable via AX; no HID tap is engaged.
    await this.runInputDriver(['activate_background']);
    await delay(this.actionDelayMs);
  }

  async menu(pathSegments) {
    const path = Array.isArray(pathSegments) ? pathSegments : [pathSegments];
    if (path.length === 0) {
      throw new Error('MacOSDriver.menu requires at least one path segment');
    }
    await this.runInputDriver([
      'menu',
      '--path',
      path.join('>'),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async frontmostBundleId() {
    return this.runInputDriverCapture(['frontmost']);
  }

  // Returns the CGWindowID of the driver's bundle's largest layer-0 onscreen
  // window, or null if no such window exists. Reliable gate for "attn's window
  // has been created": System Events returns 0 for Tauri/wry apps even when a
  // window is visible, so this path uses CGWindowListCopyWindowInfo instead.
  async mainWindowId() {
    try {
      const value = await this.runInputDriverCapture(['windowid']);
      const parsed = Number.parseInt(value, 10);
      return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
    } catch {
      return null;
    }
  }

  async waitForMainWindow(timeoutMs = 10_000, pollIntervalMs = 150) {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const wid = await this.mainWindowId();
      if (wid) {
        return wid;
      }
      await delay(pollIntervalMs);
    }
    return null;
  }

  async typeText(text) {
    await this.runInputDriver(['text', '--text', text, '--prompt-accessibility']);
    await delay(this.actionDelayMs);
  }

  async pressKey(key, modifiers = {}) {
    await this.runInputDriver([
      'key',
      '--key',
      key,
      '--modifiers',
      this.serializeModifiers(modifiers),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async pressKeyCode(keyCode, modifiers = {}) {
    await this.runInputDriver([
      'keycode',
      '--key-code',
      String(keyCode),
      '--modifiers',
      this.serializeModifiers(modifiers),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async pressEnter() {
    await this.pressKeyCode(36);
  }

  async clickWindow(relativeX, relativeY) {
    await this.runInputDriver([
      'click',
      '--relative-x',
      String(relativeX),
      '--relative-y',
      String(relativeY),
      '--prompt-accessibility',
    ]);
    await delay(this.actionDelayMs);
  }

  async screenshot(outputPath) {
    await execFileAsync('/usr/sbin/screencapture', ['-x', outputPath]);
  }

  serializeModifiers(modifiers = {}) {
    const names = [];
    if (modifiers.command) names.push('command');
    if (modifiers.option) names.push('option');
    if (modifiers.shift) names.push('shift');
    if (modifiers.control) names.push('control');
    return names.join(',');
  }

  async ensureInputDriver() {
    fs.mkdirSync(INPUT_DRIVER_BUILD_DIR, { recursive: true });
    const binaryExists = fs.existsSync(INPUT_DRIVER_BINARY);
    const sourceMtime = fs.statSync(INPUT_DRIVER_SOURCE).mtimeMs;
    const binaryMtime = binaryExists ? fs.statSync(INPUT_DRIVER_BINARY).mtimeMs : 0;
    if (binaryExists && binaryMtime >= sourceMtime) {
      return INPUT_DRIVER_BINARY;
    }
    await execFileAsync('/usr/bin/swiftc', [INPUT_DRIVER_SOURCE, '-o', INPUT_DRIVER_BINARY], {
      timeout: 30_000,
    });
    return INPUT_DRIVER_BINARY;
  }

  async runInputDriver(args) {
    const binaryPath = await this.ensureInputDriver();
    const fullArgs = ['--bundle-id', this.bundleId, ...args];
    try {
      await execFileAsync(binaryPath, fullArgs, {
        timeout: 5_000,
      });
    } catch (error) {
      const stderr = error?.stderr?.toString?.() || '';
      const hint = stderr.includes('Accessibility permission is required')
        ? 'Grant Accessibility access to the attn real-app input driver when macOS prompts, then rerun the harness.'
        : 'macOS automation failed.';
      throw new Error(`${hint}\n${stderr || error.message}`);
    }
  }

  async runInputDriverCapture(args) {
    const binaryPath = await this.ensureInputDriver();
    const fullArgs = ['--bundle-id', this.bundleId, ...args];
    try {
      const { stdout } = await execFileAsync(binaryPath, fullArgs, {
        timeout: 5_000,
      });
      return stdout.toString().trim();
    } catch (error) {
      const stderr = error?.stderr?.toString?.() || '';
      throw new Error(`macOS input driver capture failed: ${stderr || error.message}`);
    }
  }
}

export { delay };
