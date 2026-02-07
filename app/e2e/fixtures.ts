import { test as base, expect } from '@playwright/test';
import { spawn, ChildProcess, execSync } from 'child_process';
import * as http from 'http';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import * as net from 'net';
import { fileURLToPath } from 'url';

// Mock GitHub Server
class MockGitHubServer {
  private server: http.Server;
  private requests: Array<{ method: string; path: string; body: any }> = [];
  public url = '';
  private prs: Array<{ repo: string; number: number; title: string; role: string; draft: boolean; author?: string }> = [];

  constructor() {
    this.server = http.createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        const parsed = body ? JSON.parse(body) : {};
        this.requests.push({ method: req.method!, path: req.url!, body: parsed });
        console.log(`[MockGH] ${req.method} ${req.url}`);

        res.setHeader('Content-Type', 'application/json');

        // Handle search
        if (req.url?.startsWith('/search/issues')) {
          const q = new URL(`http://x${req.url}`).searchParams.get('q') || '';
          const isAuthor = q.includes('author:@me');
          const isReview = q.includes('review-requested:@me');

          const items = this.prs
            .filter((pr) => (isAuthor && pr.role === 'author') || (isReview && pr.role === 'reviewer'))
            .map((pr) => ({
              number: pr.number,
              title: pr.title,
              html_url: `https://github.com/${pr.repo}/pull/${pr.number}`,
              draft: pr.draft,
              repository_url: `https://api.github.com/repos/${pr.repo}`,
              user: { login: pr.author || 'test-user' },
            }));

          console.log(`[MockGH] Returning ${items.length} PRs for query: ${q}`);
          res.end(JSON.stringify({ total_count: items.length, items }));
          return;
        }

        // Handle approve (POST /repos/:owner/:repo/pulls/:number/reviews)
        if (req.url?.match(/\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/reviews$/) && req.method === 'POST') {
          res.end(JSON.stringify({ id: 1, state: 'APPROVED' }));
          return;
        }

        // Handle merge (PUT /repos/:owner/:repo/pulls/:number/merge)
        if (req.url?.match(/\/repos\/[^/]+\/[^/]+\/pulls\/\d+\/merge$/) && req.method === 'PUT') {
          res.end(JSON.stringify({ merged: true }));
          return;
        }

        res.statusCode = 404;
        res.end(JSON.stringify({ message: 'Not Found' }));
      });
    });
  }

  async start(): Promise<void> {
    return new Promise((resolve) => {
      this.server.listen(9999, '127.0.0.1', () => {
        const addr = this.server.address() as net.AddressInfo;
        this.url = `http://127.0.0.1:${addr.port}`;
        console.log(`Mock GitHub server started at ${this.url}`);
        resolve();
      });
    });
  }

  addPR(pr: { repo: string; number: number; title: string; role: 'author' | 'reviewer'; draft?: boolean; author?: string }) {
    this.prs.push({ ...pr, draft: pr.draft ?? false });
  }

  hasApproveRequest(repo: string, number: number): boolean {
    return this.requests.some(
      (r) => r.method === 'POST' && r.path === `/repos/${repo}/pulls/${number}/reviews` && r.body.event === 'APPROVE'
    );
  }

  hasMergeRequest(repo: string, number: number): boolean {
    return this.requests.some((r) => r.method === 'PUT' && r.path === `/repos/${repo}/pulls/${number}/merge`);
  }

  reset() {
    this.requests = [];
    this.prs = [];
  }

  close() {
    this.server.close();
  }
}

// Test port - different from production (9849) to avoid conflicts
const TEST_DAEMON_PORT = '19849';
const MOCK_GH_HOST = 'mock.github.local';
const E2E_DIR = path.dirname(fileURLToPath(import.meta.url));
const TEST_DAEMON_WS_URL = `ws://127.0.0.1:${TEST_DAEMON_PORT}/ws`;

function resolveAttnBinaryPath(): string {
  const candidates = [
    process.env.ATTN_E2E_BIN,
    path.resolve(E2E_DIR, '../../attn'),
    path.join(os.homedir(), '.local', 'bin', 'attn'),
  ].filter((candidate): candidate is string => Boolean(candidate));

  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }

  throw new Error(
    `attn binary not found. Tried: ${candidates.join(', ')}. ` +
      `Set ATTN_E2E_BIN or build binary with 'go build -o ./attn ./cmd/attn'.`
  );
}

