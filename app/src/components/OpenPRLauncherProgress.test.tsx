import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { OpenPRLauncherProgress } from './OpenPRLauncherProgress';

describe('OpenPRLauncherProgress', () => {
  it('shows the current launcher step and PR identity', () => {
    render(
      <OpenPRLauncherProgress
        repo="acme/widgets"
        number={42}
        title="Make widgets faster"
        step="creating_worktree"
      />,
    );

    expect(screen.getByRole('status', { name: 'Opening PR 42' })).toBeInTheDocument();
    expect(screen.getByText('acme/widgets#42')).toBeInTheDocument();
    expect(screen.getByText('Make widgets faster')).toBeInTheDocument();
    expect(screen.getByText('Creating worktree')).toBeInTheDocument();
  });
});
