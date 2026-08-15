import type { SeedArtifactReference, SeedNote } from '../types/generated';

/** SeedNote is deliberately forward-compatible with the later artifact verbs. */
export type SeedDocumentNote = Omit<SeedNote, 'artifact'> & {
  artifact?: SeedArtifactReference;
};

function artifactKey(artifact: SeedArtifactReference): string {
  return [
    artifact.kind,
    artifact.path ?? '',
    artifact.notebook_document_id ?? '',
    artifact.repository ?? '',
    artifact.url ?? '',
  ].join('\0');
}

/** Project the current set by replaying the newest-first log chronologically. */
export function currentSeedArtifacts(notes: readonly SeedDocumentNote[]): SeedArtifactReference[] {
  const current = new Map<string, SeedArtifactReference>();
  for (let i = notes.length - 1; i >= 0; i -= 1) {
    const note = notes[i];
    if (!note.artifact) continue;
    const key = artifactKey(note.artifact);
    if (note.kind === 'attach') {
      current.set(key, note.artifact);
    } else if (note.kind === 'detach') {
      current.delete(key);
    }
  }
  return Array.from(current.values());
}
