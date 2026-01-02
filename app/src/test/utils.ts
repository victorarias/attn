// Test utilities for rendering components with mock daemon

import { render, type RenderOptions } from '@testing-library/react';
import type { ReactElement } from 'react';
import {
  MockDaemon,
  createMockDaemon,
  createFileDiffResult,
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
    const [path] = args as [string, boolean];
    return {
      ...createFileDiffResult('// original content', '// modified content'),
      path,
    };
  });

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
  createReviewState,
  waitForCalls,
  assertNoMoreCalls,
} from './mocks/daemon';
