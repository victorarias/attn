import { test, expect } from '@playwright/test';
import { spawn, type ChildProcess } from 'child_process';
import * as fs from 'fs';
import * as net from 'net';
import * as os from 'os';
import * as path from 'path';
import { waitForDaemonSocket } from './daemonReadiness';

async function stopChild(proc: ChildProcess): Promise<void> {
  if (proc.exitCode !== null || proc.signalCode !== null) return;
  await new Promise<void>((resolve) => {
    proc.once('exit', () => resolve());
    proc.kill('SIGTERM');
  });
}

test('daemon readiness follows socket creation without a wall-clock poll', async () => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-ready-'));
  const socketPath = path.join(tempDir, 'attn.sock');
  const proc = spawn(process.execPath, ['-e', 'process.stdin.resume()'], { stdio: 'pipe' });
  const server = net.createServer();

  try {
    const ready = waitForDaemonSocket(proc, socketPath);
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject);
      server.listen(socketPath, resolve);
    });
    await expect(ready).resolves.toBeUndefined();
  } finally {
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await stopChild(proc);
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
});

test('daemon readiness reports an early child exit with its diagnostics', async () => {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-ready-'));
  const socketPath = path.join(tempDir, 'attn.sock');
  const proc = spawn(process.execPath, ['-e', 'process.exit(23)']);

  try {
    await expect(waitForDaemonSocket(proc, socketPath, () => 'daemon log: fixture failed'))
      .rejects.toThrow(/code 23[\s\S]*daemon log: fixture failed/);
  } finally {
    await stopChild(proc);
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
});
