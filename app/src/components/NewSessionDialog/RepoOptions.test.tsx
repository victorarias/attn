import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '../../test/utils';
import { RepoOptions } from './RepoOptions';

const repoInfo = {
  repo: '/tmp/repo',
  currentBranch: 'main',
  currentCommitHash: 'abcdef1234567890',
  currentCommitTime: '2026-04-03T18:00:00Z',
  defaultBranch: 'main',
  worktrees: [],
  branches: [],
};

describe('RepoOptions', () => {
  it('selects the main repo when the row is clicked', () => {
    const onSelectMainRepo = vi.fn();
    render(
      <RepoOptions
        repoInfo={repoInfo}
        onSelectMainRepo={onSelectMainRepo}
        onSelectWorktree={vi.fn()}
        onSelectBranch={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('repo-option-0'));

    expect(onSelectMainRepo).toHaveBeenCalledTimes(1);
  });
});
