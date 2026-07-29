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

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

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
    expect(screen.queryByTestId('repo-option-2')).not.toBeInTheDocument();
  });

  it('focuses the create form with a generated name when the repo root was chosen', () => {
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    const input = screen.getByTestId('repo-new-worktree-input') as HTMLInputElement;
    expect(input).toHaveFocus();
    expect(input.value).toMatch(/^[a-z]+-[a-z]+$/);
    // No destination is armed, so Enter cannot open one by accident.
    expect(screen.getByTestId('repo-option-0')).not.toHaveClass('selected');
  });

  it('creates a worktree from the prefilled name with a single Enter', async () => {
    const onCreateWorktree = vi.fn(async () => {});
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    const generated = (screen.getByTestId('repo-new-worktree-input') as HTMLInputElement).value;
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => expect(onCreateWorktree).toHaveBeenCalledWith(generated, 'origin/main'));
  });

  it('rerolls and retries when the generated name collides with a branch RepoInfo never saw', async () => {
    // `takenBranchNames` is built from `repoInfo.currentBranch` and
    // `repoInfo.worktrees` alone, so it has no visibility into an ordinary
    // local branch that was never checked out into a worktree. Git rejects
    // `worktree add -b` on that branch the same way it would on a taken one;
    // the component must recover by rerolling rather than surfacing the raw
    // git failure.
    const onCreateWorktree = vi.fn();
    let firstAttempt: string | undefined;
    onCreateWorktree.mockImplementation(async (branchName: string) => {
      if (firstAttempt === undefined) {
        firstAttempt = branchName;
        throw new Error(`git worktree add failed: fatal: a branch named '${branchName}' already exists`);
      }
    });
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
        onError={vi.fn()}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => expect(onCreateWorktree).toHaveBeenCalledTimes(2));
    const [firstCall, secondCall] = onCreateWorktree.mock.calls;
    expect(secondCall[0]).not.toBe(firstCall[0]);
    expect(secondCall[1]).toBe('origin/main');
  });

  it('surfaces the collision instead of rerolling when the user typed the name', async () => {
    // A typed name is a deliberate choice. Rerolling it would build an
    // unrelated worktree and silently discard what the user asked for, so the
    // "already exists" failure has to reach them unchanged.
    const onError = vi.fn();
    const onCreateWorktree = vi.fn(async (branchName: string) => {
      throw new Error(`git worktree add failed: fatal: a branch named '${branchName}' already exists`);
    });
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
        onError={onError}
      />,
    );

    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feature' } });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
    expect(onCreateWorktree).toHaveBeenCalledTimes(1);
    expect(onCreateWorktree).toHaveBeenCalledWith('feature', 'origin/main');
    expect(onError.mock.calls[0][0]).toMatch(/a branch named 'feature' already exists/);
    // The typed name survives so the user can correct it.
    expect((screen.getByTestId('repo-new-worktree-input') as HTMLInputElement).value).toBe('feature');
    consoleError.mockRestore();
  });

  it('still rerolls after the user types and then presses the reroll button', async () => {
    // Reroll hands ownership back to the generator, so the collision recovery
    // applies again even though the field was user-edited in between.
    const onCreateWorktree = vi.fn();
    let firstAttempt: string | undefined;
    onCreateWorktree.mockImplementation(async (branchName: string) => {
      if (firstAttempt === undefined) {
        firstAttempt = branchName;
        throw new Error(`git worktree add failed: fatal: a branch named '${branchName}' already exists`);
      }
    });
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
        onError={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'hand-typed' } });
    fireEvent.click(screen.getByTestId('repo-new-worktree-reroll'));
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => expect(onCreateWorktree).toHaveBeenCalledTimes(2));
    expect(onCreateWorktree.mock.calls[0][0]).not.toBe('hand-typed');
  });

  it('keeps focus on the typed worktree so Enter still opens it', () => {
    const onSelectWorktree = vi.fn();
    const onCreateWorktree = vi.fn(async () => {});
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={onSelectWorktree}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    expect(onSelectWorktree).toHaveBeenCalledWith('/tmp/repo--feature');
    expect(onCreateWorktree).not.toHaveBeenCalled();
  });

  it('reaches the destination list with ArrowDown from the create form', () => {
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

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'ArrowDown' });
    expect(screen.getByTestId('repo-option-0')).toHaveClass('selected');

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });
    expect(onSelectMainRepo).toHaveBeenCalledTimes(1);
  });

  it('draws a different name when the reroll button is pressed', () => {
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    const input = screen.getByTestId('repo-new-worktree-input') as HTMLInputElement;
    const names = new Set([input.value]);
    for (let i = 0; i < 12; i++) {
      fireEvent.click(screen.getByTestId('repo-new-worktree-reroll'));
      names.add(input.value);
    }

    expect(names.size).toBeGreaterThan(1);
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

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'ArrowDown' });
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

  it('does not arm delete when focus has moved to the create form', () => {
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

    // Up off the top of the destination list lands back in the create form.
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'ArrowUp' });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'ArrowUp' });
    expect(screen.getByTestId('repo-new-worktree-input')).toHaveFocus();

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

  it('esc from the create form goes back to the path input', () => {
    const onBack = vi.fn();
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={onBack}
      />,
    );

    expect(screen.getByTestId('repo-new-worktree-input')).toHaveFocus();
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Escape' });

    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it('focusing the create form clears any pending delete confirmation', () => {
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

    fireEvent.click(screen.getByTestId('repo-new-worktree-form'));

    expect(screen.queryByText(/Delete repo--feature/)).not.toBeInTheDocument();
    expect(screen.getByTestId('repo-new-worktree-input')).toHaveFocus();
  });

  it('keeps destinations visible while repo info is refreshing', () => {
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
        refreshing
      />,
    );

    expect(screen.getByRole('status', { name: 'Refreshing repo options' })).toBeInTheDocument();
    expect(screen.getByTestId('repo-option-0')).toBeInTheDocument();
    expect(screen.getByTestId('repo-option-1')).toBeInTheDocument();
  });

  it('shows form-local progress while creating a worktree', async () => {
    const createGate = deferred<void>();
    const onCreateWorktree = vi.fn(() => createGate.promise);
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('repo-new-worktree-form'));
    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feature-2' } });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    expect(screen.getByText('Creating worktree...')).toBeInTheDocument();
    expect(screen.getByTestId('repo-new-worktree-input')).toBeDisabled();

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });
    expect(onCreateWorktree).toHaveBeenCalledTimes(1);

    createGate.resolve();
    await waitFor(() => {
      expect(screen.getByTestId('repo-new-worktree-input')).not.toBeDisabled();
    });
  });

  it('defaults a new worktree to origin/<defaultBranch>', async () => {
    const onCreateWorktree = vi.fn(async () => {});
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('repo-new-worktree-form'));
    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feature-2' } });

    expect(screen.getByTestId('repo-new-worktree-start-default')).toBeChecked();
    expect(screen.getByTestId('repo-new-worktree-start-current')).not.toBeChecked();

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => expect(onCreateWorktree).toHaveBeenCalledWith('feature-2', 'origin/main'));
  });

  it('starts from the selected destination branch when "current" is chosen', async () => {
    const onCreateWorktree = vi.fn(async () => {});
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={onCreateWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('repo-new-worktree-form'));
    fireEvent.change(screen.getByTestId('repo-new-worktree-input'), { target: { value: 'feature-2' } });
    fireEvent.click(screen.getByTestId('repo-new-worktree-start-current'));

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'Enter' });

    await waitFor(() => expect(onCreateWorktree).toHaveBeenCalledWith('feature-2', 'feature'));
  });

  it('shows row-local progress while deleting a worktree', async () => {
    const deleteGate = deferred<void>();
    const onDeleteWorktree = vi.fn(() => deleteGate.promise);
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onDeleteWorktree={onDeleteWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'y' });

    expect(await screen.findByRole('status', { name: 'Deleting repo--feature' })).toBeInTheDocument();
    expect(screen.queryByText(/Delete repo--feature/)).not.toBeInTheDocument();

    deleteGate.resolve();
    await waitFor(() => {
      expect(screen.queryByRole('status', { name: 'Deleting repo--feature' })).not.toBeInTheDocument();
    });
  });

  it('offers force delete after normal delete fails with a forceable error', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    const error = Object.assign(new Error('contains modified or untracked files'), {
      forceable: true,
    });
    const onDeleteWorktree = vi.fn(async (_path: string, options?: { force?: boolean }) => {
      if (!options?.force) {
        throw error;
      }
    });
    render(
      <RepoOptions
        repoInfo={repoInfo}
        selectedPath="/tmp/repo--feature"
        onSelectedPathChange={vi.fn()}
        onSelectMainRepo={vi.fn()}
        onSelectWorktree={vi.fn()}
        onCreateWorktree={vi.fn(async () => {})}
        onDeleteWorktree={onDeleteWorktree}
        onRefresh={vi.fn()}
        onBack={vi.fn()}
      />,
    );

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'D' });
    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'y' });

    expect(await screen.findByText(/Delete failed: contains modified or untracked files/)).toBeInTheDocument();
    expect(screen.getByText(/Force delete local worktree and branch/)).toBeInTheDocument();

    fireEvent.keyDown(screen.getByTestId('repo-options'), { key: 'y' });

    await waitFor(() => {
      expect(onDeleteWorktree).toHaveBeenLastCalledWith('/tmp/repo--feature', { force: true });
    });
    consoleError.mockRestore();
  });
});
