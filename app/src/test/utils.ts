// Test utilities for rendering components with mock daemon

import {
  MockDaemon,
  createFileDiffResult,
  createBranchDiffFilesResult,
  createReviewState,
} from './mocks/daemon';

export interface RenderWithMockDaemonResult {
  mockDaemon: MockDaemon;
  // Re-export all render results
  [key: string]: unknown;
}

// Default mock responses
export function setupDefaultResponses(mockDaemon: MockDaemon): void {
  // Default fetchDiff - returns empty diff
  mockDaemon.setResponse('fetchDiff', (args: unknown[]) => {
    const [path] = args as [string, { staged?: boolean; baseRef?: string }];
    return {
      ...createFileDiffResult('// original content', '// modified content'),
      path,
    };
  });

  // Default getBranchDiffFiles - returns files matching git status
  mockDaemon.setResponse('getBranchDiffFiles', () =>
    createBranchDiffFilesResult(['src/App.tsx'])
  );

  // Default fetchRemotes - always succeeds
  mockDaemon.setResponse('fetchRemotes', () => ({ success: true }));

  // Default getReviewState - returns empty viewed files
  mockDaemon.setResponse('getReviewState', () => createReviewState([]));

  // Default markFileViewed - always succeeds
  mockDaemon.setResponse('markFileViewed', () => ({ success: true }));
}

// Sleep helper
export function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

// Re-export everything from testing-library
export * from '@testing-library/react';
export { default as userEvent } from '@testing-library/user-event';

// Re-export mock utilities
export {
  MockDaemon,
  createMockDaemon,
  createGitStatus,
  createFileDiffResult,
  createBranchDiffFilesResult,
  createReviewState,
  createReviewComment,
  createDeletedLineComment,
  waitForCalls,
  assertNoMoreCalls,
} from './mocks/daemon';