async function killTestDaemons(): Promise<void> {
  try {
    await new Promise<void>((resolve) => {
      spawn('pkill', ['-f', `ATTN_WS_PORT=${TEST_DAEMON_PORT}`], { stdio: 'ignore' }).on('close', () => resolve());
    });
    await new Promise((resolve) => setTimeout(resolve, 300));
  } catch {
    // Ignore when no test daemons are running.
  }
}

async function waitForSocket(
  socketPath: string,
  timeoutMs: number,
  getDebugInfo?: () => string
): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      const suffix = getDebugInfo ? ` ${getDebugInfo()}` : '';
      reject(new Error(`Daemon timeout after ${timeoutMs}ms.${suffix}`));
    }, timeoutMs);
    const check = setInterval(() => {
      if (fs.existsSync(socketPath)) {
        clearInterval(check);
        clearTimeout(timeout);
        resolve();
      }
    }, 100);
  });
}

// Daemon launcher - creates isolated temp directory for DB and socket
async function startDaemon(ghUrl: string): Promise<{ proc: ChildProcess; socketPath: string; tempDir: string; stop: () => void }> {
  // Create temp directory for test isolation
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-e2e-'));
  const socketPath = path.join(tempDir, 'attn.sock');
  const dbPath = path.join(tempDir, 'attn.db');
  const attnPath = resolveAttnBinaryPath();

  console.log(`[E2E] Test isolation: tempDir=${tempDir}, socket=${socketPath}, db=${dbPath}`);
  console.log(`[E2E] Using daemon binary: ${attnPath}`);

  // Clean up any existing socket (shouldn't exist in temp dir, but just in case)
  if (fs.existsSync(socketPath)) {
    try {
      fs.unlinkSync(socketPath);
    } catch (err) {
      console.warn(`Failed to remove existing socket: ${err}`);
    }
  }

  const proc = spawn(attnPath, ['daemon'], {
    env: {
      ...process.env,
      ATTN_WS_PORT: TEST_DAEMON_PORT, // Use test port to avoid conflicts with production daemon
      ATTN_SOCKET_PATH: socketPath, // Test isolation: separate socket
      ATTN_DB_PATH: dbPath, // Test isolation: separate database
      ATTN_MOCK_REVIEWER: '1', // Use mock reviewer for predictable E2E tests
      ATTN_MOCK_GH_URL: ghUrl,
      ATTN_MOCK_GH_TOKEN: 'test-token',
      ATTN_MOCK_GH_HOST: MOCK_GH_HOST,
    },
    stdio: 'pipe',
  });

  // Capture output for debugging
  let stdout = '';
  let stderr = '';
  proc.stdout?.on('data', (data) => {
    stdout += data.toString();
    console.log('[Daemon stdout]', data.toString().trim());
  });
  proc.stderr?.on('data', (data) => {
    stderr += data.toString();
    console.error('[Daemon stderr]', data.toString().trim());
  });
  proc.on('exit', (code, signal) => {
    console.log(`[Daemon] Process exited with code ${code}, signal ${signal}`);
  });

  await waitForSocket(socketPath, 5000, () => `stdout: ${stdout}\nstderr: ${stderr}`);
  console.log(`Daemon started with socket at ${socketPath}`);

  return {
    proc,
    socketPath,
    tempDir,
    stop() {
      proc.kill();
      // Clean up temp directory and all contents
      try {
        fs.rmSync(tempDir, { recursive: true, force: true });
        console.log(`[E2E] Cleaned up temp dir: ${tempDir}`);
      } catch (err) {
        console.warn(`[E2E] Failed to cleanup temp dir: ${err}`);
      }
      console.log('Daemon stopped');
    },
  };
}

interface ManagedDaemon {
  socketPath: string;
  dbPath: string;
  tempDir: string;
  start: () => Promise<void>;
  stop: () => Promise<void>;
  restart: () => Promise<void>;
  cleanup: () => Promise<void>;
}

