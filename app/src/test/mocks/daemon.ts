
import type { GitStatusUpdate, FileDiffResult } from '../../hooks/useDaemonSocket';

interface Call {
  method: string;
  args: unknown[];
  timestamp: number;
}

interface MockDaemonOptions {
  maxCalls?: Record<string, number>;
  strict?: boolean;
}

type ResponseValue<T> = T | ((args: unknown[]) => T) | ((args: unknown[]) => Promise<T>);

export class MockDaemon {
  private calls: Call[] = [];
  private responses: Map<string, ResponseValue<unknown>> = new Map();
  private delays: Map<string, number> = new Map();
  private expectedCalls: Set<string> = new Set();
  private options: MockDaemonOptions;

  constructor(options: MockDaemonOptions = {}) {
    this.options = options;
  }

  private recordCall(method: string, args: unknown[]): void {
    if (this.options.strict && !this.expectedCalls.has(method)) {
      throw new Error(`Unexpected call to ${method} in strict mode. Expected: ${[...this.expectedCalls].join(', ')}`);
    }

    const maxCalls = this.options.maxCalls?.[method];
    if (maxCalls !== undefined) {
      const currentCount = this.getCalls(method).length;
      if (currentCount >= maxCalls) {
        throw new Error(`Max calls exceeded for ${method}: ${currentCount + 1} > ${maxCalls}`);
      }
    }

    this.calls.push({
      method,
      args,
      timestamp: Date.now(),
    });
  }

  getCalls(method?: string): Call[] {
    if (method) {
      return this.calls.filter(c => c.method === method);
    }
    return [...this.calls];
  }

  clearCalls(): void {
    this.calls = [];
  }

  expect(method: string): void {
    this.expectedCalls.add(method);
  }

  setResponse<T>(method: string, response: ResponseValue<T>): void {
    this.responses.set(method, response);
  }

  setDelay(method: string, delayMs: number): void {
    this.delays.set(method, delayMs);
  }

  private async getResponse<T>(method: string, args: unknown[]): Promise<T> {
    const delay = this.delays.get(method);
    if (delay) {
      await new Promise(resolve => setTimeout(resolve, delay));
    }

    const response = this.responses.get(method);
    if (response === undefined) {
      throw new Error(`No response configured for ${method}`);
    }

    if (typeof response === 'function') {
      return await (response as (args: unknown[]) => T | Promise<T>)(args);
    }
    return response as T;
  }

  createRequest<T>(method: string): (...args: unknown[]) => Promise<T> {
    return async (...args: unknown[]) => {
      this.recordCall(method, args);
      return this.getResponse<T>(method, args);
    };
  }

  createFetchDiff(): (path: string, options?: { staged?: boolean; baseRef?: string }) => Promise<FileDiffResult> {
    return async (path: string, options?: { staged?: boolean; baseRef?: string }): Promise<FileDiffResult> => {
      this.recordCall('fetchDiff', [path, options]);
      return this.getResponse<FileDiffResult>('fetchDiff', [path, options]);
    };
  }

  createFetchRemotes(): (repo: string) => Promise<{ success: boolean; error?: string }> {
    return async (repo: string): Promise<{ success: boolean; error?: string }> => {
      this.recordCall('fetchRemotes', [repo]);
      return this.getResponse<{ success: boolean; error?: string }>('fetchRemotes', [repo]);
    };
  }
}

export function createMockDaemon(options?: MockDaemonOptions): MockDaemon {
  return new MockDaemon(options);
}

export function createGitStatus(files: string[], options?: {
  staged?: boolean;
  status?: string;
  additions?: number;
  deletions?: number;
}): GitStatusUpdate {
  const { staged = false, status = 'modified', additions = 10, deletions = 5 } = options || {};

  const fileObjects = files.map(path => ({
    path,
    status,
    additions,
    deletions,
  }));

  return {
    directory: '/test/repo',
    staged: staged ? fileObjects : [],
    unstaged: staged ? [] : fileObjects,
    untracked: [],
  };
}

export function createFileDiffResult(original: string, modified: string): FileDiffResult {
  return {
    success: true,
    original,
    modified,
  };
}

export async function waitForCalls(
  mock: MockDaemon,
  method: string,
  count: number,
  timeoutMs: number = 1000
): Promise<void> {
  const start = Date.now();
  while (mock.getCalls(method).length < count) {
    if (Date.now() - start > timeoutMs) {
      throw new Error(`Timeout waiting for ${count} calls to ${method}. Got ${mock.getCalls(method).length}`);
    }
    await new Promise(resolve => setTimeout(resolve, 10));
  }
}

export async function assertNoMoreCalls(
  mock: MockDaemon,
  method: string,
  waitMs: number = 100
): Promise<void> {
  const initialCount = mock.getCalls(method).length;
  await new Promise(resolve => setTimeout(resolve, waitMs));
  const finalCount = mock.getCalls(method).length;
  if (finalCount !== initialCount) {
    throw new Error(`Expected no more calls to ${method}, but got ${finalCount - initialCount} additional calls`);
  }
}
