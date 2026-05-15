import { render, screen } from '@testing-library/react';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ChangesPanel } from './ChangesPanel';
import type { BranchDiffFile } from '../hooks/useDaemonSocket';

const changedFiles: BranchDiffFile[] = [
  { path: 'app/src/App.tsx', status: 'modified', additions: 12, deletions: 3 },
  { path: 'internal/git/command.go', status: 'added', additions: 20 },
];

function renderPanel(overrides: Partial<ComponentProps<typeof ChangesPanel>> = {}) {
  return render(
    <ChangesPanel
      branchDiffFiles={[]}
      selectedFile={null}
      onFileSelect={vi.fn()}
      {...overrides}
    />
  );
}

describe('ChangesPanel', () => {
  it('shows initial loading only when explicitly loading', () => {
    renderPanel({ branchDiffLoading: true });

    expect(screen.queryByText('No changes')).not.toBeInTheDocument();
    expect(document.querySelector('.changes-loading')).toBeInTheDocument();
  });

  it('shows no changes after an empty successful load', () => {
    renderPanel({ branchDiffLoaded: true, branchDiffLoading: false });

    expect(screen.getByText('No changes')).toBeInTheDocument();
    expect(document.querySelector('.changes-loading')).not.toBeInTheDocument();
  });

  it('keeps files visible during background refresh', () => {
    renderPanel({
      branchDiffFiles: changedFiles,
      branchDiffBaseRef: 'origin/main',
      branchDiffLoaded: true,
      branchDiffRefreshing: true,
    });

    expect(screen.getByTitle('app/src/App.tsx')).toBeInTheDocument();
    expect(screen.getByTitle('internal/git/command.go')).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Refreshing' })).toBeInTheDocument();
    expect(document.querySelector('.changes-loading')).not.toBeInTheDocument();
  });

  it('keeps the last files visible when refresh fails after data loaded', () => {
    renderPanel({
      branchDiffFiles: changedFiles,
      branchDiffError: 'Get branch diff files timed out',
      branchDiffLoaded: true,
    });

    expect(screen.getByTitle('app/src/App.tsx')).toBeInTheDocument();
    expect(screen.getByText('Could not refresh changes. Showing last result.')).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Stale' })).toBeInTheDocument();
    expect(screen.queryByText('Get branch diff files timed out')).not.toBeInTheDocument();
  });

  it('keeps the empty no-changes state visible when refresh fails after data loaded', () => {
    renderPanel({
      branchDiffError: 'Get branch diff files timed out',
      branchDiffLoaded: true,
    });

    expect(screen.getByText('No changes')).toBeInTheDocument();
    expect(screen.getByText('Could not refresh changes. Showing last result.')).toBeInTheDocument();
    expect(screen.getByRole('status', { name: 'Stale' })).toBeInTheDocument();
    expect(screen.queryByText('Get branch diff files timed out')).not.toBeInTheDocument();
  });

  it('shows the error as the main body when no previous data exists', () => {
    renderPanel({ branchDiffError: 'Get branch diff files timed out' });

    expect(screen.getByText('Get branch diff files timed out')).toBeInTheDocument();
    expect(screen.queryByText('Could not refresh changes. Showing last result.')).not.toBeInTheDocument();
  });
});