function createManagedDaemon(ghUrl: string): ManagedDaemon {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-e2e-managed-'));
  const socketPath = path.join(tempDir, 'attn.sock');
  const dbPath = path.join(tempDir, 'attn.db');
  const attnPath = resolveAttnBinaryPath();
  let proc: ChildProcess | null = null;
  let stdout = '';
  let stderr = '';

  const start = async () => {
    if (proc && proc.exitCode === null && proc.signalCode === null) {
      return;
    }

    if (fs.existsSync(socketPath)) {
      try {
        fs.unlinkSync(socketPath);
      } catch {
        // best-effort cleanup
      }
    }

    stdout = '';
    stderr = '';
    proc = spawn(attnPath, ['daemon'], {
      env: {
        ...process.env,
        ATTN_WS_PORT: TEST_DAEMON_PORT,
        ATTN_SOCKET_PATH: socketPath,
        ATTN_DB_PATH: dbPath,
        ATTN_MOCK_REVIEWER: '1',
        ATTN_MOCK_GH_URL: ghUrl,
        ATTN_MOCK_GH_TOKEN: 'test-token',
        ATTN_MOCK_GH_HOST: MOCK_GH_HOST,
      },
      stdio: 'pipe',
    });

    proc.stdout?.on('data', (data) => {
      const text = data.toString();
      stdout += text;
      console.log('[Managed daemon stdout]', text.trim());
    });
    proc.stderr?.on('data', (data) => {
      const text = data.toString();
      stderr += text;
      console.error('[Managed daemon stderr]', text.trim());
    });
    proc.on('exit', (code, signal) => {
      console.log(`[Managed daemon] exited with code ${code}, signal ${signal}`);
      proc = null;
    });

    await waitForSocket(socketPath, 5000, () => `stdout: ${stdout}\nstderr: ${stderr}`);
    console.log(`[Managed daemon] started with socket ${socketPath}`);
  };

  const stop = async () => {
    if (!proc) {
      return;
    }
    const runningProc = proc;
    await new Promise<void>((resolve) => {
      let done = false;
      const finish = () => {
        if (!done) {
          done = true;
          resolve();
        }
      };
      runningProc.once('exit', () => finish());
      runningProc.kill('SIGTERM');
      setTimeout(() => {
        if (runningProc.exitCode === null && runningProc.signalCode === null) {
          runningProc.kill('SIGKILL');
        }
      }, 1500);
      setTimeout(() => finish(), 3000);
    });
    proc = null;
    if (fs.existsSync(socketPath)) {
      try {
        fs.unlinkSync(socketPath);
      } catch {
        // best-effort cleanup
      }
    }
  };

  const restart = async () => {
    await stop();
    await start();
  };

  const cleanup = async () => {
    await stop();
    try {
      fs.rmSync(tempDir, { recursive: true, force: true });
      console.log(`[Managed daemon] cleaned up temp dir ${tempDir}`);
    } catch (err) {
      console.warn(`[Managed daemon] failed to cleanup temp dir: ${err}`);
    }
  };

  return {
    socketPath,
    dbPath,
    tempDir,
    start,
    stop,
    restart,
    cleanup,
  };
}

// Session injection helper
async function injectTestSession(
  socketPath: string,
  session: {
    id: string;
    label: string;
    agent?: 'codex' | 'claude';
    state: string;
    directory?: string;
    is_worktree?: boolean;
    branch?: string;
    main_repo?: string;
  }
): Promise<void> {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(socketPath, () => {
      const msg = {
        cmd: 'inject_test_session',
        session: {
          id: session.id,
          label: session.label,
          agent: session.agent || 'codex',
          directory: session.directory || '/tmp/test',
          state: session.state,
          state_since: new Date().toISOString(),
          last_seen: new Date().toISOString(),
          todos: null,
          muted: false,
          ...(session.is_worktree !== undefined ? { is_worktree: session.is_worktree } : {}),
          ...(session.branch ? { branch: session.branch } : {}),
          ...(session.main_repo ? { main_repo: session.main_repo } : {}),
        },
      };
      client.write(JSON.stringify(msg));
    });
    client.on('data', () => {
      client.end();
      resolve();
    });
    client.on('error', reject);
  });
}

// Session state update helper
async function updateSessionState(
  socketPath: string,
  id: string,
  state: string
): Promise<void> {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(socketPath, () => {
      client.write(JSON.stringify({ cmd: 'state', id, state }));
    });
    client.on('data', () => {
      client.end();
      resolve();
    });
    client.on('error', reject);
  });
}

