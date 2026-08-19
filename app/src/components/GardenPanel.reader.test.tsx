import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../types/generated';
import { GardenPanel } from './GardenPanel';
import type { SeedDocument } from './SeedDocumentView';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-plan11',
    title: 'Open this plan',
    body: '# First body',
    status: 'growing',
    step_slug: 'open-this-plan',
    planter_session: '',
    planter_member: '',
    tender_session: 'sess-a',
    tender_member: 'trellis',
    edges: [],
    template: false,
    gate: false,
    vars: [],
    ready: false,
    rev: 1,
    created_at: '2026-08-15T08:00:00Z',
    updated_at: '2026-08-15T08:00:00Z',
    ...overrides,
  };
}

function seedDocument(root: Seed, noteBody: string): SeedDocument {
  return {
    seed: root,
    tender_holds: Boolean(root.tender_session || root.tender_member),
    children: [],
    notes: noteBody ? [{
      id: `n-${noteBody}`,
      seed_id: root.id,
      kind: 'note',
      body: noteBody,
      author_session: 'sess-a',
      author_member: 'trellis',
      created_at: '2026-08-15T09:00:00Z',
    }] : [],
    notes_total: noteBody ? 1 : 0,
    artifacts: [],
  };
}

describe('GardenPanel seed reader drill', () => {
  it('fetches the typed document, renders it read-only, and hands off to a tile', async () => {
    const root = seed();
    const fetched = seedDocument(root, 'The live log entry');
    fetched.notes.unshift({
      id: 'n-attach1',
      seed_id: root.id,
      kind: 'attach',
      body: '',
      author_session: 'sess-a',
      author_member: 'trellis',
      created_at: '2026-08-15T09:01:00Z',
      artifact: { kind: 'markdown_file', path: '/repo/evidence.md' },
    });
    fetched.notes_total += 1;
    // The set is the daemon's projection over the whole log, not something the
    // panel recomputes from the notes it happens to have been sent.
    fetched.artifacts = [{ kind: 'markdown_file', path: '/repo/evidence.md' }];
    const fetchSeedDocument = vi.fn().mockResolvedValue(fetched);
    const onOpenAsTile = vi.fn();
    const onOpenMarkdownArtifact = vi.fn();
    const { container } = render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[root]}
        seedsTotal={1}
        fetchSeedDocument={fetchSeedDocument}
        onOpenAsTile={onOpenAsTile}
        onOpenMarkdownArtifact={onOpenMarkdownArtifact}
      />,
    );

    fireEvent.click(screen.getByText('Open this plan'));

    await waitFor(() => expect(fetchSeedDocument).toHaveBeenCalledWith('s-plan11'));
    expect(await screen.findByText('The live log entry')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'First body' })).toBeInTheDocument();
    expect(container.querySelector('.md-reader--annotating')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Open as tile' }));
    expect(onOpenAsTile).toHaveBeenCalledWith('s-plan11');

    fireEvent.click(screen.getByRole('button', { name: '/repo/evidence.md' }));
    expect(onOpenMarkdownArtifact).toHaveBeenCalledWith('/repo/evidence.md');
  });

  it('re-reads the open drill on a garden push even when the seed revision is unchanged', async () => {
    const root = seed();
    const fetchSeedDocument = vi.fn()
      .mockResolvedValueOnce(seedDocument(root, 'Before the push'))
      .mockResolvedValueOnce(seedDocument(root, 'After the push'));
    const { rerender } = render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[root]}
        seedsTotal={1}
        fetchSeedDocument={fetchSeedDocument}
      />,
    );

    fireEvent.click(screen.getByText('Open this plan'));
    expect(await screen.findByText('Before the push')).toBeInTheDocument();

    // garden.noted re-pushes a freshly decoded snapshot, but writing a note
    // does not revise the seed document itself.
    rerender(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[{ ...root }]}
        seedsTotal={1}
        fetchSeedDocument={fetchSeedDocument}
      />,
    );

    expect(await screen.findByText('After the push')).toBeInTheDocument();
    expect(fetchSeedDocument).toHaveBeenCalledTimes(2);
  });

  it('surfaces a failed detail read instead of leaving an empty drill', async () => {
    const root = seed();
    render(
      <GardenPanel
        isOpen
        onClose={vi.fn()}
        seeds={[root]}
        seedsTotal={1}
        fetchSeedDocument={vi.fn().mockRejectedValue(new Error('no seed s-plan11 is planted here'))}
      />,
    );

    fireEvent.click(screen.getByText('Open this plan'));

    expect(await screen.findByText('no seed s-plan11 is planted here')).toBeInTheDocument();
  });
});
