import { useId, type Ref } from 'react';
import type {
  Seed,
  SeedArtifactReference,
} from '../types/generated';
import { Markdown } from './Markdown';
import { MarkdownReader, type MarkdownAnnotationsSendHandle } from './MarkdownReader';
import { seedMarkdownSource } from './MarkdownReader/documentSource';
import { artifactKey, artifactLabel, type SeedDocumentNote } from './seedArtifacts';
import './SeedDocumentView.css';

/** The one read model shared by the panel drill and the docked seed tile. */
export interface SeedDocument {
  seed: Seed;
  tender_holds: boolean;
  children: Seed[];
  /** Newest first, matching the garden log's wire order. */
  notes: SeedDocumentNote[];
  notes_total: number;
  /** Attach minus detach, projected by the daemon over the seed's whole log. */
  artifacts: SeedArtifactReference[];
}

export interface SeedDocumentViewProps {
  document: SeedDocument;
  compact?: boolean;
  annotationsEnabled?: boolean;
  onAnnotationsCountChange?: (count: number) => void;
  annotationsSendRef?: Ref<MarkdownAnnotationsSendHandle | null>;
  onOpenMarkdownArtifact?: (path: string) => void;
}

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

function SeedArtifacts({
  artifacts,
  onOpenMarkdownArtifact,
  headingId,
}: {
  artifacts: readonly SeedArtifactReference[];
  onOpenMarkdownArtifact?: (path: string) => void;
  headingId: string;
}) {
  if (artifacts.length === 0) return null;

  return (
    <section className="seed-document__artifacts" aria-labelledby={headingId}>
      <h3 id={headingId}>Artifacts</h3>
      <ul>
        {artifacts.map((artifact) => {
          const key = artifactKey(artifact);
          const label = artifactLabel(artifact);
          if (artifact.kind === 'markdown_file' && artifact.path && onOpenMarkdownArtifact) {
            const path = artifact.path;
            return (
              <li key={key}>
                <button type="button" onClick={() => onOpenMarkdownArtifact(path)}>
                  {label}
                </button>
              </li>
            );
          }
          if (artifact.url) {
            return <li key={key}><a href={artifact.url}>{label}</a></li>;
          }
          return <li key={key}>{label}</li>;
        })}
      </ul>
    </section>
  );
}

export function SeedDocumentView({
  document,
  compact = false,
  annotationsEnabled = false,
  onAnnotationsCountChange,
  annotationsSendRef,
  onOpenMarkdownArtifact,
}: SeedDocumentViewProps) {
  const { seed, children, notes, notes_total: notesTotal, artifacts } = document;
  const withheld = Math.max(0, notesTotal - notes.length);
  const artifactsHeadingId = useId();
  const ledgerHeadingId = useId();

  return (
    <div className={`seed-document${compact ? ' seed-document--compact' : ''}`}>
      {seed.body.trim() ? (
        <MarkdownReader
          content={seed.body}
          source={seedMarkdownSource(seed.id)}
          allowLocalTargets={false}
          annotationsEnabled={annotationsEnabled}
          onAnnotationsCountChange={onAnnotationsCountChange}
          annotationsSendRef={annotationsSendRef}
        />
      ) : (
        <p className="seed-document__empty-body">No body — the title is the whole seed.</p>
      )}

      <SeedArtifacts
        artifacts={artifacts}
        onOpenMarkdownArtifact={onOpenMarkdownArtifact}
        headingId={artifactsHeadingId}
      />

      <section className="seed-document__ledger" aria-labelledby={ledgerHeadingId}>
        <h3 id={ledgerHeadingId}>Ledger</h3>

        {children.length > 0 && (
          <ul className="seed-document__children" aria-label="Children">
            {children.map((child) => (
              <li key={child.id} className={`is-${child.status}`}>
                <span className="seed-document__child-state" aria-label={child.status} />
                <span className="seed-document__child-title">{child.title}</span>
                <span className="seed-document__child-id">{child.id}</span>
                <span className="seed-document__child-status">{child.status}</span>
              </li>
            ))}
          </ul>
        )}

        {notes.length > 0 ? (
          <ol className="seed-document__notes">
            {notes.map((note) => (
              <li key={note.id} data-kind={note.kind}>
                <div className="seed-document__note-head">
                  <span>{note.author_member || note.author_session || '—'}</span>
                  {note.kind !== 'note' && <span>{note.kind}</span>}
                  <time dateTime={note.created_at}>{formatTimestamp(note.created_at)}</time>
                </div>
                {note.body && <Markdown className="seed-document__note-body" breaks>{note.body}</Markdown>}
              </li>
            ))}
          </ol>
        ) : children.length === 0 ? (
          <p className="seed-document__empty-ledger">Nothing on this seed’s log yet.</p>
        ) : null}

        {withheld > 0 && (
          <p className="seed-document__withheld">{withheld} more {withheld === 1 ? 'entry' : 'entries'} on the log.</p>
        )}
      </section>
    </div>
  );
}
