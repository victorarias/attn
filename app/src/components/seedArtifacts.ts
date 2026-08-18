import type { SeedArtifactReference, SeedNote } from '../types/generated';

/**
 * The seed log entry as the view reads it. Identical to the wire shape; the
 * alias exists so the ledger's type follows the note, not the generated file.
 */
export type SeedDocumentNote = SeedNote;

/**
 * A stable React key for one artifact. Every field participates: two references
 * differing anywhere are two artifacts, and collapsing them would drop one.
 */
export function artifactKey(artifact: SeedArtifactReference): string {
  return [
    artifact.kind,
    artifact.path ?? '',
    artifact.notebook_document_id ?? '',
    artifact.repository ?? '',
    artifact.url ?? '',
  ].join('\0');
}

/** The field that identifies an artifact — the part a person recognizes. */
export function artifactLabel(artifact: SeedArtifactReference): string {
  return artifact.path
    || artifact.notebook_document_id
    || artifact.url
    || artifact.repository
    || artifact.kind;
}