// Create a temporary git repo with uncommitted changes for testing
async function createTestGitRepo(): Promise<{ repoPath: string; cleanup: () => void }> {
  const repoPath = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-test-repo-'));

  // Initialize git repo
  execSync('git init', { cwd: repoPath, stdio: 'pipe' });
  execSync('git config user.email "test@test.com"', { cwd: repoPath, stdio: 'pipe' });
  execSync('git config user.name "Test User"', { cwd: repoPath, stdio: 'pipe' });

  // Create initial commit with a larger file (50+ lines for scroll testing)
  const initialContent = `package main

import "fmt"

func main() {
	// Line 6
	fmt.Println("Start")
	// Line 8
	// Line 9
	// Line 10
	doSomething()
	// Line 12
	// Line 13
	// Line 14
	// Line 15
	// Line 16
	// Line 17
	// Line 18
	// Line 19
	// Line 20
	// Line 21
	// Line 22
	// Line 23
	// Line 24
	// Line 25
	// Line 26
	// Line 27
	// Line 28
	// Line 29
	// Line 30
	// Line 31
	// Line 32
	// Line 33
	// Line 34
	// Line 35
	// Line 36
	// Line 37
	// Line 38
	// Line 39
	// Line 40 - target for scroll test
	fmt.Println("End")
}

func doSomething() {
	// Helper function
}
`;
  fs.writeFileSync(path.join(repoPath, 'example.go'), initialContent);
  execSync('git add .', { cwd: repoPath, stdio: 'pipe' });
  execSync('git commit -m "Initial commit"', { cwd: repoPath, stdio: 'pipe' });

  // Create uncommitted changes - modify line 40
  const modifiedContent = initialContent.replace(
    '// Line 40 - target for scroll test',
    '// Line 40 - MODIFIED for review'
  );
  fs.writeFileSync(path.join(repoPath, 'example.go'), modifiedContent);

  return {
    repoPath,
    cleanup: () => {
      try {
        fs.rmSync(repoPath, { recursive: true, force: true });
      } catch (err) {
        console.warn(`Failed to cleanup test repo: ${err}`);
      }
    },
  };
}

// Export fixtures
type DaemonFixture = {
  start: () => Promise<{ wsUrl: string; socketPath: string }>;
  stop: () => Promise<void>;
  restart: () => Promise<void>;
  injectSession: (s: {
    id: string;
    label: string;
    agent?: 'codex' | 'claude';
    state: string;
    directory?: string;
    is_worktree?: boolean;
    branch?: string;
    main_repo?: string;
  }) => Promise<void>;
  updateSessionState: (id: string, state: string) => Promise<void>;
  createTestRepo: () => Promise<{ repoPath: string; cleanup: () => void }>;
};

type Fixtures = {
  mockGitHub: MockGitHubServer;
  startDaemonWithPRs: () => Promise<{ wsUrl: string; socketPath: string }>;
  daemon: DaemonFixture;
};

export const test = base.extend<Fixtures>({
  mockGitHub: async ({}, use) => {
    const mock = new MockGitHubServer();
    await mock.start();
    // Reset PRs before each test
    mock.reset();
    await use(mock);
    mock.close();
  },

  // This fixture returns a function that test code calls AFTER adding PRs
  startDaemonWithPRs: async ({ mockGitHub }, use) => {
    let daemon: { proc: ChildProcess; socketPath: string; tempDir: string; stop: () => void } | null = null;

    const startFn = async () => {
      await killTestDaemons();

      daemon = await startDaemon(mockGitHub.url);
      return {
        wsUrl: TEST_DAEMON_WS_URL,
        socketPath: daemon.socketPath,
      };
    };

    await use(startFn);

    // Cleanup after test
    if (daemon) {
      daemon.stop();
    }
  },

  // Session testing fixture with injection helpers
  daemon: async ({ mockGitHub }, use) => {
    const managed = createManagedDaemon(mockGitHub.url);
    let started = false;

    const fixture: DaemonFixture = {
      start: async () => {
        await killTestDaemons();
        await managed.start();
        started = true;
        return {
          wsUrl: TEST_DAEMON_WS_URL,
          socketPath: managed.socketPath,
        };
      },
      stop: async () => {
        if (!started) {
          return;
        }
        await managed.stop();
        started = false;
      },
      restart: async () => {
        if (!started) {
          await fixture.start();
          return;
        }
        await managed.restart();
      },
      injectSession: async (s) => {
        if (!started) throw new Error('Daemon not started');
        await injectTestSession(managed.socketPath, s);
      },
      updateSessionState: async (id, state) => {
        if (!started) throw new Error('Daemon not started');
        await updateSessionState(managed.socketPath, id, state);
      },
      createTestRepo: async () => {
        return createTestGitRepo();
      },
    };

    await use(fixture);

    await managed.cleanup();
  },
});

export { expect };
