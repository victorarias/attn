import { renderHook, act } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useOpenPR, type OpenPRProgressStep } from './useOpenPR';
import type { DaemonPR } from './useDaemonSocket';
import { PRRole } from '../types/generated';

function buildPR(overrides: Partial<DaemonPR> = {}): DaemonPR {
  return {
    approved_by_me: false,
    author: 'octo',
    details_fetched: true,
    has_new_changes: false,
    head_branch: 'feature/slow-pr',
    host: 'github.com',
    id: 'pr-1',
    last_polled: '2026-05-16T00:00:00Z',
    last_updated: '2026-05-16T00:00:00Z',
    muted: false,
    number: 42,
    reason: 'review_requested',
    repo: 'acme/widgets',
    role: PRRole.Reviewer,
    state: 'open',
    title: 'Make widgets faster',
    url: 'https://github.com/acme/widgets/pull/42',
    ...overrides,
  };
}

describe('useOpenPR', () => {
  it('reports launcher progress for each blocking git/session step', async () => {
    const progress: OpenPRProgressStep[] = [];
    const sendFetchPRDetails = vi.fn();
    const sendEnsureRepo = vi.fn(async () => ({ success: true }));
    const sendCreateWorktreeFromBranch = vi.fn(async () => ({ success: true, path: '/projects/widgets--feature-slow-pr' }));
    const createSession = vi.fn(async () => 'session-1');
    const { result } = renderHook(() => useOpenPR({
      settings: { projects_directory: '/projects' },
      sendFetchPRDetails,
      sendEnsureRepo,
      sendCreateWorktreeFromBranch,
      createSession,
    }));

    let openResult: Awaited<ReturnType<typeof result.current>>;
    await act(async () => {
      openResult = await result.current(buildPR(), 'codex', {
        onProgress: (next) => progress.push(next.step),
      });
    });

    expect(openResult!).toMatchObject({ success: true, sessionId: 'session-1' });
    expect(progress).toEqual(['ensuring_repo', 'creating_worktree', 'starting_session']);
    expect(sendFetchPRDetails).not.toHaveBeenCalled();
  });

  it('reports PR detail progress before repo sync when branch data is missing', async () => {
    const progress: OpenPRProgressStep[] = [];
    const sendFetchPRDetails = vi.fn(async () => ({
      success: true,
      prs: [buildPR({ head_branch: 'feature/from-details' })],
    }));
    const sendEnsureRepo = vi.fn(async () => ({ success: true }));
    const sendCreateWorktreeFromBranch = vi.fn(async () => ({ success: true, path: '/projects/widgets--feature-from-details' }));
    const createSession = vi.fn(async () => 'session-1');
    const { result } = renderHook(() => useOpenPR({
      settings: { projects_directory: '/projects' },
      sendFetchPRDetails,
      sendEnsureRepo,
      sendCreateWorktreeFromBranch,
      createSession,
    }));

    await act(async () => {
      await result.current(buildPR({ head_branch: undefined }), 'codex', {
        onProgress: (next) => progress.push(next.step),
      });
    });

    expect(progress).toEqual(['fetching_pr_details', 'ensuring_repo', 'creating_worktree', 'starting_session']);
    expect(sendEnsureRepo).toHaveBeenCalledWith('/projects/widgets', 'https://github.com/acme/widgets.git');
    expect(sendCreateWorktreeFromBranch).toHaveBeenCalledWith('/projects/widgets', 'origin/feature/from-details');
  });
});
