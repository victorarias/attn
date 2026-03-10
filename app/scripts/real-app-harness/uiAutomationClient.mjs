import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function defaultManifestPath() {
  return path.join(
    os.homedir(),
    'Library',
    'Application Support',
    'com.attn.manager',
    'debug',
    'ui-automation.json'
  );
}

export class UiAutomationClient {
  constructor({
    appPath = '/Applications/attn.app',
    manifestPath = defaultManifestPath(),
  } = {}) {
    this.appPath = appPath;
    this.manifestPath = manifestPath;
  }

  async launchApp() {
    await execFileAsync('open', ['-a', this.appPath]);
  }

  async waitForManifest(timeoutMs = 15_000) {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const manifest = this.readManifest();
        if (manifest?.enabled && manifest.port && manifest.token) {
          return manifest;
        }
      } catch (error) {
        lastError = error;
      }
      await delay(200);
    }

    throw new Error(
      `Timed out waiting for UI automation manifest at ${this.manifestPath}: ${lastError instanceof Error ? lastError.message : lastError || 'manifest unavailable'}`
    );
  }

  readManifest() {
    return JSON.parse(fs.readFileSync(this.manifestPath, 'utf8'));
  }

  async request(action, payload = {}) {
    const manifest = this.readManifest();
    const requestId = `ui-automation-client-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    const request = {
      id: requestId,
      token: manifest.token,
      action,
      payload,
    };

    return new Promise((resolve, reject) => {
      const socket = net.createConnection({
        host: '127.0.0.1',
        port: manifest.port,
      });

      let buffer = '';
      const cleanup = () => {
        socket.removeAllListeners();
        socket.end();
      };

      socket.on('connect', () => {
        socket.write(`${JSON.stringify(request)}\n`);
      });

      socket.on('data', (chunk) => {
        buffer += chunk.toString();
        const newlineIndex = buffer.indexOf('\n');
        if (newlineIndex === -1) {
          return;
        }
        const line = buffer.slice(0, newlineIndex).trim();
        cleanup();
        try {
          const response = JSON.parse(line);
          if (!response.ok) {
            reject(new Error(response.error || `Automation request failed: ${action}`));
            return;
          }
          resolve(response.result);
        } catch (error) {
          reject(error);
        }
      });

      socket.on('error', (error) => {
        cleanup();
        reject(error);
      });
    });
  }

  async waitForReady(timeoutMs = 20_000) {
    const startedAt = Date.now();
    let lastError = null;

    while (Date.now() - startedAt < timeoutMs) {
      try {
        const result = await this.request('ping');
        if (result?.frontendReady) {
          return result;
        }
      } catch (error) {
        lastError = error;
      }
      await delay(250);
    }

    throw new Error(
      `Timed out waiting for frontend automation readiness: ${lastError instanceof Error ? lastError.message : lastError || 'bridge not ready'}`
    );
  }
}
