import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../types/generated';
import {
  SeedDocumentView,
  type SeedDocument,
} from './SeedDocumentView';
import { currentSeedArtifacts, type SeedDocumentNote } from './seedArtifacts';

function seed(overrides: Partial<Seed> = {}): Seed {
  return {
    id: 's-plan11',
    title: 'The plan',
    body: '## Rendered plan\n\nRead **this**.',
    status: 'growing',
    step_slug: 'the-plan',
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

function note(overrides: Partial<SeedDocumentNote> & { id: string }): SeedDocumentNote {
  return {
    seed_id: 's-plan11',
    kind: 'note',
    body: '',
    author_session: '',
    author_member: '',
    created_at: '2026-08-15T09:00:00Z',
    ...overrides,
  };
}

function document(overrides: Partial<SeedDocument> = {}): SeedDocument {
  return {
    seed: seed(),
    tender_holds: false,
    children: [],
    notes: [],
    notes_total: 0,
    ...overrides,
  };
}

describe('SeedDocumentView', () => {
  it('renders the seed body as markdown and the live child/note ledger beneath it', () => {
    const child = seed({ id: 's-step11', title: 'Build the reader', body: '', status: 'harvested' });
    render(
      <SeedDocumentView
        document={document({
          children: [child],
          notes: [note({ id: 'n-one111', body: 'Verified the **reader**.', author_member: 'alder' })],
          notes_total: 2,
        })}
      />,
    );

    expect(screen.getByRole('heading', { name: 'Rendered plan' })).toBeInTheDocument();
    expect(screen.getByText('Build the reader')).toBeInTheDocument();
    expect(screen.getByText('harvested')).toBeInTheDocument();
    expect(screen.getByText('reader', { selector: 'strong' })).toBeInTheDocument();
    expect(screen.getByText('1 more entry on the log.')).toBeInTheDocument();
  });

  it('is read-only unless its tile owner explicitly enables annotations', () => {
    const { container } = render(<SeedDocumentView document={document()} />);

    expect(container.querySelector('.md-reader--annotating')).not.toBeInTheDocument();
  });

  it('projects attach minus detach in chronological order and opens a current markdown artifact', () => {
    const old = { kind: 'markdown_file' as const, path: '/repo/old.md' };
    const current = { kind: 'markdown_file' as const, path: '/repo/current.md' };
    // Wire order is newest first. The detach must be applied after the older
    // attach, not before it, or old.md incorrectly survives the projection.
    const notes = [
      note({ id: 'n-detach', kind: 'detach', artifact: old }),
      note({ id: 'n-current', kind: 'attach', artifact: current }),
      note({ id: 'n-old111', kind: 'attach', artifact: old }),
    ];

    expect(currentSeedArtifacts(notes)).toEqual([current]);

    const onOpenMarkdownArtifact = vi.fn();
    render(
      <SeedDocumentView
        document={document({ notes, notes_total: notes.length })}
        onOpenMarkdownArtifact={onOpenMarkdownArtifact}
      />,
    );

    expect(screen.queryByText('/repo/old.md')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '/repo/current.md' }));
    expect(onOpenMarkdownArtifact).toHaveBeenCalledWith('/repo/current.md');
  });

  it('renders no artifact section for today’s note-only log', () => {
    render(
      <SeedDocumentView
        document={document({
          notes: [note({ id: 'n-plain1', body: 'No attachment here.' })],
          notes_total: 1,
        })}
      />,
    );

    expect(screen.queryByRole('heading', { name: 'Artifacts' })).not.toBeInTheDocument();
  });
});
