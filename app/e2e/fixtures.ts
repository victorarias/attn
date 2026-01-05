import { test as base, expect } from '@playwright/test';
import { spawn, ChildProcess, execSync } from 'child_process';
import * as http from 'http';
import * as path from 'path';
import * as fs from 'fs';
import * as os from 'os';
import * as net from 'net';

// Mock GitHub Server
class MockGitHubServer {
  private server: http.Server;
  private requests: Array<{ method: string; path: string; body: any }> = [];
  public url = '';
  private prs: Array<{ repo: string; number: number; title: string; role: string; draft: boolean }> = [];

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

  addPR(pr: { repo: string; number: number; title: string; role: 'author' | 'reviewer'; draft?: boolean }) {
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

// Daemon launcher - creates isolated temp directory for DB and socket
async function startDaemon(ghUrl: string): Promise<{ proc: ChildProcess; socketPath: string; tempDir: string; stop: () => void }> {
  // Create temp directory for test isolation
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'attn-e2e-'));
  const socketPath = path.join(tempDir, 'attn.sock');
  const dbPath = path.join(tempDir, 'attn.db');
  const attnPath = path.join(os.homedir(), '.local', 'bin', 'attn');

  console.log(`[E2E] Test isolation: tempDir=${tempDir}, socket=${socketPath}, db=${dbPath}`);

  // Verify attn binary exists
  if (!fs.existsSync(attnPath)) {
    throw new Error(`attn binary not found at ${attnPath}. Run 'make install' first.`);
  }

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
      GITHUB_API_URL: ghUrl,
      GITHUB_TOKEN: 'test-token',
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

  // Wait for socket to appear
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => {
      const error = new Error(
        `Daemon timeout after 5s. stdout: ${stdout}\nstderr: ${stderr}`
      );
      reject(error);
    }, 5000);
    const check = setInterval(() => {
      if (fs.existsSync(socketPath)) {
        clearInterval(check);
        clearTimeout(timeout);
        console.log(`Daemon started with socket at ${socketPath}`);
        resolve();
      }
    }, 100);
  });

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

// Session injection helper
async function injectTestSession(
  socketPath: string,
  session: { id: string; label: string; state: string; directory?: string }
): Promise<void> {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(socketPath, () => {
      const msg = {
        cmd: 'inject_test_session',
        session: {
          id: session.id,
          label: session.label,
          directory: session.directory || '/tmp/test',
          state: session.state,
          state_since: new Date().toISOString(),
          last_seen: new Date().toISOString(),
          todos: null,
          muted: false,
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

  // Create initial commit
  fs.writeFileSync(path.join(repoPath, 'example.go'), 'package main\n\nfunc main() {\n}\n');
  execSync('git add .', { cwd: repoPath, stdio: 'pipe' });
  execSync('git commit -m "Initial commit"', { cwd: repoPath, stdio: 'pipe' });

  // Create uncommitted changes
  fs.writeFileSync(path.join(repoPath, 'example.go'), 'package main\n\nfunc main() {\n\tfmt.Println("Hello")\n}\n');

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
  injectSession: (s: { id: string; label: string; state: string; directory?: string }) => Promise<void>;
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
      // Kill any existing TEST daemons (on test port) to avoid interference
      // Note: We no longer kill all daemons since tests are now isolated with temp dirs
      try {
        await new Promise<void>((resolve) => {
          spawn('pkill', ['-f', `ATTN_WS_PORT=${TEST_DAEMON_PORT}`], { stdio: 'ignore' }).on('close', () => resolve());
        });
        await new Promise(resolve => setTimeout(resolve, 300)); // Wait for cleanup
      } catch (err) {
        // Ignore errors if no daemons running
      }

      daemon = await startDaemon(mockGitHub.url);
      return {
        wsUrl: `ws://127.0.0.1:${TEST_DAEMON_PORT}/ws`,
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
    let daemonInstance: { proc: ChildProcess; socketPath: string; tempDir: string; stop: () => void } | null = null;
    let socketPath = '';

    const fixture: DaemonFixture = {
      start: async () => {
        // Kill any existing TEST daemons (on test port) to avoid interference
        // Note: We no longer kill all daemons since tests are now isolated with temp dirs
        try {
          await new Promise<void>((resolve) => {
            spawn('pkill', ['-f', `ATTN_WS_PORT=${TEST_DAEMON_PORT}`], { stdio: 'ignore' }).on('close', () => resolve());
          });
          await new Promise(resolve => setTimeout(resolve, 300));
        } catch {
          // Ignore
        }

        daemonInstance = await startDaemon(mockGitHub.url);
        socketPath = daemonInstance.socketPath;
        return {
          wsUrl: `ws://127.0.0.1:${TEST_DAEMON_PORT}/ws`,
          socketPath,
        };
      },
      injectSession: async (s) => {
        if (!socketPath) throw new Error('Daemon not started');
        await injectTestSession(socketPath, s);
      },
      updateSessionState: async (id, state) => {
        if (!socketPath) throw new Error('Daemon not started');
        await updateSessionState(socketPath, id, state);
      },
      createTestRepo: async () => {
        return createTestGitRepo();
      },
    };

    await use(fixture);

    if (daemonInstance) {
      daemonInstance.stop();
    }
  },
});

export { expect };
