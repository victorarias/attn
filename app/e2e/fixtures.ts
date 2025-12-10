import { test as base, expect } from '@playwright/test';
import { spawn, ChildProcess } from 'child_process';
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
  private prs: Array<{ repo: string; number: number; title: string; role: string }> = [];

  constructor() {
    this.server = http.createServer((req, res) => {
      let body = '';
      req.on('data', (c) => (body += c));
      req.on('end', () => {
        const parsed = body ? JSON.parse(body) : {};
        this.requests.push({ method: req.method!, path: req.url!, body: parsed });

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
              draft: false,
              repository_url: `https://api.github.com/repos/${pr.repo}`,
            }));

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

  addPR(pr: { repo: string; number: number; title: string; role: 'author' | 'reviewer' }) {
    this.prs.push(pr);
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

// Daemon launcher
async function startDaemon(ghUrl: string): Promise<{ proc: ChildProcess; socketPath: string; stop: () => void }> {
  const socketPath = path.join(os.tmpdir(), `cm-e2e-${Date.now()}.sock`);
  const cmPath = path.join(os.homedir(), '.local', 'bin', 'cm');

  // Verify cm binary exists
  if (!fs.existsSync(cmPath)) {
    throw new Error(`cm binary not found at ${cmPath}. Run 'make install' first.`);
  }

  const proc = spawn(cmPath, ['--daemon'], {
    env: {
      ...process.env,
      CM_SOCKET: socketPath,
      CM_WS_PORT: '29849',
      GITHUB_API_URL: ghUrl,
      GITHUB_TOKEN: 'test-token',
    },
    stdio: 'pipe',
  });

  // Wait for socket to appear
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('Daemon timeout after 5s')), 5000);
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
    stop() {
      proc.kill();
      try {
        fs.unlinkSync(socketPath);
      } catch {}
      console.log('Daemon stopped');
    },
  };
}

// Export fixtures
type Fixtures = {
  mockGitHub: MockGitHubServer;
  daemonInfo: { wsUrl: string; socketPath: string; stop: () => void };
};

export const test = base.extend<Fixtures>({
  mockGitHub: async ({}, use) => {
    const mock = new MockGitHubServer();
    await mock.start();
    await use(mock);
    mock.close();
  },

  daemonInfo: async ({ mockGitHub }, use) => {
    const daemon = await startDaemon(mockGitHub.url);
    await use({
      wsUrl: 'ws://127.0.0.1:29849/ws',
      socketPath: daemon.socketPath,
      stop: daemon.stop,
    });
    daemon.stop();
  },
});

export { expect };
