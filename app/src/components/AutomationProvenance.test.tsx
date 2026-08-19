import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { openUrl } from '@tauri-apps/plugin-opener';
import { AutomationProvenance } from './AutomationProvenance';

vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

const provenance = {
  run_id: 'run-1',
  definition_id: 'requested-pr-review-sol-medium',
  definition_name: 'Requested PR review - GPT Sol medium',
  trigger_type: 'github_review_requested',
  pull_request: {
    repository: 'ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web',
    number: 101,
    url: 'https://ghe.spotify.net/audiobook-feed-mgmt/feed-nexus-web/pull/101',
    title: 'Fix validation race',
    head_sha: '82f1c7a000000000000000000000000000000000',
  },
};

describe('AutomationProvenance', () => {
  it('renders automation, definition, PR identity, and title as one line', () => {
    render(<AutomationProvenance provenance={provenance} />);

    expect(screen.getByText('Automation')).toBeInTheDocument();
    expect(screen.getByText('GPT Sol medium')).toBeInTheDocument();
    expect(screen.getByText('feed-nexus-web#101')).toBeInTheDocument();
    expect(screen.getByText('Fix validation race')).toBeInTheDocument();
  });

  it('opens the exact PR from an interactive surface', () => {
    render(<AutomationProvenance provenance={provenance} interactive />);

    fireEvent.click(screen.getByRole('button', { name: 'feed-nexus-web#101 ↗' }));
    expect(openUrl).toHaveBeenCalledWith(provenance.pull_request.url);
  });

  it('keeps the sidebar badge compact while exposing full provenance accessibly', () => {
    render(<AutomationProvenance provenance={provenance} density="badge" />);

    expect(screen.getByLabelText(/Requested PR review - GPT Sol medium/)).toHaveTextContent('⚡');
  });
});
