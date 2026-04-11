import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '../../test/utils';
import { RepoOptions } from './RepoOptions';

const repoInfo = {
  repo: '/tmp/repo',
  currentBranch: 'main',
  currentCommitHash: 'abcdef1234567890',
  currentCommitTime: '2026-04-03T18:00:00Z',
  defaultBranch: 'main',
  worktrees: [{ path: '/tmp/repo--feature', branch: 'feature' }],
};

describe('RepoOptions', () => {
  it('selects the main repo when the row is clicked', () => {
    const onSelectMainRepo = vi.fn();
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={onSelectMainRepo}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('repo-option-0'));

    expect(onSelectMainRepo).toHaveBeenCalledTimes(1);
  });

  it('preselects the matching worktree row and does not render branch rows', () => {
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    expect(screen.getByTestId('repo-option-0')).toHaveAttribute('data-option-kind', 'main-repo');
    expect(screen.getByTestId('repo-option-1')).toHaveAttribute('data-option-kind', 'worktree');
    expect(screen.getByTestId('repo-option-2')).toHaveAttribute('data-option-kind', 'new-worktree');
  });

  it('keeps committed selection stable when hovering another destination', () => {
    const onSelectMainRepo = vi.fn();
    const onSelectWorktree = vi.fn();
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={onSelectMainRepo}
        onSelectWorktree={onSelectWorktree}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.mouseEnter(screen.getByTestId('repo-option-1'));
    expect(screen.getByTestId('repo-option-0')).toHaveClass('selected');
    expect(screen.getByTestId('repo-option-1')).not.toHaveClass('selected');

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    expect(onSelectMainRepo).toHaveBeenCalledTimes(1);
    expect(onSelectWorktree).not.toHaveBeenCalled();
  });

  it('keeps adjacent selection after deleting a worktree', async () => {
    function Wrapper() {
      const [selectedPath, setSelectedPath] = React.useState('/tmp/repo--feature-b');
      const [currentRepoInfo, setCurrentRepoInfo] = React.useState({
        ...repoInfo,
        worktrees: [
          { path: '/tmp/repo--feature-a', branch: 'feature-a' },
          { path: '/tmp/repo--feature-b', branch: 'feature-b' },
        ],
      });

      return (
        <RepoOptions
          repoInfo={currentRepoInfo}
          selectedPath={selectedPath}
          onSelectedPathChange={setSelectedPath}
          onSelectMainRepo={vi.fn()}
          onSelectWorktree={vi.fn()}
          onCreateWorktree={vi.fn(async () => {})}
          onDeleteWorktree={vi.fn(async (path: string) => {
            setCurrentRepoInfo((prev) => ({
              ...prev,
              worktrees: prev.worktrees.filter((worktree) => worktree.path !== path),
            }));
          })}
          onRefresh={vi.fn()}
          onBack={vi.fn()}
        />
      );
    }

    render(<Wrapper />);

    expect(screen.getByTestId('repo-option-2')).toHaveClass('selected');
    expect(screen.getByTestId('repo-options')).toHaveFocus();
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'y' });

    await waitFor(() => {
      expect(screen.getByTestId('repo-option-1')).toHaveClass('selected');
    });
  });

  it('does not arm delete when focus has moved to the create-worktree action', () => {
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onDeleteWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'ArrowDown' });
    expect(screen.getByTestId('repo-option-2')).toHaveClass('selected');

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    expect(screen.queryByText(/Delete .* \(y\/n\)/)).not.toBeInTheDocument();
  });

  it('esc cancels delete confirmation without leaving the chooser', () => {
    const onBack = vi.fn();
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onDeleteWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={onBack}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    expect(screen.getByText(/Delete repo--feature/)).toBeInTheDocument();

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Escape' });

    expect(screen.getByTestId('repo-options')).toBeInTheDocument();
    expect(screen.queryByText(/Delete repo--feature/)).not.toBeInTheDocument();
    expect(onBack).not.toHaveBeenCalled();
  });

  it('esc cancels the new-worktree form without leaving the chooser', () => {
    const onBack = vi.fn();
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={onBack}
      />,
    );

    fireEvent.click(screen.getByTestId('repo-option-2'));
    expect(screen.getByTestId('repo-new-worktree-form')).toBeInTheDocument();

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Escape' });

    expect(screen.getByTestId('repo-options')).toBeInTheDocument();
    expect(screen.queryByTestId('repo-new-worktree-form')).not.toBeInTheDocument();
    expect(onBack).not.toHaveBeenCalled();
  });

  it('opening the create-worktree form clears any pending delete confirmation', () => {
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onDeleteWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    expect(screen.getByText(/Delete repo--feature/)).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('repo-option-2'));

    expect(screen.queryByText(/Delete repo--feature/)).not.toBeInTheDocument();
    expect(screen.getByTestId('repo-new-worktree-form')).toBeInTheDocument();
  });
});
