import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Seed } from '../types/generated';
import {
  SeedDocumentView,
  type SeedDocument,
} from './SeedDocumentView';
import type { SeedDocumentNote } from './seedArtifacts';

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
    artifacts: [],
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

  it('renders the daemon’s artifact set and opens a current markdown artifact', () => {
    const current = { kind: 'markdown_file' as const, path: '/repo/current.md' };
    // The detached one is still on the log; the set the daemon projected is
    // what the reader renders, so it must not reappear from the timeline.
    const notes = [
      note({ id: 'n-detach', kind: 'detach', artifact: { kind: 'markdown_file', path: '/repo/old.md' } }),
      note({ id: 'n-current', kind: 'attach', artifact: current }),
    ];

    const onOpenMarkdownArtifact = vi.fn();
    render(
      <SeedDocumentView
        document={document({ notes, notes_total: notes.length, artifacts: [current] })}
        onOpenMarkdownArtifact={onOpenMarkdownArtifact}
      />,
    );

    expect(screen.queryByRole('button', { name: '/repo/old.md' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '/repo/current.md' }));
    expect(onOpenMarkdownArtifact).toHaveBeenCalledWith('/repo/current.md');
  });

  it('renders a notebook artifact and a url artifact from the same set', () => {
    render(
      <SeedDocumentView
        document={document({
          artifacts: [
            { kind: 'notebook', notebook_document_id: 'nb-plan-7' },
            { kind: 'url', url: 'https://example.test/pr/1' },
          ],
        })}
      />,
    );

    expect(screen.getByText('nb-plan-7')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'https://example.test/pr/1' }))
      .toHaveAttribute('href', 'https://example.test/pr/1');
  });

  it('renders no artifact section for an empty set', () => {
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
