import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { WorkspaceContextNavigator, type WorkspaceContextView } from './WorkspaceContextNavigator';

const contexts: WorkspaceContextView[] = [
  {
    title: 'Attn',
    directory: '/projects/attn',
    updatedByLabel: 'Agent one',
    context: {
      workspace_id: 'workspace-attn',
      content: '# Goal\n\nBuild the action menu.',
      revision: 3,
      updated_at: '2026-06-09T10:00:00Z',
      updated_by_session_id: 'session-1',
    },
  },
  {
    title: 'Services pilot',
    directory: '/projects/services-pilot',
    context: {
      workspace_id: 'workspace-services',
      content: '# Handoff\n\nDeploy the service.',
      revision: 1,
      updated_at: '2026-06-09T11:00:00Z',
      updated_by_session_id: 'session-2',
    },
  },
];

describe('WorkspaceContextNavigator', () => {
  it('navigates contexts and renders the selected markdown', () => {
    render(
      <WorkspaceContextNavigator
        isOpen
        contexts={contexts}
        isLoading={false}
        error={null}
        onClose={() => {}}
        onRetry={() => {}}
      />,
    );

    expect(screen.getByRole('heading', { name: 'Goal' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Services pilot/ }));
    expect(screen.getByRole('heading', { name: 'Handoff' })).toBeVisible();
    expect(screen.getByText('Deploy the service.')).toBeVisible();
  });

  it('filters by context content', () => {
    render(
      <WorkspaceContextNavigator
        isOpen
        contexts={contexts}
        isLoading={false}
        error={null}
        onClose={() => {}}
        onRetry={() => {}}
      />,
    );

    fireEvent.change(screen.getByLabelText('Search workspace contexts'), {
      target: { value: 'deploy' },
    });
    expect(screen.queryByRole('button', { name: /Attn/ })).toBeNull();
    expect(screen.getByRole('button', { name: /Services pilot/ })).toBeVisible();
    expect(screen.getByText('Deploy the service.')).toBeVisible();
  });
});
