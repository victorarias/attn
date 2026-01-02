// Mock daemon for testing components that interact with the daemon
// Tracks all calls and allows controlling responses

import type { GitStatusUpdate, FileDiffResult, ReviewState } from '../../hooks/useDaemonSocket';
import type { ReviewComment } from '../../types/generated';

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

  // Record a call
  private recordCall(method: string, args: unknown[]): void {
    // Check strict mode
    if (this.options.strict && !this.expectedCalls.has(method)) {
      throw new Error(`Unexpected call to ${method} in strict mode. Expected: ${[...this.expectedCalls].join(', ')}`);
    }

    // Check max calls
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

  // Get calls, optionally filtered by method
  getCalls(method?: string): Call[] {
    if (method) {
      return this.calls.filter(c => c.method === method);
    }
    return [...this.calls];
  }

  // Clear recorded calls
  clearCalls(): void {
    this.calls = [];
  }

  // Set expected calls for strict mode
  expect(method: string): void {
    this.expectedCalls.add(method);
  }

  // Set a response for a method
  setResponse<T>(method: string, response: ResponseValue<T>): void {
    this.responses.set(method, response);
  }

  // Set delay for a method
  setDelay(method: string, delayMs: number): void {
    this.delays.set(method, delayMs);
  }

  // Get response, applying delay if set
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

  // Create mock functions that match the daemon API
  createFetchDiff(): (path: string, staged: boolean) => Promise<FileDiffResult> {
    return async (path: string, staged: boolean): Promise<FileDiffResult> => {
      this.recordCall('fetchDiff', [path, staged]);
      return this.getResponse<FileDiffResult>('fetchDiff', [path, staged]);
    };
  }

  createGetReviewState(): (repoPath: string, branch: string) => Promise<{ success: boolean; state?: ReviewState; error?: string }> {
    return async (repoPath: string, branch: string) => {
      this.recordCall('getReviewState', [repoPath, branch]);
      return this.getResponse('getReviewState', [repoPath, branch]);
    };
  }

  createMarkFileViewed(): (reviewId: string, filepath: string, viewed: boolean) => Promise<{ success: boolean; error?: string }> {
    return async (reviewId: string, filepath: string, viewed: boolean) => {
      this.recordCall('markFileViewed', [reviewId, filepath, viewed]);
      return this.getResponse('markFileViewed', [reviewId, filepath, viewed]);
    };
  }

  createAddComment(): (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => Promise<{ success: boolean; comment?: ReviewComment }> {
    return async (reviewId: string, filepath: string, lineStart: number, lineEnd: number, content: string) => {
      this.recordCall('addComment', [reviewId, filepath, lineStart, lineEnd, content]);
      return this.getResponse('addComment', [reviewId, filepath, lineStart, lineEnd, content]);
    };
  }

  createUpdateComment(): (commentId: string, content: string) => Promise<{ success: boolean }> {
    return async (commentId: string, content: string) => {
      this.recordCall('updateComment', [commentId, content]);
      return this.getResponse('updateComment', [commentId, content]);
    };
  }

  createResolveComment(): (commentId: string, resolved: boolean) => Promise<{ success: boolean }> {
    return async (commentId: string, resolved: boolean) => {
      this.recordCall('resolveComment', [commentId, resolved]);
      return this.getResponse('resolveComment', [commentId, resolved]);
    };
  }

  createDeleteComment(): (commentId: string) => Promise<{ success: boolean }> {
    return async (commentId: string) => {
      this.recordCall('deleteComment', [commentId]);
      return this.getResponse('deleteComment', [commentId]);
    };
  }

  createGetComments(): (reviewId: string, filepath?: string) => Promise<{ success: boolean; comments?: ReviewComment[] }> {
    return async (reviewId: string, filepath?: string) => {
      this.recordCall('getComments', [reviewId, filepath]);
      return this.getResponse('getComments', [reviewId, filepath]);
    };
  }
}

// Factory function
export function createMockDaemon(options?: MockDaemonOptions): MockDaemon {
  return new MockDaemon(options);
}

// Fixture creators
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

export function createReviewState(viewedFiles: string[] = []): { success: boolean; state: ReviewState } {
  return {
    success: true,
    state: {
      review_id: 'test-review-id',
      repo_path: '/test/repo',
      branch: 'main',
      viewed_files: viewedFiles,
    },
  };
}

/**
 * Create a ReviewComment fixture.
 *
 * For deleted-line comments, use a negative line_end:
 * - line_end = -1: comment after deleted line index 0 (first)
 * - line_end = -2: comment after deleted line index 1 (second)
 * - etc.
 */
export function createReviewComment(overrides: Partial<ReviewComment> = {}): ReviewComment {
  return {
    id: `comment-${Date.now()}-${Math.random().toString(36).slice(2)}`,
    review_id: 'test-review-id',
    filepath: 'src/App.tsx',
    line_start: 10,
    line_end: 10,
    content: 'Test comment',
    author: 'user',
    resolved: false,
    created_at: new Date().toISOString(),
    ...overrides,
  };
}

/**
 * Create a deleted-line comment.
 *
 * @param anchorLine - The line number before the deleted chunk
 * @param deletedLineIndex - The index of the deleted line within the chunk (0-based)
 * @param overrides - Additional overrides
 */
export function createDeletedLineComment(
  anchorLine: number,
  deletedLineIndex: number,
  overrides: Partial<ReviewComment> = {}
): ReviewComment {
  return createReviewComment({
    line_start: anchorLine,
    line_end: -(deletedLineIndex + 1),  // Encode: -1 for index 0, -2 for index 1, etc.
    ...overrides,
  });
}

// Helper to wait for a condition
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

// Helper to ensure no more calls happen
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
